package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNASGatewayProjectionRejectsTamperAndKeepsReceipts(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	f.request(t)
	if e := f.r.publish(); e != nil {
		t.Fatal(e)
	}
	if e := f.r.install(); e != nil {
		t.Fatal(e)
	}
	nasEnableActivationFixture(t, f)
	if e := f.r.engine.activate(f.r); e != nil {
		t.Fatal(e)
	}
	path, _ := nasAssertGatewayPair(t, f)
	rows, e := f.r.releases()
	if e != nil {
		t.Fatal(e)
	}
	dir, _, e := f.r.installed(rows, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.r.checkGatewayRuntime(path, dir); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(before, []byte("9007199254740993")) {
		t.Fatal("projection rounded unrelated source fields")
	}
	for _, bad := range []string{"bytes", "mode", "link"} {
		t.Run(bad, func(t *testing.T) {
			switch bad {
			case "bytes":
				if e := os.WriteFile(path, []byte("{}"), 0600); e != nil {
					t.Fatal(e)
				}
				defer os.WriteFile(path, before, 0600)
			case "mode":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
				defer os.Chmod(path, 0600)
			case "link":
				if e := os.Rename(path, path+".saved"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(path+".saved", path); e != nil {
					t.Fatal(e)
				}
				defer func() { _ = os.Remove(path); _ = os.Rename(path+".saved", path) }()
			}
			if _, e := f.r.gatewayRuntime(dir); e == nil {
				t.Fatal("changed gateway projection silently repaired/admitted")
			}
			if e := f.r.checkGatewayRuntime(path, dir); e == nil {
				t.Fatal("invalid consumer projection accepted by stop gate")
			}
		})
	}
	if _, _, e = f.r.installed(rows, true); e != nil {
		t.Fatalf("activation changed receipted installation: %v", e)
	}
	// Public start uses the legacy budget source. The next tick must safely
	// migrate existing signers through the production ownership/stop checks.
	for id, c := range f.containers {
		if c.Config.Labels["com.docker.compose.service"] != "network" {
			continue
		}
		for i, m := range c.Mounts {
			if m.Destination == "/etc/auriga/network/runtime.json" {
				c.Mounts[i].Source = filepath.Join(f.r.data, "config/network-policy/runtime.json")
			}
		}
		f.containers[id] = c
	}
	if e = f.r.engine.activate(f.r); e != nil {
		t.Fatal(e)
	}
	nasAssertGatewayPair(t, f)
}

func TestNASQueueGatewayPromotionAndCrashRecovery(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	r := f.r
	grid := time.Now().UTC().Truncate(12 * time.Hour)
	// Seed the preceding approved publication with next keys whose modeled PG
	// firstPublished is already 36h old. The queue's current slot then promotes
	// them without moving the OS clock or weakening publish's real-time checks.
	for i := range f.payload.Keys {
		f.payload.Keys[i].Before = grid.Add(-48 * time.Hour).Format(time.RFC3339)
		f.payload.Keys[i].After = grid.Add(24 * time.Hour).Format(time.RFC3339)
	}
	base, private, e := nasRotationRequest(f.payload, f.status.First, grid.Add(-24*time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		for _, key := range private {
			clear(key)
		}
	}()
	base.Version = "7"
	base.Issued = grid.Add(-time.Hour).Format(time.RFC3339)
	base.Until = grid.Add(23 * time.Hour).Format(time.RFC3339)
	f.payload = base
	for _, key := range base.Keys {
		if key.State == "next" {
			f.status.First[key.Kid] = grid.Add(-36 * time.Hour).Format(time.RFC3339Nano)
		}
	}
	for kid, key := range private {
		nasRotationTestWrite(t, filepath.Join(r.root, "requests/6/keys", kid+".der"), key)
	}
	f.sign(t, filepath.Join(r.source, "collab-installation"), f.payload, false)
	if e = r.queuePrepareAt(nil, 1, io.Discard, grid.Add(-12*time.Hour)); e != nil {
		t.Fatal(e)
	}
	q := approveNASQueue(t, f)
	if e = r.queueImport(io.Discard); e != nil {
		t.Fatal(e)
	}
	x := nasEnableActivationFixture(t, f)
	at := timeMustParse(t, q.Entries[0].Issued)
	x.failNetworkStart = true
	if e = r.queueTickAt(nil, io.Discard, at); e == nil {
		t.Fatal("injected activation crash did not surface")
	}
	rows, e := r.releases()
	if e != nil {
		t.Fatal(e)
	}
	if e = r.queueTickAt(rows, io.Discard, at); e != nil {
		t.Fatal(e)
	}
	runtimePath, privatePath := nasAssertGatewayPair(t, f)
	created := x.created
	if e = r.queueTickAt(rows, io.Discard, at); e != nil {
		t.Fatal(e)
	}
	if x.created != created {
		t.Fatal("repeat tick recreated an already matching consumer")
	}
	promoted := false
	for _, key := range base.Keys {
		if key.Purpose != "protocol_response" || key.State != "next" {
			continue
		}
		for _, current := range f.payload.Keys {
			if current.Kid == key.Kid && current.State == "active" {
				promoted = true
			}
		}
	}
	if !promoted {
		t.Fatal("queue never exercised next to active protocol key promotion")
	}
	nasCheckProductionGatewayConsumer(t, f, runtimePath, privatePath)
}

func nasCheckProductionGatewayConsumer(t *testing.T, f *nasRotationFixture, runtimePath, privatePath string) {
	t.Helper()
	repo, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	work := t.TempDir()
	input := filepath.Join(work, "input.json")
	output := filepath.Join(work, "frames.json")
	nasRotationTestJSON(t, input, map[string]string{"runtime": runtimePath, "privateKey": privatePath, "legacyRuntime": filepath.Join(f.r.data, "config/network-policy/runtime.json"), "output": output})
	virtual := filepath.Join(repo, "infra/ai-gateway/internal/network/nas_activation_consumer_test.go")
	overlay := filepath.Join(work, "overlay.json")
	nasRotationTestJSON(t, overlay, map[string]any{"Replace": map[string]string{virtual: filepath.Join(repo, "deploy/nas/test/gateway-protocol-consumer.go.fixture")}})
	// Overlay compiles the thin test adapter beside the unchanged production
	// signer. No file is created/edited under infra and no runtime is started.
	cmd := exec.Command("go", "test", "-vet=off", "-overlay", overlay, "./internal/network", "-run", "^TestNASActivatedGatewayProtocolConsumer$", "-count=1", "-v")
	cmd.Dir = filepath.Join(repo, "infra/ai-gateway")
	cmd.Env = append(os.Environ(), "AURIGA_NAS_PROTOCOL_INPUT="+input)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("production gateway signer: %v\n%s", e, b)
	} else {
		t.Log(strings.TrimSpace(string(b)))
	}
	version := f.payload.Version
	check := filepath.Join(work, "verify.json")
	nasRotationTestJSON(t, check, map[string]string{"frames": output, "signed": filepath.Join(f.r.root, "installs", version, "config/signed.json"), "root": filepath.Join(f.r.source, "collab-installation/root.der")})
	cmd = exec.Command("node", filepath.Join(repo, "deploy/nas/test/gateway-protocol-verify.mjs"), check)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("production plugin verifier: %v\n%s", e, b)
	} else {
		t.Log(strings.TrimSpace(string(b)))
	}
}
