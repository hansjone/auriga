package relaysupervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type memoryLedger struct {
	mu            sync.Mutex
	c             budget.RecoveryCandidate
	fail          bool
	reads, writes int
}

type faultyLedger struct {
	base                             *budget.Ledger
	fail, hide, stale, commitUnknown atomic.Bool
	reads                            atomic.Int64
}

func (l *faultyLedger) GetRecoveryCandidate(ctx context.Context, r budget.Ref) (*budget.RecoveryCandidate, error) {
	l.reads.Add(1)
	if l.fail.Load() {
		return nil, errors.New("database unavailable")
	}
	if l.hide.Load() {
		return nil, errors.New("common.not_found")
	}
	c, e := l.base.GetRecoveryCandidate(ctx, r)
	if e == nil && l.stale.Swap(false) {
		// A real competing dispatch wins after the read. The old reserved
		// revision is still valid syntactically but must lose the recovery CAS.
		if _, e = l.base.MarkDispatching(ctx, r, c.Revision); e != nil {
			return nil, e
		}
	}
	return c, e
}
func (l *faultyLedger) Recover(ctx context.Context, r budget.Ref, rev int64, p budget.RecoveryEvidence) (*budget.Reservation, error) {
	n, e := l.base.Recover(ctx, r, rev, p)
	if e == nil && l.commitUnknown.Swap(false) {
		return nil, errors.New("COMMIT response lost")
	}
	return n, e
}
func pgID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	b[6] = b[6]&15 | 0x70
	b[8] = b[8]&63 | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func pgAdmission(t *testing.T, l *budget.Ledger) (budget.Ref, *budget.Reservation) {
	t.Helper()
	quota := int64(10000)
	until := time.Now().Add(time.Minute)
	a := &ai.BudgetAdmission{TenantId: pgID(t), AccountId: pgID(t), ModelId: "synthetic", ProviderId: pgID(t), CredentialId: pgID(t), Currency: "USD", PriceVersion: "fake-v1", InputTokenUpperBound: 8, MaxOutputTokens: 2, DailyTokens: &quota, MonthlyBudgetMicros: &quota, Origin: &ai.BudgetAdmission_DeviceId{DeviceId: pgID(t)}, TokenExpiresAt: timestamppb.New(until)}
	r, e := l.Admit(t.Context(), budget.TrustedSnapshot{Admission: a, Price: api.Price{Currency: "USD", Version: "fake-v1", InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 1000000, EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, ValidUntil: until}, budget.Request{Method: "POST", Path: "/ai/v1/chat/completions", Fingerprint: sha256.Sum256([]byte("synthetic"))})
	if e != nil {
		t.Fatal(e)
	}
	return budget.Ref{TenantID: a.TenantId, AccountID: a.AccountId, RequestID: r.Admission.RequestId}, r
}
func pgSupervisor(t *testing.T, l ledger, n int) *Supervisor {
	t.Helper()
	s, e := New(l, n, 100*time.Millisecond, 5*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer c()
		_ = s.Drain(ctx)
	})
	return s
}
func pgState(t *testing.T, l *budget.Ledger, r budget.Ref) *budget.RecoveryCandidate {
	t.Helper()
	c, e := l.GetRecoveryCandidate(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func pgCounts(t *testing.T, db *store.Store, r budget.Ref) (int, int64, int64, int, int) {
	t.Helper()
	var active, u, o int
	var tokens, micros int64
	e := db.Transaction(t.Context(), r.TenantID, func(tx pgx.Tx) error {
		if e := tx.QueryRow(t.Context(), `SELECT active,held_tokens,held_micros FROM ai.budget_subject WHERE tenant_id=$1 AND subject=$2`, r.TenantID, r.AccountID).Scan(&active, &tokens, &micros); e != nil {
			return e
		}
		return tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1 AND subject=$2),(SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1 AND subject=$2)`, r.TenantID, r.AccountID).Scan(&u, &o)
	})
	if e != nil {
		t.Fatal(e)
	}
	return active, tokens, micros, u, o
}
func TestSupervisorPGReservedFenceAndDispatchProof(t *testing.T) {
	db := testpg.Open(t)
	l, e := budget.New(db, "USD", nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("reserved cancellation fences old dispatch", func(t *testing.T) {
		ref, r := pgAdmission(t, l)
		s := pgSupervisor(t, l, 1)
		finish, _ := s.Reserve()
		finish(&Pending{Ref: ref})
		eventually(t, func() bool { return s.Stats().Occupied == 0 })
		if _, e := l.MarkDispatching(t.Context(), ref, r.Revision); e == nil {
			t.Fatal("old dispatch survived cancelled CAS")
		}
		a, tokens, micros, u, o := pgCounts(t, db, ref)
		if a != 0 || tokens != 0 || micros != 0 || u != 1 || o != 1 {
			t.Fatal("reserved refund/accounting", a, tokens, micros, u, o)
		}
	})
	t.Run("dispatch wins and sent value never releases", func(t *testing.T) {
		ref, r := pgAdmission(t, l)
		if _, e = l.MarkDispatching(t.Context(), ref, r.Revision); e != nil {
			t.Fatal(e)
		}
		proof := make(chan struct{}, 1)
		proof <- struct{}{}
		s := pgSupervisor(t, l, 2)
		for range 2 {
			finish, _ := s.Reserve()
			finish(&Pending{Ref: ref, StopProof: proof})
		}
		eventually(t, func() bool { return pgState(t, l, ref).State == "unknown" })
		a, tokens, micros, u, o := pgCounts(t, db, ref)
		if a != 1 || tokens != 10 || micros != 10 || u != 1 || o != 1 {
			t.Fatal("value was treated as proof or refunded", a, tokens, micros, u, o)
		}
		if _, ok := s.Reserve(); ok {
			t.Fatal("unknown responsibility lost")
		}
		close(proof)
		eventually(t, func() bool { return s.Stats().Occupied == 0 })
		a, tokens, micros, u, o = pgCounts(t, db, ref)
		if a != 0 || tokens != 10 || micros != 10 || u != 1 || o != 1 {
			t.Fatal("closure duplicated usage/refunded holds", a, tokens, micros, u, o)
		}
	})
}
func TestSupervisorPGFaultsRereadAndNoScan(t *testing.T) {
	db := testpg.Open(t)
	l, e := budget.New(db, "USD", nil)
	if e != nil {
		t.Fatal(e)
	}
	active, ar := pgAdmission(t, l)
	dispatching, dr := pgAdmission(t, l)
	if _, e = l.MarkDispatching(t.Context(), dispatching, dr.Revision); e != nil {
		t.Fatal(e)
	}
	ref, _ := pgAdmission(t, l)
	proof := make(chan struct{})
	close(proof)
	fault := &faultyLedger{base: l}
	fault.fail.Store(true)
	s := pgSupervisor(t, fault, 1)
	finish, _ := s.Reserve()
	if fault.reads.Load() != 0 {
		t.Fatal("read active cell")
	}
	finish(&Pending{Ref: ref, StopProof: proof})
	eventually(t, func() bool { return s.Stats().Faulted == 1 })
	fault.hide.Store(true)
	fault.fail.Store(false)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	<-ctx.Done()
	if s.Stats().Occupied != 1 {
		t.Fatal("not found lost responsibility")
	}
	if pgState(t, l, active).Revision != ar.Revision || pgState(t, l, dispatching).State != "dispatching" {
		t.Fatal("worker touched unhanded active rows")
	}
	fault.stale.Store(true)
	fault.commitUnknown.Store(true)
	fault.hide.Store(false)
	eventually(t, func() bool { return s.Stats().Occupied == 0 })
	if pgState(t, l, ref).State != "unknown" || fault.reads.Load() < 3 {
		t.Fatal("no fresh reads after stale/ambiguous commit")
	}
	// Restart has no cells/proofs and does not discover or mutate old dispatch.
	restart := pgSupervisor(t, l, 1)
	if restart.Stats().Occupied != 0 || pgState(t, l, dispatching).State != "dispatching" {
		t.Fatal("restart fabricated recovery evidence")
	}
}
func TestSupervisorPGTwoWorkersAndMissingReferences(t *testing.T) {
	db := testpg.Open(t)
	l, e := budget.New(db, "USD", nil)
	if e != nil {
		t.Fatal(e)
	}
	ref, _ := pgAdmission(t, l)
	a, b := pgSupervisor(t, l, 1), pgSupervisor(t, l, 1)
	for _, s := range []*Supervisor{a, b} {
		f, _ := s.Reserve()
		f(&Pending{Ref: ref})
	}
	eventually(t, func() bool { return a.Stats().Occupied == 0 && b.Stats().Occupied == 0 })
	_, _, _, u, o := pgCounts(t, db, ref)
	if u != 1 || o != 1 {
		t.Fatal("two recoverers duplicate accounting")
	}
	s := pgSupervisor(t, l, 2)
	f, _ := s.Reserve()
	f(&Pending{})
	f, _ = s.Reserve()
	missing := ref
	missing.RequestID = pgID(t)
	f(&Pending{Ref: missing})
	eventually(t, func() bool { return s.Stats().Faulted == 1 })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if s.Drain(ctx) == nil || s.Stats().Occupied != 1 {
		t.Fatal("missing refs reported clean drain")
	}
}

type admissionCommitLost struct{ db *store.Store }

func (l admissionCommitLost) Transaction(ctx context.Context, tenant string, fn func(pgx.Tx) error) error {
	if e := l.db.Transaction(ctx, tenant, fn); e != nil {
		return e
	}
	return errors.New("admission COMMIT reply lost")
}
func TestSupervisorPGAdmissionCommitUnknownAndNoProof(t *testing.T) {
	db := testpg.Open(t)
	l, e := budget.New(db, "USD", nil)
	if e != nil {
		t.Fatal(e)
	}
	ref, r := pgAdmission(t, l)
	// Exercise the same durable transaction boundary as an ambiguous Admit.
	unknown, e := budget.New(admissionCommitLost{db}, "USD", nil)
	if e != nil {
		t.Fatal(e)
	}
	a := r.Admission
	a.RequestId = ""
	a.AccountId = pgID(t)
	until := time.Now().Add(time.Minute)
	_, e = unknown.Admit(t.Context(), budget.TrustedSnapshot{Admission: a, Price: api.Price{Currency: "USD", Version: "fake-v1", InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 1000000, EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, ValidUntil: until}, budget.Request{Method: "POST", Path: "/ai/v1/chat/completions", Fingerprint: sha256.Sum256([]byte("commit-unknown"))})
	pending, ok := budget.AdmissionPendingRef(e)
	if !ok || pending.RequestID == "" {
		t.Fatal("admission uncertainty lost full location")
	}
	s := pgSupervisor(t, l, 1)
	finish, _ := s.Reserve()
	finish(&Pending{Ref: pending})
	eventually(t, func() bool { return s.Stats().Occupied == 0 })
	active, tokens, micros, u, o := pgCounts(t, db, pending)
	if active != 0 || tokens != 0 || micros != 0 || u != 1 || o != 1 {
		t.Fatal("ambiguous admission reexecuted or not recovered")
	}
	if _, e = l.MarkDispatching(t.Context(), ref, r.Revision); e != nil {
		t.Fatal(e)
	}
	s = pgSupervisor(t, l, 1)
	finish, _ = s.Reserve()
	finish(&Pending{Ref: ref})
	eventually(t, func() bool { return pgState(t, l, ref).State == "unknown" })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if s.Drain(ctx) == nil || !pgState(t, l, ref).SlotHeld {
		t.Fatal("no proof falsely drained")
	}
}

func TestSupervisorPGPersistentDatabaseFailureCannotDrain(t *testing.T) {
	db := testpg.Open(t)
	l, e := budget.New(db, "USD", nil)
	if e != nil {
		t.Fatal(e)
	}
	ref, r := pgAdmission(t, l)
	if _, e = l.MarkDispatching(t.Context(), ref, r.Revision); e != nil {
		t.Fatal(e)
	}
	var role string
	if e = db.Transaction(t.Context(), ref.TenantID, func(tx pgx.Tx) error { return tx.QueryRow(t.Context(), "SELECT current_user").Scan(&role) }); e != nil {
		t.Fatal(e)
	}
	conn, e := pgx.Connect(t.Context(), testpg.DSN(t))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	acl := func(ctx context.Context, sql string) error {
		tx, e := conn.Begin(ctx)
		if e != nil {
			return e
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718245010)"); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, sql); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	quoted := pgx.Identifier{role}.Sanitize()
	if e = acl(t.Context(), "REVOKE UPDATE ON ai.budget_request FROM "+quoted); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e := acl(ctx, "GRANT UPDATE ON ai.budget_request TO "+quoted); e != nil {
			t.Error(e)
		}
	})
	s := pgSupervisor(t, l, 1)
	proof := make(chan struct{})
	close(proof)
	finish, _ := s.Reserve()
	finish(&Pending{Ref: ref, StopProof: proof})
	eventually(t, func() bool { return s.Stats().Faulted == 1 })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if s.Drain(ctx) == nil || s.Stats().Occupied != 1 {
		t.Fatal("database failure falsely drained")
	}
	if c := pgState(t, l, ref); c.State != "dispatching" || !c.SlotHeld {
		t.Fatal("failed commit lost durable responsibility")
	}
	a, tokens, micros, u, o := pgCounts(t, db, ref)
	if a != 1 || tokens != 10 || micros != 10 || u != 0 || o != 0 {
		t.Fatal("database failure changed accounting", a, tokens, micros, u, o)
	}
}

func (l *memoryLedger) GetRecoveryCandidate(_ context.Context, ref budget.Ref) (*budget.RecoveryCandidate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reads++
	if l.fail || ref != l.c.Ref {
		return nil, errors.New("unavailable")
	}
	c := l.c
	return &c, nil
}
func (l *memoryLedger) Recover(_ context.Context, ref budget.Ref, rev int64, p budget.RecoveryEvidence) (*budget.Reservation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ref != l.c.Ref || rev != l.c.Revision {
		return nil, errors.New("CAS")
	}
	l.writes++
	l.c.Revision++
	if l.c.State == "reserved" && p.NeverSubmitted {
		l.c.State = "cancelled"
		l.c.SlotHeld = false
	} else {
		l.c.State = "unknown"
		l.c.SlotHeld = !p.ExecutionStopped
	}
	return &budget.Reservation{State: l.c.State, Revision: l.c.Revision}, nil
}
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !f() {
		select {
		case <-timer.C:
			t.Fatal("condition timed out")
		case <-tick.C:
		}
	}
}
func newTest(t *testing.T, l *memoryLedger, n int) *Supervisor {
	t.Helper()
	s, e := New(l, n, 10*time.Millisecond, 5*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer c()
		_ = s.Drain(ctx)
	})
	return s
}
func TestReservationOwnsCapacityBeforeHandoff(t *testing.T) {
	l := &memoryLedger{}
	s := newTest(t, l, 1)
	finish, ok := s.Reserve()
	if !ok {
		t.Fatal("reserve")
	}
	if _, ok = s.Reserve(); ok {
		t.Fatal("second admitted before handoff")
	}
	l.mu.Lock()
	if l.reads != 0 {
		t.Fatal("active reservation was read")
	}
	l.mu.Unlock()
	finish(nil)
	finish(&Pending{Faulted: true})
	if s.Stats().Occupied != 0 {
		t.Fatal("finish was not exactly once")
	}
	finish, ok = s.Reserve()
	if !ok {
		t.Fatal("capacity not returned")
	}
	s.CloseAdmission()
	if _, ok = s.Reserve(); ok {
		t.Fatal("closed admission")
	}
	finish(&Pending{Ref: budget.Ref{TenantID: "late", AccountID: "a", RequestID: "r"}})
	if s.Stats().Occupied != 1 {
		t.Fatal("late handoff lost")
	}
}

func TestEmptyAdmissionRefCanDrain(t *testing.T) {
	l := &memoryLedger{}
	s := newTest(t, l, 1)
	finish, _ := s.Reserve()
	finish(&Pending{})
	if s.Stats().Occupied != 0 {
		t.Fatal("empty admission Ref retained capacity")
	}
	if e := s.Drain(t.Context()); e != nil {
		t.Fatal(e)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.reads != 0 {
		t.Fatal("empty Ref queried PG")
	}
}
func TestUnknownWaitsLocallyForClosedProof(t *testing.T) {
	ref := budget.Ref{TenantID: "t", AccountID: "a", RequestID: "r"}
	l := &memoryLedger{c: budget.RecoveryCandidate{Ref: ref, State: "unknown", Revision: 4, SlotHeld: true}}
	s := newTest(t, l, 1)
	proof := make(chan struct{}, 1)
	finish, _ := s.Reserve()
	finish(&Pending{Ref: ref, StopProof: proof})
	eventually(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.cells[0].waitProof })
	l.mu.Lock()
	reads := l.reads
	l.mu.Unlock()
	proof <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-tick.C:
			s.signal()
			l.mu.Lock()
			got := l.reads
			l.mu.Unlock()
			if got != reads {
				t.Fatal("waiting proof caused PG polling")
			}
		}
	}
	close(proof)
	eventually(t, func() bool { return s.Stats().Occupied == 0 })
}
func TestDatabaseFaultUsesPerCellBackoff(t *testing.T) {
	ref := budget.Ref{TenantID: "t", AccountID: "a", RequestID: "r"}
	l := &memoryLedger{fail: true, c: budget.RecoveryCandidate{Ref: ref}}
	s := newTest(t, l, 1)
	finish, _ := s.Reserve()
	finish(&Pending{Ref: ref})
	eventually(t, func() bool { return s.Stats().Faulted == 1 })
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Millisecond)
	defer cancel()
	<-ctx.Done()
	l.mu.Lock()
	reads := l.reads
	l.mu.Unlock()
	if reads < 2 || reads > 5 {
		t.Fatal("fault retry did not back off", reads)
	}
}

type stalledRecovery struct {
	started chan struct{}
	once    sync.Once
}

func (l *stalledRecovery) GetRecoveryCandidate(ctx context.Context, r budget.Ref) (*budget.RecoveryCandidate, error) {
	if r.RequestID == "good" {
		return &budget.RecoveryCandidate{Ref: r, State: "completed", Revision: 3}, nil
	}
	l.once.Do(func() { close(l.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*stalledRecovery) Recover(context.Context, budget.Ref, int64, budget.RecoveryEvidence) (*budget.Reservation, error) {
	return nil, errors.New("unexpected recovery")
}
func TestNewHandoffNotBehindFullDatabaseFailureSweep(t *testing.T) {
	l := &stalledRecovery{started: make(chan struct{})}
	s, e := New(l, 32, time.Second, 5*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancel()
		_ = s.Drain(ctx)
		<-s.done
	})
	for range 31 {
		finish, _ := s.Reserve()
		finish(&Pending{Ref: budget.Ref{TenantID: "t", AccountID: "a", RequestID: "bad"}})
	}
	<-l.started
	finish, _ := s.Reserve()
	finish(&Pending{Ref: budget.Ref{TenantID: "t", AccountID: "a", RequestID: "good"}})
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for s.Stats().Occupied == 32 {
		select {
		case <-ctx.Done():
			t.Fatal("new handoff stuck behind capacity*timeout sweep")
		case <-tick.C:
		}
	}
}
func TestSentValueIsNotStopProof(t *testing.T) {
	ref := budget.Ref{TenantID: "tenant", AccountID: "account", RequestID: "request"}
	l := &memoryLedger{c: budget.RecoveryCandidate{Ref: ref, State: "dispatching", Revision: 7, SlotHeld: true}}
	s := newTest(t, l, 1)
	proof := make(chan struct{}, 1)
	proof <- struct{}{}
	finish, _ := s.Reserve()
	finish(&Pending{Ref: ref, StopProof: proof})
	eventually(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.c.State == "unknown" })
	l.mu.Lock()
	held, writes := l.c.SlotHeld, l.writes
	l.mu.Unlock()
	if !held || writes != 1 || s.Stats().Occupied != 1 {
		t.Fatal("sent value released responsibility")
	}
	close(proof)
	eventually(t, func() bool { return s.Stats().Occupied == 0 })
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c.SlotHeld || l.writes != 2 {
		t.Fatal("closed proof did not release exactly once")
	}
}
func TestUnlocatedMissingAndDatabaseFailureRetainCells(t *testing.T) {
	l := &memoryLedger{fail: true}
	s := newTest(t, l, 3)
	for _, p := range []Pending{{Faulted: true}, {Ref: budget.Ref{TenantID: "wrong"}}, {Ref: budget.Ref{TenantID: "t", AccountID: "a", RequestID: "r"}}} {
		f, ok := s.Reserve()
		if !ok {
			t.Fatal("reserve")
		}
		f(&p)
	}
	eventually(t, func() bool { return s.Stats().Faulted > 0 })
	ctx, c := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer c()
	if s.Drain(ctx) == nil || s.Stats().Occupied != 3 {
		t.Fatal("false clean drain")
	}
}
func TestLatestRevisionAndDatabaseRecovery(t *testing.T) {
	ref := budget.Ref{TenantID: "t", AccountID: "a", RequestID: "r"}
	l := &memoryLedger{fail: true, c: budget.RecoveryCandidate{Ref: ref, State: "reserved", Revision: 93, SlotHeld: true}}
	s := newTest(t, l, 1)
	f, _ := s.Reserve()
	f(&Pending{Ref: ref})
	eventually(t, func() bool { return s.Stats().Faulted > 0 })
	l.mu.Lock()
	l.fail = false
	l.mu.Unlock()
	eventually(t, func() bool { return s.Stats().Occupied == 0 })
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.c.State != "cancelled" || l.c.Revision != 94 {
		t.Fatal("latest CAS not used")
	}
}
