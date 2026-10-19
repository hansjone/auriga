package enroll_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
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
		CertificateNotBefore: now.Add(-21 * 24 * time.Hour),
		CertificateExpiresAt: now.Add(9 * 24 * time.Hour), // 30d total, 9d left < 10d but > 7d
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
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	opts := enroll.Options{AgentDir: filepath.Join(t.TempDir(), "agent"), Server: fw.URL(), Token: fw.Token(), CAFingerprint: fw.CA.Fingerprint(), AllowInsecure: true}
	if _, err := enroll.Join(ctx, opts); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, name := range []string{identity.KeyFileName, identity.CertFileName, identity.CAFileName, identity.SealingFileName} {
		b, err := os.ReadFile(filepath.Join(opts.AgentDir, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = b
	}
	if _, err := enroll.Join(ctx, opts); err == nil || !strings.Contains(err.Error(), "already enrolled") {
		t.Fatalf("duplicate join not rejected locally: %v", err)
	}
	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(opts.AgentDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("duplicate join changed %s", name)
		}
	}
}
