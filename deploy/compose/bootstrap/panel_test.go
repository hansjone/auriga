package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPanelInstallationIsIndependentAndReceipted(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bootstrap")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	proofs, image := runtimeProofFixture(t, "fake")
	out := filepath.Join(root, "installation")
	args := []string{"-out", out, "-image", image, "-fixture", "fake", "-runtime-proofs", proofs}
	invoke := func() error { return exec.Command(binary, args...).Run() }
	if err := invoke(); err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	key := read("panel-signing.key")
	public := read("panel-keys.json")
	identity := read("identity-keys.json")
	defer clear(key)
	var set, ids struct {
		Keys []map[string]string `json:"keys"`
	}
	if json.Unmarshal(public, &set) != nil || json.Unmarshal(identity, &ids) != nil || len(set.Keys) != 1 || len(ids.Keys) != 1 {
		t.Fatal("missing public key sets")
	}
	x, err := base64.RawURLEncoding.DecodeString(set.Keys[0]["x"])
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != ed25519.PrivateKeySize || !bytes.Equal(x, ed25519.PrivateKey(key).Public().(ed25519.PublicKey)) || set.Keys[0]["x"] == ids.Keys[0]["x"] || set.Keys[0]["d"] != "" {
		t.Fatal("panel signing identity is not independent/public-only")
	}
	var config struct {
		Static struct {
			PanelRoot string `json:"panelRoot"`
		} `json:"static"`
	}
	if json.Unmarshal(read("edge.config.json"), &config) != nil || config.Static.PanelRoot != "/srv/auriga/panel" {
		t.Fatal("panel root missing from actual bootstrap output")
	}
	if err := invoke(); err != nil {
		t.Fatal("repeated prepare:", err)
	}
	if !bytes.Equal(key, read("panel-signing.key")) || !bytes.Equal(public, read("panel-keys.json")) {
		t.Fatal("prepare silently rotated panel keys")
	}
	if err := os.WriteFile(filepath.Join(out, "panel-keys.json"), []byte(`{"keys":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := invoke(); err == nil {
		t.Fatal("tampered public key accepted by receipt")
	}
	if err := os.WriteFile(filepath.Join(out, "panel-keys.json"), public, 0644); err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte{}, key...)
	corrupted[0] ^= 1
	defer clear(corrupted)
	if err := os.WriteFile(filepath.Join(out, "panel-signing.key"), corrupted, 0600); err != nil {
		t.Fatal(err)
	}
	if err := invoke(); err == nil {
		t.Fatal("tampered private key accepted by receipt")
	}
}
