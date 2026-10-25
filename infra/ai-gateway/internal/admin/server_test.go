package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

const providerInput = `{"name":"example","kind":"openai_compatible","baseUrl":"https://upstream.example.com/v1","enabled":true,"connectTimeoutSeconds":10,"firstByteTimeoutSeconds":30,"idleTimeoutSeconds":30,"totalTimeoutSeconds":120,"maxConcurrency":10}`

type endpoint struct{ method, path string }

func endpoints() []endpoint {
	return []endpoint{{"GET", prefix + "providers"}, {"POST", prefix + "providers"}, {"GET", prefix + "providers/" + testUUID}, {"PATCH", prefix + "providers/" + testUUID}, {"POST", prefix + "providers/" + testUUID + "/credentials"}, {"POST", prefix + "providers/" + testUUID + "/credentials/" + testUUID + "/activate"}, {"POST", prefix + "providers/" + testUUID + "/credentials/" + testUUID + "/revoke"}, {"GET", prefix + "models"}, {"POST", prefix + "models"}, {"PATCH", prefix + "models/example"}, {"PUT", prefix + "models/example/grants"}}
}
func request(s *Server, method, path, token, body, match, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func expect(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body)
	}
	if code != "" {
		var p api.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Code != code {
			t.Fatalf("problem=%s: %v", w.Body, err)
		}
	}
}

func TestAdminAuthorization(t *testing.T) {
	key, auth := signer(t)
	s := &Server{authenticate: auth}
	for _, ep := range endpoints() {
		t.Run(ep.method+ep.path, func(t *testing.T) {
			claims := validClaims()
			claims["perms"] = []string{}
			expect(t, request(s, ep.method, ep.path, sign(t, key, claims, "at+jwt"), `{}`, `"1"`, ""), 403, "common.forbidden")
			claims = validClaims()
			claims["ctx"] = "portal"
			expect(t, request(s, ep.method, ep.path, sign(t, key, claims, "at+jwt"), `{}`, `"1"`, ""), 403, "common.forbidden")
			expect(t, request(s, ep.method, ep.path, "invalid", `{}`, `"1"`, ""), 401, "common.unauthenticated")
			if ep.method != "GET" {
				claims = validClaims()
				claims["stepup_time"] = time.Now().Unix() - 601
				expect(t, request(s, ep.method, ep.path, sign(t, key, claims, "at+jwt"), `{}`, `"1"`, ""), 403, "iam.step_up_required")
			}
		})
	}
}

type fixture struct {
	s      *Server
	token  string
	tenant string
}

