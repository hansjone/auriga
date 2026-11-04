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

func TestUnsetQuotaDoesNotDisableStrictReservationOrFinalUsageBounds(t *testing.T) {
	s := snapshot(t)
	s.Admission.DailyTokens = nil
	s.Admission.MonthlyBudgetMicros = nil
	l := &Ledger{currency: "USD", policy: api.AdmissionPolicy{MaxActiveRequestsPerSubject: 2, MaxInFlightPercent: 25}}
	a, err := l.snapshot(s, time.Now())
	if err != nil || a.MeteringOnly || a.ReservedTokens != 10 || a.ReservedMicros != 10 || a.MaxActiveRequestsPerSubject != 2 {
		t.Fatalf("unset quota bypassed strict admission: %v %v", a, err)
	}
	r := &record{a: a, price: s.Price}
	if !validFinalUsage(r, &FinalUsage{InputTokens: 8, OutputTokens: 2, Price: s.Price}) {
		t.Fatal("valid strict final usage rejected")
	}
	if validFinalUsage(r, &FinalUsage{InputTokens: 9, OutputTokens: 2, Price: s.Price}) || validFinalUsage(r, &FinalUsage{InputTokens: 8, OutputTokens: 3, Price: s.Price}) {
		t.Fatal("unset quota bypassed strict input/output reservations")
	}
}
