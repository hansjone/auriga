package network

import (
	"bytes"
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
	// Some source tests cancel the entire reserved pool without reading its
	// diagnostics. Keep completion evidence bounded by the existing worker pools.
	p := &registrationPhasePublisher{nc: run.mailbox.nc, completed: make(chan registrationPublication, mailboxWorkers+cap(run.mailbox.sourceRevokes)+cap(run.mailbox.errorSlots))}
	run.mailbox.publisher = p
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
	b = sourceReady(t, run, nc, down, b, cfg.ProtocolSigning, "req-ready", []byte(`{"app_version":"1.0","type":"ready","request_id":"req-ready","channel_nonce":"nonce-1","boot_id":"boot-1"}`))
	return run, nc, down, b, cfg
}

func sourceReady(t *testing.T, run *Runtime, nc *nats.Conn, down *nats.Subscription, b liveBinding, cfg ProtocolSigningConfig, requestID string, raw []byte) liveBinding {
	t.Helper()
	p := registrationPhaseObserver(run)
	p.client = nc
	if err := nc.Publish(UpSubject(b.Ingress), raw); err != nil {
		t.Fatal(err)
	}
	if err := nc.FlushTimeout(registrationPhaseGuard); err != nil {
		t.Fatal(err)
	}
	publication, err := awaitRegistrationPublication(p, registrationPhaseGuard)
	if err != nil {
		t.Fatalf("READY publication: %v; %s", err, registrationPhaseDiagnostic(run, nc, b))
	}
	msg, err := down.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != DownSubject(b.Ingress) || msg.Subject != publication.subject || !bytes.Equal(msg.Data, publication.raw) {
		t.Fatal("READY downlink differs from completed publication")
	}
	if decodeObject(t, msg.Data)["type"] != "ready_result" {
		t.Fatalf("READY success required; %s", registrationPhaseDiagnostic(run, nc, b))
	}
	updated, err := validateMailboxResponse(b, "ready", requestID, raw, msg.Data)
	if err != nil || updated != b {
		t.Fatalf("READY binding mismatch: %v", err)
	}
	if err := validateRegistrationPhaseAuth(cfg, b, msg.Data, requestID); err != nil {
		t.Fatal(err)
	}
	// Publication completion proves worker entry; Wait alone cannot do so.
	run.mailbox.workers.Wait()
	run.redeemer.mu.Lock()
	current, ok := run.redeemer.bindings[b.Ingress]
	run.redeemer.mu.Unlock()
	if !ok || !sameMailboxBinding(current, b) || current.Revoked || !time.Now().Before(current.NotAfter) || current.NotAfter != b.NotAfter {
		t.Fatal("READY binding not effective")
	}
	return current
}
