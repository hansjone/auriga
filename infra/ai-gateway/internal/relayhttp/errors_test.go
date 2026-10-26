package relayhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestErrorsMatchFormalCatalog(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/catalog/m3.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Errors map[string]struct {
			HTTP    int               `json:"http"`
			Message map[string]string `json:"message"`
		} `json:"errors"`
	}
	if err = json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	for code, info := range errorCatalog {
		if !strings.HasPrefix(code, "ai.") {
			continue
		}
		want, ok := catalog.Errors[code]
		if !ok || want.HTTP != info.status || want.Message["en"] != info.en || want.Message["zh-CN"] != info.zh {
			t.Errorf("catalog drift: %s", code)
		}
	}
}

func TestSafeErrorsAndReplayFields(t *testing.T) {
	for _, problem := range []bool{false, true} {
		for _, language := range []string{"en", "zh-CN"} {
			w := &deadlineRecorder{httptest.NewRecorder()}
			s := response{w: w, rc: http.NewResponseController(w), timeout: time.Second, max: 1 << 20, language: language}
			s.failure(t.Context(), "local-request", problem, &modelprotocol.Error{Code: "ai.budget_unavailable", Message: "SQL bearer secret https://upstream.example.com/private"})
			if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "SQL") || strings.Contains(w.Body.String(), "upstream.example.com") {
				t.Fatal("secret escaped")
			}
			if problem {
				var p api.Problem
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Code != "common.unavailable" || w.Code != 503 || p.GetTraceId() != "local-request" {
					t.Fatal(w.Body.String(), err)
				}
			} else {
				var e api.AiError
				if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Code != "ai.budget_unavailable" || e.RequestId != "local-request" {
					t.Fatal(w.Body.String(), err)
				}
			}
		}
	}
	w := &deadlineRecorder{httptest.NewRecorder()}
	s := response{w: w, rc: http.NewResponseController(w), timeout: time.Second, max: 1 << 20}
	s.failure(t.Context(), "retry", false, &budget.Error{Code: "ai.replay_unavailable", OriginalRequestID: "original", Outcome: "unknown"})
	var e api.AiError
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.GetOriginalRequestId() != "original" || e.GetOutcome() != "unknown" || w.Code != 409 {
		t.Fatal(w.Body.String(), err)
	}
	w = &deadlineRecorder{httptest.NewRecorder()}
	s = response{w: w, rc: http.NewResponseController(w), timeout: time.Second, max: 1 << 20}
	s.failure(t.Context(), "request", false, errors.New("SQL connection secret"))
	if !strings.Contains(w.Body.String(), "common.internal") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Body.String())
	}
}

func TestConfigurationAndDeadlineCapabilityFailClosed(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Core = nil }, func(c *Config) { c.Gate = nil }, func(c *Config) { c.Authority = nil }, func(c *Config) { c.Ledger = nil }, func(c *Config) { c.Recover = nil },
		func(c *Config) { c.ReadTimeout = 0 }, func(c *Config) { c.WriteTimeout = 0 }, func(c *Config) { c.QueryTimeout = 0 }, func(c *Config) { c.MaxResponseBytes = 1 },
	} {
		c := testConfig()
		mutate(&c)
		if _, err := New(c); err == nil {
			t.Fatal("unsafe config accepted")
		}
	}
	h, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("unsupported deadline surface must abort before execute")
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", modelsPath, nil))
}

func TestNodeFetchMetadataDoesNotSelectIdentity(t *testing.T) {
	r := httptest.NewRequest("GET", modelsPath, nil)
	r.Header.Set("Authorization", "Bearer original")
	r.Header.Set("Sec-Fetch-Mode", "cors") // Node's real fetch sends this metadata too.
	if raw, err := bearer(r); err != nil || raw != "original" {
		t.Fatal("nonbrowser fetch metadata blocked", err)
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if _, err := bearer(r); err == nil {
		t.Fatal("browser channel admitted")
	}
}
