package upstream

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func submission(base string, stream bool) relay.Submission {
	return relay.Submission{Target: admin.RelayTarget{BaseURL: base, UpstreamModel: "provider-model", ConnectTimeoutSeconds: 1}, PublicModel: "public-model", RequestID: "local-request", Request: api.ChatCompletionRequest{Model: "ignored-model", Stream: &stream, MaxTokens: 2, Messages: []api.ChatMessage{{Role: "user", Content: *api.NewNullableString(new("hi"))}}}}
}
func clientFor(t *testing.T) *Client {
	t.Helper()
	c, err := New(Config{AllowedHosts: []string{"127.0.0.1"}, MaxResponseBytes: 4096, MaxEventBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func readExchange(t *testing.T, x relay.Exchange, err error) []byte {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = x.Body.Close() }()
	b, err := io.ReadAll(x.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func completion() string {
	return `{"id":"provider-id","object":"chat.completion","created":1,"model":"provider-model","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
}
func chunks() string {
	return "data: " + `{"id":"provider-id","object":"chat.completion.chunk","created":1,"model":"provider-model","choices":[{"index":0,"delta":{"role":"assistant","content":"OK"},"finish_reason":null}]}` + "\n\ndata: " + `{"id":"provider-id","object":"chat.completion.chunk","created":1,"model":"provider-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}` + "\n\ndata: [DONE]\n\n"
}
func TestTransportJSON(t *testing.T) {
	secret := []byte(rand.Text())
	var calls atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+string(secret) || r.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect dispatch")
		}
		var v map[string]any
		if json.NewDecoder(r.Body).Decode(&v) != nil || v["model"] != "provider-model" || v["stream"] != false {
			t.Error("incorrect wire request")
		}
		_, _ = io.WriteString(w, completion())
	}))
	defer s.Close()
	x, err := clientFor(t).Transport(t.Context(), submission(s.URL+"/v1/", false), secret)
	b := readExchange(t, x, err)
	if !strings.Contains(string(b), `"model":"public-model"`) || calls.Load() != 1 {
		t.Fatal("model mapping or single submission failed")
	}
	select {
	case <-x.Stopped:
	default:
		t.Fatal("complete terminal response must prove stop")
	}
}
func protocolRequest(t *testing.T, stream bool) *modelprotocol.Request {
	t.Helper()
	r, err := modelprotocol.DecodeRequest(t.Context(), strings.NewReader(fmt.Sprintf(`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"max_tokens":2,"stream":%t}`, stream)), modelprotocol.Policy{Model: "public-model", ContextWindow: 64, MaxOutputTokens: 2, MeasureInput: func(context.Context, api.ChatCompletionRequest) (int64, error) { return 1, nil }, VerifyUsage: func(context.Context, api.TokenUsage) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestTransportSSE(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if json.NewDecoder(r.Body).Decode(&v) != nil || v["stream"] != true {
			t.Error("stream changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, b := range []byte(chunks()) {
			_, _ = w.Write([]byte{b})
			w.(http.Flusher).Flush()
		}
	}))
	defer s.Close()
	x, err := clientFor(t).Transport(t.Context(), submission(s.URL, true), []byte(rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	r, err := modelprotocol.Stream(t.Context(), x.Body, protocolRequest(t, true), modelprotocol.Limits{MaxBytes: 4096, MaxEvents: 8, MaxEventBytes: 2048}, "local-request", func(_ context.Context, b []byte) (int, error) { return output.Write(b) })
	if err != nil || !r.Completed || !r.UsageKnown || !strings.Contains(output.String(), "[DONE]") || strings.Contains(output.String(), "provider-model") {
		t.Fatalf("SSE normalization: %+v %v", r, err)
	}
	select {
	case <-x.Stopped:
	default:
		t.Fatal("terminal stream must prove stop")
	}
}
func TestTransportStatusRedaction(t *testing.T) {
	for _, status := range []int{400, 401, 403, 408, 429, 500, 502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			secret := []byte(rand.Text())
			var calls atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write(secret)
			}))
			defer s.Close()
			x, err := clientFor(t).Transport(t.Context(), submission(s.URL, false), secret)
			if err != nil {
				t.Fatal(err)
			}
			_, err = modelprotocol.Complete(t.Context(), x.Body, protocolRequest(t, false), modelprotocol.Limits{MaxBytes: 4096, MaxEvents: 8, MaxEventBytes: 2048})
			want := "ai.upstream_unavailable"
			if status < 500 {
				want = "ai.upstream_rejected"
			}
			if status == 408 || status == 504 {
				want = "ai.upstream_timeout"
			}
			var e *modelprotocol.Error
			if !errors.As(err, &e) || e.Code != want || strings.Contains(fmt.Sprintf("%+v %#v", err, e.Envelope("local-request")), string(secret)) || calls.Load() != 1 {
				t.Fatalf("status mapping/redaction: %v", err)
			}
		})
	}
}
func TestTransportInterruptedAndLimited(t *testing.T) {
	for _, mode := range []string{"interrupted", "oversized", "closed"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "oversized" {
					_, _ = io.WriteString(w, strings.Repeat("x", 10000))
					return
				}
				_, _ = io.WriteString(w, strings.Split(chunks(), "\n\n")[0]+"\n\n")
				w.(http.Flusher).Flush()
				if mode == "closed" {
					<-r.Context().Done()
					return
				}
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					_ = conn.Close()
				}
			}))
			defer s.Close()
			x, err := clientFor(t).Transport(t.Context(), submission(s.URL, true), []byte(rand.Text()))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "closed" {
				_ = x.Body.Close()
			} else {
				_, err = modelprotocol.Stream(t.Context(), x.Body, protocolRequest(t, true), modelprotocol.Limits{MaxBytes: 1024, MaxEvents: 8, MaxEventBytes: 512}, "local-request", func(_ context.Context, b []byte) (int, error) { return len(b), nil })
				if err == nil {
					t.Fatal("incomplete or oversized stream accepted")
				}
			}
			select {
			case <-x.Stopped:
				t.Fatal("failure/Close forged remote stop proof")
			default:
			}
		})
	}
}
func TestTransportCancellation(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(fmt.Sprint(headers), func(t *testing.T) {
			entered := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if headers {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				close(entered)
				<-r.Context().Done()
			}))
			defer s.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				x, err := clientFor(t).Transport(ctx, submission(s.URL, false), []byte(rand.Text()))
				if err == nil {
					_, err = io.ReadAll(x.Body)
					_ = x.Body.Close()
				}
				done <- err
			}()
			<-entered
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancellation succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation blocked")
			}
		})
	}
}
func TestOutboundPolicy(t *testing.T) {
	c, err := New(Config{MaxResponseBytes: 4096, MaxEventBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.0.1", "169.254.1.1", "169.254.169.254", "0.0.0.0", "100.100.100.200", "224.0.0.1", "::1", "fc00::1", "fe80::1", "::", "::ffff:127.0.0.1"} { // gitleaks:allow
		t.Run(host, func(t *testing.T) {
			if _, err := c.Transport(t.Context(), submission("https://"+net.JoinHostPort(host, "443"), false), []byte(rand.Text())); err == nil {
				t.Fatal("unsafe address accepted")
			}
		})
	}
	for _, base := range []string{"http://example.com", "https://alice:password@example.com", "https://example.com?secret=yes", "https://example.com#fragment", "ftp://example.com"} {
		if _, err := c.Transport(t.Context(), submission(base, false), []byte(rand.Text())); err == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
	for _, host := range []string{"*", "*.example.com", "example.com:443", "https://example.com", "example.com/path", "example.com@evil.example"} {
		if _, err := New(Config{AllowedHosts: []string{host}, MaxResponseBytes: 4096, MaxEventBytes: 2048}); err == nil {
			t.Fatal("unsafe allowlist accepted")
		}
	}
}
func TestRedirectNotFollowed(t *testing.T) {
	var destinationCalls atomic.Int64
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalls.Add(1)
		_, _ = io.WriteString(w, completion())
	}))
	defer dest.Close()
	for _, status := range []int{301, 302, 303, 307, 308} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dest.URL, status) }))
		x, err := clientFor(t).Transport(t.Context(), submission(s.URL, false), []byte(rand.Text()))
		b := readExchange(t, x, err)
		s.Close()
		if !strings.Contains(string(b), "ai.upstream_rejected") || destinationCalls.Load() != 0 {
			t.Fatal("redirect followed")
		}
	}
}
func TestDNSRebinding(t *testing.T) {
	c, _ := New(Config{MaxResponseBytes: 4096, MaxEventBytes: 2048})
	var lookups, dials atomic.Int64
	c.lookup = func(context.Context, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("192.0.2.10")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	c.dial = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	}
	_, err := c.Transport(t.Context(), submission("https://model.example.com", false), []byte(rand.Text()))
	if err == nil || lookups.Load() != 2 || dials.Load() != 0 {
		t.Fatalf("rebinding bypass: %v %d %d", err, lookups.Load(), dials.Load())
	}
}

