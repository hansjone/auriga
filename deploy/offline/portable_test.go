package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableSourceCRLFAndExecutableModes(t *testing.T) {
	root := t.TempDir()
	r := release{Version: "1.0", Build: &buildSpec{}}
	for name, body := range map[string]string{"mvnw": "#!/bin/sh\necho ok\n", "go.mod": "module fixture\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeSourceManifest(root, provenance{Commit: strings.Repeat("a", 40), UpstreamVersion: "1.0", Version: "1.0"}, strings.Repeat("b", 40), r); err != nil {
		t.Fatal(err)
	}
	before, err := dependencyLocks(root, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mvnw", "go.mod"} {
		p := filepath.Join(root, name)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(raw), "\n", "\r\n")), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "snapshot")
	info, err := snapshotSource(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if info.SiteChanges {
		t.Fatal("editor line endings must not become semantic site edits")
	}
	raw, err := os.ReadFile(filepath.Join(target, "mvnw"))
	if err != nil || strings.Contains(string(raw), "\r") {
		t.Fatal(string(raw), err)
	}
	after, err := dependencyLocks(root, r)
	if err != nil || before.Digest != after.Digest {
		t.Fatal("CRLF changed dependency identity", before, after, err)
	}
}

func TestPortablePathsRejectAliasesAndPreserveLongNames(t *testing.T) {
	for _, names := range [][]string{{"A/a.go", "a/b.go"}, {"σ/a.go", "ς/b.go"}, {"CON.txt"}, {"COM¹"}, {"LPT².txt"}, {"a/trailing."}, {"a/name "}} {
		if err := portablePaths(names); err == nil {
			t.Fatal("accepted Windows alias", names)
		}
	}
	root := t.TempDir()
	name := strings.Repeat("deep-folder/", 25) + "main.go"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte("package fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "long.tar")
	if err := pack(root, archive); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "unpack")
	if err := unpack(archive, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, name)); err != nil {
		t.Fatal(err)
	}
}

func TestBuildBudgetRefusesInsufficientAvailableMemory(t *testing.T) {
	budget := buildBudget{MemoryMiB: 4096, CPUs: 2}
	if err := validateBuildBudget(budget, 8<<30, 4<<30); err == nil {
		t.Fatal("must reserve memory for running services")
	}
	if err := validateBuildBudget(budget, 8<<30, 6<<30); err != nil {
		t.Fatal(err)
	}
	if err := validateBuildBudget(buildBudget{MemoryMiB: 64, CPUs: 0}, 8<<30, 8<<30); err == nil {
		t.Fatal("unsafe budget")
	}
}

func TestDockerEndpointRejectsRemoteBeforeConnection(t *testing.T) {
	for _, endpoint := range []string{"tcp://192.0.2.1:2376", "ssh://example.com", "npipe:////remote/pipe/docker"} {
		if err := localDockerEndpoint(endpoint); err == nil {
			t.Fatal(endpoint)
		}
	}
	for _, endpoint := range []string{"unix:///var/run/docker.sock", "npipe:////./pipe/dockerDesktopLinuxEngine"} {
		if err := localDockerEndpoint(endpoint); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchiveCaseCollisionRejectedBeforePublishing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "case.tar")
	out, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(out)
	for _, name := range []string{"Dir/a.go", "dir/b.go"} {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "out")
	err = extractVerified(file, target, "")
	if err == nil || !strings.Contains(err.Error(), "大小写") {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("published invalid archive", err)
	}
}

func TestInstallAfterBuildRequiresOffline(t *testing.T) {
	if err := bundle([]string{"--build", "--install"}); err == nil || !strings.Contains(err.Error(), "--offline") {
		t.Fatal(err)
	}
}
