package runtime

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func releasePoolMember(b *nodev1.SharedDshBinding) *nodev1.SharedDshRevocation {
	return &nodev1.SharedDshRevocation{SchemaVersion: 1, TenantId: b.TenantId, MemberId: b.MemberId, PlacementId: b.PlacementId, Epoch: b.Epoch + 1, Revision: b.Revision + 1, AuthGeneration: 2, Reason: "switch", RevokedAt: 1000}
}

type delayedPoolStop struct {
	entered, release chan struct{}
	calls            atomic.Int32
}

func (s *delayedPoolStop) Stop(context.Context, string, time.Duration) error {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
	return nil
}
func (s *delayedPoolStop) ResourceProcessStopped(context.Context, string) (bool, error) {
	return true, nil
}

func TestSharedPoolRecoveryWaitsForAllStopCalls(t *testing.T) {
	m, _, _ := newPoolTest(t)
	applyPool(t, m, poolIntent("prepare-empty", "p", "alice"))
	driver := &delayedPoolStop{entered: make(chan struct{}), release: make(chan struct{})}
	m.Supervisor = driver
	released := false
	defer func() {
		if !released {
			close(driver.release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, e := m.Trip(context.Background(), poolID("pool"), poolID("first-stop"), "resource-expired")
		done <- e
	}()
	<-driver.entered
	if duplicate, e := m.Trip(context.Background(), poolID("pool"), poolID("first-stop"), "resource-expired"); e != nil || duplicate.State != "stopping" || driver.calls.Load() != 1 {
		t.Fatal("duplicate stop issued", duplicate, e)
	}
	if _, e := m.Trip(context.Background(), poolID("pool"), poolID("second-stop"), "resource-revoked"); e != nil {
		t.Fatal(e)
	}
	if e := m.RecoverPool(context.Background(), poolID("pool"), poolViewDouble{}); e == nil {
		t.Fatal("recovered while an older Engine Stop was still pending")
	}
	close(driver.release)
	released = true
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := m.RecoverPool(context.Background(), poolID("pool"), poolViewDouble{}); e != nil {
		t.Fatal(e)
	}
}

func TestSharedPoolMemberJoinSwitchAndReleasePreserveOtherMember(t *testing.T) {
	ctx := context.Background()
	m, supervisor, _ := newPoolTest(t)
	supervisor.stopped = false
	alice := poolIntent("prepare-empty", "alice-prepare", "alice")
	applyPool(t, m, alice)
	if e := m.AdmitTask(ctx, alice.Binding, poolID("alice-task"), 1, 100); e != nil {
		t.Fatal(e)
	}
	bob := poolIntent("prepare-empty", "bob-prepare", "bob")
	applyPool(t, m, bob)
	if e := m.AdmitTask(ctx, bob.Binding, poolID("bob-task"), 1, 100); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Apply(ctx, poolIntent("archive-reset", "bob-busy-archive", "bob")); e == nil {
		t.Fatal("active member archived")
	}
	if r := applyPool(t, m, poolIntent("cooperative-cancel", "bob-cancel", "bob")); r.Outcome != "confirmed" {
		t.Fatal(r)
	}
	if r := applyPool(t, m, poolIntent("archive-reset", "bob-archive", "bob")); r.Outcome != "confirmed" {
		t.Fatal(r)
	}
	if r := applyPool(t, m, poolIntent("discard-sessions", "bob-discard", "bob")); !r.SessionsDiscarded {
		t.Fatal(r)
	}
	sessions := m.Sessions.(*poolSessionDouble)
	if supervisor.stops != 0 || sessions.discards != 1 || sessions.discarded[0] != bob.Binding.MemberId {
		t.Fatal("member action touched other sessions")
	}
	if e := m.RevokeMember(ctx, poolID("pool"), releasePoolMember(bob.Binding)); e != nil {
		t.Fatal(e)
	}
	if e := m.AuthorizePoolStart(ctx, poolID("pool"), []*nodev1.SharedDshResourceView{poolView(alice.Binding)}, poolViewDouble{}); e != nil {
		t.Fatal("released member still required", e)
	}
	if e := m.CompleteTask(alice.Binding, poolID("alice-task")); e != nil {
		t.Fatal("other task interrupted", e)
	}
	restarted := &SharedPools{Layout: m.Layout, Supervisor: supervisor, Sessions: sessions, Now: m.Now}
	if _, e := restarted.Apply(ctx, bob); e == nil {
		t.Fatal("released epoch replay accepted")
	}
	rejoin := poolIntent("prepare-empty", "bob-rejoin", "bob")
	rejoin.Binding.Epoch = 2
	rejoin.Binding.Revision = 2
	if r := applyPool(t, restarted, rejoin); r.Outcome != "confirmed" {
		t.Fatal(r)
	}
	// Late duplicate release from the prior switch cannot remove the new epoch.
	if e := restarted.RevokeMember(ctx, poolID("pool"), releasePoolMember(bob.Binding)); e != nil {
		t.Fatal(e)
	}
	if e := restarted.AuthorizePoolStart(ctx, poolID("pool"), []*nodev1.SharedDshResourceView{poolView(alice.Binding), poolView(rejoin.Binding)}, poolViewDouble{}); e != nil {
		t.Fatal(e)
	}
}

func TestSharedPoolReleasedMemberFreesRegistration(t *testing.T) {
	m, _, _ := newPoolTest(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		applyPool(t, m, poolIntent("prepare-empty", fmt.Sprint("p", i), fmt.Sprint("m", i)))
	}
	if _, e := m.Apply(ctx, poolIntent("prepare-empty", "full", "replacement")); e == nil {
		t.Fatal("capacity exceeded")
	}
	old := poolIntent("prepare-empty", "p0", "m0").Binding
	if e := m.RevokeMember(ctx, poolID("pool"), releasePoolMember(old)); e != nil {
		t.Fatal(e)
	}
	if r := applyPool(t, m, poolIntent("prepare-empty", "replacement", "replacement")); r.Outcome != "confirmed" {
		t.Fatal(r)
	}
	impact, e := m.Trip(ctx, poolID("pool"), poolID("trip"), "resource-revoked")
	if e != nil || len(impact.Affected) != 20 {
		t.Fatal(impact, e)
	}
	for _, b := range impact.Affected {
		if b.MemberId == old.MemberId {
			t.Fatal("released member in impact")
		}
	}
}

type signaledPoolSessions struct{ started, release chan struct{} }

func (s *signaledPoolSessions) Cancel(context.Context, *nodev1.SharedDshBinding) error {
	close(s.started)
	<-s.release
	return nil
}
func (s *signaledPoolSessions) Discard(context.Context, *nodev1.SharedDshBinding) error { return nil }

func TestSharedPoolGuardianPreemptsBlockedCancellation(t *testing.T) {
	m, _, _ := newPoolTest(t)
	applyPool(t, m, poolIntent("prepare-empty", "p", "alice"))
	host := &signaledPoolSessions{started: make(chan struct{}), release: make(chan struct{})}
	m.Sessions = host
	released := false
	defer func() {
		if !released {
			close(host.release)
		}
	}()
	result := make(chan *nodev1.SharedDshRuntimeReceipt, 1)
	failed := make(chan error, 1)
	go func() {
		r, e := m.Apply(context.Background(), poolIntent("cooperative-cancel", "cancel", "alice"))
		result <- r
		failed <- e
	}()
	<-host.started
	trip := make(chan error, 1)
	go func() {
		_, e := m.Trip(context.Background(), poolID("pool"), poolID("expiry"), "resource-expired")
		trip <- e
	}()
	select {
	case e := <-trip:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("guardian delayed by cancellation lock")
	}
	close(host.release)
	released = true
	r := <-result
	if e := <-failed; e != nil || r.Outcome != "unconfirmed" {
		t.Fatal("late ACK advanced a fenced operation", r, e)
	}
	if e := m.AuthorizePoolStart(context.Background(), poolID("pool"), []*nodev1.SharedDshResourceView{poolView(r.Binding)}, poolViewDouble{}); e == nil {
		t.Fatal("late ACK cleared contamination")
	}
}