func TestStreamingStatusRedaction(t *testing.T) {
	for _, status := range []int{400, 500, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			secret := []byte(rand.Text())
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status); _, _ = w.Write(secret) }))
			defer s.Close()
			x, err := clientFor(t).Transport(t.Context(), submission(s.URL, true), secret)
			if err != nil {
				t.Fatal(err)
			}
			_, err = modelprotocol.Stream(t.Context(), x.Body, protocolRequest(t, true), modelprotocol.Limits{MaxBytes: 4096, MaxEvents: 8, MaxEventBytes: 2048}, "local-request", func(_ context.Context, b []byte) (int, error) { return len(b), nil })
			want := "ai.upstream_unavailable"
			if status == 400 {
				want = "ai.upstream_rejected"
			}
			if status == 504 {
				want = "ai.upstream_timeout"
			}
			var e *modelprotocol.Error
			if !errors.As(err, &e) || e.Code != want || strings.Contains(err.Error(), string(secret)) {
				t.Fatalf("stream status: %v", err)
			}
		})
	}
}

func TestConnectTimeoutAndErrorRedaction(t *testing.T) {
	c, _ := New(Config{MaxResponseBytes: 4096, MaxEventBytes: 2048})
	secret := []byte(rand.Text())
	c.lookup = func(ctx context.Context, _ string) ([]netip.Addr, error) {
		<-ctx.Done()
		return nil, fmt.Errorf("%s: %w", secret, ctx.Err())
	}
	started := time.Now()
	_, err := c.Transport(t.Context(), submission("https://model.example.com", false), secret)
	if err == nil || strings.Contains(fmt.Sprintf("%+v", err), string(secret)) || time.Since(started) > 3*time.Second {
		t.Fatal("connect timeout or redaction failed")
	}
}

