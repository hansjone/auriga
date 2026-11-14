package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/edge/internal/projection"
	trust "github.com/hansjone/auriga/infra/edge/pkg/msgnet"
	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
	"github.com/nats-io/nats.go"
)

// MsgnetProjectionConfig is trusted D assembly, separate from signed wire models.
type MsgnetProjectionConfig struct {
	Root, Control, State, SchemaFile, RootSPKI, RootKid, Tenant, Realm string
	LedgerOrigin, TokenFile                                            string
	Installation                                                       api.InstallationPin
	SubmitLeafSAN, PresenceLeafSAN                                     string
}
type edgeTrustLedger struct {
	Transport   trust.TransportState   `json:"transport"`
	Publication trust.PublicationState `json:"publication"`
}
type edgeTLSSnapshot struct {
	selector   api.LocalProjectionSelector
	set        *trust.TLSSet
	handshakes map[string]bool
}
type edgeMsgnet struct {
	mu           sync.Mutex
	reader       *projection.Reader
	consumer     *trust.TransportConsumer
	source       *trust.HTTPSource
	ledger       edgeTrustLedger
	active       *edgeTLSSnapshot
	connections  map[*msgnetConn]bool
	closeFailure bool
	cfg          MsgnetProjectionConfig
}

func newEdgeMsgnet(cfg MsgnetProjectionConfig, hop IAMHopConfig) (*edgeMsgnet, error) {
	if cfg.Installation.Component != "edge" || cfg.Installation.IdentityId == "" || cfg.RootKid == "" || cfg.Tenant == "" || cfg.Realm == "" || cfg.SubmitLeafSAN == "" || cfg.PresenceLeafSAN == "" || cfg.SubmitLeafSAN == cfg.PresenceLeafSAN {
		return nil, fmt.Errorf("incomplete edge message network installation")
	}
	schemas, e := trust.LoadSchemas(cfg.SchemaFile)
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(cfg.LedgerOrigin)
	if e != nil {
		return nil, e
	}
	tr, e := newIAMHopTransport(hop, u)
	if e != nil {
		return nil, e
	}
	source, e := trust.NewHTTPSource(cfg.LedgerOrigin, &http.Client{Transport: tr}, schemas, func(context.Context) (string, error) { return readIAMServiceToken(cfg.TokenFile) })
	if e != nil {
		return nil, e
	}
	x := &edgeMsgnet{cfg: cfg, source: source, connections: map[*msgnetConn]bool{}}
	x.reader = &projection.Reader{Root: cfg.Root, Control: cfg.Control, State: cfg.State, Scope: api.LocalProjectionSelector{TenantId: cfg.Tenant, Realm: cfg.Realm, InstallationId: cfg.Installation.IdentityId, Component: "edge"}}
	if e = x.reader.Restore(); e != nil {
		return nil, e
	}
	raw, e := x.reader.ReadState("trust-ledger.json", 32<<20)
	if e == nil {
		if e = trust.Decode(raw, &x.ledger, true); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	x.consumer = &trust.TransportConsumer{Schemas: schemas, Domain: trust.TransportDomain{RootSPKI: cfg.RootSPKI, Tenant: cfg.Tenant, Realm: cfg.Realm}, RootKid: cfg.RootKid, Source: source, State: x.ledger.Transport}
	x.consumer.Save = func(state trust.TransportState) error { next := x.ledger; next.Transport = state; return x.save(next) }
	// Restored success never implies a loaded TLS handle. Recheck the durable root
	// before using the ledger as a high-water mark; material is reloaded below.
	if c := x.consumer.State.Control; c != nil {
		if c.RootAuth.Kid != cfg.RootKid || c.Payload.TenantId != cfg.Tenant || c.Payload.Realm != cfg.Realm || trust.VerifySignature(c.Payload, c.RootAuth, "Auriga.NetworkRootControl.v2", cfg.RootSPKI) != nil {
			return nil, fmt.Errorf("invalid restored edge root fence")
		}
	}
	x.reader.Prepare = x.prepare
	x.reader.Current = func(s api.LocalProjectionSelector) error {
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.active == nil || !trust.Equal(s, x.active.selector) {
			return trust.Fault("projection_pending")
		}
		return x.currentLocked()
	}
	return x, nil
}
func (x *edgeMsgnet) save(next edgeTrustLedger) error {
	raw, e := trust.Bytes(next)
	if e != nil {
		return e
	}
	if e = x.reader.WriteState("trust-ledger.json", raw); e != nil {
		return e
	}
	x.ledger = next
	return nil
}
func (x *edgeMsgnet) currentLocked() error {
	if x.active == nil || x.consumer.State.Control == nil || x.consumer.State.Bundle == nil {
		return trust.Fault("projection_pending")
	}
	c := x.consumer.State.Control.Payload
	b := x.consumer.State.Bundle.Payload
	if slices.Contains(c.RevokedInstallationIds, x.cfg.Installation.IdentityId) || slices.Contains(c.RevokedAuthorityIds, b.AuthorityId) {
		return trust.Fault("authority_revoked")
	}
	if e := x.consumer.CheckCurrent(x.cfg.Installation.IdentityId); e != nil {
		return e
	}
	if !slices.ContainsFunc(c.AllowedAuthorities, func(a api.AuthorityReference) bool {
		return a.AuthorityId == b.AuthorityId && a.AuthorityDigest == b.AuthorityDigest
	}) {
		return trust.Fault("authority_unknown")
	}
	for _, cert := range x.active.set.Leaves {
		if !currentTLSLeaf(cert) {
			return trust.Fault("projection_pending")
		}
	}
	return nil
}
func (x *edgeMsgnet) withdrawLocked() bool {
	x.active = nil
	x.closeFailure = false
	for c := range x.connections {
		if e := c.Conn.Close(); e != nil && !errors.Is(e, net.ErrClosed) {
			x.closeFailure = true
			continue
		}
		delete(x.connections, c)
	}
	return !x.closeFailure
}
func (x *edgeMsgnet) prepare(s api.LocalProjectionSelector, m api.LocalProjectionManifest, files map[string][]byte) (func() error, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, e := x.source.Control(ctx)
	if e != nil {
		return nil, e
	}
	if e = x.consumer.UpdateControl(c); e != nil {
		return nil, e
	}
	if x.currentLocked() != nil {
		x.withdrawLocked()
	}
	if slices.Contains(c.Payload.RevokedInstallationIds, x.cfg.Installation.IdentityId) {
		return nil, trust.Fault("authority_revoked")
	}
	g := &trust.Generation{Selector: s, Manifest: m, Files: files}
	p, b, set, e := trust.DecodeTLSGeneration(g, x.consumer.Schemas, x.cfg.Installation, map[string]string{"edge": x.cfg.SubmitLeafSAN, "edge-presence": x.cfg.PresenceLeafSAN}, time.Now())
	if e != nil {
		return nil, e
	}
	publication, e := trust.AcceptPublication(p, c, x.consumer.Domain, x.cfg.RootKid, time.Now(), x.ledger.Publication)
	if e != nil {
		return nil, e
	}
	// Every transport update first denies and closes existing message connections.
	// This supplies real local isolation for emergency recovery, never a wire flag.
	isolated := x.withdrawLocked()
	if e = x.consumer.Accept(ctx, b, p.Authority, isolated); e != nil {
		return nil, e
	}
	next := x.ledger
	next.Publication = publication
	if e = x.save(next); e != nil {
		return nil, e
	}
	snapshot := &edgeTLSSnapshot{selector: s, set: set, handshakes: map[string]bool{}}
	return func() error { x.mu.Lock(); defer x.mu.Unlock(); x.active = snapshot; return x.currentLocked() }, nil
}

type msgnetConn struct {
	net.Conn
	owner *edgeMsgnet
	once  sync.Once
}

func (c *msgnetConn) Close() error {
	e := c.Conn.Close()
	c.once.Do(func() { c.owner.mu.Lock(); delete(c.owner.connections, c); c.owner.mu.Unlock() })
	return e
}
func (x *edgeMsgnet) configure(tr *http.Transport, role, serverName string) error {
	if serverName == "" || net.ParseIP(serverName) != nil || role != "edge" && role != "edge-presence" {
		return fmt.Errorf("fixed internal msgnet TLS scope required")
	}
	tr.Proxy = nil
	tr.TLSClientConfig = nil
	tr.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		x.mu.Lock()
		if e := x.currentLocked(); e != nil {
			x.mu.Unlock()
			return nil, e
		}
		snapshot := x.active
		cert, ok := snapshot.set.Leaves[role]
		roots := snapshot.set.ServerRoots
		x.mu.Unlock()
		if !ok {
			return nil, trust.Fault("scope_denied")
		}
		cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName, Certificates: []tls.Certificate{cert}, RootCAs: roots}
		conn, e := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: cfg}).DialContext(ctx, network, address)
		if e != nil {
			return nil, e
		}
		x.mu.Lock()
		if x.active != snapshot || x.currentLocked() != nil {
			x.mu.Unlock()
			_ = conn.Close()
			return nil, trust.Fault("projection_pending")
		}
		tracked := &msgnetConn{Conn: conn, owner: x}
		x.connections[tracked] = true
		snapshot.handshakes[role] = true
		verified := snapshot.handshakes["edge"] && snapshot.handshakes["edge-presence"]
		x.mu.Unlock()
		if verified {
			x.reader.ConfirmHandshake(snapshot.selector)
		}
		return tracked, nil
	}
	return nil
}

