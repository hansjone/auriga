package panel

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestInstallationKeysAndSignedBundle(t *testing.T) {
	root := t.TempDir()
	if err := Generate(root); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "panel-signing.key")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := Generate(root); err == nil {
		t.Fatal("must not silently replace installation keys")
	}
	dist := t.TempDir()
	out := filepath.Join(t.TempDir(), "public")
	if err := os.WriteFile(filepath.Join(dist, "auriga-team-panel.js"), []byte("export const panel = 1;"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	if err := Publish(keyPath, dist, out, "1.0.0", now); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var sig struct{ Value string }
	if err := json.Unmarshal(m["signature"], &sig); err != nil {
		t.Fatal(err)
	}
	delete(m, "signature")
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(sig.Value)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(ed25519.PrivateKey(key).Public().(ed25519.PublicKey), body, signature) {
		t.Fatal("signature differs from canonical manifest")
	}
	cmd := exec.Command("node", "verify-plugin.mjs", root, out, strconv.FormatInt(now.Unix(), 10))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real plugin verification: %v\n%s", err, output)
	}
	if err := Publish(keyPath, dist, out, "0.9.0", now); err == nil {
		t.Fatal("rollback accepted")
	}
	if err := Publish(keyPath, dist, out, "1.0.0", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := Publish(filepath.Join(root, "missing"), dist, out, "1.0.0", now); err == nil {
		t.Fatal("missing key accepted")
	}
	if err := Publish(keyPath, dist, out, "1.0.0", now.Add(-time.Hour)); err == nil {
		t.Fatal("clock rollback accepted")
	}
	if err := os.WriteFile(filepath.Join(dist, "auriga-team-panel.js"), []byte("export const panel = 2;"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Publish(keyPath, dist, out, "1.0.0", now.Add(time.Hour)); err == nil {
		t.Fatal("changed bytes accepted at the same version")
	}
	if err := Publish(keyPath, dist, out, "1.0.1", now.Add(31*24*time.Hour)); err != nil {
		t.Fatal("upgrade/expired-manifest renewal:", err)
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatal("old hashed asset was removed")
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	var tampered map[string]any
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered["version"] = "9.0.0"
	bad, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), bad, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Publish(keyPath, dist, out, "10.0.0", now.Add(32*24*time.Hour)); err == nil {
		t.Fatal("tampered previous manifest accepted")
	}
}
