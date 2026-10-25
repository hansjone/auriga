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
	return []endpoint{{"GET", prefix + "providers"}, {"POST", prefix + "providers"}, {"GET", prefix + "providers/" + testUUID}, {"PATCH", prefix + "providers/" + testUUID}, {"POST", prefix + "providers/" + testUUID + "/credentials"}, {"GET", prefix + "providers/" + testUUID + "/credentials/" + testUUID}, {"POST", prefix + "providers/" + testUUID + "/credentials/" + testUUID + "/activate"}, {"POST", prefix + "providers/" + testUUID + "/credentials/" + testUUID + "/revoke"}, {"GET", prefix + "models"}, {"POST", prefix + "models"}, {"GET", prefix + "models/example"}, {"PATCH", prefix + "models/example"}, {"GET", prefix + "models/example/grants"}, {"PUT", prefix + "models/example/grants"}}
}

func TestAdminReadRoutes(t *testing.T) {
	for _, ep := range endpoints() {
		if ep.method != "GET" {
			continue
		}
		t.Run(ep.path, func(t *testing.T) {
			o, ok := route(ep.method, ep.path)
			if !ok || o.write || o.permission != "ai.providers.read" {
				t.Fatalf("read route not registered: %+v %v", o, ok)
			}
		})
	}
}

