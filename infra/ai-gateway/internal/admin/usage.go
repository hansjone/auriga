package admin

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

const usageScope = "auriga/ai/admin/usage/v1"

func validateUsageQuery(r *http.Request) error {
	q, err := baseQuery(r)
	if err != nil {
		return err
	}
	for key, values := range q {
		if (key != "limit" && key != "cursor") || len(values) != 1 || values[0] == "" {
			return failure("common.validation_failed")
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return failure("common.validation_failed")
		}
	}
	if v := q.Get("cursor"); v != "" {
		if _, _, err := usageCursorParts(v); err != nil {
			return err
		}
	}
	return nil
}

func usageCursorParts(cursor string) ([]byte, []byte, error) {
	if len(cursor) > 512 {
		return nil, nil, failure("common.validation_failed")
	}
	parts := strings.Split(cursor, ".")
	if len(parts) != 2 {
		return nil, nil, failure("common.validation_failed")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	mac, macErr := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || macErr != nil || len(mac) != 32 {
		return nil, nil, failure("common.validation_failed")
	}
	return raw, mac, nil
}

func (s *Server) usageCursor(tenant string, at time.Time, id string) string {
	raw, _ := json.Marshal([4]string{usageScope, tenant, at.UTC().Format(time.RFC3339Nano), id})
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(s.cipher.fingerprint(raw))
}

func (s *Server) parseUsageCursor(tenant, cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Unix(0, 0).UTC(), "00000000-0000-0000-8000-000000000000", nil
	}
	raw, mac, err := usageCursorParts(cursor)
	if err != nil || !hmac.Equal(mac, s.cipher.fingerprint(raw)) {
		return time.Time{}, "", failure("common.validation_failed")
	}
	var parts []string
	if json.Unmarshal(raw, &parts) != nil || len(parts) != 4 || parts[0] != usageScope || parts[1] != tenant || !uuidPattern.MatchString(parts[3]) {
		return time.Time{}, "", failure("common.validation_failed")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[2])
	if err != nil {
		return time.Time{}, "", failure("common.validation_failed")
	}
	return at, parts[3], nil
}

// listUsage reads only durable settlement documents. The enclosing transaction
// binds FORCE RLS to the verified admin tenant; no owner read port is widened.
func (s *Server) listUsage(ctx context.Context, tx pgx.Tx, tenant string, r *http.Request) (result, error) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	at, id, err := s.parseUsageCursor(tenant, r.URL.Query().Get("cursor"))
	if err != nil {
		return result{}, err
	}
	rows, err := tx.Query(ctx, `SELECT document,recorded_at,request_id::text FROM ai.budget_usage WHERE tenant_id=$1 AND (recorded_at,request_id)>($2,$3::uuid) ORDER BY recorded_at,request_id LIMIT $4`, tenant, at, id, limit+1)
	if err != nil {
		return result{}, err
	}
	defer rows.Close()
	page := api.UsagePage{Items: []api.Usage{}}
	var last string
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw, &at, &id); err != nil {
			return result{}, err
		}
		if len(page.Items) == limit {
			page.NextCursor = &last
			break
		}
		// Decode into the generated closed Usage shape; never return DB JSON.
		var usage api.Usage
		if err = json.Unmarshal(raw, &usage); err != nil {
			return result{}, err
		}
		page.Items = append(page.Items, usage)
		last = s.usageCursor(tenant, at, id)
	}
	if err = rows.Err(); err != nil {
		return result{}, err
	}
	return response(http.StatusOK, page, 0)
}
