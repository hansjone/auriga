package budget

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Execute the real callback/INSERT under RLS, then control only the transaction
// boundary. A returned failure deliberately says nothing about its outcome.
type pendingStore struct {
	*store.Store
	mode     string
	cause    error
	inserted chan Ref
	release  <-chan struct{}
}

func (s *pendingStore) Transaction(ctx context.Context, tenant string, fn func(pgx.Tx) error) error {
	if s.mode == "before" {
		return s.cause
	}
	err := s.Store.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		if s.mode == "insert" || s.mode == "zero-insert" {
			return fn(&pendingInsertTx{Tx: tx, zero: s.mode == "zero-insert"})
		}
		if err := fn(tx); err != nil {
			return err
		}
		var r Ref
		err := tx.QueryRow(ctx, `SELECT tenant_id::text,subject::text,request_id::text FROM ai.budget_request WHERE tenant_id=$1`, tenant).Scan(&r.TenantID, &r.AccountID, &r.RequestID)
		if err != nil {
			return err
		}
		s.inserted <- r
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if s.mode == "rollback" {
			return s.cause
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.cause // COMMIT really succeeded; its reply was lost.
}

type pendingInsertTx struct {
	pgx.Tx
	zero bool
}

func (tx *pendingInsertTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(sql, "INSERT INTO ai.budget_request(") {
		if tx.zero {
			return pgconn.NewCommandTag("INSERT 0 0"), nil
		}
		sql += " RETURNING nonexistent_pending_ref_column"
	}
	return tx.Tx.Exec(ctx, sql, args...)
}

func TestAdmissionPendingRefStages(t *testing.T) {
	s := testpg.Open(t)
	reader := ledger(t, s, nil)
	for _, key := range []string{"", "internal-key"} {
		for _, mode := range []string{"commit", "rollback"} {
			t.Run(mode+"/key="+key, func(t *testing.T) {
				snap := snapshot(t)
				cause := &Error{Code: "common.conflict", OriginalRequestID: id(t), Outcome: "private SQL cause"}
				release := make(chan struct{})
				defer close(release)
				db := &pendingStore{Store: s, mode: mode, cause: cause, inserted: make(chan Ref, 1), release: release}
				l, err := New(db, "USD", nil)
				if err != nil {
					t.Fatal(err)
				}
				type outcome struct {
					r   *Reservation
					err error
				}
				done := make(chan outcome, 1)
				go func() { r, e := l.Admit(t.Context(), snap, request(key)); done <- outcome{r, e} }()
				var actual Ref
				select {
				case actual = <-db.inserted:
				case <-time.After(5 * time.Second):
					t.Fatal("INSERT callback did not finish")
				}
				if actual.TenantID != snap.Admission.TenantId || actual.AccountID != snap.Admission.AccountId || actual.RequestID == "" || actual.RequestID == cause.OriginalRequestID {
					t.Fatal("incorrect inserted location")
				}
				// Missing under an independent READ COMMITTED transaction while the
				// original transaction is still unresolved is NOT rollback proof.
				_, err = reader.GetRecoveryCandidate(t.Context(), actual)
				wantCode(t, err, "common.not_found")
				select {
				case <-done:
					t.Fatal("admission returned before commit boundary")
				default:
				}
				release <- struct{}{}
				var got outcome
				select {
				case got = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("admission did not return")
				}
				hint, ok := AdmissionPendingRef(got.err)
				if got.r != nil || !ok || hint != actual {
					t.Fatal("missing exact pending hint or executable reservation returned")
				}
				var be *Error
				if !errors.Is(got.err, cause) || !errors.As(got.err, &be) || be != cause {
					t.Fatal("lost internal cause")
				}
				wrapped := fmt.Errorf("transaction: %w", got.err)
				if h, ok := AdmissionPendingRef(wrapped); !ok || h != actual {
					t.Fatal("wrapped hint lost")
				}
				for _, v := range []string{got.err.Error(), fmt.Sprint(got.err), fmt.Sprintf("%+v", got.err), fmt.Sprintf("%#v", got.err), fmt.Sprintf("%s %q", got.err, got.err)} {
					for _, secret := range []string{actual.TenantID, actual.AccountID, actual.RequestID, cause.OriginalRequestID, "SQL", "private", "conflict"} {
						if strings.Contains(v, secret) {
							t.Fatal("formatted pending error leaked")
						}
					}
				}
				row, err := reader.GetRecoveryCandidate(t.Context(), hint)
				if mode == "commit" {
					if err != nil || row.Ref != actual || row.State != "reserved" || row.Revision != 1 || !row.SlotHeld {
						t.Fatal("committed row not found under RLS", err)
					}
					wrong := hint
					wrong.AccountID = id(t)
					_, err = reader.GetRecoveryCandidate(t.Context(), wrong)
					wantCode(t, err, "common.not_found")
					o, u, b := counts(t, s, snap.Admission)
					if o != (subject{1, 10, 10}) || u != 0 || b != 0 {
						t.Fatal("ambiguous admission dispatched/settled/refunded")
					}
				} else {
					wantCode(t, err, "common.not_found")
					if row != nil {
						t.Fatal("rollback fabricated row")
					}
					err = s.Transaction(t.Context(), actual.TenantID, func(tx pgx.Tx) error {
						var n int
						if err := tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1)+(SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1)+(SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1)+(SELECT count(*) FROM ai.budget_subject WHERE tenant_id=$1)`, actual.TenantID).Scan(&n); err != nil {
							return err
						}
						if n != 0 {
							t.Error("rollback manufactured accounting")
						}
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestAdmissionPendingRefAbsent(t *testing.T) {
	s := testpg.Open(t)
	for _, mode := range []string{"before", "insert", "zero-insert", "validation", "budget", "snapshot", "in-progress", "conflict", "replay"} {
		t.Run(mode, func(t *testing.T) {
			snap := snapshot(t)
			l := ledger(t, s, nil)
			req := request("old-key")
			switch mode {
			case "before", "insert", "zero-insert":
				l.store = &pendingStore{Store: s, mode: mode, cause: errors.New("private transaction failure")}
			case "validation":
				req.Method = "GET"
			case "budget":
				n := int64(0)
				snap.Admission.DailyTokens = &n
			case "snapshot":
				snap.ValidUntil = time.Now().Add(-time.Second)
			case "in-progress", "conflict", "replay":
				r := admit(t, l, snap, req.Key)
				if mode == "conflict" {
					req.Fingerprint[0] ^= 1
				}
				if mode == "replay" {
					finish(t, l, dispatch(t, l, r), snap, 8, 2)
				}
			}
			r, err := l.Admit(t.Context(), snap, req)
			if err == nil || r != nil {
				t.Fatal("expected admission refusal")
			}
			if h, ok := AdmissionPendingRef(err); ok || h != (Ref{}) {
				t.Fatal("non-inserted attempt fabricated pending hint")
			}
			if mode == "in-progress" || mode == "replay" {
				var be *Error
				if !errors.As(err, &be) || be.OriginalRequestID == "" {
					t.Fatal("lost existing idempotency semantics")
				}
			}
		})
	}
	if h, ok := AdmissionPendingRef(nil); ok || h != (Ref{}) {
		t.Fatal("nil produced hint")
	}
}

func TestAdmissionPendingErrorFormatting(t *testing.T) {
	cause := &Error{Code: "common.conflict", OriginalRequestID: "private-original", Outcome: "private SQL"}
	err := &admissionPending{ref: Ref{TenantID: "private-tenant", AccountID: "private-account", RequestID: "private-request"}, cause: cause}
	for _, value := range []any{err, *err, fmt.Errorf("transaction: %w", err)} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			v := fmt.Sprintf(format, value)
			if strings.Contains(v, "private") || strings.Contains(v, "SQL") || strings.Contains(v, "conflict") {
				t.Fatalf("format %s leaked internal pending error", format)
			}
		}
	}
	var be *Error
	if !errors.Is(err, cause) || !errors.As(err, &be) || be != cause {
		t.Fatal("lost cause identity")
	}
	if r, ok := AdmissionPendingRef(fmt.Errorf("wrapped: %w", err)); !ok || r != err.ref {
		t.Fatal("wrapped location lost")
	}
}
