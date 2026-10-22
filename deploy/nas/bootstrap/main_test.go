package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNASPublicAccessCLI(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	for _, mode := range []string{"domain-proxy", "ip-http"} {
		t.Run(mode, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "deployment", "fixtures", mode+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var a map[string]any
			if err := json.Unmarshal(raw, &a); err != nil {
				t.Fatal(err)
			}
			// Recipe supports direct HTTP, while the production loader owns all semantics.
			a["ingress"] = map[string]any{"kind": "direct"}
			a["insecureHttpAcknowledged"] = true
			for _, key := range []string{"portalOrigin", "adminOrigin", "issuerOrigin"} {
				a[key].(map[string]any)["scheme"] = "http"
			}
			if inst := a["instances"].(map[string]any); inst["mode"] == "subdomain" {
				inst["scheme"] = "http"
			}
			for _, v := range a["listeners"].(map[string]any) {
				v.(map[string]any)["tls"] = false
				v.(map[string]any)["address"] = "0.0.0.0"
			}
			control := a["instanceControl"].(map[string]any)
			control["edgeContainer"] = "auriga-nas-test-edge"
			control["origin"].(map[string]any)["scheme"] = "http"
			a["tenantId"] = "ffffffff-ffff-4fff-8fff-ffffffffffff"
			raw, _ = json.Marshal(a)
			path := filepath.Join(root, mode+".json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(root, mode+"-out")
			args := []string{"-validate-access", "-out", out, "-access-file", path, "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26"}
			if b, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
				t.Fatalf("valid %s: %v %s", mode, err, b)
			}
			if _, err := os.Lstat(out); !os.IsNotExist(err) {
				t.Fatal("validation wrote installation output")
			}
			if b, err := exec.Command(binary, args[:len(args)-4]...).CombinedOutput(); err == nil {
				t.Fatalf("missing LAN isolation input accepted: %s", b)
			}
			// Unknown/duplicate JSON fields must fail in the production loader before writes.
			for _, bad := range [][]byte{append([]byte(`{"unknown":1,`), raw[1:]...), append([]byte(`{"mode":"ip",`), raw[1:]...)} {
				if err := os.WriteFile(path, bad, 0o600); err != nil {
					t.Fatal(err)
				}
				if b, err := exec.Command(binary, args...).CombinedOutput(); err == nil {
					t.Fatalf("malformed accepted: %s", b)
				}
				if _, err := os.Lstat(out); !os.IsNotExist(err) {
					t.Fatal("invalid input wrote installation output")
				}
			}
		})
	}
}

func TestNASLANIsolationInputs(t *testing.T) {
	if err := validateNASLAN("192.0.2.10", "192.0.2.0/26", "192.0.2.128/26"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ address, prefix string }{
		{"", "192.0.2.0/26"}, {"192.0.2.10", ""}, {"::ffff:192.0.2.10", "192.0.2.0/26"},
		{"192.0.2.010", "192.0.2.0/26"}, {"192.0.2.10", "192.0.2.0/24"},
		{"192.0.2.138", "192.0.2.128/26"}, {"198.51.100.10", "192.0.2.0/26"},
		{"192.0.2.0", "192.0.2.0/26"}, {"192.0.2.1", "192.0.2.0/26"}, {"192.0.2.63", "192.0.2.0/26"},
		{"127.0.0.2", "127.0.0.0/26"},
	} {
		if err := validateNASLAN(test.address, test.prefix, "192.0.2.128/26"); err == nil {
			t.Errorf("invalid LAN isolation input accepted: %+v", test)
		}
	}
}

func TestNASPublicProxyAndDirectExamples(t *testing.T) {
	for _, mode := range []string{"domain", "ip", "proxy"} {
		raw, err := os.ReadFile("../access-" + mode + ".example.json")
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "access.json")
		if err := os.WriteFile(file, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		cfgRaw, cfg, err := loadNASAccess(file, "192.0.2.138", "192.0.2.128/26", 15000)
		if err != nil || len(cfgRaw) == 0 || !cfg.Valid() {
			t.Fatalf("%s: %v", mode, err)
		}
		if mode == "proxy" {
			var doc map[string]any
			json.Unmarshal(raw, &doc)
			doc["listeners"].(map[string]any)["portal"].(map[string]any)["tls"] = false
			bad, _ := json.Marshal(doc)
			os.WriteFile(file, bad, 0o600)
			if _, _, err := loadNASAccess(file, "192.0.2.138", "192.0.2.128/26", 15000); err == nil {
				t.Fatal("trusted proxy plaintext hop accepted")
			}
		}
	}
}
