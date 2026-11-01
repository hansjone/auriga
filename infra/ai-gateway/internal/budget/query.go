package budget

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// GetReservation lets a trusted supervisor inspect durable state after restart
// or an ambiguous commit. Reading dispatching never grants permission to submit.
func (l *Ledger) GetReservation(ctx context.Context, ref Ref) (*Reservation, error) {
	if !validRef(ref) {
		return nil, failure("common.validation_failed")
	}
	r := &Reservation{Admission: new(ai.BudgetAdmission)}
	err := l.store.Transaction(ctx, ref.TenantID, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT admission,state,revision FROM ai.budget_request WHERE tenant_id=$1 AND subject=$2 AND request_id=$3`, ref.TenantID, ref.AccountID, ref.RequestID).Scan(&raw, &r.State, &r.Revision)
		if err == pgx.ErrNoRows {
			return failure("common.not_found")
		}
		if err != nil {
			return err
		}
		return proto.Unmarshal(raw, r.Admission)
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// ListUsage is an owner-scoped read port. Its caller must independently enforce
// usage permission and derive tenant/owner from verified identity, never params.
// Cursor is a stable (recorded_at,request_id) key, bound to tenant and owner.
func (l *Ledger) ListUsage(ctx context.Context, tenant, owner string, limit int, cursor string) (*api.UsagePage, error) {
	if !uuidPattern.MatchString(tenant) || !uuidPattern.MatchString(owner) || limit < 1 || limit > 200 || len(cursor) > 512 {
		return nil, failure("common.validation_failed")
	}
	start := time.Unix(0, 0).UTC()
	id := "00000000-0000-0000-8000-000000000000"
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		parts := strings.Split(string(raw), "|")
		if err != nil || len(parts) != 4 || parts[0] != tenant || parts[1] != owner || !uuidPattern.MatchString(parts[3]) {
			return nil, failure("common.validation_failed")
		}
		start, err = time.Parse(time.RFC3339Nano, parts[2])
		if err != nil {
			return nil, failure("common.validation_failed")
		}
		id = parts[3]
	}
	page := &api.UsagePage{Items: []api.Usage{}}
	err := l.store.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT document,recorded_at,request_id::text FROM ai.budget_usage WHERE tenant_id=$1 AND subject=$2 AND (recorded_at,request_id)>($3,$4::uuid) ORDER BY recorded_at,request_id LIMIT $5`, tenant, owner, start, id, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		var last string
		for rows.Next() {
			var raw []byte
			var at time.Time
			var request string
			if err = rows.Scan(&raw, &at, &request); err != nil {
				return err
			}
			if len(page.Items) == limit {
				page.NextCursor = &last
				break
			}
			var usage api.Usage
			if err = json.Unmarshal(raw, &usage); err != nil {
				return err
			}
			page.Items = append(page.Items, usage)
			last = base64.RawURLEncoding.EncodeToString([]byte(tenant + "|" + owner + "|" + at.UTC().Format(time.RFC3339Nano) + "|" + request))
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}
