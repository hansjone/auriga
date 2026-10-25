package budget

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func id(t *testing.T) string {
	t.Helper()
	n, err := newID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func snapshot(t *testing.T) TrustedSnapshot {
	t.Helper()
	n := int64(10000)
	tenant, owner := id(t), id(t)
	return TrustedSnapshot{Admission: &ai.BudgetAdmission{TenantId: tenant, AccountId: owner, ModelId: "fake-model", ProviderId: id(t), CredentialId: id(t), Currency: "USD", PriceVersion: "fake-v1", InputTokenUpperBound: 8, MaxOutputTokens: 2, DailyTokens: &n, MonthlyBudgetMicros: &n, Origin: &ai.BudgetAdmission_DeviceId{DeviceId: id(t)}, TokenExpiresAt: timestamppb.New(time.Now().Add(time.Hour))}, Price: api.Price{Currency: "USD", Version: "fake-v1", InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 1000000, EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, ValidUntil: time.Now().Add(time.Minute)}
}
func request(key string) Request {
	return Request{Method: "POST", Path: "/ai/v1/chat/completions", Key: key, Fingerprint: sha256.Sum256([]byte("canonical fake-model request"))}
}
func ledger(t *testing.T, s *store.Store, p *api.AdmissionPolicy) *Ledger {
	t.Helper()
	l, err := New(s, "USD", p)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func ref(r *Reservation) Ref {
	return Ref{r.Admission.TenantId, r.Admission.AccountId, r.Admission.RequestId}
}
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func admit(t *testing.T, l *Ledger, s TrustedSnapshot, key string) *Reservation {
	t.Helper()
	r, err := l.Admit(t.Context(), s, request(key))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func dispatch(t *testing.T, l *Ledger, r *Reservation) *Reservation {
	t.Helper()
	n, err := l.MarkDispatching(t.Context(), ref(r), r.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func finish(t *testing.T, l *Ledger, r *Reservation, s TrustedSnapshot, input, output int64) *Reservation {
	t.Helper()
	n, err := l.Settle(t.Context(), ref(r), r.Revision, Finish{Outcome: "completed", ExecutionStopped: true, Usage: &FinalUsage{InputTokens: input, OutputTokens: output, Price: s.Price}})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func counts(t *testing.T, s *store.Store, a *ai.BudgetAdmission) (subject, int64, int64) {
	t.Helper()
	var owner subject
	var usage, outbox int64
	err := s.Transaction(t.Context(), a.TenantId, func(tx pgx.Tx) error {
		if err := tx.QueryRow(t.Context(), `SELECT active,held_tokens,held_micros FROM ai.budget_subject WHERE tenant_id=$1 AND subject=$2`, a.TenantId, a.AccountId).Scan(&owner.active, &owner.tokens, &owner.micros); err != nil {
			return err
		}
		return tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1 AND subject=$2),(SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1 AND subject=$2)`, a.TenantId, a.AccountId).Scan(&usage, &outbox)
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner, usage, outbox
}

func TestAdmissionRace(t *testing.T) {
	s1, s2 := testpg.Open(t), testpg.Open(t)
	l1, l2 := ledger(t, s1, nil), ledger(t, s2, nil)
	snap := snapshot(t)
	var granted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := l1
			if i%2 == 1 {
				l = l2
			}
			_, err := l.Admit(t.Context(), snap, request("same-key"))
			if err == nil {
				granted.Add(1)
			} else {
				wantCode(t, err, "ai.request_in_progress")
			}
		}(i)
	}
	wg.Wait()
	if granted.Load() != 1 {
		t.Fatalf("executors %d", granted.Load())
	}
	o, u, b := counts(t, s1, snap.Admission)
	if o != (subject{1, 10, 10}) || u != 0 || b != 0 {
		t.Fatalf("atomic reservation: %+v %d %d", o, u, b)
	}
	changed := request("same-key")
	changed.Fingerprint = sha256.Sum256([]byte("different body"))
	_, err := l1.Admit(t.Context(), snap, changed)
	wantCode(t, err, "common.conflict")
	other := snapshot(t)
	if _, err = l2.Admit(t.Context(), other, request("same-key")); err != nil {
		t.Fatal(err)
	}
	// No key creates independent server request IDs, but still shares owner slots.
	r := admit(t, l1, snap, "")
	if r.Admission.RequestId == "" || r.Admission.MaxActiveRequestsPerSubject != 2 || r.Admission.MaxInFlightPercent != 25 {
		t.Fatal("missing generated ID/default policy")
	}
	_, err = l2.Admit(t.Context(), snap, request(""))
	wantCode(t, err, "ai.budget_exceeded")
}

func TestLimitsAndPeriods(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	t.Run("zero", func(t *testing.T) {
		for _, daily := range []bool{true, false} {
			snap := snapshot(t)
			zero := int64(0)
			if daily {
				snap.Admission.DailyTokens = &zero
			} else {
				snap.Admission.MonthlyBudgetMicros = &zero
				snap.Price.InputMicrosPerMillionTokens = 0
				snap.Price.OutputMicrosPerMillionTokens = 0
			}
			_, err := l.Admit(t.Context(), snap, request(""))
			wantCode(t, err, "ai.budget_exceeded")
		}
	})
	t.Run("unlimited", func(t *testing.T) {
		snap := snapshot(t)
		snap.Admission.DailyTokens = nil
		snap.Admission.MonthlyBudgetMicros = nil
		_, err := l.Admit(t.Context(), snap, request(""))
		wantCode(t, err, "ai.budget_unavailable")
		cap := int64(10)
		bounded := ledger(t, s, &api.AdmissionPolicy{MaxActiveRequestsPerSubject: 2, MaxInFlightPercent: 25, MaxInFlightTokens: &cap, MaxInFlightMicros: &cap})
		admit(t, bounded, snap, "")
		_, err = bounded.Admit(t.Context(), snap, request(""))
		wantCode(t, err, "ai.budget_exceeded")
	})
	t.Run("total quota", func(t *testing.T) {
		snap := snapshot(t)
		quota := int64(40)
		snap.Admission.DailyTokens = &quota
		snap.Admission.MonthlyBudgetMicros = &quota
		for i := 0; i < 4; i++ {
			finish(t, l, dispatch(t, l, admit(t, l, snap, "")), snap, 8, 2)
		}
		_, err := l.Admit(t.Context(), snap, request(""))
		wantCode(t, err, "ai.budget_exceeded")
	})
	t.Run("fixed UTC periods", func(t *testing.T) {
		snap := snapshot(t)
		r := dispatch(t, l, admit(t, l, snap, ""))
		// Simulate a durable admission from the previous month, without a product
		// clock override. Settlement must update exactly those stored periods.
		oldDay := "2026-01-31T00:00:00Z"
		oldMonth := "2026-01-01T00:00:00Z"
		err := s.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
			a := proto.Clone(r.Admission).(*ai.BudgetAdmission)
			a.DayPeriodStart = oldDay
			a.MonthPeriodStart = oldMonth
			raw, err := proto.Marshal(a)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(t.Context(), `UPDATE ai.budget_request SET admission=$4 WHERE tenant_id=$1 AND subject=$2 AND request_id=$3`, a.TenantId, a.AccountId, a.RequestId, raw); err != nil {
				return err
			}
			_, err = tx.Exec(t.Context(), `UPDATE ai.budget_period SET start_at=CASE WHEN kind='day' THEN $3::timestamptz ELSE $4::timestamptz END WHERE tenant_id=$1 AND subject=$2`, a.TenantId, a.AccountId, oldDay, oldMonth)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		finish(t, l, r, snap, 3, 1)
		page, err := l.ListUsage(t.Context(), snap.Admission.TenantId, snap.Admission.AccountId, 10, "")
		if err != nil || len(page.Items) != 1 || page.Items[0].DayPeriodStart.Format(time.RFC3339) != oldDay || page.Items[0].MonthPeriodStart.Format(time.RFC3339) != oldMonth {
			t.Fatalf("fixed periods: %+v %v", page, err)
		}
		err = s.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
			var n int
			if e := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_period WHERE tenant_id=$1 AND subject=$2 AND spent=4 AND held=0`, snap.Admission.TenantId, snap.Admission.AccountId).Scan(&n); e != nil {
				return e
			}
			if n != 2 {
				t.Fatalf("original period totals %d", n)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestLifecycle(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	snap := snapshot(t)
	r := admit(t, l, snap, "lifecycle")
	_, err := l.Settle(t.Context(), ref(r), r.Revision, Finish{Outcome: "unknown"})
	wantCode(t, err, "common.precondition_failed")
	d := dispatch(t, l, r)
	_, err = l.MarkDispatching(t.Context(), ref(r), r.Revision)
	wantCode(t, err, "common.precondition_failed")
	done := finish(t, l, d, snap, 3, 1)
	stored, err := l.GetReservation(t.Context(), ref(done))
	if err != nil || stored.Revision != done.Revision || stored.State != "completed" {
		t.Fatalf("durable state %+v %v", stored, err)
	}
	_, err = l.Settle(t.Context(), ref(done), done.Revision, Finish{Outcome: "unknown"})
	wantCode(t, err, "common.precondition_failed")
	_, err = l.Recover(t.Context(), ref(done), d.Revision, RecoveryEvidence{ExecutionStopped: true})
	wantCode(t, err, "common.precondition_failed")
	_, err = l.Admit(t.Context(), snap, request("lifecycle"))
	wantCode(t, err, "ai.replay_unavailable")
	var replay *Error
	if !errors.As(err, &replay) || replay.OriginalRequestID != r.Admission.RequestId || replay.Outcome != "completed" {
		t.Fatal("replay metadata")
	}
	o, u, b := counts(t, s, snap.Admission)
	if o != (subject{}) || u != 1 || b != 1 {
		t.Fatalf("one settlement: %+v %d %d", o, u, b)
	}
	page, err := l.ListUsage(t.Context(), snap.Admission.TenantId, snap.Admission.AccountId, 10, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].GetCostMicros() != 4 || page.Items[0].GetInputTokens() != 3 {
		t.Fatalf("usage %+v %v", page, err)
	}
	// The outbox is a durable CloudEvent containing null-safe contract data.
	err = s.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
		var raw []byte
		if e := tx.QueryRow(t.Context(), `SELECT envelope FROM ai.budget_outbox WHERE tenant_id=$1 AND subject=$2`, snap.Admission.TenantId, snap.Admission.AccountId).Scan(&raw); e != nil {
			return e
		}
		var event map[string]any
		if e := json.Unmarshal(raw, &event); e != nil {
			return e
		}
		data := event["data"].(map[string]any)
		if event["type"] != "usage.recorded" || event["source"] != "auriga/ai-gateway" || data["revision"] != float64(3) || data["deviceId"] != snap.Admission.GetDeviceId() || data["costMicros"] != float64(4) {
			t.Fatalf("outbox %s", raw)
		}
		for _, key := range []string{"frozenTokens", "frozenMicros", "createdAt", "prompt", "body"} {
			if _, ok := data[key]; ok {
				t.Fatalf("unexpected event field %s", key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnknownRecovery(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	t.Run("reserved proof", func(t *testing.T) {
		snap := snapshot(t)
		r := admit(t, l, snap, "")
		_, err := l.Recover(t.Context(), ref(r), r.Revision, RecoveryEvidence{})
		wantCode(t, err, "common.precondition_failed")
		if _, err = l.Recover(t.Context(), ref(r), r.Revision, RecoveryEvidence{NeverSubmitted: true}); err != nil {
			t.Fatal(err)
		}
		o, u, b := counts(t, s, snap.Admission)
		if o != (subject{}) || u != 1 || b != 1 {
			t.Fatalf("release %+v %d %d", o, u, b)
		}
	})
	t.Run("dispatch never refunds", func(t *testing.T) {
		snap := snapshot(t)
		quota := int64(40)
		snap.Admission.DailyTokens = &quota
		r := dispatch(t, l, admit(t, l, snap, ""))
		u, err := l.Recover(t.Context(), ref(r), r.Revision, RecoveryEvidence{NeverSubmitted: true})
		if err != nil || u.State != "unknown" {
			t.Fatalf("unknown %v", err)
		}
		o, _, _ := counts(t, s, snap.Admission)
		if o != (subject{1, 10, 10}) {
			t.Fatalf("unknown held %+v", o)
		}
		_, err = l.Recover(t.Context(), ref(u), u.Revision, RecoveryEvidence{})
		wantCode(t, err, "common.precondition_failed")
		if _, err = l.Recover(t.Context(), ref(u), u.Revision, RecoveryEvidence{ExecutionStopped: true}); err != nil {
			t.Fatal(err)
		}
		o, n, b := counts(t, s, snap.Admission)
		if o != (subject{0, 10, 10}) || n != 1 || b != 1 {
			t.Fatalf("slot only %+v %d %d", o, n, b)
		}
		_, err = l.Admit(t.Context(), snap, request(""))
		wantCode(t, err, "ai.budget_exceeded")
		page, err := l.ListUsage(t.Context(), snap.Admission.TenantId, snap.Admission.AccountId, 10, "")
		if err != nil || len(page.Items) != 1 {
			t.Fatal(err)
		}
		x := page.Items[0]
		if !x.InputTokens.IsSet() || x.InputTokens.Get() != nil || x.OutputTokens.Get() != nil || x.CostMicros.Get() != nil || x.FrozenTokens != 10 || x.FrozenMicros != 10 {
			t.Fatalf("unknown null/freeze %+v", x)
		}
	})
	t.Run("missing final usage", func(t *testing.T) {
		snap := snapshot(t)
		r := dispatch(t, l, admit(t, l, snap, ""))
		u, err := l.Settle(t.Context(), ref(r), r.Revision, Finish{Outcome: "failed", ExecutionStopped: true})
		if err != nil || u.State != "unknown" {
			t.Fatalf("missing usage %v", err)
		}
		o, _, _ := counts(t, s, snap.Admission)
		if o != (subject{0, 10, 10}) {
			t.Fatalf("missing usage refund %+v", o)
		}
	})
	for _, kind := range []string{"negative", "over input", "over output", "price", "currency"} {
		t.Run(kind, func(t *testing.T) {
			snap := snapshot(t)
			r := dispatch(t, l, admit(t, l, snap, ""))
			u := &FinalUsage{InputTokens: 3, OutputTokens: 1, Price: snap.Price}
			switch kind {
			case "negative":
				u.InputTokens = -1
			case "over input":
				u.InputTokens = 9
			case "over output":
				u.OutputTokens = 3
			case "price":
				u.Price.Version = "wrong"
			case "currency":
				u.Price.Currency = "EUR"
			}
			n, err := l.Settle(t.Context(), ref(r), r.Revision, Finish{Outcome: "completed", Usage: u, ExecutionStopped: true})
			wantCode(t, err, "ai.budget_unavailable")
			if n == nil || n.State != "unknown" {
				t.Fatal("anomaly not durably quarantined")
			}
			o, usage, outbox := counts(t, s, snap.Admission)
			if o != (subject{0, 10, 10}) || usage != 1 || outbox != 1 {
				t.Fatalf("anomaly %+v %d %d", o, usage, outbox)
			}
		})
	}
}

func TestAtomicRollback(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	snap := snapshot(t)
	r := dispatch(t, l, admit(t, l, snap, ""))
	// Deny the outbox insert with a transaction-local row-security policy owned
	// by the fixture migration role. This is a real PostgreSQL write failure.
	admin, err := pgx.Connect(t.Context(), testpg.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	policy := "deny_" + strings.ReplaceAll(id(t), "-", "")
	ddl := fmt.Sprintf(`CREATE POLICY %s ON ai.budget_outbox AS RESTRICTIVE FOR INSERT WITH CHECK (tenant_id <> '%s'::uuid)`, pgx.Identifier{policy}.Sanitize(), snap.Admission.TenantId)
	if _, err = admin.Exec(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := admin.Exec(context.Background(), "DROP POLICY IF EXISTS "+pgx.Identifier{policy}.Sanitize()+" ON ai.budget_outbox"); e != nil {
			t.Error(e)
		}
	})
	_, err = l.Settle(t.Context(), ref(r), r.Revision, Finish{Outcome: "completed", ExecutionStopped: true, Usage: &FinalUsage{InputTokens: 3, OutputTokens: 1, Price: snap.Price}})
	if err == nil {
		t.Fatal("outbox failure accepted")
	}
	o, u, b := counts(t, s, snap.Admission)
	if o != (subject{1, 10, 10}) || u != 0 || b != 0 {
		t.Fatalf("partial settlement %+v %d %d", o, u, b)
	}
	if _, err = admin.Exec(t.Context(), "DROP POLICY "+pgx.Identifier{policy}.Sanitize()+" ON ai.budget_outbox"); err != nil {
		t.Fatal(err)
	}
	finish(t, l, r, snap, 3, 1)
}

func TestRLSAndUsagePage(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	snap := snapshot(t)
	for i := 0; i < 3; i++ {
		finish(t, l, dispatch(t, l, admit(t, l, snap, "")), snap, 1, 1)
	}
	page, err := l.ListUsage(t.Context(), snap.Admission.TenantId, snap.Admission.AccountId, 2, "")
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("first page %+v %v", page, err)
	}
	last, err := l.ListUsage(t.Context(), snap.Admission.TenantId, snap.Admission.AccountId, 2, *page.NextCursor)
	if err != nil || len(last.Items) != 1 || last.NextCursor != nil || last.Items[0].RequestId == page.Items[1].RequestId {
		t.Fatalf("last page %+v %v", last, err)
	}
	_, err = l.ListUsage(t.Context(), id(t), snap.Admission.AccountId, 2, *page.NextCursor)
	wantCode(t, err, "common.validation_failed")
	other, err := l.ListUsage(t.Context(), snap.Admission.TenantId, id(t), 10, "")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("owner leak")
	}
	otherTenant := id(t)
	err = s.Transaction(t.Context(), otherTenant, func(tx pgx.Tx) error {
		for _, table := range []string{"budget_subject", "budget_period", "budget_request", "budget_usage", "budget_outbox"} {
			var n int
			if e := tx.QueryRow(t.Context(), "SELECT count(*) FROM ai."+table+" WHERE tenant_id=$1", snap.Admission.TenantId).Scan(&n); e != nil {
				return e
			}
			if n != 0 {
				t.Fatalf("RLS leaked %s", table)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Transaction(t.Context(), otherTenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `INSERT INTO ai.budget_subject(tenant_id,subject) VALUES ($1,$2)`, snap.Admission.TenantId, id(t))
		return e
	})
	if err == nil {
		t.Fatal("cross tenant write accepted")
	}
}

func TestProcessAdmissionHelper(t *testing.T) {
	if os.Getenv("AURIGA_LEDGER_CHILD") == "" {
		t.Skip("subprocess entry point")
	}
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	snap := snapshot(t)
	snap.Admission.TenantId = os.Getenv("AURIGA_LEDGER_TENANT")
	snap.Admission.AccountId = snap.Admission.TenantId
	r, err := l.Admit(t.Context(), snap, request("process-key"))
	if err != nil {
		wantCode(t, err, "ai.request_in_progress")
		return
	}
	dispatch(t, l, r)
	t.Log("EXECUTE " + r.Admission.RequestId)
}
func TestProcessAdmission(t *testing.T) {
	_ = testpg.DSN(t)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tenant := id(t)
	var wg sync.WaitGroup
	var executions atomic.Int64
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.CommandContext(t.Context(), bin, "-test.run=^TestProcessAdmissionHelper$", "-test.v")
			cmd.Env = append(os.Environ(), "AURIGA_LEDGER_CHILD=1", "AURIGA_LEDGER_TENANT="+tenant)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("child: %v\n%s", err, out)
				return
			}
			if strings.Contains(string(out), "EXECUTE ") {
				executions.Add(1)
			}
		}()
	}
	wg.Wait()
	if executions.Load() != 1 {
		t.Fatalf("process executions %d", executions.Load())
	}
	// The executing process has exited and its pool/role have been removed.
	// A new supervisor can still see dispatching and conservatively recover it.
	s := testpg.Open(t)
	var requestID string
	err = s.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT request_id::text FROM ai.budget_request WHERE tenant_id=$1 AND subject=$1`, tenant).Scan(&requestID)
	})
	if err != nil {
		t.Fatal(err)
	}
	l := ledger(t, s, nil)
	r, err := l.GetReservation(t.Context(), Ref{tenant, tenant, requestID})
	if err != nil || r.State != "dispatching" || r.Revision != 2 {
		t.Fatalf("process durability %+v %v", r, err)
	}
	if _, err = l.Recover(t.Context(), ref(r), r.Revision, RecoveryEvidence{ExecutionStopped: true}); err != nil {
		t.Fatal(err)
	}
	o, u, b := counts(t, s, r.Admission)
	if o != (subject{0, 10, 10}) || u != 1 || b != 1 {
		t.Fatalf("process recovery %+v %d %d", o, u, b)
	}
}
