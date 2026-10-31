package network

import (
	"testing"
	"time"
)

func TestWANOperationBudgetBoundsAndNestedDeadlines(t *testing.T) {
	for _, ms := range []int{0, 10000, 30000, 120000} {
		b, err := networkTimeoutBudget(ms)
		if err != nil {
			t.Fatal(err)
		}
		if b.Redeem <= 0 || b.Redeem >= b.Auth || b.Auth >= b.RPC || b.RPC >= b.Operation || b.Authority >= b.Grace {
			t.Fatalf("invalid nesting: %+v", b)
		}
		if ms == 0 && b.Operation != 30*time.Second {
			t.Fatal("default operation")
		}
	}
	for _, ms := range []int{-1, 9999, 120001} {
		if _, err := networkTimeoutBudget(ms); err == nil {
			t.Fatalf("accepted %d", ms)
		}
	}
}
