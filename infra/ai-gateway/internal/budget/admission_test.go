package budget

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestIndependentRequestCompetition(t *testing.T) {
	s1, s2 := testpg.Open(t), testpg.Open(t)
	for _, kind := range []string{"slots", "tokens", "micros", "absolute minimum", "unlimited tokens", "unlimited micros"} {
		t.Run(kind, func(t *testing.T) {
			snap := snapshot(t)
			p := &api.AdmissionPolicy{MaxActiveRequestsPerSubject: 100, MaxInFlightPercent: 25}
			expected := int64(1)
			quota := int64(40)
			switch kind {
			case "slots":
				p.MaxActiveRequestsPerSubject = 2
				expected = 2
			case "tokens":
				snap.Admission.DailyTokens = &quota
			case "micros":
				snap.Admission.MonthlyBudgetMicros = &quota
			case "absolute minimum":
				cap := int64(10)
				p.MaxInFlightTokens = &cap
				p.MaxInFlightMicros = &cap
			case "unlimited tokens":
				cap := int64(10)
				snap.Admission.DailyTokens = nil
				p.MaxInFlightTokens = &cap
			case "unlimited micros":
				cap := int64(10)
				snap.Admission.MonthlyBudgetMicros = nil
				p.MaxInFlightMicros = &cap
			}
			l1, l2 := ledger(t, s1, p), ledger(t, s2, p)
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
					_, err := l.Admit(t.Context(), snap, request(""))
					if err == nil {
						granted.Add(1)
					} else {
						wantCode(t, err, "common.rate_limited")
					}
				}(i)
			}
			wg.Wait()
			if granted.Load() != expected {
				t.Fatalf("granted %d want %d", granted.Load(), expected)
			}
			o, _, _ := counts(t, s1, snap.Admission)
			if o != (subject{expected, expected * 10, expected * 10}) {
				t.Fatalf("overspend %+v", o)
			}
			err := s1.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
				var requests, periods int64
				if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2`, snap.Admission.TenantId, snap.Admission.AccountId).Scan(&requests); err != nil {
					return err
				}
				if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_period WHERE tenant_id=$1 AND subject=$2 AND held=$3 AND spent=0`, snap.Admission.TenantId, snap.Admission.AccountId, expected*10).Scan(&periods); err != nil {
					return err
				}
				if requests != expected || periods != 2 {
					t.Fatalf("rejected admission persisted writes: requests=%d periods=%d", requests, periods)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTrustedSnapshotValidation(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	for _, kind := range []string{"input absent", "output absent", "overflow", "price overflow", "currency", "version", "future price", "expired snapshot", "expired token", "origin absent", "negative quota"} {
		t.Run(kind, func(t *testing.T) {
			snap := snapshot(t)
			switch kind {
			case "input absent":
				snap.Admission.InputTokenUpperBound = 0
			case "output absent":
				snap.Admission.MaxOutputTokens = 0
			case "overflow":
				snap.Admission.InputTokenUpperBound = maxInteger
			case "price overflow":
				snap.Price.InputMicrosPerMillionTokens = maxInteger
				snap.Admission.InputTokenUpperBound = maxInteger - 2
			case "currency":
				snap.Price.Currency = "EUR"
			case "version":
				snap.Price.Version = "other"
			case "future price":
				snap.Price.EffectiveAt = time.Now().Add(time.Hour)
			case "expired snapshot":
				snap.ValidUntil = time.Now().Add(-time.Second)
			case "expired token":
				snap.Admission.TokenExpiresAt = timestamppb.New(time.Now().Add(-time.Second))
			case "origin absent":
				snap.Admission.Origin = nil
			case "negative quota":
				n := int64(-1)
				snap.Admission.DailyTokens = &n
			}
			_, err := l.Admit(t.Context(), snap, request(""))
			wantCode(t, err, "ai.budget_unavailable")
		})
	}
	admit(t, l, snapshot(t), "")
	snap := snapshot(t)
	other, err := New(s, "EUR", nil)
	if err != nil {
		t.Fatal(err)
	}
	snap.Admission.Currency = "EUR"
	snap.Price.Currency = "EUR"
	_, err = other.Admit(t.Context(), snap, request(""))
	wantCode(t, err, "ai.budget_unavailable")
	// Policy pointers, including the returned proto, cannot enlarge deployment limits.
	cap := int64(10)
	p := &api.AdmissionPolicy{MaxActiveRequestsPerSubject: 2, MaxInFlightPercent: 25, MaxInFlightTokens: &cap}
	limited := ledger(t, s, p)
	cap = 1000
	snap = snapshot(t)
	r := admit(t, limited, snap, "redacted-key")
	*r.Admission.MaxInFlightTokens = 1000
	_, err = limited.Admit(t.Context(), snap, request(""))
	wantCode(t, err, "common.rate_limited")
	err = s.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
		var text string
		if err := tx.QueryRow(t.Context(), `SELECT row_to_json(r)::text FROM ai.budget_request r WHERE tenant_id=$1 AND subject=$2`, snap.Admission.TenantId, snap.Admission.AccountId).Scan(&text); err != nil {
			return err
		}
		if strings.Contains(text, "redacted-key") || strings.Contains(text, "canonical fake-model request") {
			t.Fatal("plaintext persisted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDispatchAndSettlementCASRace(t *testing.T) {
	s1, s2 := testpg.Open(t), testpg.Open(t)
	l1, l2 := ledger(t, s1, nil), ledger(t, s2, nil)
	snap := snapshot(t)
	r := admit(t, l1, snap, "")
	race := func(fn func(*Ledger) error) {
		var successes atomic.Int64
		var wg sync.WaitGroup
		for i := 0; i < 24; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				l := l1
				if i%2 == 1 {
					l = l2
				}
				err := fn(l)
				if err == nil {
					successes.Add(1)
				} else {
					wantCode(t, err, "common.precondition_failed")
				}
			}(i)
		}
		wg.Wait()
		if successes.Load() != 1 {
			t.Fatalf("CAS winners %d", successes.Load())
		}
	}
	race(func(l *Ledger) error { _, err := l.MarkDispatching(t.Context(), ref(r), 1); return err })
	race(func(l *Ledger) error {
		_, err := l.Settle(t.Context(), ref(r), 2, Finish{Outcome: "completed", ExecutionStopped: true, Usage: &FinalUsage{InputTokens: 2, OutputTokens: 1, Price: snap.Price}})
		return err
	})
	o, u, b := counts(t, s1, snap.Admission)
	if o != (subject{}) || u != 1 || b != 1 {
		t.Fatalf("duplicate settlement %+v %d %d", o, u, b)
	}
}

func TestAdmissionRollback(t *testing.T) {
	s := testpg.Open(t)
	l := ledger(t, s, nil)
	snap := snapshot(t)
	// Monthly total check fails after the daily row was tentatively increased.
	// Enough in-flight capacity remains, so failure exercises transaction rollback.
	r := finish(t, l, dispatch(t, l, admit(t, l, snap, "")), snap, 8, 2)
	err := s.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE ai.budget_period SET spent=35 WHERE tenant_id=$1 AND subject=$2 AND kind='month'`, snap.Admission.TenantId, snap.Admission.AccountId)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	quota := int64(40)
	snap.Admission.MonthlyBudgetMicros = &quota
	_, err = l.Admit(t.Context(), snap, request("rollback-key"))
	wantCode(t, err, "ai.budget_exceeded")
	o, u, b := counts(t, s, snap.Admission)
	if o != (subject{}) || u != 1 || b != 1 {
		t.Fatal("partial admission")
	}
	err = s.Transaction(t.Context(), snap.Admission.TenantId, func(tx pgx.Tx) error {
		var held, n int64
		if err := tx.QueryRow(t.Context(), `SELECT held FROM ai.budget_period WHERE tenant_id=$1 AND subject=$2 AND kind='day'`, snap.Admission.TenantId, snap.Admission.AccountId).Scan(&held); err != nil {
			return err
		}
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2`, snap.Admission.TenantId, snap.Admission.AccountId).Scan(&n); err != nil {
			return err
		}
		if held != 0 || n != 1 {
			t.Fatalf("partial day/idempotency write %d %d", held, n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wrong := ref(r)
	wrong.AccountID = id(t)
	_, err = l.MarkDispatching(t.Context(), wrong, 1)
	wantCode(t, err, "common.not_found")
}

func TestSnapshotUTCAndPriceIdentity(t *testing.T) {
	// Unit-only check: the admission clock is DB-supplied in production.
	snap := snapshot(t)
	now := time.Date(2026, 2, 1, 0, 30, 0, 0, time.FixedZone("UTC+1", 3600))
	snap.ValidUntil = now.Add(time.Minute)
	snap.Admission.TokenExpiresAt = timestamppb.New(now.Add(time.Minute))
	l := &Ledger{currency: "USD", policy: api.AdmissionPolicy{MaxActiveRequestsPerSubject: 2, MaxInFlightPercent: 25}}
	a, err := l.snapshot(snap, now)
	if err != nil || a.DayPeriodStart != "2026-01-31T00:00:00Z" || a.MonthPeriodStart != "2026-01-01T00:00:00Z" {
		t.Fatalf("UTC boundary %+v %v", a, err)
	}
	p := snap.Price
	p.EffectiveAt = p.EffectiveAt.In(time.FixedZone("UTC+1", 3600))
	if !samePrice(p, snap.Price) {
		t.Fatal("same price instant treated as conflict")
	}
}
