package relay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/jackc/pgx/v5"
)

type pendingTransaction struct {
	*store.Store
	mode          string
	ref           budget.Ref
	cause         error
	target, calls int
}

func (s *pendingTransaction) Transaction(ctx context.Context, tenant string, fn func(pgx.Tx) error) error {
	s.calls++
	if s.calls != s.target {
		return s.Store.Transaction(ctx, tenant, fn)
	}
	if s.mode == "before" {
		return s.cause
	}
	if s.mode == "cas-loser" {
		l, err := budget.New(s.Store, "USD", nil)
		if err != nil {
			return err
		}
		page, err := l.ListRecoveryCandidates(ctx, tenant, 1, "")
		if err != nil {
			return err
		}
		if len(page.Items) != 1 {
			return errors.New("missing reserved CAS competitor")
		}
		r := page.Items[0]
		if _, err = l.Recover(ctx, r.Ref, r.Revision, budget.RecoveryEvidence{NeverSubmitted: true}); err != nil {
			return err
		}
	}
	err := s.Store.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT tenant_id::text,subject::text,request_id::text FROM ai.budget_request WHERE tenant_id=$1`, tenant).Scan(&s.ref.TenantID, &s.ref.AccountID, &s.ref.RequestID); err != nil {
			return err
		}
		if s.mode == "rollback" {
			return s.cause
		}
		return nil
	})
	if err != nil {
		return err
	}
	return fmt.Errorf("lost reply: %w", s.cause)
}

func TestCoreAdmissionPendingRef(t *testing.T) {
	for _, key := range []string{"", "pending-key"} {
		for _, mode := range []string{"commit", "rollback", "before"} {
			t.Run(mode+"/key="+key, func(t *testing.T) {
				f := fixtureNew(t)
				// A budget.Error hidden beneath the ambiguous transaction must not
				// reclassify this result as a definite business denial or leak its ID.
				cause := &budget.Error{Code: "common.conflict", OriginalRequestID: device, Outcome: "private SQL cause"}
				db := &pendingTransaction{Store: f.db, mode: mode, cause: cause, target: 1}
				if mode == "before" {
					db.cause = errors.New("private SQL unavailable")
				}
				l, err := budget.New(db, "USD", nil)
				if err != nil {
					t.Fatal(err)
				}
				f.core.c.Ledger = l
				var calls atomic.Int32
				f.reply(t, completion(textChoice), &calls)
				out, err := f.core.Execute(t.Context(), f.control.raw, strings.NewReader(plainRequest), key, nil)
				expectCode(t, err, "ai.budget_unavailable")
				if !out.Pending || out.Ref != (budget.Ref{}) || out.Reservation != nil || calls.Load() != 0 || db.calls != 1 || out.StopProof != nil {
					t.Fatal("ambiguous Admit gained execution/accounting rights")
				}
				if mode == "before" {
					if out.PendingRef != (budget.Ref{}) {
						t.Fatal("unlocated uncertainty fabricated a hint")
					}
				} else if out.PendingRef != db.ref || out.PendingRef.TenantID != f.control.tenant || out.PendingRef.AccountID != account || out.PendingRef.RequestID == "" {
					t.Fatal("lost precise internal location")
				}
				if _, ok := budget.AdmissionPendingRef(err); ok || errors.Is(err, cause) {
					t.Fatal("internal error escaped Core sanitation")
				}
				for _, v := range []string{fmt.Sprint(out), fmt.Sprintf("%+v %#v", out, out), fmt.Sprintf("%v %+v %#v", err, err, err)} {
					for _, secret := range []string{db.ref.RequestID, f.control.tenant, account, device, "SQL", "private", "conflict"} {
						if secret != "" && strings.Contains(v, secret) {
							t.Fatal("public formatting leaked pending details")
						}
					}
				}
				row, e := f.core.c.Ledger.GetRecoveryCandidate(t.Context(), db.ref)
				if mode == "commit" {
					if e != nil || row.State != "reserved" || row.Revision != 1 {
						t.Fatal("lost reply did not preserve real row", e)
					}
				} else if row != nil {
					t.Fatal("uncertainty manufactured admission")
				}
			})
		}
	}
}

func TestDispatchCommitAmbiguityDoesNotDispatch(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "cas-loser"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtureNew(t)
			db := &pendingTransaction{Store: f.db, mode: mode, cause: errors.New("lost dispatch COMMIT"), target: 2}
			l, err := budget.New(db, "USD", nil)
			if err != nil {
				t.Fatal(err)
			}
			f.core.c.Ledger = l
			var calls atomic.Int32
			f.reply(t, completion(textChoice), &calls)
			out, err := f.core.Execute(t.Context(), f.control.raw, strings.NewReader(plainRequest), "", nil)
			if err == nil || calls.Load() != 0 || out.Ref.RequestID == "" || (out.Pending && out.PendingRef != out.Ref) || (!out.Pending && out.PendingRef != (budget.Ref{})) {
				t.Fatal("dispatch ambiguity gained dispatch rights or admission hint")
			}
			want := "unknown"
			if mode != "commit" {
				want = "cancelled"
			}
			f.facts(t, out, want, false, mode == "commit")
			if mode == "commit" {
				_, e := l.Recover(t.Context(), out.Ref, out.Reservation.Revision, budget.RecoveryEvidence{ExecutionStopped: true})
				expectCode(t, e, "common.precondition_failed")
				f.facts(t, out, "unknown", false, true) // Re-scanning cannot duplicate usage/outbox or refund frozen amounts.
			}
		})
	}
}

func TestPendingRefOutputFormatting(t *testing.T) {
	o := Output{Pending: true, PendingRef: budget.Ref{TenantID: "private-tenant", AccountID: "private-owner", RequestID: "private-request"}}
	for _, v := range []string{fmt.Sprint(o), fmt.Sprintf("%+v %#v", o, o)} {
		if strings.Contains(v, "private") {
			t.Fatal("formatting leaked pending location")
		}
	}
}
