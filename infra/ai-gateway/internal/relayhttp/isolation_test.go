package relayhttp

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

const otherOwner = "01900000-0000-7000-8000-000000000099"

func (f *fixture) secondOwner(t *testing.T) *fixture {
	t.Helper()
	claims, _ := json.Marshal(map[string]any{"tid": f.control.tenant, "sub": otherOwner, "did": device, "exp": time.Now().Add(2 * time.Minute).Unix()})
	raw := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"at+jwt"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims) + ".ZmFrZQ"
	f.control.mu.Lock()
	f.control.owners[raw] = otherOwner
	f.control.mu.Unlock()
	f.exec(t, `UPDATE ai.model SET grants='{"items":[{"kind":"member","id":"`+account+`"},{"kind":"member","id":"`+otherOwner+`"}]}'`)
	g := *f
	g.raw = raw
	return &g
}

func TestHTTPOwnerTenantCursorIsolation(t *testing.T) {
	f := fixtureNew(t)
	g := f.secondOwner(t)
	other := fixtureNew(t)
	var calls atomic.Int32
	s := f.server(t, finite(completion(textChoice), &calls))
	otherServer := other.server(t, finite(completion(textChoice), &calls))
	for n := 0; n < 2; n++ {
		status, _, b := f.request(t, s, "POST", chatPath, plainRequest, "")
		if status != 200 {
			t.Fatal(status, b)
		}
	}
	// Same key across owners and tenants grants independent execution rights.
	for _, pair := range []struct {
		f *fixture
		s *httptest.Server
	}{{f, s}, {g, s}, {other, otherServer}} {
		status, _, b := pair.f.request(t, pair.s, "POST", chatPath, plainRequest, "shared")
		if status != 200 {
			t.Fatal(status, b)
		}
	}
	status, _, body := f.request(t, s, "GET", usagePath+"?limit=1", "", "")
	var page api.UsagePage
	if err := json.Unmarshal([]byte(body), &page); err != nil || status != 200 || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(status, body, err)
	}
	cursor := url.QueryEscape(*page.NextCursor)
	status, _, body = f.request(t, s, "GET", usagePath+"?limit=1&cursor="+cursor, "", "")
	var next api.UsagePage
	if err := json.Unmarshal([]byte(body), &next); err != nil || status != 200 || len(next.Items) != 1 || next.Items[0].RequestId == page.Items[0].RequestId {
		t.Fatal(status, body, err)
	}
	for _, pair := range []struct {
		f *fixture
		s *httptest.Server
	}{{g, s}, {other, otherServer}} {
		status, _, body = pair.f.request(t, pair.s, "GET", usagePath+"?cursor="+cursor, "", "")
		if status != 400 || !strings.Contains(body, "common.validation_failed") || strings.Contains(body, `"error":`) {
			t.Fatal(status, body)
		}
		status, _, body = pair.f.request(t, pair.s, "GET", usagePath, "", "")
		var own api.UsagePage
		if err := json.Unmarshal([]byte(body), &own); err != nil || status != 200 || len(own.Items) != 1 {
			t.Fatal(status, body, err)
		}
		if own.Items[0].AccountId == account && pair.f == g {
			t.Fatal("owner leakage")
		}
	}
	if calls.Load() != 5 {
		t.Fatal("read or retry dispatched", calls.Load())
	}
}

func TestHTTPPostgresRejectionsDoNotDispatch(t *testing.T) {
	f := fixtureNew(t)
	var calls atomic.Int32
	s := f.server(t, finite(completion(textChoice), &calls))
	for _, c := range []struct {
		method, path, body, header, value string
		status                            int
	}{
		{"GET", modelsPath, "", "Authorization", "Bearer ait_example", 401},
		{"GET", modelsPath, "", "Authorization", "Bearer invalid.original.ticket", 401},
		{"GET", modelsPath, "", "Origin", "https://example.com", 403},
		{"GET", modelsPath, "", "Cookie", "session=x", 403},
		{"GET", modelsPath, "", "Auriga-Forwarded-Node", device, 403},
		{"GET", modelsPath + "?limit=1", "", "", "", 400},
		{"GET", usagePath + "?account=" + otherOwner, "", "", "", 400},
		{"POST", chatPath, strings.Replace(plainRequest, `"max_tokens":`, `"url":"https://example.com", "max_tokens":`, 1), "", "", 400},
		{"POST", chatPath, plainRequest, "Content-Type", "text/plain", 400},
		{"POST", chatPath, strings.Repeat("x", (1<<20)+1), "", "", 400},
		{"HEAD", modelsPath, "", "", "", 404},
		{"GET", "/ai/v1/%6dodels", "", "", "", 404},
		{"GET", "/ai/v1/../models", "", "", "", 404},
	} {
		r, err := http.NewRequestWithContext(t.Context(), c.method, s.URL+c.path, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+f.raw)
		if c.method == "POST" {
			r.Header.Set("Content-Type", "application/json")
		}
		if c.header != "" {
			r.Header.Set(c.header, c.value)
		}
		res, err := s.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != c.status {
			t.Errorf("%s %s: %d", c.method, c.path, res.StatusCode)
		}
	}
	for _, p := range []string{modelsPath, usagePath} {
		status, _, b := f.request(t, s, "GET", p, "", "")
		if status != 200 {
			t.Fatal(status, b)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejection/read dispatched")
	}
	err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		var n int
		if e := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1`, f.control.tenant).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			t.Fatal("read/rejection reserved")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A dependency failure on usage retains that operation's Problem schema/code.
	f.control.mu.Lock()
	f.control.fail = true
	f.control.mu.Unlock()
	status, headers, body := f.request(t, s, "GET", usagePath, "", "")
	if status != 503 || headers.Get("Content-Type") != "application/problem+json" || !strings.Contains(body, "common.unavailable") {
		t.Fatal(status, body)
	}
}
