package budget

import (
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestMeteringOnlyKeepsIdentityAndPriceWithoutCapacityCeiling(t *testing.T) {
	s := snapshot(t)
	s.Admission.MeteringOnly = true
	s.Admission.MonthlyBudgetMicros = nil
	s.Admission.DailyTokens = nil
	l := &Ledger{currency: "USD", policy: api.AdmissionPolicy{MaxActiveRequestsPerSubject: 0, MaxInFlightPercent: 100}}
	a, err := l.snapshot(s, time.Now())
	if err != nil || !a.MeteringOnly || a.ReservedTokens != 0 || a.ReservedMicros != 0 {
		t.Fatalf("snapshot=%v err=%v", a, err)
	}
	r := &record{a: a, price: s.Price}
	u := &FinalUsage{InputTokens: 1000000, OutputTokens: 200000, Price: s.Price}
	if !validFinalUsage(r, u) {
		t.Fatal("metered usage was cut to an estimate")
	}
	r.a.MeteringOnly = false
	if validFinalUsage(r, u) {
		t.Fatal("strict budget capacity bypass")
	}
	r.a.MeteringOnly = true
	u.InputTokens = -1
	if validFinalUsage(r, u) {
		t.Fatal("negative metering usage accepted")
	}
	u.InputTokens = 1
	u.Price.Version = "foreign"
	if validFinalUsage(r, u) {
		t.Fatal("foreign price accepted")
	}
	s.Admission.TenantId = "foreign"
	if _, err = l.snapshot(s, time.Now()); err == nil {
		t.Fatal("identity validation bypass")
	}
}

func TestMeteringOnlyCannotCoexistWithExplicitBudget(t *testing.T) {
	s := snapshot(t)
	s.Admission.MeteringOnly = true
	l := &Ledger{currency: "USD", policy: api.AdmissionPolicy{MaxInFlightPercent: 100}}
	if _, err := l.snapshot(s, time.Now()); err == nil {
		t.Fatal("explicit budget silently ignored")
	}
	if cap, err := flightCap(nil, 100, nil); err != nil || cap != maxInteger {
		t.Fatalf("unlimited flight=%d %v", cap, err)
	}
}