func TestNormalizationRejectsDuplicateAndFalseTerminal(t *testing.T) {
	for _, payload := range []string{strings.Replace(completion(), `"model":"provider-model"`, `"model":"other","model":"provider-model"`, 1), strings.Replace(completion(), `"OK"`, "\"\xff\"", 1)} {
		if _, err := normalize([]byte(payload), "public-model", "provider-model"); err == nil {
			t.Fatal("ambiguous provider JSON accepted")
		}
	}
	for _, payload := range []string{"data: [DONE]\n\n", strings.ReplaceAll(chunks(), `"finish_reason":"stop"`, `"finish_reason":null`)} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, payload) }))
		x, err := clientFor(t).Transport(t.Context(), submission(s.URL, true), []byte(rand.Text()))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, x.Body)
		_ = x.Body.Close()
		s.Close()
		select {
		case <-x.Stopped:
			t.Fatal("unvalidated terminal forged stop proof")
		default:
		}
	}
}

func TestSSELineEndings(t *testing.T) {
	for _, ending := range []string{"\r", "\r\n"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.ReplaceAll(chunks(), "\n", ending))
		}))
		x, err := clientFor(t).Transport(t.Context(), submission(s.URL, true), []byte(rand.Text()))
		b := readExchange(t, x, err)
		s.Close()
		if !strings.Contains(string(b), "[DONE]") {
			t.Fatal("valid SSE ending rejected")
		}
		select {
		case <-x.Stopped:
		default:
			t.Fatal("valid terminal not recognized")
		}
	}
}

func TestBodyProgressFollowsNetworkReads(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			release := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				prefix := "{"
				if stream {
					prefix = "data: {"
				}
				_, _ = io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer s.Close()
			defer close(release)
			x, err := clientFor(t).Transport(t.Context(), submission(s.URL, stream), []byte(rand.Text()))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = x.Body.Close() }()
			done := make(chan error, 1)
			go func() {
				var b [1]byte
				n, err := x.Body.Read(b[:])
				if err == nil && n != 1 {
					err = io.ErrNoProgress
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("normalization hid network progress from Core timeout monitor")
			}
			select {
			case <-x.Stopped:
				t.Fatal("partial response forged stop proof")
			default:
			}
		})
	}
}
