package budget

import (
	"context"
	"errors"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/identifiers"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

// ReconcileExpiredSubject is scoped to an already authenticated key. The relay
// has a 150s dispatch budget and 120s network bounds. After ten minutes it never
// refunds uncertain execution: it converts the entire pinned reservation into
// estimated spend, freeing in-flight capacity without permitting overspend.
// A reserved CAS is cancelled with zero spend and fences any late dispatcher.
func (l *Ledger) ReconcileExpiredSubject(ctx context.Context, tenant, owner string) error {
	if !identifiers.ValidUUID(tenant) || !identifiers.ValidUUID(owner) {
		return failure("common.validation_failed")
	}
	type candidate struct {
		id       string
		revision int64
	}
	var candidates []candidate
	err := l.store.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT request_id::text,revision FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2 AND state IN ('reserved','dispatching','unknown') AND created_at<clock_timestamp()-interval '10 minutes' ORDER BY request_id LIMIT 200`, tenant, owner)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var c candidate
			if e = rows.Scan(&c.id, &c.revision); e != nil {
				return e
			}
			candidates = append(candidates, c)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, c := range candidates {
		_, err = l.mutate(ctx, Ref{tenant, owner, c.id}, c.revision, func(r *record) error {
			if r.state != "reserved" && r.state != "dispatching" && r.state != "unknown" {
				return failure("common.precondition_failed")
			}
			input, output := r.a.InputTokenUpperBound, r.a.MaxOutputTokens
			wasReserved := r.state == "reserved"
			quality := "estimated"
			r.state = "failed"
			if r.a.TokenExpiresAt != nil && r.a.TokenExpiresAt.AsTime().Add(8*time.Minute).After(time.Now()) {
				return failure("common.precondition_failed")
			}
			if wasReserved {
				input, output = 0, 0
				quality = "confirmed"
				r.state = "cancelled"
			}
			amount, e := cost(input, output, r.price)
			if e != nil {
				return e
			}
			r.usage = &api.Usage{InputTokens: *api.NewNullableInt64(&input), OutputTokens: *api.NewNullableInt64(&output), CostMicros: *api.NewNullableInt64(&amount), UsageQuality: quality}
			r.slot = false
			reason := "deadline_reconciled"
			r.reason = &reason
			return nil
		}, true)
		var fault *Error
		if err != nil && (!errors.As(err, &fault) || fault.Code != "common.precondition_failed") {
			return err
		}
	}
	return nil
}
