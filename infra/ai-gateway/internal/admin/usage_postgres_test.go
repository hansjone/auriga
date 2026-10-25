package admin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func usageID(t *testing.T) string {
	t.Helper()
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Uses real admission/dispatch/settlement. The trusted authorization snapshot is
// a fixture, not an online IAM/modelgate response or a paid model execution.
func usageRecord(t *testing.T, l *budget.Ledger, tenant, owner, kind string) *budget.Reservation {
	t.Helper()
	n := int64(10000)
	price := api.Price{Currency: "USD", Version: "fixture-original-v1", InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 1000000, EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	snap := budget.TrustedSnapshot{Admission: &ai.BudgetAdmission{TenantId: tenant, AccountId: owner, ModelId: "fixture-model", ProviderId: usageID(t), CredentialId: usageID(t), Currency: "USD", PriceVersion: price.Version, InputTokenUpperBound: 8, MaxOutputTokens: 2, DailyTokens: &n, MonthlyBudgetMicros: &n, Origin: &ai.BudgetAdmission_DeviceId{DeviceId: usageID(t)}, TokenExpiresAt: timestamppb.New(time.Now().Add(time.Hour))}, Price: price, ValidUntil: time.Now().Add(time.Minute)}
	r, err := l.Admit(t.Context(), snap, budget.Request{Method: "POST", Path: "/ai/v1/chat/completions", Fingerprint: sha256.Sum256([]byte("fixture request"))})
	if err != nil {
		t.Fatal(err)
	}
	if kind == "reserved" {
		return r
	}
	ref := budget.Ref{TenantID: tenant, AccountID: owner, RequestID: r.Admission.RequestId}
	r, err = l.MarkDispatching(t.Context(), ref, r.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if kind == "dispatching" {
		return r
	}
	f := budget.Finish{Outcome: "completed", ExecutionStopped: true, Usage: &budget.FinalUsage{InputTokens: 3, OutputTokens: 1, Price: price}}
	if kind == "unknown" {
		f.Outcome, f.Usage = "unknown", nil
	}
	if kind == "anomaly" {
		f.Usage.InputTokens = 99
	}
	r, err = l.Settle(t.Context(), ref, r.Revision, f)
	if kind == "anomaly" {
		var e *budget.Error
		if !errors.As(err, &e) || e.Code != "ai.budget_unavailable" {
			t.Fatalf("anomaly: %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

func usageState(t *testing.T, db *store.Store, tenant string) string {
	t.Helper()
	var state string
	err := db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		// Whole rows include revisions, reservations, fixed periods, outbox and
		// admin idempotency; stable aggregate ordering detects any mutation.
		return tx.QueryRow(t.Context(), `SELECT jsonb_build_array(
		 (SELECT jsonb_agg(x ORDER BY x::text) FROM (SELECT to_jsonb(b) x FROM ai.budget_subject b) s),
		 (SELECT jsonb_agg(x ORDER BY x::text) FROM (SELECT to_jsonb(b) x FROM ai.budget_period b) s),
		 (SELECT jsonb_agg(x ORDER BY x::text) FROM (SELECT to_jsonb(b) x FROM ai.budget_request b) s),
		 (SELECT jsonb_agg(x ORDER BY x::text) FROM (SELECT to_jsonb(b) x FROM ai.budget_usage b) s),
		 (SELECT jsonb_agg(x ORDER BY x::text) FROM (SELECT to_jsonb(b) x FROM ai.budget_outbox b) s),
		 (SELECT jsonb_agg(x ORDER BY x::text) FROM (SELECT to_jsonb(b) x FROM ai.idempotency b) s))::text`).Scan(&state)
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestPostgresAdminUsage(t *testing.T) {
	db := testpg.Open(t)
	l, err := budget.New(db, "USD", nil)
	if err != nil {
		t.Fatal(err)
	}
	tenant, other, owner1, owner2 := usageID(t), usageID(t), usageID(t), usageID(t)
	key, auth := signer(t)
	s := &Server{store: db, cipher: testCipher(t), authenticate: auth}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	claims := validClaims()
	claims["tid"], claims["perms"], claims["stepup_time"] = tenant, []string{"ai.usage.admin"}, 0
	token := sign(t, key, claims, "at+jwt")
	call := func(query, rawToken string, status int, code string) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+prefix+"usage"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+rawToken)
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var doc json.RawMessage
		if err = json.NewDecoder(res.Body).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status || res.Header.Get("ETag") != "" || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("response %d: %s", res.StatusCode, doc)
		}
		if code != "" {
			var problem api.Problem
			if json.Unmarshal(doc, &problem) != nil || problem.Code != code {
				t.Fatalf("problem: %s", doc)
			}
		}
		return doc
	}
	var empty api.UsagePage
	if raw := call("", token, 200, ""); json.Unmarshal(raw, &empty) != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("empty page: %s", raw)
	}
	want := map[string]api.Usage{}
	for _, spec := range []struct{ tenant, owner, kind string }{{tenant, owner1, "confirmed"}, {tenant, owner2, "unknown"}, {tenant, owner2, "anomaly"}, {other, owner1, "confirmed"}, {other, owner2, "unknown"}} {
		r := usageRecord(t, l, spec.tenant, spec.owner, spec.kind)
		if spec.tenant == tenant {
			page, err := l.ListUsage(t.Context(), tenant, spec.owner, 200, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range page.Items {
				want[u.RequestId] = u
			}
			if _, ok := want[r.Admission.RequestId]; !ok {
				t.Fatal("settled usage absent")
			}
		}
	}
	usageRecord(t, l, tenant, owner1, "reserved")
	usageRecord(t, l, tenant, owner1, "dispatching")
	// Only the sort timestamp is adjusted; durable Usage financial facts remain
	// exactly those produced by Settle, including their original UTC periods.
	err = db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `UPDATE ai.budget_usage SET recorded_at='2026-02-01T00:00:00Z' WHERE tenant_id=$1`, tenant)
		if err != nil {
			return err
		}
		var visible, forced int
		if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_usage`).Scan(&visible); err != nil {
			return err
		}
		if err = tx.QueryRow(t.Context(), `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='ai' AND c.relname='budget_usage' AND c.relrowsecurity AND c.relforcerowsecurity`).Scan(&forced); err != nil {
			return err
		}
		if visible != len(want) || forced != 1 {
			return fmt.Errorf("RLS visible=%d force=%d", visible, forced)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, otherBefore := usageState(t, db, tenant), usageState(t, db, other)
	var ids []string
	var firstCursor string
	query := "?limit=1"
	for i := 0; i < len(want); i++ {
		raw := call(query, token, 200, "")
		var page api.UsagePage
		if json.Unmarshal(raw, &page) != nil || len(page.Items) != 1 {
			t.Fatalf("page %s", raw)
		}
		u := page.Items[0]
		if !reflect.DeepEqual(u, want[u.RequestId]) {
			t.Fatalf("changed or foreign usage: %s", raw)
		}
		if u.UsageQuality == "unknown" && (u.InputTokens.Get() != nil || u.OutputTokens.Get() != nil || u.CostMicros.Get() != nil || u.FrozenMicros != 10 || u.FrozenTokens != 10) {
			t.Fatalf("unknown facts: %s", raw)
		}
		ids = append(ids, u.RequestId)
		if i < len(want)-1 {
			if page.NextCursor == nil {
				t.Fatal("missing cursor")
			}
			query = "?limit=1&cursor=" + url.QueryEscape(*page.NextCursor)
			if firstCursor == "" {
				firstCursor = *page.NextCursor
			}
		} else if page.NextCursor != nil {
			t.Fatal("unexpected cursor")
		}
	}
	if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(want) {
		t.Fatalf("tie-break IDs: %v", ids)
	}
	var all api.UsagePage
	if raw := call("?limit=200", token, 200, ""); json.Unmarshal(raw, &all) != nil || len(all.Items) != len(want) || all.NextCursor != nil {
		t.Fatalf("full page: %s", raw)
	}
	end := s.usageCursor(tenant, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), ids[len(ids)-1])
	if raw := call("?cursor="+url.QueryEscape(end), token, 200, ""); json.Unmarshal(raw, &empty) != nil || empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatalf("end page: %s", raw)
	}
	// Same-scope read replay is deterministic and has no settlement side effects.
	replayQuery := "?limit=1&cursor=" + url.QueryEscape(firstCursor)
	if a, b := string(call(replayQuery, token, 200, "")), string(call(replayQuery, token, 200, "")); a != b {
		t.Fatal("cursor replay changed page")
	}
	ownerPage, err := l.ListUsage(t.Context(), tenant, owner2, 1, "")
	if err != nil || ownerPage.NextCursor == nil {
		t.Fatalf("owner cursor: %v", err)
	}
	call("?cursor="+url.QueryEscape(*ownerPage.NextCursor), token, 400, "common.validation_failed")
	if _, err := l.ListUsage(t.Context(), tenant, owner1, 1, firstCursor); err == nil {
		t.Fatal("admin cursor accepted by owner port")
	}
	claims["tid"] = other
	call(replayQuery, sign(t, key, claims, "at+jwt"), 400, "common.validation_failed")
	claims["tid"] = tenant
	for _, q := range []string{"?limit=0", "?limit=201", "?limit=1&limit=1", "?cursor=x&cursor=y", "?accountId=" + owner1, "?cursor=" + strings.Repeat("a", 513), "?cursor=" + url.QueryEscape(firstCursor+"x")} {
		call(q, token, 400, "common.validation_failed")
	}
	for _, tc := range []struct {
		field  string
		value  any
		status int
		code   string
	}{{"perms", []string{"ai.providers.read"}, 403, "common.forbidden"}, {"ctx", "portal", 403, "common.forbidden"}, {"aud", []string{"auriga.ai-gateway"}, 401, "common.unauthenticated"}, {"exp", time.Now().Add(-time.Hour).Unix(), 401, "common.unauthenticated"}} {
		bad := validClaims()
		bad["tid"], bad["perms"] = tenant, []string{"ai.usage.admin"}
		bad[tc.field] = tc.value
		call("", sign(t, key, bad, "at+jwt"), tc.status, tc.code)
	}
	if usageState(t, db, tenant) != before || usageState(t, db, other) != otherBefore {
		t.Fatal("read or rejection mutated durable accounting/outbox")
	}
	// Revoke only this fixture's isolated role, causing a real PG permission
	// error through HTTP/execute. Always restore its ACL, even on assertion failure.
	var role string
	if err = db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT current_user`).Scan(&role)
	}); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(t.Context(), testpg.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "GRANT SELECT ON ai.budget_usage TO "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error(err)
		}
		_ = admin.Close(ctx)
	}()
	if _, err = admin.Exec(t.Context(), "REVOKE SELECT ON ai.budget_usage FROM "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	raw := string(call("", token, 500, "common.internal"))
	if strings.Contains(raw, "budget_usage") || strings.Contains(raw, "SELECT") || strings.Contains(raw, role) {
		t.Fatal("error detail leaked")
	}
}
