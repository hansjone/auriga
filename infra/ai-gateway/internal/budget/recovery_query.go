package budget

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// RecoveryCandidate is a minimal observation, never execution or refund evidence.
// It deliberately contains no admission, pricing, usage or request digests.
type RecoveryCandidate struct {
	Ref       Ref
	State     string
	Revision  int64
	SlotHeld  bool
	CreatedAt time.Time
}

type RecoveryPage struct {
	Items      []RecoveryCandidate
	NextCursor *string
}

const recoveryScope = "recovery-v1"

// ListRecoveryCandidates is only for trusted internal runtime callers. The
// explicit single tenant must come from verified identity or protected deployment
// configuration, never unverified client parameters. It discovers no tenants.
// Each page is a fresh RLS transaction, not an atomic snapshot across pages;
// concurrent admissions/state changes may require a new cycle from the start.
func (l *Ledger) ListRecoveryCandidates(ctx context.Context, tenant string, limit int, cursor string) (*RecoveryPage, error) {
	if !uuidPattern.MatchString(tenant) || limit < 1 || limit > 200 || len(cursor) > 512 {
		return nil, failure("common.validation_failed")
	}
	var requestID, owner string
	if cursor != "" {
		raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		parts := strings.Split(string(raw), "|")
		if err != nil || base64.RawURLEncoding.EncodeToString(raw) != cursor || len(parts) != 4 || parts[0] != recoveryScope || parts[1] != tenant || !uuidPattern.MatchString(parts[2]) || !uuidPattern.MatchString(parts[3]) {
			return nil, failure("common.validation_failed")
		}
		requestID, owner = parts[2], parts[3]
	}
	page := &RecoveryPage{Items: []RecoveryCandidate{}}
	err := l.store.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		// Separate fixed statements keep the continuation key an index range even
		// with cached generic plans; the first page needs no artificial UUID floor.
		var rows pgx.Rows
		var err error
		if cursor == "" {
			rows, err = tx.Query(ctx, `SELECT subject::text,request_id::text,state,revision,slot_held,created_at
			 FROM ai.budget_request r WHERE tenant_id=$1
			 AND (state IN ('reserved','dispatching') OR (state='unknown' AND slot_held))
			 ORDER BY r.request_id,r.subject LIMIT $2`, tenant, limit+1)
		} else {
			rows, err = tx.Query(ctx, `SELECT subject::text,request_id::text,state,revision,slot_held,created_at
			 FROM ai.budget_request r WHERE tenant_id=$1
			 AND (state IN ('reserved','dispatching') OR (state='unknown' AND slot_held))
			 AND (r.request_id,r.subject)>($2::uuid,$3::uuid)
			 ORDER BY r.request_id,r.subject LIMIT $4`, tenant, requestID, owner, limit+1)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		var last string
		for rows.Next() {
			var item RecoveryCandidate
			item.Ref.TenantID = tenant
			if err = rows.Scan(&item.Ref.AccountID, &item.Ref.RequestID, &item.State, &item.Revision, &item.SlotHeld, &item.CreatedAt); err != nil {
				return err
			}
			if len(page.Items) == limit {
				page.NextCursor = &last
				break
			}
			page.Items = append(page.Items, item)
			last = base64.RawURLEncoding.EncodeToString([]byte(recoveryScope + "|" + tenant + "|" + item.Ref.RequestID + "|" + item.Ref.AccountID))
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

// GetRecoveryCandidate reads the same minimal projection for a known Ref,
// including terminal rows and unknown rows whose slot has been released. A
// missing row is common.not_found and must never be interpreted as admission.
func (l *Ledger) GetRecoveryCandidate(ctx context.Context, ref Ref) (*RecoveryCandidate, error) {
	if !validRef(ref) {
		return nil, failure("common.validation_failed")
	}
	item := &RecoveryCandidate{Ref: ref}
	err := l.store.Transaction(ctx, ref.TenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT state,revision,slot_held,created_at FROM ai.budget_request
		 WHERE tenant_id=$1 AND subject=$2 AND request_id=$3`, ref.TenantID, ref.AccountID, ref.RequestID).Scan(&item.State, &item.Revision, &item.SlotHeld, &item.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return failure("common.not_found")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return item, nil
}
