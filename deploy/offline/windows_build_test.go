package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsTransferPreservesModesAndCanonicalSource(t *testing.T) {
	source := t.TempDir()
	single := fixtureSingleKit(t, "1.0", "a")
	kit, err := readDevkit(single)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "mvnw"), []byte("#!/bin/sh\necho original\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := provenance{Commit: strings.Repeat("a", 40), UpstreamVersion: "1.0", Version: "1.0"}
	if err := signedTestSourceManifest(t, source, p, strings.Repeat("b", 40), kit.Release); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin/auriga-cli"), []byte("fixture-linux-cli"), 0644); err != nil {
		t.Fatal(err)
	}
	locks, err := dependencyLocks(source, kit.Release)
	if err != nil {
		t.Fatal(err)
	}
	kit.Locks = &locks
	if err := jsonFile(filepath.Join(single, "devkit.json"), kit, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(single); err != nil {
		t.Fatal(err)
	}
	key, _ := testSigningKeys(t)
	signTestRoot(t, source, key)
	signTestRoot(t, single, key)
	full := filepath.Join(t.TempDir(), "full")
	if _, err := composeDevkit("", single, "1.0", p.Commit, full, key, 1<<30, true); err != nil {
		t.Fatal(err)
	}
	if err := verifyCumulative(full, source, "1.0", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "new script.sh"), []byte("#!/bin/sh\r\necho new\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(source)
	transfer, err := prepareSiteTransfer([]string{"--build", "--offline", "--sign-key", key, "--out", filepath.Join(t.TempDir(), "out.tar")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(transfer.Root) }()
	if !strings.HasPrefix(transfer.Version, "1.0-site.") {
		t.Fatal(transfer.Version)
	}
	var buf bytes.Buffer
	if err := transfer.stream(&buf); err != nil {
		t.Fatal(err)
	}
	r := tar.NewReader(&buf)
	found := false
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "source/new script.sh" {
			raw, err := io.ReadAll(r)
			if err != nil || h.Mode != 0755 || bytes.Contains(raw, []byte("\r")) {
				t.Fatal(h, string(raw), err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("new script missing")
	}
}

func TestWindowsBackendArgumentValidation(t *testing.T) {
	b, rest, err := parseWindowsBackend([]string{"changes", "export", "--backend=wsl", "--wsl-distro", "Ubuntu", "--out", "a b.tar"})
	if err != nil || b.Kind != "wsl" || b.Distro != "Ubuntu" || rest[len(rest)-1] != "a b.tar" {
		t.Fatal(b, rest, err)
	}
	if _, _, err := parseWindowsBackend([]string{"--backend", "remote"}); err == nil {
		t.Fatal("accepted unsupported backend")
	}
	if _, _, err := parseWindowsBackend([]string{"--wsl-distro", "-bad"}); err == nil {
		t.Fatal("accepted option injection")
	}
}

func TestWindowsBuildRequiresOfflineBeforeDockerAccess(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := (windowsBackend{Kind: "docker"}).build([]string{"--build"}); err == nil || !strings.Contains(err.Error(), "--offline") {
		t.Fatal(err)
	}
}
