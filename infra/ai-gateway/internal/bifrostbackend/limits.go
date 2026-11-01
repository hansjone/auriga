package bifrostbackend

import (
	"context"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func limits(concurrency, tokens *int64, member bool) (int64, int64, error) {
	c, n := int64(32), int64(128<<20)
	if member {
		c, n = 16, 64<<20
	}
	if concurrency != nil {
		c = *concurrency
	}
	if tokens != nil {
		n = *tokens
	}
	if c < 1 || c > 100 || n < 1 || n > 1<<30 {
		return 0, 0, invalid
	}
	return c, n, nil
}
func normalizeLimits(k *api.RelayKey) {
	c, n, e := limits(k.MaxConcurrency, k.MaxInFlightTokens, k.Source == "member")
	if e == nil {
		k.MaxConcurrency = &c
		k.MaxInFlightTokens = &n
	}
}
func (b *Backend) admit(ctx context.Context, key api.RelayKey, snapshot budget.TrustedSnapshot, request budget.Request) (*budget.Reservation, error) {
	c, n, e := limits(key.MaxConcurrency, key.MaxInFlightTokens, key.Source == "member")
	if e != nil {
		return nil, e
	}
	ledger, e := budget.New(b.db, "USD", &api.AdmissionPolicy{MaxActiveRequestsPerSubject: c, MaxInFlightPercent: 100, MaxInFlightTokens: &n})
	if e != nil {
		return nil, e
	}
	return ledger.Admit(ctx, snapshot, request)
}
