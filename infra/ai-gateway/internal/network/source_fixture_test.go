package network

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"github.com/nats-io/nats.go"
)

// Production callout/router, real TLS NATS WebSocket and generated gRPC.
// IAM/collab are explicit protocol fixtures, not a real authority service.
func sourceFixture(t *testing.T, f *fakeMailboxRPC, server collab.NetworkMailboxServiceServer, authorities ...*fakeIAM) (*Runtime, *nats.Conn, *nats.Subscription, liveBinding, RuntimeConfig) {
	t.Helper()
	dir := t.TempDir()
	mat := genTestTLSCA(t, dir)
	o := defaultBrokerOpts()
	o.noCallout, o.tls, o.tlsCertFile, o.tlsKeyFile = true, true, mat.certFile, mat.keyFile
	h := newHarness(t, o)
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	issuer, err := h.issuer.Seed()
	if err != nil {
		t.Fatal(err)
	}
	xkey, err := h.xkey.Seed()
	if err != nil {
		t.Fatal(err)
	}
	authority := &fakeIAM{active: true}
	if len(authorities) > 0 {
		authority = authorities[0]
	}
	cfg := RuntimeConfig{BrokerConfigFile: h.configFile, ServerAudience: "server-a", ClientAccount: clientAccount, IssuerSeedFile: write("issuer.seed", issuer), XKeySeedFile: write("xkey.seed", xkey), IAM: startIAMRPC(t, authority), RedeemTimeoutMillis: 500, ActiveIntervalMillis: 10, RequestTimeoutMillis: 100, MaxKickAttempts: 3, MaxConnections: 100, Collab: startMailboxRPC(t, server), ProtocolSigning: signingFixture(t)}
	cfg.Auth = NATSConfig{URL: h.tcpURL(), Username: h.authUser, PasswordFile: write("auth.password", []byte(h.authPass))}
	cfg.Ops = NATSConfig{URL: h.tcpURL(), Username: h.opsUser, PasswordFile: write("ops.password", []byte(h.opsPass))}
	cfg.Data = NATSConfig{URL: h.tcpURL(), Username: h.adptUser, PasswordFile: write("data.password", []byte(h.adptPass))}
	run, err := StartRuntime(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(run.Close)
	if !strings.HasPrefix(h.wsURL(), "wss://") {
		t.Fatal("source fixture requires real WSS")
	}
	nc, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect(), nats.Secure(&tls.Config{RootCAs: mat.pool, MinVersion: tls.VersionTLS12}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	b := run.redeemer.snapshot()[0]
	b.Epoch, b.Incarnation, b.ChannelNonce = 42, "inc-1", "nonce-1"
	down, err := nc.SubscribeSync(DownSubject(b.Ingress))
	if err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.response = signedMailboxFrame(t, cfg.ProtocolSigning, "ready", b, "req-ready")
	f.mu.Unlock()
	if err = nc.Publish(UpSubject(b.Ingress), []byte(`{"app_version":"1.0","type":"ready","request_id":"req-ready","channel_nonce":"nonce-1","boot_id":"boot-1"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = down.NextMsg(time.Second); err != nil {
		t.Fatal(err)
	}
	run.mailbox.workers.Wait()
	return run, nc, down, b, cfg
}
