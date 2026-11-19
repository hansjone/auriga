package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

func publisherRootArgs(t *testing.T) ([]string, ed25519.PublicKey) {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input := filepath.Join(dir, "issuer-public.pem")
	if err := os.WriteFile(input, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"publisher-root", "--public-key", input, "--kid", "existing-issuer", "--not-after", time.Now().Add(time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339Nano), "--out", filepath.Join(dir, "root.pb")}, public
}

func TestPublisherRootCLIUsesExplicitPublicMetadata(t *testing.T) {
	args, public := publisherRootArgs(t)
	var stdout, stderr bytes.Buffer
	if err := run(args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	var key nodev1.ArtifactSigningKey
	if err := proto.Unmarshal(raw, &key); err != nil || key.Kid != "existing-issuer" || !bytes.Equal(key.PublicKey, public) || key.NotAfter == nil || key.NotAfter.AsTime().Format(time.RFC3339Nano) != args[6] {
		t.Fatal("CLI did not encode the requested existing issuer metadata")
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("sha256=%x", sha256.Sum256(raw))) || strings.Contains(stdout.String(), "verified") || strings.Contains(stdout.String(), "PRIVATE") || stderr.Len() != 0 {
		t.Fatal("CLI output did not describe only prepared public metadata")
	}
	if err := run(args, new(bytes.Buffer), new(bytes.Buffer)); err == nil {
		t.Fatal("CLI replaced an existing output")
	}
}

func TestPublisherRootCLIFlagLookingKidUsesOriginalPolicy(t *testing.T) {
	args, _ := publisherRootArgs(t)
	args[4] = "--out" // Valid existing kid syntax; this is a flag value, not a duplicate flag.
	if err := run(args, new(bytes.Buffer), new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	var key nodev1.ArtifactSigningKey
	if err := proto.Unmarshal(raw, &key); err != nil || key.Kid != "--out" {
		t.Fatal("valid flag-looking kid was narrowed by the CLI parser")
	}
}

func TestPublisherRootCLIRejectsMissingDuplicateAndInapplicableFlags(t *testing.T) {
	args, _ := publisherRootArgs(t)
	for _, flag := range []string{"public-key", "kid", "not-after", "out"} {
		var missing []string
		for i := 0; i < len(args); i++ {
			if args[i] == "--"+flag {
				i++
				continue
			}
			missing = append(missing, args[i])
		}
		for _, bad := range [][]string{missing, append(append([]string{}, args...), "--"+flag, "duplicate")} {
			if err := run(bad, new(bytes.Buffer), new(bytes.Buffer)); err == nil {
				t.Fatalf("invalid explicit flag accepted: %s", flag)
			}
		}
	}
	for _, flag := range []string{"private-key", "root", "sequence", "image", "os", "arch", "unknown"} {
		if err := run(append(append([]string{}, args...), "--"+flag, "not-an-input"), new(bytes.Buffer), new(bytes.Buffer)); err == nil {
			t.Fatalf("inapplicable flag accepted: %s", flag)
		}
	}
	if err := run(append(append([]string{}, args...), "extra"), new(bytes.Buffer), new(bytes.Buffer)); err == nil {
		t.Fatal("positional argument accepted")
	}
	if _, err := os.Lstat(args[len(args)-1]); !os.IsNotExist(err) {
		t.Fatal("rejected arguments created an output")
	}
}

func TestPublisherRootCLIRejectsNoncanonicalOrExpiredTime(t *testing.T) {
	args, _ := publisherRootArgs(t)
	for _, expiry := range []string{"", "2020-01-01T00:00:00Z", "2031-01-01T00:00:00+00:00", "2031-01-01T00:00:00.100Z", "2031-01-01T00:00:00.1234567890Z", "10000-01-01T00:00:00Z"} {
		bad := append([]string{}, args...)
		bad[6] = expiry
		if err := run(bad, new(bytes.Buffer), new(bytes.Buffer)); err == nil {
			t.Fatalf("noncanonical or expired not-after accepted: %q", expiry)
		}
	}
	if _, err := os.Lstat(args[len(args)-1]); !os.IsNotExist(err) {
		t.Fatal("invalid time created an output")
	}
}
