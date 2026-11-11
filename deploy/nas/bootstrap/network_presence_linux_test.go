package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hansjone/auriga/deploy/bootstrap/materials"
)

// Reconstruct the historical manifest only inside a disposable test installation.
func TestNASNetworkPresenceLegacyFixture(t *testing.T) {
	base := os.Getenv("AURIGA_TEST_PRESENCE_LEGACY_BASE")
	if base == "" {
		return
	}
	if os.Geteuid() != 10001 {
		t.Fatal("fixed fixture installer required")
	}
	receipt, err := os.ReadFile(filepath.Join(base, "iam-installation/bootstrap.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	set := nasMaterialSet()
	var oldFiles []string
	for _, name := range set.PublicFiles {
		if strings.HasPrefix(name, "network/edge-presence.") {
			if err := os.Remove(filepath.Join(base, name)); err != nil {
				t.Fatal(err)
			}
		} else {
			oldFiles = append(oldFiles, name)
		}
	}
	set.PublicFiles = oldFiles
	path := filepath.Join(base, "edge.config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	delete(config, "devicePresence")
	raw, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(base, "network/broker.conf")
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start, ops := strings.Index(text, "SYS {users: ["), strings.Index(text, `{user: "ops"`)
	if start < 0 || ops < start {
		t.Fatal("Broker fixture missing system users")
	}
	text = text[:start+len("SYS {users: [")] + text[ops:]
	text = strings.ReplaceAll(text, `, "edge-presence"`, "")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(base, "iam-installation/bootstrap.receipt")); err != nil {
		t.Fatal(err)
	}
	if err := materials.SealReceipt(base, strings.Split(string(receipt), "\n")[0], set); err != nil {
		t.Fatal(err)
	}
}

func TestNASNetworkPresenceLegacyReentry(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires fixed installer chown capability")
	}
	root, err := os.MkdirTemp("", "auriga-presence-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bootstrap")
	if raw, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, raw)
	}
	for _, legacy := range []bool{true, false} {
		name := "new-install-missing-materials"
		if legacy {
			name = "sealed-old-install"
		}
		t.Run(name, func(t *testing.T) {
			base := filepath.Join(root, name)
			args := []string{"-out", base, "-access-file", "../access-proxy.example.json", "-edge-peer-ip", "192.0.2.138", "-internal-subnet", "192.0.2.128/26", "-lan-ip", "192.0.2.10", "-lan-subnet", "192.0.2.0/26"}
			if raw, err := exec.Command(binary, args...).CombinedOutput(); err != nil {
				t.Fatalf("install: %v %s", err, raw)
			}
			if legacy {
				helper, err := os.Open("/proc/self/exe")
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("/proc/self/fd/3", "-test.run=^TestNASNetworkPresenceLegacyFixture$")
				cmd.ExtraFiles = []*os.File{helper}
				cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001, Groups: []uint32{10001}}}
				cmd.Env = append(os.Environ(), "AURIGA_TEST_PRESENCE_LEGACY_BASE="+base)
				raw, runErr := cmd.CombinedOutput()
				_ = helper.Close()
				if runErr != nil {
					t.Fatalf("old installation fixture: %v %s", runErr, raw)
				}
			} else {
				for _, name := range []string{"edge-presence.pem", "edge-presence.key", "edge-presence.password"} {
					if err := os.Remove(filepath.Join(base, "network", name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := nasSnapshot(t, base)
			for range 2 {
				raw, err := exec.Command(binary, args...).CombinedOutput()
				if (err == nil) != legacy {
					t.Fatalf("reentry: %v %s", err, raw)
				}
				if nasSnapshot(t, base) != before {
					t.Fatal("sealed sources or receipt changed during reentry")
				}
			}
		})
	}
}

func TestNASNetworkPresenceScripts(t *testing.T) {
	if raw, err := exec.Command("sh", "../test/device-presence.sh").CombinedOutput(); err != nil {
		t.Fatalf("init/render material migration: %v %s", err, raw)
	}
}
