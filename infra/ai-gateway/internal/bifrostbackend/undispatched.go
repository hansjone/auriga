package bifrostbackend

import (
	"context"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// This caller has not entered Run, and therefore has never submitted upstream.
// Read back a failed COMMIT using a fresh context before releasing its own hold.
func (b *Backend) cancelUndispatched(ref budget.Ref, revision int64, price api.Price) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stored, err := b.ledger.GetReservation(ctx, ref)
	if err != nil {
		return err
	}
	switch {
	case stored.State == "reserved" && stored.Revision == revision:
		_, err = b.ledger.Recover(ctx, ref, revision, budget.RecoveryEvidence{NeverSubmitted: true})
	case stored.State == "dispatching" && stored.Revision == revision+1:
		// MarkDispatching may have committed before its response was lost. Its
		// caller still obtained no dispatch authority and did not call Run.
		_, err = b.ledger.Settle(ctx, ref, stored.Revision, budget.Finish{
			Outcome: "cancelled", ExecutionStopped: true,
			Usage: &budget.FinalUsage{Price: price},
		})
	case stored.State == "reserved" || stored.State == "dispatching" || stored.State == "unknown":
		return &budget.Error{Code: "common.precondition_failed"}
	}
	return err
}
