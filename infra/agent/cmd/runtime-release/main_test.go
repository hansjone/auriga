package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Build and invoke the actual CLI; no mock verifier or signing implementation.
func TestCLIExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "runtime-release")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, ".")
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, raw)
	}
	put := func(name string, raw []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := put("publisher.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	protectCLIPrivate(t, keyPath)
	rootRaw, err := proto.MarshalOptions{Deterministic: true}.Marshal(&nodev1.ArtifactSigningKey{Kid: "cli-root", PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	root := put("root.pb", rootRaw)
	configRaw := []byte("{\"os\":\"linux\",\"architecture\":\"amd64\",\"config\":{},\"rootfs\":{\"type\":\"layers\",\"diff_ids\":[]}}\n")
	config := put("config.json", configRaw)
	digest := func(raw []byte) string { sum := sha256.Sum256(raw); return "sha256:" + hex.EncodeToString(sum[:]) }
	manifestRaw := []byte(fmt.Sprintf("{\"schemaVersion\":2,\"mediaType\":\"application/vnd.oci.image.manifest.v1+json\",\"config\":{\"mediaType\":\"application/vnd.oci.image.config.v1+json\",\"size\":%d,\"digest\":%q},\"layers\":[]}\n", len(configRaw), digest(configRaw)))
	manifest := put("manifest.json", manifestRaw)
	image := "192.0.2.9:5000/auriga/dsh@" + digest(manifestRaw)
	out := filepath.Join(dir, "public")
	signArgs := []string{"sign", "--root", root, "--os", "linux", "--arch", "amd64", "--image", image, "--artifact", "dsh-cli", "--version", "1.0.0", "--sequence", "7", "--issued-at", "2026-01-02T03:04:05.123456789Z", "--private-key", keyPath, "--manifest", manifest, "--config", config, "--out", out}
	invoke := func(ok bool, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		raw, err := cmd.CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("CLI success=%t: err=%v output=%s", ok, err, raw)
		}
		if bytes.Contains(raw, []byte("BEGIN PRIVATE KEY")) || bytes.Contains(raw, der) {
			t.Fatal("CLI emitted private key")
		}
		return string(raw)
	}
	if !strings.Contains(invoke(true, signArgs...), "verified body=sha256:") {
		t.Fatal("missing verification result")
	}
	verifyArgs := []string{"verify", "--root", root, "--dir", out, "--os", "linux", "--arch", "amd64", "--allow-image", image}
	invoke(true, verifyArgs...)
	invoke(true, verifyArgs...)
	invoke(false, signArgs...)                                                                        // existing output
	invoke(false, "verify", "--dir", out, "--os", "linux", "--arch", "amd64", "--allow-image", image) // no independent root
	invoke(false, "verify", "--root", root, "--dir", out, "--os", "linux", "--arch", "amd64", "--allow-image", "registry.example.com/unapproved/dsh@"+digest(manifestRaw))
	for _, name := range []string{"sequence", "issued-at", "root", "private-key", "image", "artifact", "os", "arch"} {
		t.Run("explicit-"+name, func(t *testing.T) {
			var args []string
			for i := 0; i < len(signArgs); i++ {
				if signArgs[i] == "--"+name {
					i++
					continue
				}
				args = append(args, signArgs[i])
			}
			invoke(false, args...)
		})
	}
	for _, issued := range []string{"", "2026-01-02T03:04:05.100Z", "2026-01-02T03:04:05+00:00", "2026-01-02T03:04:05.1234567890Z", "10000-01-02T03:04:05Z"} {
		args := append([]string(nil), signArgs...)
		for i := range args {
			if args[i] == "--issued-at" {
				args[i+1] = issued
			}
		}
		invoke(false, args...)
	}
	proofConfig := filepath.Join(out, "runtime-proofs", strings.TrimPrefix(digest(manifestRaw), "sha256:"), "config.json")
	if err := os.WriteFile(proofConfig, append(bytes.Clone(configRaw), ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	invoke(false, verifyArgs...)
}

func TestCLIArgumentRejections(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"verify", "--unknown"}, {"sign"}, {"verify", "extra"}} {
		if err := run(args, new(bytes.Buffer), new(bytes.Buffer)); err == nil {
			t.Fatalf("arguments accepted: %v", args)
		}
	}
}
