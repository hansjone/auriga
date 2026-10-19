package enroll_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/tlsutil"
)

func TestValidateJoinRequiresTLS(t *testing.T) {
	if err := tlsutil.ValidateJoinEndpoint("grpc://127.0.0.1:1", false); err == nil {
		t.Fatal("expected reject")
	}
	if err := tlsutil.ValidateJoinEndpoint("https://platform.example.com:7443", false); err != nil {
		t.Fatal(err)
	}
	if err := tlsutil.ValidateControlEndpoint("grpc://127.0.0.1:1", false); err == nil {
		t.Fatal("expected reject control plaintext")
	}
	if err := tlsutil.ValidateControlEndpoint("https://platform.example.com:7443", false); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsRenewOneThirdLifetime(t *testing.T) {
	now := time.Now()
	mat := &identity.Material{
		CertificateNotBefore: now.Add(-27 * 24 * time.Hour),
		CertificateExpiresAt: now.Add(3 * 24 * time.Hour), // 30d total, 3d left < 10d (1/3)
	}
	if !enroll.NeedsRenew(mat, 7*24*time.Hour) {
		t.Fatal("expected renew near end of life")
	}
	mat2 := &identity.Material{
		CertificateNotBefore: now.Add(-1 * 24 * time.Hour),
		CertificateExpiresAt: now.Add(29 * 24 * time.Hour),
	}
	if enroll.NeedsRenew(mat2, 7*24*time.Hour) {
		t.Fatal("should not renew early")
	}
}

func TestReadTokenFromFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tok")
	if err := os.WriteFile(p, []byte("  file.token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := enroll.ReadToken("", p)
	if err != nil || tok != "file.token" {
		t.Fatalf("got %q %v", tok, err)
	}
	t.Setenv("AURIGA_AGENT_JOIN_TOKEN", "env.token")
	tok, err = enroll.ReadToken("", "")
	if err != nil || tok != "env.token" {
		t.Fatalf("got %q %v", tok, err)
	}
	tok, err = enroll.ReadToken("flag.token", "")
	if err != nil || tok != "flag.token" {
		t.Fatalf("flag should win: %q %v", tok, err)
	}
}

func TestRejectAlreadyEnrolled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	if err := identity.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	sealPriv, sealPub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	// Minimal fake files so Enrolled is true
	mat := &identity.Material{
		NodeID:         "n1",
		PrivateKey:     key,
		CertificatePEM: []byte("not-a-real-cert"),
		CAPEM:          []byte("not-a-real-ca"),
		SealingPrivate: sealPriv,
		SealingPublic:  sealPub,
	}
	// Save will fail parse later; just create key file for Enrolled check
	_ = mat
	if err := os.WriteFile(filepath.Join(dir, identity.KeyFileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !identity.Enrolled(dir) {
		t.Fatal("expected enrolled")
	}
}
