package budget

import (
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"testing"
)

// Root runs this with the exclusive real PostgreSQL slot; the author only
// compiles it and runs the adjacent pure policy tests.
func TestMeteringOnlyDurableUsageExceedsEstimateWithoutQuota(t *testing.T) {
	db := testpg.Open(t)
	l := ledger(t, db, nil)
	s := snapshot(t)
	s.Admission.MeteringOnly = true
	s.Admission.DailyTokens = nil
	s.Admission.MonthlyBudgetMicros = nil
	var reservations []*Reservation
	for i := 0; i < 4; i++ {
		reservations = append(reservations, admit(t, l, s, ""))
	}
	for _, r := range reservations {
		loaded, err := l.GetReservation(t.Context(), ref(r))
		if err != nil || !loaded.Admission.MeteringOnly || loaded.Admission.ReservedMicros != 0 || loaded.Admission.ReservedTokens != 0 {
			t.Fatalf("durable mode=%v err=%v", loaded, err)
		}
		r = dispatch(t, l, r)
		n := finish(t, l, r, s, 1000000, 200000)
		if n.State != "completed" {
			t.Fatal("metered usage rejected")
		}
	}
	owner, usage, outbox := counts(t, db, s.Admission)
	if owner.active != 0 || owner.tokens != 0 || owner.micros != 0 || usage != 4 || outbox != 4 {
		t.Fatalf("owner=%v usage=%d outbox=%d", owner, usage, outbox)
	}
	page, err := l.ListUsage(t.Context(), s.Admission.TenantId, s.Admission.AccountId, 10, "")
	if err != nil || len(page.Items) != 4 {
		t.Fatalf("usage=%v err=%v", page, err)
	}
	for _, u := range page.Items {
		if u.GetInputTokens() != 1000000 || u.GetOutputTokens() != 200000 || u.UsageQuality != "confirmed" {
			t.Fatal("real usage was truncated")
		}
	}
}
