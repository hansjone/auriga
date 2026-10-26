package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
)

func TestMailboxTransportRetainsTrustAndTokenBoundary(t *testing.T) {
	for _, target := range []string{"192.0.2.10:443", "localhost:443", "dns:///example.com:443", "not-a-target"} {
		if conn, err := dialMailboxRPC(team.RPCConfig{Target: target}); err == nil {
			_ = conn.Close()
			t.Fatalf("plaintext target accepted: %s", target)
		}
	}
	f := &fakeMailboxRPC{response: []byte(`{}`)}
	cfg := startMailboxRPC(t, f)
	conn, err := dialMailboxRPC(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	c, err := NewMailboxClient(conn, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b := liveBinding{Tenant: testTenant}
	if _, ae := c.Call(context.Background(), b, "source_get", sourceRequest("source_get")); ae != nil {
		t.Fatal(ae)
	}
	// Rotation is read on each invocation, including loss of usable credentials.
	if err = os.WriteFile(cfg.ServiceTokenFile, []byte(" \n"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, ae := c.Call(context.Background(), b, "source_get", sourceRequest("source_get")); ae == nil || data != nil || ae.Code != "network.overloaded" {
		t.Fatal("empty rotated JWT did not fail closed")
	}
	f.mu.Lock()
	calls := len(f.calls)
	f.mu.Unlock()
	if calls != 1 {
		t.Fatal("request reached backend after credential loss")
	}
	// Partial or invalid mTLS configuration must never fall back to plaintext.
	cfg.CertFile = filepath.Join(t.TempDir(), "absent.pem")
	if other, e := dialMailboxRPC(cfg); e == nil {
		_ = other.Close()
		t.Fatal("partial TLS fell back")
	}
}
