package budget

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

type subject struct{ active, tokens, micros int64 }
type record struct {
	a        *ai.BudgetAdmission
	price    api.Price
	state    string
	revision int64
	slot     bool
	reason   *string
	usage    *api.Usage
}

func validRef(ref Ref) bool {
	return uuidPattern.MatchString(ref.TenantID) && uuidPattern.MatchString(ref.AccountID) && uuidPattern.MatchString(ref.RequestID)
}

// Separate statements after locking are intentional: READ COMMITTED must read
// a fresh snapshot after another process commits. Higher isolation may abort,
// which grants no execution right and is safe to retry before dispatch.
func lockSubject(ctx context.Context, tx pgx.Tx, tenant, owner string) (subject, error) {
	var s subject
	_, err := tx.Exec(ctx, `INSERT INTO ai.budget_subject(tenant_id,subject) VALUES ($1,$2) ON CONFLICT DO NOTHING`, tenant, owner)
	if err != nil {
		return s, err
	}
	err = tx.QueryRow(ctx, `SELECT active,held_tokens,held_micros FROM ai.budget_subject WHERE tenant_id=$1 AND subject=$2 FOR UPDATE`, tenant, owner).Scan(&s.active, &s.tokens, &s.micros)
	return s, err
}

func (l *Ledger) Admit(ctx context.Context, s TrustedSnapshot, req Request) (*Reservation, error) {
	if s.Admission == nil || !uuidPattern.MatchString(s.Admission.TenantId) || !uuidPattern.MatchString(s.Admission.AccountId) || req.Method != "POST" || req.Path != "/ai/v1/chat/completions" || len(req.Key) > 256 || req.Fingerprint == ([32]byte{}) {
		return nil, failure("common.validation_failed")
	}
	var result *Reservation
	err := l.store.Transaction(ctx, s.Admission.TenantId, func(tx pgx.Tx) error {
		owner, err := lockSubject(ctx, tx, s.Admission.TenantId, s.Admission.AccountId)
		if err != nil {
			return err
		}
		var key []byte
		if req.Key != "" {
			h := sha256.Sum256([]byte(req.Key))
			key = h[:]
			var id, state string
			var fingerprint []byte
			err = tx.QueryRow(ctx, `SELECT request_id::text,state,fingerprint FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2 AND method=$3 AND path=$4 AND key_hash=$5`, s.Admission.TenantId, s.Admission.AccountId, req.Method, req.Path, key).Scan(&id, &state, &fingerprint)
			if err == nil {
				if !bytes.Equal(fingerprint, req.Fingerprint[:]) {
					return failure("common.conflict")
				}
				code := "ai.replay_unavailable"
				if state == "reserved" || state == "dispatching" {
					code = "ai.request_in_progress"
				}
				return &Error{Code: code, OriginalRequestID: id, Outcome: state}
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO ai.budget_deployment(currency) VALUES ($1) ON CONFLICT DO NOTHING`, l.currency); err != nil {
			return err
		}
		var currency string
		if err = tx.QueryRow(ctx, `SELECT currency FROM ai.budget_deployment WHERE singleton`).Scan(&currency); err != nil {
			return err
		}
		if currency != l.currency {
			return failure("ai.budget_unavailable")
		}
		var now time.Time
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			return err
		}
		a, err := l.snapshot(s, now)
		if err != nil {
			return err
		}
		if err = l.reserve(ctx, tx, owner, a); err != nil {
			return err
		}
		raw, err := proto.Marshal(a)
		if err != nil {
			return err
		}
		price, err := json.Marshal(s.Price)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ai.budget_request(tenant_id,subject,request_id,method,path,key_hash,fingerprint,admission,price,state,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'reserved',$10)`, a.TenantId, a.AccountId, a.RequestId, req.Method, req.Path, key, req.Fingerprint[:], raw, price, now)
		if err == nil {
			result = &Reservation{Admission: a, State: "reserved", Revision: 1}
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (l *Ledger) reserve(ctx context.Context, tx pgx.Tx, s subject, a *ai.BudgetAdmission) error {
	tokens, err := add(s.tokens, a.ReservedTokens)
	if err != nil {
		return err
	}
	micros, err := add(s.micros, a.ReservedMicros)
	if err != nil {
		return err
	}
	tcap, err := flightCap(a.DailyTokens, l.policy.MaxInFlightPercent, l.policy.MaxInFlightTokens)
	if err != nil {
		return err
	}
	mcap, err := flightCap(a.MonthlyBudgetMicros, l.policy.MaxInFlightPercent, l.policy.MaxInFlightMicros)
	if err != nil {
		return err
	}
	if (a.DailyTokens != nil && *a.DailyTokens == 0) || (a.MonthlyBudgetMicros != nil && *a.MonthlyBudgetMicros == 0) {
		return failure("ai.budget_exceeded")
	}
	if s.active >= l.policy.MaxActiveRequestsPerSubject || tokens > tcap || micros > mcap {
		return failure("common.rate_limited")
	}
	if err = reservePeriod(ctx, tx, a, "day", a.DayPeriodStart, a.ReservedTokens, a.DailyTokens); err != nil {
		return err
	}
	if err = reservePeriod(ctx, tx, a, "month", a.MonthPeriodStart, a.ReservedMicros, a.MonthlyBudgetMicros); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE ai.budget_subject SET active=active+1,held_tokens=$3,held_micros=$4 WHERE tenant_id=$1 AND subject=$2`, a.TenantId, a.AccountId, tokens, micros)
	return err
}

func reservePeriod(ctx context.Context, tx pgx.Tx, a *ai.BudgetAdmission, kind, start string, amount int64, quota *int64) error {
	if _, err := tx.Exec(ctx, `INSERT INTO ai.budget_period(tenant_id,subject,kind,start_at) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, a.TenantId, a.AccountId, kind, start); err != nil {
		return err
	}
	var spent, held int64
	if err := tx.QueryRow(ctx, `SELECT spent,held FROM ai.budget_period WHERE tenant_id=$1 AND subject=$2 AND kind=$3 AND start_at=$4`, a.TenantId, a.AccountId, kind, start).Scan(&spent, &held); err != nil {
		return err
	}
	total, err := add(spent, held)
	if err != nil {
		return err
	}
	total, err = add(total, amount)
	if err != nil {
		return err
	}
	if quota != nil && total > *quota {
		return failure("ai.budget_exceeded")
	}
	_, err = tx.Exec(ctx, `UPDATE ai.budget_period SET held=held+$5 WHERE tenant_id=$1 AND subject=$2 AND kind=$3 AND start_at=$4`, a.TenantId, a.AccountId, kind, start, amount)
	return err
}

func load(ctx context.Context, tx pgx.Tx, ref Ref) (*record, error) {
	r := &record{a: new(ai.BudgetAdmission)}
	var raw, price []byte
	err := tx.QueryRow(ctx, `SELECT admission,price,state,revision,slot_held,reconciliation_reason FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2 AND request_id=$3 FOR UPDATE`, ref.TenantID, ref.AccountID, ref.RequestID).Scan(&raw, &price, &r.state, &r.revision, &r.slot, &r.reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, failure("common.not_found")
	}
	if err != nil {
		return nil, err
	}
	if err = proto.Unmarshal(raw, r.a); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(price, &r.price); err != nil {
		return nil, err
	}
	return r, nil
}
