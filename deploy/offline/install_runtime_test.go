package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRuntimeArguments(t *testing.T) {
	base := []string{"--address", "192.0.2.10", "--data", "/srv/auriga-test"}
	image := "127.0.0.1:15000/auriga/dsh@sha256:" + strings.Repeat("a", 64)
	runtime := []string{"--runtime-bundle", "/srv/releases/runtime-1", "--runtime-root", "/srv/operator/root.pb", "--runtime-image", image}
	o, err := parseInstall(append(append([]string{}, base...), runtime...))
	if err != nil || o.Runtime.Bundle != "/srv/releases/runtime-1" || o.Runtime.Root != "/srv/operator/root.pb" || o.Runtime.Image != image {
		t.Fatalf("runtime install flags lost: %+v %v", o.Runtime, err)
	}
	for name, flags := range map[string][]string{
		"bundle only":      runtime[:2],
		"root only":        runtime[2:4],
		"image only":       runtime[4:],
		"missing image":    runtime[:4],
		"empty flags":      {"--runtime-bundle", "", "--runtime-root", "", "--runtime-image", ""},
		"relative bundle":  {"--runtime-bundle", "runtime-1", "--runtime-root", "/srv/operator/root.pb", "--runtime-image", image},
		"relative root":    {"--runtime-bundle", "/srv/releases/runtime-1", "--runtime-root", "root.pb", "--runtime-image", image},
		"parent traversal": {"--runtime-bundle", "/srv/releases/../runtime-1", "--runtime-root", "/srv/operator/root.pb", "--runtime-image", image},
		"tag":              {"--runtime-bundle", "/srv/releases/runtime-1", "--runtime-root", "/srv/operator/root.pb", "--runtime-image", "auriga/dsh:latest"},
		"bare config id":   {"--runtime-bundle", "/srv/releases/runtime-1", "--runtime-root", "/srv/operator/root.pb", "--runtime-image", "sha256:" + strings.Repeat("a", 64)},
		"duplicate":        append(append([]string{}, runtime...), "--runtime-root=/srv/operator/other.pb"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseInstall(append(append([]string{}, base...), flags...)); err == nil {
				t.Fatal("unsafe or incomplete runtime request accepted")
			}
		})
	}
}

func TestInstallRuntimeEnvUsesPinnedSnapshot(t *testing.T) {
	base, err := filepath.Abs("../nas")
	if err != nil {
		t.Fatal(err)
	}
	pkg := t.TempDir()
	if err := os.Mkdir(filepath.Join(pkg, "deploy-nas"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(filepath.Join(base, "env.example"), filepath.Join(pkg, "deploy-nas/env.example"), 0644); err != nil {
		t.Fatal(err)
	}
	s := state{Data: t.TempDir(), Package: pkg, Runtime: &installRuntime{Source: installRuntimeOptions{Bundle: "/media/runtime-1", Root: "/media/operator/root.pb", Image: "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("b", 64)}}}
	if err := makeEnv(s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Data, "stack.env"))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range installRuntimeEnv(s) {
		if !strings.Contains(string(raw), key+"="+value+"\n") {
			t.Fatal("pinned runtime not propagated", key)
		}
	}
	if strings.Contains(string(raw), "/media/runtime-1") || strings.Contains(string(raw), "/media/operator/root.pb") {
		t.Fatal("mutable removable inputs leaked into init environment")
	}
}
