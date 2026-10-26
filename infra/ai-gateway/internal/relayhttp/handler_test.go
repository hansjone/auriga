package relayhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
)

func testConfig() Config {
	return Config{Core: new(relay.Core), Gate: new(modelgate.Gate), Authority: new(modelgate.Authority), Ledger: new(budget.Ledger), Recover: func(Pending) {}, ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond, QueryTimeout: time.Second, MaxResponseBytes: 1 << 20}
}

// Deadline support is mandatory even when an embedding wrapper is present.
type deadlineRecorder struct{ *httptest.ResponseRecorder }

func (*deadlineRecorder) SetReadDeadline(time.Time) error  { return nil }
func (*deadlineRecorder) SetWriteDeadline(time.Time) error { return nil }

func TestBoundaryRejections(t *testing.T) {
	h, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		method, uri, header, value string
		status                     int
	}{
		{"HEAD", "/ai/v1/models", "", "", 404}, {"OPTIONS", "/ai/v1/models", "", "", 404},
		{"GET", "/ai/v1/models/", "", "", 404}, {"GET", "/ai/v1/./models", "", "", 404},
		{"GET", "/ai/v1/%6dodels", "", "", 404}, {"GET", "http://example.com/ai/v1/models", "", "", 404},
		{"GET", "/ai/v1/models?tenant=x", "", "", 400},
		{"GET", "/api/ai/v1/me/usage?limit=0", "", "", 400},
		{"GET", "/api/ai/v1/me/usage?limit=1&limit=2", "", "", 400},
		{"GET", "/api/ai/v1/me/usage?cursor=x&owner=x", "", "", 400},
		{"GET", "/ai/v1/models", "Origin", "", 403}, {"GET", "/ai/v1/models", "Cookie", "", 403},
		{"GET", "/ai/v1/models", "Auriga-Forwarded-Tenant", "x", 403},
		{"GET", "/ai/v1/models", "Authorization", "Bearer a, Bearer b", 401},
		{"GET", "/ai/v1/models", "Authorization", "Bearer a b", 401},
		{"GET", "/ai/v1/models", "", "", 401},
		{"POST", "/ai/v1/chat/completions", "Authorization", "Bearer a", 400},
	}
	for _, c := range cases {
		t.Run(c.method+c.uri+c.header+c.value, func(t *testing.T) {
			r := httptest.NewRequest(c.method, c.uri, nil)
			if c.header != "" {
				r.Header[c.header] = []string{c.value}
			}
			w := &deadlineRecorder{httptest.NewRecorder()}
			h.ServeHTTP(w, r)
			if w.Code != c.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(c.uri, "/api/") && !strings.Contains(w.Header().Get("Content-Type"), "problem+json") {
				t.Fatal("usage must use Problem")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS opened")
			}
		})
	}
}

type zeroWriter struct {
	header   http.Header
	statuses []int
}

func (w *zeroWriter) Header() http.Header            { return w.header }
func (w *zeroWriter) WriteHeader(n int)              { w.statuses = append(w.statuses, n) }
func (*zeroWriter) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (*zeroWriter) SetWriteDeadline(time.Time) error { return nil }
func TestCommittedZeroWrite(t *testing.T) {
	w := &zeroWriter{header: make(http.Header)}
	s := response{w: w, rc: http.NewResponseController(w), timeout: time.Second, max: 1024}
	_, err := s.sink(t.Context(), []byte("data: {}\n\n"))
	if err == nil || !s.committed {
		t.Fatal("zero write lost committed header")
	}
	s.failure(t.Context(), "request", false, errors.New("private SQL secret"))
	if len(w.statuses) != 1 || w.statuses[0] != 200 {
		t.Fatalf("rewrote committed status %v", w.statuses)
	}
}

func TestSinkCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := &deadlineRecorder{httptest.NewRecorder()}
	s := response{w: w, rc: http.NewResponseController(w), timeout: time.Second, max: 1024}
	if _, err := s.sink(ctx, []byte("data: {}\n\n")); !errors.Is(err, context.Canceled) || s.committed {
		t.Fatal("cancel must return before headers")
	}
}
