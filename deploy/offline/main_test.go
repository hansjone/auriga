package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallArguments(t *testing.T) {
	for _, args := range [][]string{{"--wat"}, {"--address", "$(id)"}, {"--address", "192.0.2.10", "--domain", "example.com"}, {"--domain", "../evil"}} {
		if _, err := parseInstall(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	o, err := parseInstall([]string{"--address", "192.0.2.10", "--data", "/srv/auriga"})
	if err != nil || o.Data != "/srv/auriga/auriga-data" {
		t.Fatalf("%+v %v", o, err)
	}
	o, err = parseInstall([]string{"--address", "192.0.2.10", "--data", "/srv/auriga-rehearsal"})
	if err != nil || o.Data != "/srv/auriga-rehearsal/auriga-data" {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestChecksumsRejectTamperingAndExtraFiles(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "payload"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(d); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(d); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "payload"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(d); err == nil {
		t.Fatal("accepted tampering")
	}
	if err := writeSums(d); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "extra"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(d); err == nil {
		t.Fatal("accepted unlisted file")
	}
}

func TestPrivateResourceKeys(t *testing.T) {
	d := filepath.Join(t.TempDir(), "keys")
	if err := resourceKeys(d); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(d, "platform/release.pk8"))
	if err != nil {
		t.Fatal(err)
	}
	if err := resourceKeys(d); err == nil {
		t.Fatal("silently replaced keys")
	}
	after, _ := os.ReadFile(filepath.Join(d, "platform/release.pk8"))
	if string(before) != string(after) {
		t.Fatal("key changed")
	}
	roots, err := os.ReadFile(filepath.Join(d, "resource-roots.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"release", "revocation", "downgrade"} {
		if !strings.Contains(string(roots), role) {
			t.Fatal(role)
		}
	}
}
