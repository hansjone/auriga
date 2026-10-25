package network

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
)

func scaleBindings(r *IAMRedeemer, n int) map[uint64]*ConnInfo {
	r.cfg.MaxConnections = max(r.cfg.MaxConnections, n)
	conns := make(map[uint64]*ConnInfo, n)
	for i := 0; i < n; i++ {
		ingress := fmt.Sprintf("ingress-%d", i)
		b := liveBinding{Binding: Binding{ServerID: r.cfg.ServerID, ClientID: uint64(i + 1), Account: clientAccount, Ingress: ingress}, Tenant: testTenant, Realm: "1", Node: fmt.Sprint(i + 1), NotAfter: time.Now().Add(time.Hour)}
		r.bindings[ingress] = b
		conns[b.ClientID] = &ConnInfo{CID: b.ClientID, Account: b.Account, AuthorizedUser: ingress}
	}
	return conns
}

func TestReconcile5000ConnectionsUsesOneSnapshotAndOneIAMBatch(t *testing.T) {
	f := &fakeIAM{active: false, delay: 100 * time.Millisecond}
	r := testRedeemer(t, f)
	conns := scaleBindings(r, 5000)
	for key, b := range r.bindings {
		b.Tenant = fmt.Sprintf("01900000-0000-7000-8000-%012d", b.ClientID%50+1)
		r.bindings[key] = b
	}
	var snapshots, kicks atomic.Int32
	cfg := ReconcileConfig{Interval: 100 * time.Millisecond, RequestTimeout: 250 * time.Millisecond, MaxKickAttempts: 3, Snapshot: func(context.Context) (map[uint64]*ConnInfo, error) { snapshots.Add(1); return conns, nil }}
	start := time.Now()
	state := reconcileState{}
	r.reconcileRound(context.Background(), cfg, func(ctx context.Context, _ Binding) error {
		select {
		case <-time.After(10 * time.Millisecond):
			kicks.Add(1)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, &state)
	if snapshots.Load() != 1 || f.calls != 1 || kicks.Load() != 5000 || len(r.snapshot()) != 0 {
		t.Fatalf("snapshots=%d IAM=%d kicks=%d retained=%d", snapshots.Load(), f.calls, kicks.Load(), len(r.snapshot()))
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("5000 revocations exceeded round budget: %v", elapsed)
	}
}

func TestReconcileTransientIAMErrorDoesNotRevokeAndRecoveryResetsFailures(t *testing.T) {
	f := &fakeIAM{failure: errors.New("temporary outage")}
	r := testRedeemer(t, f)
	conns := scaleBindings(r, 1)
	cfg := ReconcileConfig{Interval: time.Millisecond, RequestTimeout: time.Second / 4, MaxKickAttempts: 3, FailureGrace: time.Second, Snapshot: func(context.Context) (map[uint64]*ConnInfo, error) { return conns, nil }}
	var kicks int
	kick := func(context.Context, Binding) error { kicks++; return nil }
	state := reconcileState{}
	r.reconcileRound(context.Background(), cfg, kick, &state)
	if kicks != 0 || r.snapshot()[0].Revoked {
		t.Fatal("single query error revoked connection")
	}
	f.mu.Lock()
	f.failure = nil
	f.active = true
	f.mu.Unlock()
	state.nextQuery = time.Time{}
	r.reconcileRound(context.Background(), cfg, kick, &state)
	if kicks != 0 || r.snapshot()[0].Failures != 0 {
		t.Fatal("recovery did not reset failures")
	}
}

func TestReconcilePersistentIAMErrorKicksWithinGrace(t *testing.T) {
	f := &fakeIAM{failure: errors.New("persistent outage")}
	r := testRedeemer(t, f)
	conns := scaleBindings(r, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	kicked := make(chan struct{}, 1)
	cfg := ReconcileConfig{Interval: 5 * time.Millisecond, RequestTimeout: time.Second / 4, MaxKickAttempts: 3, FailureGrace: 50 * time.Millisecond, Snapshot: func(context.Context) (map[uint64]*ConnInfo, error) { return conns, nil }}
	done := make(chan error, 1)
	go func() {
		done <- r.Reconcile(ctx, cfg, func(context.Context, Binding) error {
			select {
			case kicked <- struct{}{}:
			default:
			}
			return nil
		})
	}()
	select {
	case <-kicked:
	case <-ctx.Done():
		t.Fatal("persistent IAM error exceeded revocation bound")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStrictConnectTokenRejectsUnknownFields(t *testing.T) {
	f := &fakeIAM{}
	r := testRedeemer(t, f)
	a := testAttempt()
	a.Ticket = a.Ticket[:len(a.Ticket)-1] + `,"audience":"forged"}`
	if _, err := r.Redeem(context.Background(), a); err == nil {
		t.Fatal("unknown CONNECT field accepted")
	}
	if f.calls != 0 {
		t.Fatal("invalid token reached IAM")
	}
}

func TestBatchAuthorityRejectsMissingDuplicateAndForeignResults(t *testing.T) {
	for name, mutate := range map[string]func(*iam.BatchNetworkNodesActiveResponse){
		"missing":   func(r *iam.BatchNetworkNodesActiveResponse) { r.Nodes = nil },
		"duplicate": func(r *iam.BatchNetworkNodesActiveResponse) { r.Nodes = append(r.Nodes, r.Nodes[0]) },
		"foreign":   func(r *iam.BatchNetworkNodesActiveResponse) { r.Nodes[0].Identity.Node = "foreign" },
	} {
		t.Run(name, func(t *testing.T) {
			r := testRedeemer(t, &fakeIAM{active: true, mutateBatch: mutate})
			scaleBindings(r, 1)
			if status, err := r.activeBatch(context.Background(), r.snapshot()); err == nil || status != nil {
				t.Fatal("invalid authority batch accepted")
			}
		})
	}
}

func TestBatchSnapshotNeverKicksAnotherIngress(t *testing.T) {
	r := testRedeemer(t, &fakeIAM{})
	conns := scaleBindings(r, 1)
	conns[1].AuthorizedUser = "another-ingress"
	errorsSeen := 0
	cfg := ReconcileConfig{Interval: time.Millisecond, RequestTimeout: time.Second / 4, MaxKickAttempts: 3, Snapshot: func(context.Context) (map[uint64]*ConnInfo, error) { return conns, nil }, OnError: func(_ Binding, err error) {
		if !errors.Is(err, ErrNotOwned) {
			t.Error(err)
		}
		errorsSeen++
	}}
	r.reconcileRound(context.Background(), cfg, func(context.Context, Binding) error { t.Error("foreign connection kicked"); return nil }, &reconcileState{})
	if len(r.snapshot()) != 0 || errorsSeen != 1 {
		t.Fatal("ownership conflict was not reported and reclaimed")
	}
}

func TestCancelledKickStillConsumesAttemptBudget(t *testing.T) {
	r := testRedeemer(t, &fakeIAM{})
	conns := scaleBindings(r, 1)
	cfg := ReconcileConfig{Interval: time.Millisecond, RequestTimeout: 250 * time.Millisecond, MaxKickAttempts: 1, Snapshot: func(context.Context) (map[uint64]*ConnInfo, error) { return conns, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	r.reconcileRound(ctx, cfg, func(ctx context.Context, _ Binding) error { calls++; cancel(); return ctx.Err() }, &reconcileState{})
	if b := r.snapshot(); len(b) != 1 || b[0].Attempts != 1 {
		t.Fatal("attempt was lost when round context ended")
	}
	r.reconcileRound(context.Background(), cfg, func(context.Context, Binding) error { calls++; return errors.New("unavailable") }, &reconcileState{})
	if calls != 1 {
		t.Fatal("cancelled KICK bypassed the retry limit")
	}
}
