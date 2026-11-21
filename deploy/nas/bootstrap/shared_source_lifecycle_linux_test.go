package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Fixture-only entry for running a cross-compiled ELF with a separately built
// exact-HEAD bootstrap. Ordinary CI covers the same helper through the existing
// root TestNASFixedInstallerAndReceipt/shared-source child, without a new root
// test being selected by the ordinary non-root package invocation.
func TestSharedSourceExplicitPrebuiltFixture(t *testing.T) {
	binary := os.Getenv("AURIGA_TEST_SHARED_SOURCE_BINARY")
	if binary == "" {
		return
	}
	if os.Geteuid() != 0 || !filepath.IsAbs(binary) {
		t.Fatal("explicit prebuilt fixture requires Linux root and an absolute public bootstrap executable")
	}
	parent, err := os.MkdirTemp("", "auriga-shared-source-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	}()
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	testNASSharedSourceLifecycle(t, parent, binary, "../access-ip.example.json")
}

func testNASSharedSourceLifecycle(t *testing.T, parent, binary, input string) {
	for _, teamOnly := range []bool{false, true} {
		name := "preserve-company-adapter"
		if teamOnly {
			name = "team-only-no-adapter"
		}
		t.Run(name, func(t *testing.T) { testNASSharedSourceVariant(t, parent, binary, input, teamOnly, false) })
	}
	t.Run("team-only-with-approved-dedicated-profile", func(t *testing.T) { testNASSharedSourceVariant(t, parent, binary, input, true, true) })
}

func testNASSharedSourceVariant(t *testing.T, parent, binary, input string, teamOnly, dedicated bool) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("existing NAS root fixture must invoke shared source lifecycle")
	}
	r, _ := runtimeTestFixture(t, parent) // original production signer and real verification inputs
	name := "shared-data-company"
	if teamOnly {
		name = "shared-data-team"
	}
	if dedicated {
		name += "-dedicated"
	}
	data := filepath.Join(parent, name)
	for _, path := range []string{data, filepath.Join(data, "config")} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	base := filepath.Join(data, "config/materials")
	args := []string{"-out", base, "-access-file", input, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26", "-runtime-bundle", r.bundle, "-runtime-root", r.root, "-runtime-image", r.image}
	if output, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
		t.Fatalf("real sealed-base installation: %v %s", err, output)
	}
	if !teamOnly {
		path := filepath.Join(base, "uac/adapter.json")
		if err := os.WriteFile(path, []byte(sharedApprovedAdapterFixture), 0640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, 10001, 10001); err != nil {
			t.Fatal(err)
		}
	}
	baseBefore := nasSnapshot(t, base)
	baseReceipt, err := os.ReadFile(filepath.Join(base, "iam-installation/bootstrap.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	baseSHA := sharedSHA(baseReceipt)
	caPEM, err := os.ReadFile(filepath.Join(base, "pki/ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(base, "pki/ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(caKeyPEM)
	caBlock, _ := pem.Decode(caPEM)
	keyBlock, _ := pem.Decode(caKeyPEM)
	if caBlock == nil || keyBlock == nil {
		t.Fatal("synthetic installed CA missing")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := private.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatal("unexpected synthetic installed CA type")
	}
	for _, path := range []string{filepath.Join(data, "data"), filepath.Join(data, "data/agent"), filepath.Join(data, "data/agent/agent")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	nodePath := filepath.Join(data, "data/agent/agent/node.crt")
	nodeID := "11111111-2222-4333-8444-555555555555"
	writeNode := func(id string, serial int64) {
		t.Helper()
		public, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		uri, _ := url.Parse("auriga://node/" + id)
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{uri}}
		der, err := x509.CreateCertificate(rand.Reader, cert, ca, &public.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(nodePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeNode(nodeID, 101)
	adapter := filepath.Join(base, "uac/adapter.json")
	profileRaw, err := os.ReadFile("../shared-runtime-profile.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(parent, "approved-profile.yml")
	if err := os.WriteFile(profile, profileRaw, 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(data, "config/shared-source")
	stageArgs := []string{"-adapter-file", adapter, "-adapter-sha256", sharedSHA([]byte(sharedApprovedAdapterFixture)), "-profile-file", profile, "-profile-sha256", sharedSHA(profileRaw), "-model", "gpt-6.1-sol"}
	if teamOnly {
		stageArgs = []string{"-team-only", "-profile-file", profile, "-profile-sha256", sharedSHA(profileRaw), "-model", "gpt-6.1-sol"}
	}
	var dedicatedRaw []byte
	dedicatedPath := filepath.Join(parent, "approved-dedicated-profile.yml")
	if dedicated {
		dedicatedRaw, err = os.ReadFile("../dedicated-runtime-profile.example.yml")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dedicatedPath, dedicatedRaw, 0644); err != nil {
			t.Fatal(err)
		}
		stageArgs = append(stageArgs, "-dedicated-profile-file", dedicatedPath, "-dedicated-profile-sha256", sharedSHA(dedicatedRaw))
	}
	run := func(action string, success bool, extra ...string) string {
		t.Helper()
		arguments := []string{"shared-runtime", "-action", action, "-base", base, "-out", out}
		if action == "stage" || action == "set-model" || action == "commit" || action == "rollback" {
			arguments = append(arguments, "-expected-base-sha256", baseSHA)
		}
		arguments = append(arguments, extra...)
		command := exec.Command("/bin/sh", append([]string{"-c", `umask 077; exec "$@"`, "sh", binary}, arguments...)...)
		output, err := command.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("actual shared CLI %s expected success=%v: %v %s", action, success, err, output)
		}
		if nasSnapshot(t, base) != baseBefore {
			t.Fatal("shared installation changed original sealed material/receipt/runtime proofs")
		}
		return strings.TrimSpace(string(output))
	}
	runtimeAction := testNASSharedSignedUpgrade(t, data, base, binary, r)
	runtimeAction("stage", true)
	run("stage", false, stageArgs...) // initial shared source must never accept a pending candidate
	runtimeAction("rollback", true)
	if dedicated {
		withoutDedicated := stageArgs[:len(stageArgs)-4]
		run("stage", false, append(append([]string{}, withoutDedicated...), "-dedicated-profile-file", dedicatedPath)...)
		run("stage", false, append(append([]string{}, withoutDedicated...), "-dedicated-profile-sha256", sharedSHA(dedicatedRaw))...)
		run("stage", false, append(append([]string{}, withoutDedicated...), "-dedicated-profile-file", dedicatedPath, "-dedicated-profile-sha256", strings.Repeat("0", 64))...)
		if err := os.Chmod(dedicatedPath, 0664); err != nil {
			t.Fatal(err)
		}
		run("stage", false, stageArgs...)
		if err := os.Chmod(dedicatedPath, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dedicatedPath, 10001, 10001); err != nil {
			t.Fatal(err)
		}
		run("stage", false, stageArgs...)
		if err := os.Chown(dedicatedPath, 0, 0); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(parent, "dedicated-symlink.yml")
		if err := os.Symlink(dedicatedPath, link); err != nil {
			t.Fatal(err)
		}
		run("stage", false, append(append([]string{}, withoutDedicated...), "-dedicated-profile-file", link, "-dedicated-profile-sha256", sharedSHA(dedicatedRaw))...)
		for i, invalid := range [][]byte{[]byte("[]"), profileRaw, bytes.Repeat([]byte(" "), (1<<20)+1)} {
			path := filepath.Join(parent, fmt.Sprintf("invalid-dedicated-%d.yml", i))
			if err := os.WriteFile(path, invalid, 0644); err != nil {
				t.Fatal(err)
			}
			run("stage", false, append(append([]string{}, withoutDedicated...), "-dedicated-profile-file", path, "-dedicated-profile-sha256", sharedSHA(invalid))...)
		}
	}
	run("stage", true, stageArgs...)
	generation := filepath.Join(out, sharedInstallationGeneration)
	stat, err := os.Stat(filepath.Join(generation, "public/host.json"))
	if err != nil || stat.Mode().Perm() != 0444 {
		t.Fatal("actual private-umask stage did not produce exact public mode", err)
	}
	if _, err := os.Stat(filepath.Join(out, ".pending")); !os.IsNotExist(err) {
		t.Fatal("successful full stage left interrupted evidence")
	}
	if dedicated {
		got, err := runtimeReadLeaf(filepath.Join(generation, "public/dedicated-profile.yml"), 0600, 2<<20)
		if err != nil || !bytes.Equal(got, dedicatedRaw) {
			t.Fatal("approved dedicated bytes were not staged with exact source metadata", err)
		}
		raw, err := runtimeReadLeaf(filepath.Join(generation, "installation.receipt"), 0600, 65536)
		if err != nil {
			t.Fatal(err)
		}
		var receipt sharedInstallationReceipt
		if json.Unmarshal(raw, &receipt) != nil || receipt.Version != 1 || receipt.Profile != sharedSHA(profileRaw) || receipt.DedicatedProfile != sharedSHA(dedicatedRaw) {
			t.Fatal("independent shared and dedicated approvals were not recorded together")
		}
	}
	if teamOnly {
		raw, err := os.ReadFile(filepath.Join(generation, "public/host.json"))
		if err != nil {
			t.Fatal(err)
		}
		var host struct {
			Enterprise struct {
				AllowRawAgentToken bool     `json:"allowRawAgentToken"`
				Hosts              []string `json:"outboundAllowedHosts"`
				Employee           string   `json:"empNoHeader"`
				Token              string   `json:"authValueHeader"`
			}
		}
		if json.Unmarshal(raw, &host) != nil || !host.Enterprise.AllowRawAgentToken || host.Enterprise.Hosts == nil || len(host.Enterprise.Hosts) != 0 || host.Enterprise.Employee != "" || host.Enterprise.Token != "" {
			t.Fatal("actual no-adapter install invented enterprise access or disabled member credentials")
		}
	}
	staged := nasSnapshot(t, generation)
	if !strings.Contains(run("status", true), "state=staged") {
		t.Fatal("stage not recorded")
	}
	run("check", false)
	run("stage", true, stageArgs...)
	if nasSnapshot(t, generation) != staged {
		t.Fatal("idempotent staging rotated source credentials or public material")
	}
	run("rollback", true)
	if !strings.Contains(run("status", true), "state=rolled-back") {
		t.Fatal("rollback event absent")
	}
	run("stage", true, stageArgs...)
	run("commit", true)
	run("check", true)
	if dedicated {
		leaf := filepath.Join(generation, "public/dedicated-profile.yml")
		if err := os.WriteFile(leaf, append(bytes.Clone(dedicatedRaw), []byte("\n# tampered stored profile\n")...), 0600); err != nil {
			t.Fatal(err)
		}
		run("check", false)
		if err := os.WriteFile(leaf, dedicatedRaw, 0600); err != nil {
			t.Fatal(err)
		}
		run("check", true) // a fresh CLI process re-reads the approved receipt/bytes
		if err := os.Rename(dedicatedPath, dedicatedPath+".offline"); err != nil {
			t.Fatal(err)
		}
		run("check", true) // recovery uses the immutable source, not a transient approval path
		if err := os.Rename(dedicatedPath+".offline", dedicatedPath); err != nil {
			t.Fatal(err)
		}
		run("stage", false, stageArgs[:len(stageArgs)-4]...) // cannot downgrade a committed approval
	}
	if run("path", true) != generation {
		t.Fatal("committed source path differs")
	}
	run("rollback", false)
	if !teamOnly {
		run("stage", false, "-team-only", "-profile-file", profile, "-profile-sha256", sharedSHA(profileRaw), "-model", "gpt-6.1-sol")
	}
	if nasSnapshot(t, generation) != staged {
		t.Fatal("resume/commit changed the fixed material generation")
	}
	writeNode(nodeID, 102)
	run("check", true) // normal same-account certificate renewal
	writeNode("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", 103)
	run("check", false)
	writeNode(nodeID, 104)
	run("check", true)
	host := filepath.Join(generation, "public/host.json")
	if err := os.Chmod(host, 0400); err != nil {
		t.Fatal(err)
	}
	run("check", false)
	if err := os.Chmod(host, 0444); err != nil {
		t.Fatal(err)
	}
	run("check", true)
	template := filepath.Join(base, "templates/real-dsh.pb")
	original, err := os.ReadFile(template)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(template, append(bytes.Clone(original), 0), 0644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "shared-runtime", "-action", "check", "-base", base, "-out", out).CombinedOutput()
	if err == nil {
		t.Fatalf("tampered sealed runtime accepted: %s", output)
	}
	if err := os.WriteFile(template, original, 0644); err != nil {
		t.Fatal(err)
	}
	run("check", true)
	if !dedicated {
		testNASSharedExplicitModelRotation(t, base, out, generation, baseBefore, staged, stageArgs, run)
	}
	t.Log("actual sealed bootstrap + signed runtime + enrolled public Node + umask077 stage/resume/commit/check completed without services")
	testNASSharedRoleAndReplayPreparation(t, data, base, binary, run, runtimeAction)
}

func testNASSharedExplicitModelRotation(t *testing.T, base, out, generation, baseBefore, committedSnapshot string, stageArgs []string, run func(string, bool, ...string) string) {
	t.Helper()
	secretBefore, err := runtimeReadLeaf(filepath.Join(generation, "platform/agent-secret/installation.secret"), 0600, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	adapterBefore, err := runtimeReadLeaf(filepath.Join(generation, "platform/uac-adapter.json"), 0600, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	profileBefore, err := runtimeReadLeaf(filepath.Join(generation, "public/profile.yml"), 0600, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	receiptBefore, err := runtimeReadLeaf(filepath.Join(generation, "installation.receipt"), 0600, 65536)
	if err != nil {
		t.Fatal(err)
	}
	var oldReceipt sharedInstallationReceipt
	if json.Unmarshal(receiptBefore, &oldReceipt) != nil || oldReceipt.Model != "gpt-6.1-sol" {
		t.Fatal("committed receipt model changed before explicit rotation")
	}
	run("stage", false, append(append([]string{}, stageArgs[:len(stageArgs)-2]...), "-model", "auto")...) // implicit stage still refused
	run("set-model", false, "-model", " auto")
	run("set-model", false, "-model", "auto\n")
	run("set-model", false, "-model", "")
	run("set-model", true, "-model", "auto")
	if !strings.Contains(run("status", true), "state=staged") {
		t.Fatal("set-model did not leave a staged generation")
	}
	run("check", false)
	run("set-model", true, "-model", "auto") // same-parameter reentry
	run("set-model", false, "-model", "other-model")
	if _, err := os.Stat(filepath.Join(out, sharedModelRotationDir)); err != nil {
		t.Fatal("staged set-model must retain previous committed generation for rollback", err)
	}
	hostRaw, err := os.ReadFile(filepath.Join(generation, "public/host.json"))
	if err != nil {
		t.Fatal(err)
	}
	var host struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(hostRaw, &host) != nil || host.Model != "auto" {
		t.Fatal("set-model did not re-render host.json model")
	}
	receiptRaw, err := runtimeReadLeaf(filepath.Join(generation, "installation.receipt"), 0600, 65536)
	if err != nil {
		t.Fatal(err)
	}
	var rotated sharedInstallationReceipt
	if json.Unmarshal(receiptRaw, &rotated) != nil || rotated.Model != "auto" || rotated.Adapter != oldReceipt.Adapter || rotated.Profile != oldReceipt.Profile || rotated.Node != oldReceipt.Node || rotated.CA != oldReceipt.CA || rotated.Tenant != oldReceipt.Tenant || rotated.InitialRuntime != oldReceipt.InitialRuntime || rotated.TeamOnly != oldReceipt.TeamOnly || rotated.DedicatedProfile != oldReceipt.DedicatedProfile || rotated.Files == oldReceipt.Files {
		t.Fatal("set-model changed non-model receipt fields or left files digest unchanged", rotated)
	}
	secretAfter, err := runtimeReadLeaf(filepath.Join(generation, "platform/agent-secret/installation.secret"), 0600, 1<<20)
	if err != nil || !bytes.Equal(secretAfter, secretBefore) {
		t.Fatal("set-model rotated installation secret")
	}
	adapterAfter, err := runtimeReadLeaf(filepath.Join(generation, "platform/uac-adapter.json"), 0600, 1<<20)
	if err != nil || !bytes.Equal(adapterAfter, adapterBefore) {
		t.Fatal("set-model changed adapter bytes")
	}
	profileAfter, err := runtimeReadLeaf(filepath.Join(generation, "public/profile.yml"), 0600, 2<<20)
	if err != nil || !bytes.Equal(profileAfter, profileBefore) {
		t.Fatal("set-model changed profile bytes")
	}
	run("rollback", true)
	if nasSnapshot(t, generation) != committedSnapshot {
		t.Fatal("model rotation rollback did not restore the previous committed generation")
	}
	if _, err := os.Stat(filepath.Join(out, sharedModelRotationDir)); !os.IsNotExist(err) {
		t.Fatal("rollback left model-rotation evidence")
	}
	if !strings.Contains(run("status", true), "state=committed") {
		t.Fatal("rollback did not restore committed state")
	}
	run("check", true)
	run("set-model", true, "-model", "auto")
	run("commit", true)
	run("check", true)
	if _, err := os.Stat(filepath.Join(out, sharedModelRotationDir)); !os.IsNotExist(err) {
		t.Fatal("commit left model-rotation evidence")
	}
	run("set-model", true, "-model", "auto") // committed same-model idempotent
	run("rollback", false)                   // committed rollback still refused
	run("stage", false, append(append([]string{}, stageArgs[:len(stageArgs)-2]...), "-model", "gpt-6.1-sol")...)
	if nasSnapshot(t, base) != baseBefore {
		t.Fatal("explicit model rotation changed sealed base")
	}
}

func testNASSharedRoleAndReplayPreparation(t *testing.T, data, base, binary string, run func(string, bool, ...string) string, runtimeAction func(string, bool)) {
	t.Helper()
	natsDir := filepath.Join(data, "config/nats")
	if err := os.Mkdir(natsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(natsDir, "nats.conf"), sharedRenderedNATSFixture(t), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(filepath.Join(natsDir, "nats.conf"), 65532, 65532); err != nil {
		t.Fatal(err)
	}
	projected := run("project", true)
	if !strings.HasPrefix(projected, filepath.Join(data, "config/shared-runtime/g-")) {
		t.Fatal("unexpected immutable role projection")
	}
	checkDedicated := func(path string) {
		t.Helper()
		configRaw, err := sharedRoleReadLeaf(filepath.Join(path, "agent/agent.json"), 0, 0600, 4<<20)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Runtime struct {
				Dedicated string `json:"dedicatedProfileFile"`
			} `json:"sharedDshRuntime"`
		}
		if err := json.Unmarshal(configRaw, &config); err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.Join(data, "config/shared-source", sharedInstallationGeneration, "public/dedicated-profile.yml"))
		if os.IsNotExist(err) {
			if config.Runtime.Dedicated != "" {
				t.Fatal("shared-only receipt silently enabled dedicated")
			}
			if _, err := os.Lstat(filepath.Join(path, "agent/dedicated-profile.yml")); !os.IsNotExist(err) {
				t.Fatal("shared-only projection invented a dedicated file")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := sharedRoleReadLeaf(filepath.Join(path, "agent/dedicated-profile.yml"), 0, 0444, 2<<20)
		if err != nil || !bytes.Equal(got, source) || config.Runtime.Dedicated != "/etc/auriga/shared-runtime/dedicated-profile.yml" {
			t.Fatal("actual Agent role lost its approved dedicated bytes/UID/mode/config field", err)
		}
	}
	checkDedicated(projected)
	for name, uid := range map[string]uint32{"platform/platform.properties": 10001, "edge/edge.config.json": 65532, "gateway/bifrost-runtime.json": 10001, "agent/agent.json": 0, "nats/nats.conf": 65532} {
		if _, err := sharedRoleReadLeaf(filepath.Join(projected, name), uid, 0600, 4<<20); err != nil {
			t.Fatal(name, err)
		}
	}
	if run("project", true) != projected {
		t.Fatal("same installation changed immutable role generation")
	}
	if _, err := os.Lstat(filepath.Join(projected, "agent/dedicated-profile.yml")); err == nil {
		leaf := filepath.Join(projected, "agent/dedicated-profile.yml")
		original, err := os.ReadFile(leaf)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(leaf, 0644); err != nil {
			t.Fatal(err)
		}
		run("project", false)
		if err := os.Chmod(leaf, 0444); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(leaf, append(bytes.Clone(original), []byte("\n# changed role projection\n")...), 0444); err != nil {
			t.Fatal(err)
		}
		run("project", false)
		if err := os.WriteFile(leaf, original, 0444); err != nil {
			t.Fatal(err)
		}
		if run("project", true) != projected {
			t.Fatal("restored immutable dedicated projection changed generation")
		}
	}
	replay := func(action string, success bool) string {
		t.Helper()
		command := exec.Command("/bin/sh", "-c", `umask 077; exec "$@"`, "sh", binary, "shared-replay-prepare", "-base", base, "-action", action)
		output, err := command.CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("replay %s success=%v: %v %s", action, success, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	prepare := replay("stage", true)
	if replay("prepare-path", true) != prepare {
		t.Fatal("prepare path drift")
	}
	root := filepath.Join(data, "config/shared-replay-source")
	installer, err := os.ReadFile(filepath.Join(root, "source/installer.nkey"))
	if err != nil {
		t.Fatal(err)
	}
	final, err := os.ReadFile(filepath.Join(projected, "nats/nats.conf"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := os.ReadFile(filepath.Join(prepare, "nats.conf"))
	if err != nil || bytes.Contains(final, installer) || !bytes.Contains(prepared, installer) || bytes.Contains(final, []byte("$JS.API.STREAM.CREATE.KV_auriga-shared-entry")) {
		t.Fatal("temporary installer leaked into final config")
	}
	replay("check", false) // file-only stage is never proof of network provisioning/revocation
	before := nasSnapshot(t, filepath.Join(root, "source"))
	if replay("stage", true) != prepare || nasSnapshot(t, filepath.Join(root, "source")) != before {
		t.Fatal("prepare retry replaced installer identity")
	}
	replay("close", true)
	replay("check", false)
	seed := filepath.Join(root, "source/installer.seed")
	if err := os.Chmod(seed, 0640); err != nil {
		t.Fatal(err)
	}
	replay("stage", false)
	if err := os.Chmod(seed, 0600); err != nil {
		t.Fatal(err)
	}
	bad := []byte("changed immutable private role projection")
	leaf := filepath.Join(projected, "edge/replay.seed")
	original, err := os.ReadFile(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, bad, 0600); err != nil {
		t.Fatal(err)
	}
	run("project", false)
	if err := os.WriteFile(leaf, original, 0600); err != nil {
		t.Fatal(err)
	}
	run("project", true)
	runtimeAction("stage", true)
	if run("project", true) != projected {
		t.Fatal("pending runtime candidate replaced committed shared predecessor")
	}
	runtimeAction("rollback", true)
	if run("project", true) != projected {
		t.Fatal("original rollback changed shared predecessor")
	}
	runtimeAction("stage", true)
	runtimeAction("commit", true)
	newProjection := run("project", true)
	if newProjection == projected {
		t.Fatal("committed runtime did not switch actual shared Host projection")
	}
	if run("project", true) != newProjection {
		t.Fatal("committed shared projection not stable")
	}
	checkDedicated(newProjection)    // signed runtime upgrade keeps the same approved profile
	runtimeAction("rollback", false) // original contract: no implicit rollback after commit
	t.Log("actual role projection and temporary replay source/config stage passed with umask077; readiness remained closed without real NATS provisioning/revocation")
}

func testNASSharedSignedUpgrade(t *testing.T, data, base, binary string, r runtimeInputs) func(string, bool) {
	t.Helper()
	dir := filepath.Dir(r.bundle)
	bundle := filepath.Join(dir, "shared-release8")
	_, err := runtimebundle.Sign(runtimebundle.SignOptions{Image: r.image, ArtifactID: "dsh-test-linux-amd64", Version: "1.2.8", OS: "linux", Arch: "amd64", Sequence: 8, IssuedAt: timestamppb.New(time.Now().Add(-time.Minute)), RootPath: r.root, PrivateKeyPath: filepath.Join(dir, "publisher.pem"), ManifestPath: filepath.Join(dir, "manifest.json"), ConfigPath: filepath.Join(dir, "config.json"), OutputDir: bundle})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bundle, 0755); err != nil {
		t.Fatal(err)
	}
	// Explicit public fixture metadata, never a global umask change or repair of
	// existing source. All actual upgrade validation/mutations use the real CLI.
	out := filepath.Join(data, "config/runtime-source")
	if err := os.Mkdir(out, 0755); err != nil {
		t.Fatal(err)
	}
	return func(action string, success bool) {
		t.Helper()
		output, err := exec.Command(binary, "runtime-upgrade", "-base", base, "-out", out, "-action", action, "-bundle", bundle, "-image", r.image).CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("original runtime %s success=%v: %v %s", action, success, err, output)
		}
	}
}