// NATS performs STARTTLS after INFO on the tracked raw socket. Its immutable
// certificate/root pair comes from one verified generation; NoReconnect makes
// each subsequent presence request obtain the then-current snapshot again.
type msgnetPresenceDialer struct {
	x        *edgeMsgnet
	snapshot *edgeTLSSnapshot
}

func (d *msgnetPresenceDialer) Dial(network, address string) (net.Conn, error) {
	conn, e := net.DialTimeout(network, address, 500*time.Millisecond)
	if e != nil {
		return nil, e
	}
	d.x.mu.Lock()
	defer d.x.mu.Unlock()
	if d.x.active != d.snapshot || d.x.currentLocked() != nil {
		_ = conn.Close()
		return nil, trust.Fault("projection_pending")
	}
	tracked := &msgnetConn{Conn: conn, owner: d.x}
	d.x.connections[tracked] = true
	return tracked, nil
}
func (x *edgeMsgnet) presenceOptions(serverName string) ([]nats.Option, func(), error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if serverName == "" || net.ParseIP(serverName) != nil {
		return nil, nil, trust.Fault("scope_denied")
	}
	if e := x.currentLocked(); e != nil {
		return nil, nil, e
	}
	snapshot := x.active
	cert, ok := snapshot.set.Leaves["edge-presence"]
	if !ok {
		return nil, nil, trust.Fault("scope_denied")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName, RootCAs: snapshot.set.ServerRoots, Certificates: []tls.Certificate{cert}}
	mark := func() {
		x.mu.Lock()
		current := x.active == snapshot && x.currentLocked() == nil
		if current {
			snapshot.handshakes["edge-presence"] = true
		}
		verified := current && snapshot.handshakes["edge"]
		x.mu.Unlock()
		if verified {
			x.reader.ConfirmHandshake(snapshot.selector)
		}
	}
	return []nats.Option{nats.Secure(cfg), nats.SetCustomDialer(&msgnetPresenceDialer{x, snapshot})}, mark, nil
}

// Run uses fixed discovery and retries indefinitely. Loss of the authoritative
// control source withdraws message traffic after 60 seconds; success recovers.
func (x *edgeMsgnet) Run(ctx context.Context) error {
	listenErr := make(chan error, 1)
	go func() { listenErr <- x.reader.Listen(ctx) }()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var failed time.Time
	defer func() { x.mu.Lock(); x.withdrawLocked(); x.mu.Unlock() }()
	for {
		x.mu.Lock()
		query, cancel := context.WithTimeout(ctx, 10*time.Second)
		c, e := x.source.Control(query)
		cancel()
		if e == nil {
			e = x.consumer.UpdateControl(c)
			if e == nil && x.currentLocked() != nil {
				x.withdrawLocked()
			}
		}
		x.mu.Unlock()
		if e == nil {
			e = x.reader.Reload()
		}
		if e != nil {
			if failed.IsZero() {
				failed = time.Now()
			}
			if time.Since(failed) >= time.Minute {
				x.mu.Lock()
				x.withdrawLocked()
				x.mu.Unlock()
			}
		} else {
			failed = time.Time{}
		}
		select {
		case <-ctx.Done():
			return nil
		case e := <-listenErr:
			return e
		case <-tick.C:
		}
	}
}
