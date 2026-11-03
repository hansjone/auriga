package bifrost

import "testing"

func TestMemberDefaultIsExplicitMeteringOnlyAndDoesNotInventTrafficLimits(t *testing.T) {
	c, err := NewClient("http://bifrost:8080", "unused", MemberPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.policy.MeteringOnly == nil || !*c.policy.MeteringOnly || c.policy.MaxConcurrency != 0 || c.policy.MaxInFlightTokens != 0 || c.policy.RequestsPerMinute != 0 {
		t.Fatal("default traffic restriction inserted")
	}
	no := false
	c2, err := NewClient("http://bifrost:8080", "unused", MemberPolicy{MeteringOnly: &no, MonthlyBudgetMicros: 100, RequestsPerMinute: 3, MaxConcurrency: 2, MaxInFlightTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	no = true
	if *c2.policy.MeteringOnly || c2.policy.MonthlyBudgetMicros != 100 || c2.policy.RequestsPerMinute != 3 || c2.policy.MaxConcurrency != 2 || c2.policy.MaxInFlightTokens != 1000 {
		t.Fatal("explicit policy changed")
	}
}
