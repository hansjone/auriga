package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSigningKeys(t *testing.T) (string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	private := filepath.Join(root, "private.pem")
	public := filepath.Join(root, "public.pem")
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(private, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	der, err = x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(public, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0644); err != nil {
		t.Fatal(err)
	}
	return private, public
}

func TestReleaseCacheSignedPartsAndTagGate(t *testing.T) {
	source := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.com"}} {
		if _, err := gitAt(source, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "commit", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "tag", "1.0"); err != nil {
		t.Fatal(err)
	}
	key, public := testSigningKeys(t)
	r := release{Schema: 1, Version: "1.0", Build: &buildSpec{Inputs: artifact{SHA256: strings.Repeat("a", 64)}, Recipes: artifact{SHA256: strings.Repeat("b", 64)}}}
	index := filepath.Join(t.TempDir(), "release.json")
	if err := jsonFile(index, r, 0644); err != nil {
		t.Fatal(err)
	}
	builds := 0
	fake := func(file string) error {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "payload"), []byte(strings.Repeat("data", 10000)), 0644); err != nil {
			return err
		}
		if err := writeSums(root); err != nil {
			return err
		}
		if err := signSums(root, key); err != nil {
			return err
		}
		return pack(root, file)
	}
	builders := releaseBuilders{Bundle: func(_ []string, _ string) error { return nil }, Devkit: func(_ []string) error { return nil }}
	builders.Bundle = func(args []string, _ string) error { return fake(flagValue(args, "--out")) }
	builders.Devkit = func(args []string) error { builds++; return fake(flagValue(args, "--out")) }
	builders.Executable = filepath.Join(t.TempDir(), "cli")
	if err := os.WriteFile(builders.Executable, []byte("fixture cli"), 0755); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	opts := releaseOptions{Source: source, Tag: "1.0", Index: index, Out: filepath.Join(parent, "v1"), Cache: filepath.Join(parent, "cache"), Key: key, Limit: 16384}
	if err := generateRelease(opts, builders); err != nil {
		t.Fatal(err)
	}
	if err := verifySums(opts.Out); err != nil {
		t.Fatal(err)
	}
	if err := verifySignature(opts.Out, public); err != nil {
		t.Fatal(err)
	}
	if err := extractVerified(filepath.Join(opts.Out, "auriga-offline-1.0.tar.part001"), filepath.Join(parent, "unpack"), public); err != nil {
		t.Fatal(err)
	}
	_, wrong := testSigningKeys(t)
	if err := extractVerified(filepath.Join(opts.Out, "auriga-offline-1.0.tar.part001"), filepath.Join(parent, "badkey"), wrong); err == nil {
		t.Fatal("accepted wrong signature")
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("new code"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "commit", "-m", "code only"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAt(source, "tag", "1.1"); err != nil {
		t.Fatal(err)
	}
	opts.Tag = "1.1"
	opts.Out = filepath.Join(parent, "v2")
	if err := generateRelease(opts, builders); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Fatal("did not reuse lock-identical devkit", builds)
	}
	opts.Tag = "1.0"
	opts.Out = filepath.Join(parent, "mismatch")
	if err := generateRelease(opts, builders); err == nil {
		t.Fatal("accepted HEAD not at tag")
	}
	caches, err := filepath.Glob(filepath.Join(opts.Cache, "*", "auriga-devkit-*.tar"))
	if err != nil || len(caches) != 1 {
		t.Fatal(caches, err)
	}
	f, err := os.OpenFile(caches[0], os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("tamper"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	opts.Tag = "1.1"
	opts.Out = filepath.Join(parent, "tampered")
	if err := generateRelease(opts, builders); err == nil {
		t.Fatal("accepted tampered cached devkit")
	}
}

func flagValue(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
