package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		t.Run(name, func(t *testing.T) { testNASSharedSourceVariant(t, parent, binary, input, teamOnly) })
	}
}

func testNASSharedSourceVariant(t *testing.T, parent, binary, input string, teamOnly bool) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("existing NAS root fixture must invoke shared source lifecycle")
	}
	r, _ := runtimeTestFixture(t, parent) // original production signer and real verification inputs
	name := "shared-data-company"
	if teamOnly {
		name = "shared-data-team"
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
	run := func(action string, success bool, extra ...string) string {
		t.Helper()
		arguments := []string{"shared-runtime", "-action", action, "-base", base, "-out", out}
		if action == "stage" || action == "commit" || action == "rollback" {
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
	run("stage", true, stageArgs...)
	generation := filepath.Join(out, sharedInstallationGeneration)
	stat, err := os.Stat(filepath.Join(generation, "public/host.json"))
	if err != nil || stat.Mode().Perm() != 0444 {
		t.Fatal("actual private-umask stage did not produce exact public mode", err)
	}
	if _, err := os.Stat(filepath.Join(out, ".pending")); !os.IsNotExist(err) {
		t.Fatal("successful full stage left interrupted evidence")
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
	t.Log("actual sealed bootstrap + signed runtime + enrolled public Node + umask077 stage/resume/commit/check completed without services")
}