func TestAdminResponseETag(t *testing.T) {
	for _, value := range []any{api.Provider{Revision: 7}, api.Model{Revision: 7}} {
		r, err := response(200, value, 7)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err = json.Unmarshal(r.body, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["etag"] != r.etag || r.etag == "" {
			t.Fatalf("body ETag differs from header: %s %s", r.body, r.etag)
		}
	}
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
			expect(t, request(s, ep.method, ep.path, sign(t, key, claims, "at+jwt"), `{}`, `"opaque"`, ""), 403, "common.forbidden")
			claims = validClaims()
			claims["ctx"] = "portal"
			expect(t, request(s, ep.method, ep.path, sign(t, key, claims, "at+jwt"), `{}`, `"opaque"`, ""), 403, "common.forbidden")
			expect(t, request(s, ep.method, ep.path, "invalid", `{}`, `"opaque"`, ""), 401, "common.unauthenticated")
			if ep.method != "GET" {
				claims = validClaims()
				claims["stepup_time"] = time.Now().Unix() - 601
				expect(t, request(s, ep.method, ep.path, sign(t, key, claims, "at+jwt"), `{}`, `"opaque"`, ""), 403, "iam.step_up_required")
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
func (f fixture) read(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := f.call("GET", path, "", "", "")
	expect(t, w, 200, "")
	if w.Header().Get("ETag") == "" {
		t.Fatal("single GET omitted ETag")
	}
	return w
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
	w = f.read(t, credential)
	expect(t, f.call("POST", credential+"/activate", `{"reason":"activate"}`, w.Header().Get("ETag"), ""), 200, "")
	w = f.read(t, credential)
	expect(t, f.call("POST", credential+"/revoke", `{"reason":"revoke"}`, w.Header().Get("ETag"), ""), 200, "")
	w = f.read(t, credential)
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "test-value") {
		t.Fatal("credential GET exposed secret")
	}
	w = f.call("POST", prefix+"models", modelInput(strings.TrimPrefix(provider, prefix+"providers/")), "", "")
	expect(t, w, 201, "")
	expect(t, f.call("GET", prefix+"models", "", "", ""), 200, "")
	w = f.read(t, prefix+"models/example")
	expect(t, f.call("PATCH", prefix+"models/example", `{"displayName":"changed"}`, w.Header().Get("ETag"), ""), 200, "")
	w = f.read(t, prefix+"models/example/grants")
	expect(t, f.call("PUT", prefix+"models/example/grants", `{"items":[{"kind":"member","id":"`+testUUID+`"}]}`, w.Header().Get("ETag"), ""), 200, "")
	w = f.read(t, prefix+"models/example/grants")
	var g api.ModelGrants
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil || len(g.Items) != 1 || g.Items[0].Id != testUUID {
		t.Fatalf("saved grants not readable: %s, %v", w.Body, err)
	}
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
	oldPath := path + "/credentials/" + creds[0].Id
	w = f.read(t, oldPath)
	expect(t, f.call("POST", oldPath+"/revoke", `{"reason":"staged"}`, w.Header().Get("ETag"), ""), 409, "common.conflict")
	var oldActiveTag string
	for _, c := range creds {
		credential := path + "/credentials/" + c.Id
		w = f.read(t, credential)
		w = f.call("POST", credential+"/activate", `{"reason":"rotation"}`, w.Header().Get("ETag"), c.Id)
		expect(t, w, 200, "")
		if c.Id == creds[0].Id {
			oldActiveTag = w.Header().Get("ETag")
		}
	}
	w = f.read(t, oldPath)
	var retired api.Credential
	if err := json.Unmarshal(w.Body.Bytes(), &retired); err != nil || retired.State != "retiring" {
		t.Fatalf("retired credential GET: %s %v", w.Body, err)
	}
	if w.Header().Get("ETag") == oldActiveTag {
		t.Fatal("implicit retirement did not expose a new ETag")
	}
	expect(t, f.call("POST", oldPath+"/revoke", `{"reason":"stale"}`, oldActiveTag, ""), 412, "common.precondition_failed")
	expect(t, f.call("POST", oldPath+"/activate", `{"reason":"again"}`, w.Header().Get("ETag"), ""), 409, "common.conflict")
	expect(t, f.call("POST", oldPath+"/revoke", `{"reason":"retired"}`, w.Header().Get("ETag"), ""), 200, "")
	w = f.read(t, oldPath)
	expect(t, f.call("POST", oldPath+"/revoke", `{"reason":"again"}`, w.Header().Get("ETag"), ""), 409, "common.conflict")
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

func TestRecentStepUp(t *testing.T) {
	const now int64 = 2000000000
	for _, tc := range []struct {
		value   int64
		allowed bool
	}{{0, false}, {now - 601, false}, {now - 600, true}, {now, true}, {now + 30, true}, {now + 31, false}} {
		if recentStepUp(tc.value, now) != tc.allowed {
			t.Errorf("stepup=%d allowed=%v", tc.value, tc.allowed)
		}
	}
}

func TestReadDocumentETagProjection(t *testing.T) {
	for _, value := range []any{api.Provider{Id: testUUID, Revision: 1}, api.Model{Id: "example", Revision: 1}} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "etag")
		raw, err = json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		switch value.(type) {
		case api.Provider:
			var p api.Provider
			if err = readDocument(raw, 7, &p); err != nil {
				t.Fatal(err)
			}
			if p.Etag != etag(p.Revision) || p.Revision != 7 {
				t.Fatalf("provider projection: %+v", p)
			}
		case api.Model:
			var m api.Model
			if err = readDocument(raw, 7, &m); err != nil {
				t.Fatal(err)
			}
			if m.Etag != etag(m.Revision) || m.Revision != 7 {
				t.Fatalf("model projection: %+v", m)
			}
		}
	}
}

func TestPostgresReadableETagsAndTenantIsolation(t *testing.T) {
	f := setup(t)
	provider, _ := f.provider(t)
	second, _ := f.provider(t)
	w := f.call("POST", prefix+"models", modelInput(strings.TrimPrefix(provider, prefix+"providers/")), "", "")
	expect(t, w, 201, "")
	w = f.call("POST", provider+"/credentials", `{"secret":"test-value"}`, "", "")
	expect(t, w, 201, "")
	var c api.Credential
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	credential := provider + "/credentials/" + c.Id
	// Simulate persisted rows written before CONTRACTFIX introduced response etag.
	if err := f.s.store.Transaction(context.Background(), f.tenant, func(tx pgx.Tx) error {
		for _, table := range []string{"provider", "model"} {
			if _, e := tx.Exec(context.Background(), "UPDATE ai."+table+" SET document=document-'etag' WHERE tenant_id=$1", f.tenant); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{provider, prefix + "models/example"} {
		w = f.read(t, path)
		var fields map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if fields["etag"] != w.Header().Get("ETag") {
			t.Fatalf("GET etag mismatch: %s", w.Body)
		}
	}
	w = f.call("GET", prefix+"providers?limit=1", "", "", "")
	expect(t, w, 200, "")
	var providers api.ProviderPage
	if err := json.Unmarshal(w.Body.Bytes(), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers.Items) != 1 || providers.NextCursor == nil {
		t.Fatalf("pagination missing: %s", w.Body)
	}
	for _, p := range providers.Items {
		single := f.read(t, prefix+"providers/"+p.Id)
		if p.Etag == "" || p.Etag != single.Header().Get("ETag") {
			t.Fatal("provider list etag differs from GET")
		}
		expect(t, f.call("PATCH", prefix+"providers/"+p.Id, `{"name":"renamed"}`, p.Etag, ""), 200, "")
	}
	w = f.call("GET", prefix+"providers?cursor="+*providers.NextCursor, "", "", "")
	expect(t, w, 200, "")
	var next api.ProviderPage
	if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil || len(next.Items) != 1 || next.Items[0].Id == providers.Items[0].Id {
		t.Fatalf("next page: %s %v", w.Body, err)
	}
	w = f.call("GET", prefix+"models", "", "", "")
	expect(t, w, 200, "")
	var models api.ModelPage
	if err := json.Unmarshal(w.Body.Bytes(), &models); err != nil {
		t.Fatal(err)
	}
	if len(models.Items) != 1 || models.Items[0].Etag != f.read(t, prefix+"models/example").Header().Get("ETag") {
		t.Fatalf("model list ETag: %s", w.Body)
	}
	grants := f.read(t, prefix+"models/example/grants")
	if grants.Header().Get("ETag") == models.Items[0].Etag {
		t.Fatal("grants ETag must not be a model ETag")
	}
	other := setup(t)
	for _, path := range []string{provider, credential, prefix + "models/example", prefix + "models/example/grants"} {
		expect(t, other.call("GET", path, "", "", ""), 404, "common.not_found")
	}
	expect(t, f.call("GET", second+"/credentials/"+c.Id, "", "", ""), 404, "common.not_found")
	for _, path := range []string{prefix + "models/missing", prefix + "models/missing/grants", provider + "/credentials/" + testUUID} {
		expect(t, f.call("GET", path, "", "", ""), 404, "common.not_found")
	}
	for _, path := range []string{provider, credential, prefix + "models/example", prefix + "models/example/grants"} {
		expect(t, f.call("GET", path+"?limit=1", "", "", ""), 400, "common.validation_failed")
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

func TestGrantsETagResourceScope(t *testing.T) {
	g := api.ModelGrants{Items: []api.Grant{}}
	current, err := grantsResponse(g, testUUID, "example", 7)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(current.etag, `"`) || !strings.HasSuffix(current.etag, `"`) || strings.HasPrefix(current.etag, "W/") {
		t.Fatalf("not a strong ETag: %s", current.etag)
	}
	model, err := response(200, api.Model{Id: "example", Revision: 7}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if current.etag == model.etag {
		t.Fatal("model tag accepted as grants tag")
	}
	for _, tc := range []struct {
		tenant, model string
		revision      int64
	}{
		{testUUID, "example", 8}, {testUUID, "second", 7}, {"00000000-0000-7000-8000-000000000099", "example", 7},
	} {
		other, err := grantsResponse(g, tc.tenant, tc.model, tc.revision)
		if err != nil {
			t.Fatal(err)
		}
		if other.etag == current.etag {
			t.Fatalf("ETag scope collision: %+v", tc)
		}
	}
	repeat, err := grantsResponse(g, testUUID, "example", 7)
	if err != nil || repeat.etag != current.etag || repeat.body == nil {
		t.Fatalf("unstable grants response: %+v %v", repeat, err)
	}
	var decoded api.ModelGrants
	if err = json.Unmarshal(current.body, &decoded); err != nil || len(decoded.Items) != 0 {
		t.Fatalf("grants representation changed: %s %v", current.body, err)
	}
}

func TestPostgresGrantsETagResourceScope(t *testing.T) {
	f := setup(t)
	provider, _ := f.provider(t)
	input := modelInput(strings.TrimPrefix(provider, prefix+"providers/"))
	expect(t, f.call("POST", prefix+"models", input, "", ""), 201, "")
	expect(t, f.call("POST", prefix+"models", strings.Replace(input, `"id":"example"`, `"id":"second"`, 1), "", ""), 201, "")
	modelPath := prefix + "models/example"
	grantsPath := modelPath + "/grants"
	model := f.read(t, modelPath)
	grants := f.read(t, grantsPath)
	otherModel := f.read(t, prefix+"models/second")
	otherGrants := f.read(t, prefix+"models/second/grants")
	var first, second api.Model
	if err := json.Unmarshal(model.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(otherModel.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if first.Revision != second.Revision {
		t.Fatal("regression requires equal model revisions")
	}
	listed := f.call("GET", prefix+"models", "", "", "")
	expect(t, listed, 200, "")
	var page api.ModelPage
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	var listTag string
	for _, item := range page.Items {
		if item.Id == "example" {
			listTag = item.Etag
		}
	}
	if listTag == "" || listTag != model.Header().Get("ETag") {
		t.Fatal("model list/GET ETag changed")
	}
	foreign := setup(t)
	foreignProvider, _ := foreign.provider(t)
	expect(t, foreign.call("POST", prefix+"models", modelInput(strings.TrimPrefix(foreignProvider, prefix+"providers/")), "", ""), 201, "")
	foreignGrants := foreign.read(t, grantsPath)
	assertUnchanged := func(t *testing.T, beforeGrants, beforeModel *httptest.ResponseRecorder) {
		t.Helper()
		for path, before := range map[string]*httptest.ResponseRecorder{grantsPath: beforeGrants, modelPath: beforeModel} {
			after := f.read(t, path)
			if after.Body.String() != before.Body.String() || after.Header().Get("ETag") != before.Header().Get("ETag") {
				t.Fatalf("rejected write changed %s: %s", path, after.Body)
			}
		}
	}
	grantBody := `{"items":[{"kind":"member","id":"` + testUUID + `"}]}`
	for _, tc := range []struct{ name, tag string }{
		{"model_get", model.Header().Get("ETag")}, {"model_list", listTag}, {"other_model", otherModel.Header().Get("ETag")},
		{"other_grants_same_revision", otherGrants.Header().Get("ETag")}, {"other_tenant_same_model_revision", foreignGrants.Header().Get("ETag")},
		{"missing", ""}, {"weak", "W/" + grants.Header().Get("ETag")}, {"wildcard", "*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, f.call("PUT", grantsPath, grantBody, tc.tag, ""), 412, "common.precondition_failed")
			assertUnchanged(t, grants, model)
		})
	}
	// A grants tag cannot be used in the opposite direction for model PATCH.
	expect(t, f.call("PATCH", modelPath, `{"enabled":false}`, grants.Header().Get("ETag"), ""), 412, "common.precondition_failed")
	assertUnchanged(t, grants, model)
	updated := f.call("PUT", grantsPath, grantBody, grants.Header().Get("ETag"), "")
	expect(t, updated, 200, "")
	if updated.Header().Get("ETag") == "" || updated.Header().Get("ETag") == grants.Header().Get("ETag") {
		t.Fatal("successful replacement did not issue a new grants tag")
	}
	current := f.read(t, grantsPath)
	if current.Header().Get("ETag") != updated.Header().Get("ETag") || current.Body.String() != updated.Body.String() {
		t.Fatal("PUT and GET grants response diverged")
	}
	currentModel := f.read(t, modelPath)
	if current.Header().Get("ETag") == currentModel.Header().Get("ETag") {
		t.Fatal("updated grants reused model tag")
	}
	expect(t, f.call("PUT", grantsPath, `{"items":[]}`, grants.Header().Get("ETag"), ""), 412, "common.precondition_failed")
	assertUnchanged(t, current, currentModel)
	// Shared storage revision remains an allowed conservative invalidation rule.
	expect(t, f.call("PATCH", modelPath, `{"enabled":false}`, currentModel.Header().Get("ETag"), ""), 200, "")
	afterPatch := f.read(t, grantsPath)
	if afterPatch.Header().Get("ETag") == current.Header().Get("ETag") {
		t.Fatal("shared revision did not invalidate grants tag")
	}
	if afterPatch.Body.String() != current.Body.String() {
		t.Fatal("model PATCH changed grants")
	}
	expect(t, f.call("PUT", grantsPath, `{"items":[]}`, current.Header().Get("ETag"), ""), 412, "common.precondition_failed")
	expect(t, f.call("PUT", grantsPath, `{"items":[]}`, afterPatch.Header().Get("ETag"), ""), 200, "")
}
