package bifrostbackend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/audit"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
)

func TestRelayAuditOutboxAtomicity(t *testing.T) {
	db := testpg.Open(t)
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	password := rand.Text() + rand.Text()
	b, err := New(db, hex.EncodeToString(master), password, rand.Text()+rand.Text(), true)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _ := newID()
	actor, _ := newID()
	server := httptest.NewServer(b)
	defer server.Close()
	input := api.RelayKeyInput{Name: "password=never-audit-this", Active: true, MonthlyBudgetMicros: 10, Models: []string{"*"}}
	body, _ := json.Marshal(input)
	idem := rand.Text()
	call := func(method, path string, body []byte) []byte {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(body))
		req.SetBasicAuth("auriga", password)
		req.Header.Set("X-Auriga-Tenant", tenant)
		req.Header.Set("X-Auriga-Audit-Actor", actor)
		if method != "POST" {
			req.Header.Set("X-Auriga-Audit-IP", "192.0.2.20")
		}
		req.Header.Set("traceparent", "00-12345678901234567890123456789012-1234567890123456-01")
		req.Header.Set("Idempotency-Key", idem)
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("operation status=%d: %s", resp.StatusCode, raw)
		}
		return raw
	}
	var key api.RelayKeyIssued
	if err = json.Unmarshal(call("POST", "/api/virtual-keys", body), &key); err != nil {
		t.Fatal(err)
	}
	// The same durable management request must replay, without a second business audit.
	call("POST", "/api/virtual-keys", body)
	input.MonthlyBudgetMicros = 20
	input.Active = false
	body, _ = json.Marshal(input)
	call("PUT", "/api/virtual-keys/"+key.Key.Id, body)
	var raws [][]byte
	err = db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		rows, e := tx.Query(t.Context(), `SELECT envelope FROM ai.audit_outbox WHERE tenant_id=$1 ORDER BY id`, tenant)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				return e
			}
			raws = append(raws, raw)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 3 {
		t.Fatalf("expected create, revoke and budget update once each; got %d", len(raws))
	}
	actions := map[string]int{}
	for _, raw := range raws {
		var envelope map[string]any
		if e := json.Unmarshal(raw, &envelope); e != nil {
			t.Fatal(e)
		}
		data := envelope["data"].(map[string]any)
		a := data["actor"].(map[string]any)
		actions[data["action"].(string)]++
		expectedIP := "192.0.2.20"
		if data["action"] == "ai.relay_key.create" {
			expectedIP = "127.0.0.1"
		}
		if envelope["type"] != "audit.recorded" || envelope["source"] != "auriga/ai-gateway" || a["id"] != actor || a["ip"] != expectedIP || data["result"] != "success" {
			t.Fatalf("invalid metadata: %s", raw)
		}
		if !strings.HasPrefix(envelope["traceparent"].(string), "00-12345678901234567890123456789012-") || bytes.Contains(raw, []byte(key.Secret)) || bytes.Contains(raw, []byte(input.Name)) {
			t.Fatal("secret or correlation mismatch")
		}
	}
	for _, action := range []string{"ai.relay_key.create", "ai.relay_key.revoke", "ai.relay_key.budget.update"} {
		if actions[action] != 1 {
			t.Fatalf("actions=%v", actions)
		}
	}
	// A rejected audit insert must roll the key update back, never commit silently.
	admin, e := pgx.Connect(t.Context(), testpg.DSN(t))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	policy := "audit_" + strings.ReplaceAll(tenant, "-", "")
	ddl := fmt.Sprintf(`CREATE POLICY %s ON ai.audit_outbox AS RESTRICTIVE FOR INSERT WITH CHECK (tenant_id <> '%s'::uuid)`, pgx.Identifier{policy}.Sanitize(), tenant)
	if _, e = admin.Exec(t.Context(), ddl); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, dropErr := admin.Exec(context.Background(), "DROP POLICY "+pgx.Identifier{policy}.Sanitize()+" ON ai.audit_outbox"); dropErr != nil {
			t.Error(dropErr)
		}
	}()
	input.Active = true
	if _, e = b.SaveKey(t.Context(), tenant, key.Key.Id, input); e == nil {
		t.Fatal("outbox failure committed business mutation")
	}
	err = db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		var active bool
		e := tx.QueryRow(t.Context(), `SELECT (document->>'active')::boolean FROM ai.relay_key WHERE tenant_id=$1 AND id=$2`, tenant, key.Key.Id).Scan(&active)
		if e == nil && active {
			t.Error("business update escaped rollback")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := newID()
	if err = db.Transaction(t.Context(), other, func(tx pgx.Tx) error {
		var count int
		e := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.audit_outbox WHERE tenant_id=$1`, tenant).Scan(&count)
		if count != 0 {
			t.Error("cross-tenant audit read")
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRelayAuditDefaultBudgetUsesSystemActor(t *testing.T) {
	db := testpg.Open(t)
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	b, err := New(db, hex.EncodeToString(master), rand.Text()+rand.Text(), rand.Text()+rand.Text(), true)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _ := newID()
	member, _ := newID()
	ctx := audit.WithMetadata(t.Context(), member, "192.0.2.20", "00-12345678901234567890123456789012-1234567890123456-01")
	request := api.RelayMemberEnsure{TenantId: tenant, AccountId: member, MonthlyBudgetMicros: 10}
	if _, err := b.EnsureMember(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.MonthlyBudgetMicros = 20
	for range 2 {
		if _, err := b.EnsureMember(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	err = db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai.audit_outbox WHERE tenant_id=$1`, tenant).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Fatalf("creation and changed deployment policy only: %d", count)
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT envelope FROM ai.audit_outbox WHERE tenant_id=$1 AND envelope->'data'->>'action'='ai.relay_key.budget.update'`, tenant).Scan(&raw); err != nil {
			return err
		}
		var record struct {
			Data struct{ Actor struct{ Type, Id, Ip string } }
		}
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		if record.Data.Actor.Type != "system" || record.Data.Actor.Id != "svc:ai-gateway" || record.Data.Actor.Ip != "192.0.2.20" {
			t.Fatalf("deployment policy attributed to caller: %s", raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The Java combined integration driver supplies its live audit HTTP API, signed admin
// token, temporary PostgreSQL database and NATS server. No audit rows are injected.
func TestRelayAuditPoolAndInitialCredential(t *testing.T) {
	b, _, _, tenant := reviewBackend(t)
	secret := rand.Text() + rand.Text()
	provider, err := b.SaveProvider(t.Context(), tenant, "", api.RelayProviderInput{Name: "fixture", Kind: "openai-compatible", BaseUrl: "https://api.example.com", Enabled: true}, &secret)
	if err != nil {
		t.Fatal(err)
	}
	pool := api.RelayPool{Id: "audited-pool", Enabled: true, Routes: []api.RelayRoute{{ProviderId: provider.Id, Model: "m", Weight: 1}}, ContextWindow: 16, MaxOutputTokens: 8, Price: api.Price{Currency: "USD", Version: "v1", EffectiveAt: time.Now().Add(-time.Minute), InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}}
	if err := b.CreatePool(t.Context(), tenant, pool); err != nil {
		t.Fatal(err)
	}
	pool.Enabled = false
	for range 2 {
		if err := b.SavePool(t.Context(), tenant, pool); err != nil {
			t.Fatal(err)
		}
	}
	err = b.db.Transaction(t.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(t.Context(), `SELECT envelope FROM ai.audit_outbox WHERE tenant_id=$1 ORDER BY id`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		actions := map[string]int{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("credential leaked")
			}
			var event struct{ Data struct{ Action string } }
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			actions[event.Data.Action]++
		}
		for _, action := range []string{"ai.credential.create", "ai.pool.create", "ai.pool.update"} {
			if actions[action] != 1 {
				t.Fatalf("one record per mutation: %v", actions)
			}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRelayAuditPage(t *testing.T) {
	endpoint, broker := os.Getenv("AURIGA_AUDIT_TEST_URL"), os.Getenv("AURIGA_AUDIT_TEST_NATS_URL")
	if endpoint == "" || broker == "" {
		t.Skip("run AuditCombinedIntegrationTest.relayBusinessAudit for the cross-language pipeline")
	}
	db := testpg.Open(t)
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	b, err := New(db, hex.EncodeToString(master), rand.Text()+rand.Text(), rand.Text()+rand.Text(), true)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _ := newID()
	actor, _ := newID()
	ctx := audit.WithMetadata(t.Context(), actor, "192.0.2.20", "00-12345678901234567890123456789012-1234567890123456-01")
	key, err := b.SaveKey(ctx, tenant, "", api.RelayKeyInput{Name: "prompt=never-store-this", Active: true, MonthlyBudgetMicros: 10, Models: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	providerInput := api.RelayProviderInput{Name: "fixture", Kind: "openai-compatible", BaseUrl: "https://api.example.com", Enabled: true}
	provider, err := b.SaveProvider(ctx, tenant, "", providerInput, nil)
	if err != nil {
		t.Fatal(err)
	}
	secret := rand.Text() + rand.Text()
	if _, err = b.SaveProvider(ctx, tenant, provider.Id, providerInput, &secret); err != nil {
		t.Fatal(err)
	}
	providerInput.BaseUrl = "https://other.example.com"
	if _, err = b.SaveProvider(ctx, tenant, provider.Id, providerInput, nil); err != nil {
		t.Fatal(err)
	}
	reset := func() {
		t.Helper()
		if e := db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `UPDATE ai.audit_outbox SET next_attempt_at=now() WHERE tenant_id=$1 AND published_at IS NULL`, tenant)
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	// Failure before publication retains the original envelope and schedules retry.
	if found, e := audit.ForwardOnce(ctx, db, tenant, func(context.Context, string, []byte) error { return errors.New("offline") }); !found || e == nil {
		t.Fatal("offline record was lost")
	}
	reset()
	conn, err := nats.Connect(broker, nats.Timeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	js, err := conn.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	// Broker accepted the first envelope, but the sender lost its ACK/mark. A retry
	// must use exactly that ID, rather than creating a second audit event.
	if found, e := audit.ForwardOnce(ctx, db, tenant, func(ctx context.Context, id string, raw []byte) error {
		msg := &nats.Msg{Subject: audit.Subject, Data: raw, Header: nats.Header{}}
		msg.Header.Set(nats.MsgIdHdr, id)
		if _, e := js.PublishMsg(msg, nats.Context(ctx)); e != nil {
			return e
		}
		return errors.New("lost acknowledgment")
	}); !found || e == nil {
		t.Fatal("lost-ACK fixture did not retain the envelope")
	}
	reset()
	deliveryCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- audit.Run(deliveryCtx, db, tenant, broker) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	query := func(action, target string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+"?action="+url.QueryEscape(action)+"&q="+url.QueryEscape(target), nil)
			req.Header.Set("Authorization", "Bearer "+os.Getenv("AURIGA_AUDIT_TEST_BEARER"))
			resp, e := (&http.Client{Timeout: 3 * time.Second}).Do(req)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if e != nil || resp.StatusCode != 200 {
				t.Fatalf("audit query status %d: %s", resp.StatusCode, raw)
			}
			var page struct {
				Items []struct {
					Actor   struct{ Id, IP string }
					Target  struct{ Id string }
					TraceID string
				}
			}
			if e = json.Unmarshal(raw, &page); e != nil {
				t.Fatal(e)
			}
			if len(page.Items) > 0 {
				if len(page.Items) != 1 || page.Items[0].Actor.Id != actor || page.Items[0].Actor.IP != "192.0.2.20" || page.Items[0].Target.Id != target || page.Items[0].TraceID != "12345678901234567890123456789012" {
					t.Fatalf("audit page metadata or deduplication mismatch: %s", raw)
				}
				if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte(key.Secret)) || bytes.Contains(raw, []byte("never-store-this")) {
					t.Fatal("secret in audit page")
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("business audit did not become queryable: %s", raw)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	query("ai.credential.rotate", provider.Id)
	query("ai.credential.revoke", provider.Id)
	query("ai.relay_key.create", key.Key.Id)
	if err = db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var pending, attempts int
		e := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE published_at IS NULL),sum(attempts) FROM ai.audit_outbox WHERE tenant_id=$1`, tenant).Scan(&pending, &attempts)
		if e == nil && (pending != 0 || attempts < 5) {
			t.Errorf("pending=%d attempts=%d", pending, attempts)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
