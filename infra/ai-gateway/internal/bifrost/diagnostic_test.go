package bifrost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
)

func TestPanicBeforeHeadersDoesNotLogSuccessfulResponse(t *testing.T) {
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(observability.Logger(&output))
	t.Cleanup(func() { slog.SetDefault(old) })
	h, err := New("http://127.0.0.1:1", "unused", MemberPolicy{MonthlyBudgetMicros: 10, RequestsPerMinute: 60}, func(context.Context, string) (Principal, error) { panic("fixture panic") })
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	defer func() {
		if recover() == nil {
			t.Error("panic contract changed")
		}
		if !strings.Contains(output.String(), `"http_status":0`) {
			t.Error("no-response status missing")
		}
		if strings.Contains(output.String(), `"http_status":200`) {
			t.Error("panic fabricated HTTP200")
		}
	}()
	r := httptest.NewRequest("GET", "/ai/v1/models", nil)
	r.Header.Set("Authorization", "Bearer fixture-token")
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func TestCompletedStatusAndSafeAuthFailure(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(observability.Logger(&logs))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, code := range []string{"common.unauthenticated", "common.forbidden", "ai.budget_unavailable"} {
		h, err := New("http://127.0.0.1:1", "unused", MemberPolicy{MonthlyBudgetMicros: 10, RequestsPerMinute: 60}, func(context.Context, string) (Principal, error) { return Principal{}, errors.New(code) })
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/ai/v1/models", nil)
		r.Header.Set("Authorization", "Bearer sentinel-credential")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		h.Close()
		want := 503
		if code == "common.unauthenticated" {
			want = 401
		}
		if code == "common.forbidden" {
			want = 403
		}
		if w.Code != want || !strings.Contains(logs.String(), `"http_status":`+stringStatus(w.Code)) {
			t.Fatalf("lost actual status: %s", logs.String())
		}
	}
	if strings.Contains(logs.String(), "sentinel-credential") {
		t.Fatal("header leaked")
	}
}

func stringStatus(code int) string { return map[int]string{401: "401", 403: "403", 503: "503"}[code] }
