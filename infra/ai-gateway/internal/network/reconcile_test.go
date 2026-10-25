package network

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestRevokedDeviceKicksEstablishedConnection(t *testing.T) {
	o := defaultBrokerOpts()
	o.noCallout = true
	o.authTimeout = 0.1
	h := newHarness(t, o)
	f := &fakeIAM{active: true}
	r, err := NewIAMRedeemer(f, IAMConfig{ServerID: h.srv.ID(), ServerAudience: "server-a", Account: clientAccount, RedeemTimeout: 30 * time.Millisecond, AuthTimeout: 100 * time.Millisecond, MaxConnections: 100})
	if err != nil {
		t.Fatal(err)
	}
	callout, err := NewCallout(CalloutConfig{Issuer: h.issuer, XKey: h.xkey, Account: clientAccount, Redeemer: r, RedeemTimeout: 30 * time.Millisecond, AuthTimeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	nc := h.internalConn(h.authUser, h.authPass)
	if _, err = callout.Serve(nc); err != nil {
		t.Fatal(err)
	}
	if err = nc.Flush(); err != nil {
		t.Fatal(err)
	}
	c, err := nats.Connect(h.wsURL(), nats.Token(connectTicket), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	ops := h.opsConn()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Reconcile(ctx, ReconcileConfig{Interval: 10 * time.Millisecond, RequestTimeout: 100 * time.Millisecond, MaxKickAttempts: 3, Snapshot: func(ctx context.Context) (map[uint64]*ConnInfo, error) {
			return LookupConns(ctx, ops, h.srv.ID(), clientAccount, 100)
		}}, func(ctx context.Context, b Binding) error { return Kick(ctx, ops, b.ServerID, b.ClientID) })
	}()
	defer func() { cancel(); <-done }()
	f.mu.Lock()
	f.active = false
	f.mu.Unlock()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for !c.IsClosed() {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("revoked connection was not kicked within one second")
	}
}

func TestKickRetriesAreBoundedAndRevocationStaysLatched(t *testing.T) {
	f := &fakeIAM{active: false}
	r := testRedeemer(t, f)
	if _, err := r.Redeem(context.Background(), testAttempt()); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	for k, b := range r.bindings {
		b.CheckAfter = time.Now()
		r.bindings[k] = b
	}
	r.mu.Unlock()
	var calls atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := r.Reconcile(ctx, ReconcileConfig{Interval: time.Millisecond, RequestTimeout: time.Millisecond, MaxKickAttempts: 3, Snapshot: func(context.Context) (map[uint64]*ConnInfo, error) {
		conns := make(map[uint64]*ConnInfo)
		for _, b := range r.snapshot() {
			conns[b.ClientID] = &ConnInfo{CID: b.ClientID, Account: b.Account, AuthorizedUser: b.Ingress}
		}
		return conns, nil
	}}, func(context.Context, Binding) error {
		calls.Add(1)
		f.mu.Lock()
		f.active = true
		f.mu.Unlock()
		return errors.New("system unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("kick attempts=%d", calls.Load())
	}
	b := r.snapshot()
	if len(b) != 1 || !b[0].Revoked {
		t.Fatal("failed kick restored authority")
	}
}

func TestLookupConnsRequiresCompleteAccountSnapshot(t *testing.T) {
	h := newHarness(t, defaultBrokerOpts())
	ops := h.opsConn()
	h.connectNew(time.Now().Add(time.Hour))
	h.connectNew(time.Now().Add(time.Hour))
	conns, err := LookupConns(ctxT(t), ops, h.srv.ID(), clientAccount, 100)
	if err != nil || len(conns) != 2 {
		t.Fatalf("snapshot count=%d err=%v", len(conns), err)
	}
	if _, err = LookupConns(ctxT(t), ops, h.srv.ID(), clientAccount, 1); !errors.Is(err, ErrSysAPI) {
		t.Fatalf("incomplete snapshot accepted: %v", err)
	}
}
