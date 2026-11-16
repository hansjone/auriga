package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDockerVolumeTransferIntegration(t *testing.T) {
	source, kit := os.Getenv("AURIGA_VOLUME_FIXTURE_SOURCE"), os.Getenv("AURIGA_VOLUME_FIXTURE_KIT")
	if source == "" || kit == "" {
		t.Skip("set the prepared small fixture source and cumulative kit for real Docker volume transport")
	}
	t.Chdir(source)
	if err := verifyCumulative(kit, source, "", ""); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "volume.tar")
	if err := (windowsBackend{Kind: "docker"}).build([]string{"--build", "--offline", "--out", out}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "package")
	if err := extractVerified(out, target, ""); err != nil {
		t.Fatal(err)
	}
	var p provenance
	if err := readJSON(filepath.Join(target, "provenance.json"), &p); err != nil || p.UpstreamVersion == "" {
		t.Fatal(p, err)
	}
}