func setup(t *testing.T) fixture {
	t.Helper()
	db := testpg.Open(t)
	key, auth := signer(t)
	tenant, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims()
	claims["tid"] = tenant
	s, err := New(db, testCipher(t), auth, func(_ context.Context, _ *Principal, _ api.ModelGrants) error { return nil }, func(context.Context, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := db.Transaction(context.Background(), tenant, func(tx pgx.Tx) error {
			for _, table := range []string{"idempotency", "model", "credential", "provider"} {
				if _, e := tx.Exec(context.Background(), "DELETE FROM ai."+table+" WHERE tenant_id=$1", tenant); e != nil {
					return e
				}
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	})
	return fixture{s: s, token: sign(t, key, claims, "at+jwt"), tenant: tenant}
}
func (f fixture) call(method, path, body, match, key string) *httptest.ResponseRecorder {
	return request(f.s, method, path, f.token, body, match, key)
}
func (f fixture) provider(t *testing.T) (string, string) {
	t.Helper()
	w := f.call("POST", prefix+"providers", providerInput, "", "")
	expect(t, w, 201, "")
	var p api.Provider
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	return prefix + "providers/" + p.Id, w.Header().Get("ETag")
}
func modelInput(provider string) string {
	return fmt.Sprintf(`{"id":"example","providerId":%q,"upstreamModel":"example","displayName":"Example","enabled":true,"contextWindow":8192,"maxOutputTokens":1024,"price":{"currency":"USD","inputMicrosPerMillionTokens":0,"outputMicrosPerMillionTokens":0,"version":"v1","effectiveAt":"2026-01-01T00:00:00Z"}}`, provider)
}

func TestPostgresAdminEndpoints(t *testing.T) {
	f := setup(t)
	provider, tag := f.provider(t)
	expect(t, f.call("GET", prefix+"providers", "", "", ""), 200, "")
	expect(t, f.call("GET", provider, "", "", ""), 200, "")
	expect(t, f.call("PATCH", provider, `{"enabled":false}`, `"0"`, ""), 412, "common.precondition_failed")
	expect(t, f.call("PATCH", provider, `{"enabled":false}`, tag, ""), 200, "")
	w := f.call("POST", provider+"/credentials", `{"secret":"test-value"}`, "", "")
	expect(t, w, 201, "")
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "test-value") {
		t.Fatal("secret exposed")
	}
	var c api.Credential
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.State != "staged" {
		t.Fatal(c.State)
	}
	credential := provider + "/credentials/" + c.Id
	expect(t, f.call("POST", credential+"/activate", `{"reason":"activate"}`, w.Header().Get("ETag"), ""), 200, "")
	expect(t, f.call("POST", credential+"/revoke", `{"reason":"revoke"}`, `"2"`, ""), 200, "")
	expect(t, f.call("GET", credential, "", "", ""), 404, "common.not_found")
	w = f.call("POST", prefix+"models", modelInput(strings.TrimPrefix(provider, prefix+"providers/")), "", "")
	expect(t, w, 201, "")
	expect(t, f.call("GET", prefix+"models", "", "", ""), 200, "")
	expect(t, f.call("PATCH", prefix+"models/example", `{"displayName":"changed"}`, w.Header().Get("ETag"), ""), 200, "")
	expect(t, f.call("PUT", prefix+"models/example/grants", `{"items":[{"kind":"member","id":"`+testUUID+`"}]}`, `"2"`, ""), 200, "")
}

func TestPostgresIdempotencyAndCredentialState(t *testing.T) {
	f := setup(t)
	w := f.call("POST", prefix+"providers", providerInput, "", "same")
	expect(t, w, 201, "")
	replay := f.call("POST", prefix+"providers", strings.ReplaceAll(providerInput, ",", ", "), "", "same")
	expect(t, replay, 201, "")
	if replay.Body.String() != w.Body.String() || replay.Header().Get("ETag") != w.Header().Get("ETag") {
		t.Fatal("replay changed")
	}
	expect(t, f.call("POST", prefix+"providers", strings.Replace(providerInput, "example", "changed", 1), "", "same"), 409, "common.conflict")
	var p api.Provider
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	path := prefix + "providers/" + p.Id
	var creds []api.Credential
	for i := 0; i < 2; i++ {
		w = f.call("POST", path+"/credentials", `{"secret":"test-value"}`, "", fmt.Sprintf("credential-%d", i))
		expect(t, w, 201, "")
		var c api.Credential
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		creds = append(creds, c)
	}
	expect(t, f.call("POST", path+"/credentials/"+creds[0].Id+"/revoke", `{"reason":"staged"}`, `"1"`, ""), 409, "common.conflict")
	for _, c := range creds {
		expect(t, f.call("POST", path+"/credentials/"+c.Id+"/activate", `{"reason":"rotation"}`, `"1"`, c.Id), 200, "")
	}
	expect(t, f.call("POST", path+"/credentials/"+creds[0].Id+"/activate", `{"reason":"again"}`, `"3"`, ""), 409, "common.conflict")
	expect(t, f.call("POST", path+"/credentials/"+creds[0].Id+"/revoke", `{"reason":"retired"}`, `"3"`, ""), 200, "")
	expect(t, f.call("POST", path+"/credentials/"+creds[0].Id+"/revoke", `{"reason":"again"}`, `"4"`, ""), 409, "common.conflict")
	err := f.s.store.Transaction(context.Background(), f.tenant, func(tx pgx.Tx) error {
		var encrypted []byte
		var version int
		if e := tx.QueryRow(context.Background(), "SELECT ciphertext,cipher_version FROM ai.credential WHERE tenant_id=$1 AND id=$2", f.tenant, creds[0].Id).Scan(&encrypted, &version); e != nil {
			return e
		}
		if strings.Contains(string(encrypted), "test-value") {
			t.Fatal("stored plaintext")
		}
		plain, e := f.s.cipher.Decrypt(encrypted, Binding{f.tenant, p.Id, creds[0].Id, version})
		if e != nil || string(plain) != "test-value" {
			t.Fatal("cannot decrypt stored credential", e)
		}
		rows, e := tx.Query(context.Background(), "SELECT body FROM ai.idempotency WHERE tenant_id=$1", f.tenant)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				return e
			}
			if strings.Contains(string(b), "test-value") {
				t.Fatal("idempotency leaked plaintext")
			}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostgresConcurrentIdempotencyAndTenants(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() { results <- f.call("POST", prefix+"providers", providerInput, "", "concurrent") })
	}
	wg.Wait()
	close(results)
	first := ""
	provider := ""
	for w := range results {
		expect(t, w, 201, "")
		if first == "" {
			first = w.Body.String()
			var p api.Provider
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			provider = prefix + "providers/" + p.Id
		} else if first != w.Body.String() {
			t.Fatal("concurrent duplicate created")
		}
	}
	other := setup(t)
	expect(t, other.call("GET", provider, "", "", ""), 404, "common.not_found")
	expect(t, other.call("POST", provider+"/credentials", `{"secret":"test-value"}`, "", ""), 404, "common.not_found")
	expect(t, other.call("POST", prefix+"providers", providerInput, "", "concurrent"), 201, "")
	w := f.call("GET", prefix+"providers?limit=1", "", "", "")
	expect(t, w, 200, "")
	if err := f.s.store.Transaction(context.Background(), f.tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(context.Background(), "UPDATE ai.idempotency SET expires_at=now()-interval '1 second' WHERE tenant_id=$1", f.tenant)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	w = f.call("POST", prefix+"providers", strings.Replace(providerInput, "example", "expired", 1), "", "concurrent")
	expect(t, w, 201, "")
	if w.Body.String() == first {
		t.Fatal("expired key replayed")
	}
}

func TestCanonicalJSON(t *testing.T) {
	a, e1 := canonicalJSON([]byte(`{"n":1e1}`))
	b, e2 := canonicalJSON([]byte(`{"n":10.0}`))
	if e1 != nil || e2 != nil || string(a) != string(b) {
		t.Fatal("equivalent numbers not normalized")
	}
	for _, raw := range []string{`null`, `{"enabled":null}`, `{"a":1,"a":2}`, `{} {}`, `[]`} {
		if _, e := canonicalJSON([]byte(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestPostgresValidation(t *testing.T) {
	f := setup(t)
	path, tag := f.provider(t)
	for _, body := range []string{`{}`, `{"unknown":1}`, `{"revision":9}`, `{"name":null}`, `{"enabled":true,"enabled":false}`, `{"maxConcurrency":0}`, `{"baseUrl":"http://127.0.0.1/v1"}`} {
		expect(t, f.call("PATCH", path, body, tag, ""), 400, "common.validation_failed")
	}
	for _, query := range []string{"?limit=201", "?cursor=invalid", "?tenant=other", "?limit=1&limit=2", "?bad=%zz"} {
		expect(t, f.call("GET", prefix+"providers"+query, "", "", ""), 400, "common.validation_failed")
	}
}
