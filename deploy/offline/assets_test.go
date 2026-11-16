package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMultipartRoundTripAndRejection(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "payload"), []byte(strings.Repeat("fixture", 100)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(src); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "auriga-offline-1.tar")
	if err := pack(src, archive); err != nil {
		t.Fatal(err)
	}
	assets, err := splitAsset(archive, 1024, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) < 3 {
		t.Fatal(assets)
	}
	dest := filepath.Join(root, "unpacked")
	if err := extractVerified(archive+".part001", dest, ""); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(dest); err != nil {
		t.Fatal(err)
	}
	part := archive + ".part002"
	raw, err := os.ReadFile(part)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 1
	if err := os.WriteFile(part, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := extractVerified(archive+".parts.json", filepath.Join(root, "bad"), ""); err == nil {
		t.Fatal("accepted tampered part")
	}
	if err := os.Remove(part); err != nil {
		t.Fatal(err)
	}
	if err := extractVerified(archive+".part001", filepath.Join(root, "missing"), ""); err == nil {
		t.Fatal("accepted missing part")
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatal("published partial extraction")
	}
	if effectivePartLimit(8<<30) != (4<<30)-1 {
		t.Fatal("FAT32 cap missing")
	}
}

func TestLocksReuseAndExplain(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.com"}} {
		if _, err := gitAt(root, args...); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"go.mod": "module fixture\ngo 1.27.0\n", "main.go": "original"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitAt(root, "add", "."); err != nil {
		t.Fatal(err)
	}
	r := release{Build: &buildSpec{Inputs: artifact{SHA256: strings.Repeat("a", 64)}, Recipes: artifact{SHA256: strings.Repeat("b", 64)}}}
	first, err := dependencyLocks(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	same, err := dependencyLocks(root, r)
	if err != nil || first.Digest != same.Digest {
		t.Fatal("code-only change invalidated dependencies", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("changed dependencies"), 0644); err != nil {
		t.Fatal(err)
	}
	different, err := dependencyLocks(root, r)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == different.Digest || !strings.Contains(lockDifferences(first, different), "go.mod") {
		t.Fatal("lock mismatch not explained")
	}
	r.Build.Inputs.SHA256 = strings.Repeat("c", 64)
	pins, err := dependencyLocks(root, r)
	if err != nil || pins.Digest == different.Digest {
		t.Fatal("build inputs omitted", err)
	}
}

func TestSignatureRejectsOversizedManifest(t *testing.T) {
	_, public := testSigningKeys(t)
	file := filepath.Join(t.TempDir(), "kit.tar.parts.json")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate((4 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifyFileSignature(file, public); err == nil || !strings.Contains(err.Error(), "大小限制") {
		t.Fatal("unbounded signed manifest read", err)
	}
}
