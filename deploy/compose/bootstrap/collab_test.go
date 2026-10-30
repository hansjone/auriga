package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/gen/deploymentconfig"
)

func TestCollabInputRefusesIncompleteAndHTTP(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(file, []byte(`{"tenantId":"`+composeTenant+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, access, _ := marshalAccess(composeAccess("auriga-m2-edge"))
	if _, err := readCollabInput(file, access); err == nil {
		t.Fatal("HTTP collab enable accepted")
	}
}

func collabFixture(t *testing.T) (string, deploymentconfig.Config) {
	t.Helper()
	dir := t.TempDir()
	a := customAccess("domain")
	a.PortalOrigin.Scheme, a.AdminOrigin.Scheme, a.IssuerOrigin.Scheme = "https", "https", "https"
	a.Listeners.Portal.Tls, a.Listeners.Admin.Tls = true, true
	a.InsecureHttpAcknowledged = false
	a.Instances.AccessInstancesSubdomain.Scheme = "https"
	_, access, err := marshalAccess(a)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) string {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	root, private, _ := ed25519.GenerateKey(rand.Reader)
	rootDER, _ := x509.MarshalPKIXPublicKey(root)
	if err := os.WriteFile(filepath.Join(dir, "root.der"), rootDER, 0600); err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	until := issued.Add(time.Hour)
	keys := []map[string]any{}
	stateKeys := []map[string]any{}
	secrets := map[string]string{}
	for i, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		kid := fmt.Sprintf("approved-%d", i)
		pub, secret, _ := ed25519.GenerateKey(rand.Reader)
		der, _ := x509.MarshalPKIXPublicKey(pub)
		pk, _ := x509.MarshalPKCS8PrivateKey(secret)
		path := filepath.Join(dir, kid+".der")
		if err := os.WriteFile(path, pk, 0600); err != nil {
			t.Fatal(err)
		}
		secrets[kid] = path
		keys = append(keys, map[string]any{"kid": kid, "alg": "Ed25519", "purpose": purpose, "public_key_spki": base64.RawURLEncoding.EncodeToString(der), "not_before": issued.Format(time.RFC3339), "not_after": until.Format(time.RFC3339), "state": "active"})
		stateKeys = append(stateKeys, map[string]any{"kid": kid, "realm": "1", "purpose": purpose, "public_key_spki": base64.RawURLEncoding.EncodeToString(der), "not_before": issued.Format(time.RFC3339), "not_after": until.Format(time.RFC3339), "state": "ACTIVE"})
	}
	payload := map[string]any{"schema_version": 1, "realm": "1", "keyset_version": "7", "issued_at": issued.Format(time.RFC3339), "valid_until": until.Format(time.RFC3339), "keys": keys}
	b, _ := json.Marshal(payload)
	signature := ed25519.Sign(private, append([]byte("Auriga.NetworkKeyset.v1\x00"), b...))
	sum := sha256.Sum256(b)
	announcement := map[string]any{"keyset_version": "7", "valid_until": until.Format(time.RFC3339), "manifest_digest": fmt.Sprintf("sha256:%x", sum), "root_kid": "offline-root"}
	ab, _ := json.Marshal(announcement)
	announcement["root_signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, append([]byte("Auriga.NetworkKeysetAnnouncement.v1\x00"), ab...)))
	rootSum := sha256.Sum256(rootDER)
	input := collabInput{TenantID: composeTenant, Realm: "1", RootKid: "offline-root", RootFingerprint: fmt.Sprintf("sha256:%x", rootSum), MinimumVersion: "7", RootPublicFile: filepath.Join(dir, "root.der"), SignedFile: write("signed.json", map[string]any{"keyset": payload, "root_auth": map[string]any{"alg": "Ed25519", "kid": "offline-root", "signature": base64.RawURLEncoding.EncodeToString(signature)}}), AnnouncementFile: write("announcement.json", announcement), StateFile: write("state.json", map[string]any{"version": "7", "issued_at": issued.Format(time.RFC3339), "keys": stateKeys}), PrivateFiles: secrets}
	return write("input.json", input), access
}

func TestCollabInputMetadataFailures(t *testing.T) {
	for _, name := range []string{"valid", "tenant", "realm", "fingerprint", "minimum", "missing-secret", "wrong-secret", "unknown-field", "private-readable", "private-owner", "private-hardlink"} {
		t.Run(name, func(t *testing.T) {
			file, access := collabFixture(t)
			raw, _ := os.ReadFile(file)
			var input map[string]any
			_ = json.Unmarshal(raw, &input)
			if strings.HasPrefix(name, "private-") {
				if runtime.GOOS != "linux" || os.Geteuid() != 0 {
					t.Skip("Linux root metadata guard")
				}
				path := input["privateFiles"].(map[string]any)["approved-1"].(string)
				var e error
				switch name {
				case "private-readable":
					e = os.Chmod(path, 0644)
				case "private-owner":
					e = os.Chown(path, 10002, 10002)
				case "private-hardlink":
					e = os.Link(path, path+".other")
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			switch name {
			case "tenant":
				input["tenantId"] = "00000000-0000-0000-0000-000000000002"
			case "realm":
				input["realm"] = "2"
			case "fingerprint":
				input["rootFingerprint"] = "sha256:" + strings.Repeat("0", 64)
			case "minimum":
				input["minimumVersion"] = "8"
			case "missing-secret":
				delete(input["privateFiles"].(map[string]any), "approved-1")
			case "wrong-secret":
				input["privateFiles"].(map[string]any)["approved-1"] = input["privateFiles"].(map[string]any)["approved-0"]
			case "unknown-field":
				input["rootPrivateFile"] = "must-never-read"
			}
			raw, _ = json.Marshal(input)
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			c, err := readCollabInput(file, access)
			defer c.clear()
			if (err == nil) != (name == "valid") {
				t.Fatalf("unexpected result: %v", err)
			}
		})
	}
}

func TestCollabProjectionAndReceipt(t *testing.T) {
	file, access := collabFixture(t)
	c, err := readCollabInput(file, access)
	if err != nil {
		t.Fatal(err)
	}
	defer c.clear()
	root := t.TempDir()
	binary := os.Getenv("AURIGA_COLLAB_TEST_BOOTSTRAP")
	if binary == "" {
		binary = filepath.Join(root, "bootstrap")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		if b, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, b)
		}
	}
	proofs, image := runtimeProofFixture(t, "real")
	out := filepath.Join(root, "installation")
	a := customAccess("domain")
	a.PortalOrigin.Scheme = "https"
	a.AdminOrigin.Scheme = "https"
	a.IssuerOrigin.Scheme = "https"
	a.Listeners.Portal.Tls = true
	a.Listeners.Admin.Tls = true
	a.InsecureHttpAcknowledged = false
	a.Instances.AccessInstancesSubdomain.Scheme = "https"
	raw, _ := json.Marshal(a)
	accessFile := filepath.Join(root, "access.json")
	if err := os.WriteFile(accessFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ca, cert, _ := mustCA()
	browser, key := mustIssue(ca, cert, "browser", "auriga://test/browser", []string{a.PortalOrigin.Host, a.AdminOrigin.Host, "*." + a.Instances.AccessInstancesSubdomain.ParentDomain}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	certFile, keyFile := filepath.Join(root, "browser.crt"), filepath.Join(root, "browser.key")
	if err := os.WriteFile(certFile, browser, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, key, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-out", out, "-access-file", accessFile, "-edge-container", "public-test-edge", "-image", image, "-fixture", "real", "-runtime-proofs", proofs, "-https-cert-file", certFile, "-https-key-file", keyFile, "-network-collab-file", file}
	if runtime.GOOS != "linux" {
		if b, e := exec.Command(binary, args...).CombinedOutput(); e == nil || !strings.Contains(string(b), "activation requires Linux") {
			t.Fatalf("non-Linux activation accepted/wrong error: %v %s", e, b)
		}
		if _, e := os.Lstat(out); !os.IsNotExist(e) {
			t.Fatal("non-Linux activation wrote material")
		}
		if b, e := exec.Command(binary, append(args, "-validate-access")...).CombinedOutput(); e != nil {
			t.Fatalf("non-Linux validation refused: %v %s", e, b)
		}
		return
	}
	for i := 0; i < 2; i++ {
		if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("generate/reenter: %v %s", err, b)
		}
	}
	read := func(path string) string {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(out, path))
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(read("network/runtime.json")), &cfg)
	if cfg["collab"].(map[string]any)["Target"] != "127.0.0.1:9092" || cfg["iam"].(map[string]any)["Target"] != "127.0.0.1:9091" || cfg["protocolSigning"].(map[string]any)["keyId"] != "approved-1" {
		t.Fatal("gateway not bound to approved N1 component")
	}
	properties := read("network-platform/platform.properties")
	var managed struct {
		PlatformOrigin string `json:"platformOrigin"`
		Issuer         string `json:"issuer"`
		Network        struct {
			Realm  string `json:"realm"`
			WSSURL string `json:"wssUrl"`
			Roots  []struct {
				Kid       string `json:"kid"`
				PublicKey string `json:"public_key_spki"`
			} `json:"roots"`
		} `json:"network"`
	}
	if err := json.Unmarshal([]byte(read("network-platform/managed-network.json")), &managed); err != nil {
		t.Fatal(err)
	}
	if managed.PlatformOrigin != access.Issuer() || managed.Issuer != access.Issuer() ||
		managed.Network.Realm != c.Realm || managed.Network.WSSURL != strings.Replace(access.Issuer(), "https://", "wss://", 1)+"/net/v1/ws" ||
		len(managed.Network.Roots) != 1 || managed.Network.Roots[0].Kid != c.RootKid || managed.Network.Roots[0].PublicKey != base64.RawURLEncoding.EncodeToString(c.files["root.der"]) {
		t.Fatal("managed network installation does not preserve approved origin/realm/root")
	}
	if !strings.Contains(properties, "auriga.workspace.managed-network-installation-file=/etc/auriga/network/managed-network.json") {
		t.Fatal("managed network provisioning not enabled")
	}
	if err := checkNativeClient(out); err != nil {
		t.Fatalf("N1 projection lost native client: %v", err)
	}
	for _, want := range []string{"team-token-export.enabled=true", "team-token-export.collab.reader-group=65534", "team-token-export.ai-gateway.reader-group=65533", "ai-gateway-token-export.enabled=false"} {
		if !strings.Contains(properties, want) {
			t.Fatalf("missing issuer setting %s", want)
		}
	}
	if read("network-platform/gateway.secret") == read("network-platform/collab.secret") {
		t.Fatal("identities share secret")
	}
	if !strings.Contains(read("edge.config.json"), `"collabHop"`) || !strings.Contains(read("collab-installation/collab.properties"), access.Issuer()+"/oauth2/jwks") {
		t.Fatal("HTTP/JWKS wiring absent")
	}
	if !strings.Contains(read("access.env"), "COMPOSE_FILE='docker-compose.yml:scripts/collab-required.yml'") {
		t.Fatal("mandatory collab overlay omitted")
	}
	before := installationSnapshot(t, out)
	if err := os.WriteFile(filepath.Join(out, "collab-installation", "state.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	mutated := installationSnapshot(t, out)
	if before == mutated {
		t.Fatal("fixture mutation failed")
	}
	if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil {
		t.Fatalf("receipt accepted changed state: %s", b)
	}
	if mutated != installationSnapshot(t, out) {
		t.Fatal("rejection modified source")
	}
}

func TestCollabWiringIsolation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(raw), "\n  collab:\n")
	if !ok {
		t.Fatal("independent collab absent")
	}
	block, _, _ = strings.Cut(block, "\n  edge:\n")
	for _, setting := range []string{"profiles: [network-collab]", "user: '10002:10002'", "read_only: true", "no-new-privileges:true", "network_mode: host", "collab-tokens:/run/auriga-collab-token:ro"} {
		if !strings.Contains(block, setting) {
			t.Fatalf("missing collab isolation: %s", setting)
		}
	}
	for _, forbidden := range []string{"/var/pki:", "network-tokens:", "browser.key", "gateway.secret", "ports:"} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("collab excess material: %s", forbidden)
		}
	}
}
