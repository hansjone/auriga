package budget

import (
	"context"
	"encoding/json"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

// MarkDispatching returns only after COMMIT. Only the successful CAS caller may
// submit upstream, once. A commit error never grants execution authority.
func (l *Ledger) MarkDispatching(ctx context.Context, ref Ref, revision int64) (*Reservation, error) {
	return l.mutate(ctx, ref, revision, func(r *record) error {
		if r.state != "reserved" {
			return failure("common.precondition_failed")
		}
		r.state = "dispatching"
		return nil
	}, false)
}

// Settle accepts confirmed final usage only after execution has stopped. Missing
// or contradictory usage freezes the full reservation and records unknown.
// Anomalies commit the quarantine before returning ai.budget_unavailable.
func (l *Ledger) Settle(ctx context.Context, ref Ref, revision int64, f Finish) (*Reservation, error) {
	if f.Outcome != "completed" && f.Outcome != "failed" && f.Outcome != "cancelled" && f.Outcome != "unknown" {
		return nil, failure("common.validation_failed")
	}
	anomaly := false
	result, err := l.mutate(ctx, ref, revision, func(r *record) error {
		if r.state != "dispatching" {
			return failure("common.precondition_failed")
		}
		r.state = f.Outcome
		if f.Usage == nil || f.Outcome == "unknown" {
			r.state = "unknown"
		} else {
			u := f.Usage
			n, err := cost(u.InputTokens, u.OutputTokens, r.price)
			if err != nil || u.InputTokens > r.a.InputTokenUpperBound || u.OutputTokens > r.a.MaxOutputTokens || n > r.a.ReservedMicros || !samePrice(u.Price, r.price) {
				r.state = "unknown"
				reason := "invalid_final_usage"
				r.reason = &reason
				anomaly = true
			} else if !f.ExecutionStopped {
				return failure("common.precondition_failed")
			} else {
				r.usage = &api.Usage{InputTokens: *api.NewNullableInt64(&u.InputTokens), OutputTokens: *api.NewNullableInt64(&u.OutputTokens), CostMicros: *api.NewNullableInt64(&n), UsageQuality: "confirmed"}
			}
		}
		if r.usage == nil {
			r.usage = unknownUsage(r)
		}
		r.slot = !f.ExecutionStopped
		return nil
	}, true)
	if err == nil && anomaly {
		return result, failure("ai.budget_unavailable")
	}
	return result, err
}

// Recover never uses elapsed time to refund. A reserved CAS can be cancelled
// only with proof that submission never happened. Dispatching is always unknown.
// Unknown may subsequently release a slot, but never its token/money holds.
func (l *Ledger) Recover(ctx context.Context, ref Ref, revision int64, proof RecoveryEvidence) (*Reservation, error) {
	return l.mutate(ctx, ref, revision, func(r *record) error {
		switch r.state {
		case "reserved":
			if !proof.NeverSubmitted {
				return failure("common.precondition_failed")
			}
			zero := int64(0)
			r.state = "cancelled"
			r.slot = false
			r.usage = &api.Usage{InputTokens: *api.NewNullableInt64(&zero), OutputTokens: *api.NewNullableInt64(&zero), CostMicros: *api.NewNullableInt64(&zero), UsageQuality: "confirmed"}
		case "dispatching":
			r.state = "unknown"
			r.slot = !proof.ExecutionStopped
			r.usage = unknownUsage(r)
		case "unknown":
			if !proof.ExecutionStopped || !r.slot {
				return failure("common.precondition_failed")
			}
			r.slot = false
		default:
			return failure("common.precondition_failed")
		}
		return nil
	}, true)
}

func unknownUsage(r *record) *api.Usage {
	return &api.Usage{InputTokens: *api.NewNullableInt64(nil), OutputTokens: *api.NewNullableInt64(nil), CostMicros: *api.NewNullableInt64(nil), UsageQuality: "unknown", FrozenMicros: r.a.ReservedMicros, FrozenTokens: r.a.ReservedTokens}
}

func (l *Ledger) mutate(ctx context.Context, ref Ref, revision int64, change func(*record) error, terminal bool) (*Reservation, error) {
	if !validRef(ref) || revision < 1 || revision >= maxInteger {
		return nil, failure("common.validation_failed")
	}
	var result *Reservation
	err := l.store.Transaction(ctx, ref.TenantID, func(tx pgx.Tx) error {
		if _, err := lockSubject(ctx, tx, ref.TenantID, ref.AccountID); err != nil {
			return err
		}
		r, err := load(ctx, tx, ref)
		if err != nil {
			return err
		}
		if r.revision != revision {
			return failure("common.precondition_failed")
		}
		oldSlot := r.slot
		if err = change(r); err != nil {
			return err
		}
		r.revision++
		if oldSlot && !r.slot {
			if _, err = tx.Exec(ctx, `UPDATE ai.budget_subject SET active=active-1 WHERE tenant_id=$1 AND subject=$2`, ref.TenantID, ref.AccountID); err != nil {
				return err
			}
		}
		if terminal && r.usage != nil {
			if err = account(ctx, tx, r); err != nil {
				return err
			}
			if err = recordUsage(ctx, tx, r); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE ai.budget_request SET state=$4,revision=$5,slot_held=$6,reconciliation_reason=$7 WHERE tenant_id=$1 AND subject=$2 AND request_id=$3 AND revision=$8`, ref.TenantID, ref.AccountID, ref.RequestID, r.state, r.revision, r.slot, r.reason, revision)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return failure("common.precondition_failed")
		}
		result = &Reservation{Admission: r.a, State: r.state, Revision: r.revision}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func account(ctx context.Context, tx pgx.Tx, r *record) error {
	a, u := r.a, r.usage
	if u.UsageQuality == "unknown" {
		return nil
	}
	tokens, err := add(u.GetInputTokens(), u.GetOutputTokens())
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ai.budget_subject SET held_tokens=held_tokens-$3,held_micros=held_micros-$4 WHERE tenant_id=$1 AND subject=$2`, a.TenantId, a.AccountId, a.ReservedTokens, a.ReservedMicros); err != nil {
		return err
	}
	for _, p := range []struct {
		kind, start string
		held, spent int64
	}{{"day", a.DayPeriodStart, a.ReservedTokens, tokens}, {"month", a.MonthPeriodStart, a.ReservedMicros, u.GetCostMicros()}} {
		tag, err := tx.Exec(ctx, `UPDATE ai.budget_period SET held=held-$5,spent=spent+$6 WHERE tenant_id=$1 AND subject=$2 AND kind=$3 AND start_at=$4`, a.TenantId, a.AccountId, p.kind, p.start, p.held, p.spent)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return failure("ai.budget_unavailable")
		}
	}
	return nil
}

func recordUsage(ctx context.Context, tx pgx.Tx, r *record) error {
	a, u := r.a, r.usage
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	u.RequestId, u.AccountId, u.ModelId, u.Outcome = a.RequestId, a.AccountId, a.ModelId, r.state
	u.Currency, u.PriceVersion, u.CreatedAt = a.Currency, a.PriceVersion, now.UTC()
	var err error
	u.DayPeriodStart, err = time.Parse(time.RFC3339, a.DayPeriodStart)
	if err != nil {
		return err
	}
	u.MonthPeriodStart, err = time.Parse(time.RFC3339, a.MonthPeriodStart)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO ai.budget_usage(tenant_id,subject,request_id,document,recorded_at) VALUES ($1,$2,$3,$4,$5)`, a.TenantId, a.AccountId, a.RequestId, raw, now); err != nil {
		return err
	}
	// Map the generated Usage DTO to the event data schema, which omits frozen
	// totals and creation time and adds origin/revision. No prompt or output data.
	data, err := u.ToMap()
	if err != nil {
		return err
	}
	for _, key := range []string{"frozenMicros", "frozenTokens", "createdAt"} {
		delete(data, key)
	}
	data["revision"] = r.revision
	if a.GetInstanceId() != "" {
		data["instanceId"] = a.GetInstanceId()
	} else {
		data["deviceId"] = a.GetDeviceId()
	}
	id, err := newID(now)
	if err != nil {
		return err
	}
	envelope, err := json.Marshal(map[string]any{"specversion": "1.0", "id": id, "source": "auriga/ai-gateway", "type": "usage.recorded", "time": now.UTC(), "subject": a.RequestId, "datacontenttype": "application/json", "dataversion": "1", "tenantid": a.TenantId, "data": data})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai.budget_outbox(tenant_id,subject,id,request_id,envelope,created_at) VALUES ($1,$2,$3,$4,$5,$6)`, a.TenantId, a.AccountId, id, a.RequestId, envelope, now)
	return err
}
