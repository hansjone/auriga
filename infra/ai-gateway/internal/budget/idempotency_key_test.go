package budget

import (
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
)

func TestIdempotencyKeyByteOverflowRejectedBeforeStore(t *testing.T) {
	// No store exists: a 513-byte key must fail validation before any DB work.
	l := new(Ledger)
	r, err := l.Admit(t.Context(), snapshot(t), request(strings.Repeat("x", 513)))
	if r != nil {
		t.Fatal("oversize key admitted")
	}
	wantCode(t, err, "common.validation_failed")
}

func TestIdempotencyKeyUTF8ByteCapacityAdmission(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	for _, c := range []struct {
		name, key string
		valid     bool
	}{
		{"han86", strings.Repeat("中", 86), true},
		{"han128", strings.Repeat("中", 128), true},
		{"fourbyte128", strings.Repeat("😀", 128), true},
		{"internal512ASCII", strings.Repeat("a", 512), true},
		{"over513ASCII", strings.Repeat("a", 513), false},
		{"over516UTF8", strings.Repeat("😀", 129), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			snap := snapshot(t)
			r, err := l.Admit(t.Context(), snap, request(c.key))
			if !c.valid {
				wantCode(t, err, "common.validation_failed")
				err = s.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
					var n int
					if e := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_subject WHERE tenant_id=$1`, snap.Admission.TenantId).Scan(&n); e != nil {
						return e
					}
					if n != 0 {
						t.Fatal("invalid key created a subject/reservation")
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil || r == nil || r.State != "reserved" {
				t.Fatalf("actual admission failed for %d bytes: %v", len(c.key), err)
			}
			o, usage, outbox := counts(t, s, snap.Admission)
			if o != (subject{1, 10, 10}) || usage != 0 || outbox != 0 {
				t.Fatal("actual hold not present", o, usage, outbox)
			}
			_, err = l.Admit(t.Context(), snap, request(c.key))
			wantCode(t, err, "ai.request_in_progress")
			d := dispatch(t, l, r)
			finish(t, l, d, snap, 8, 2)
			_, err = l.Admit(t.Context(), snap, request(c.key))
			wantCode(t, err, "ai.replay_unavailable")
		})
	}
}
