package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTraceRequestLogsHideCredentialPaths(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	var logs bytes.Buffer
	g.Log = newLogLogger(&logs)
	for _, path := range []string{"/api/org/v1/invitations/preview/secret-invite", "/api/iam/v1/device/secret-user-code/approve", "/api/iam/v1/device/secret-user-code/deny", "/unmatched/secret-other"} {
		g.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://portal.example.com"+path, nil))
	}
	if strings.Contains(logs.String(), "secret-") {
		t.Fatal("request log leaked credential path")
	}
}

func TestTraceMaskLongTokensBounded(t *testing.T) {
	for _, input := range []string{strings.Repeat("x", 65536), strings.Repeat("a_", 32768), strings.Repeat("a.", 32768)} {
		started := time.Now()
		Mask(input)
		if time.Since(started) > 50*time.Millisecond {
			t.Fatal("mask exceeded 50ms")
		}
	}
	if len(Mask(strings.Repeat("x", 65537))) > 65536 {
		t.Fatal("oversized log input not bounded")
	}
}

func TestTraceCrossProxyAndResponse(t *testing.T) {
	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	child := "00-4bf92f3577b34da6a3ce929d0e0e4736-1d8271a3a565c037-01"
	seen := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("traceparent")
		w.Header().Set("traceparent", child)
		w.Header().Set("X-Trace-Id", child[3:35])
		w.WriteHeader(204)
	}))
	defer backend.Close()
	cfg.Upstreams["iam"] = backend.URL
	cfg.PublicPathPrefixes = []string{"/api/iam/v1/login/"}
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	var logs bytes.Buffer
	g.Log = newLogLogger(&logs)
	req := httptest.NewRequest("GET", "http://portal.example.com/api/iam/v1/login/providers", nil)
	req.Header.Set("traceparent", parent)
	req.Header.Set("X-Request-Id", "business-id")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, req)
	if (<-seen)[3:35] != parent[3:35] || w.Header().Get("X-Trace-Id") != parent[3:35] {
		t.Fatal("trace broke at proxy/response")
	}
	if values := w.Result().Header.Values("traceparent"); len(values) != 1 || values[0] != child {
		t.Fatalf("response traceparent must be one valid same-trace child: %q", values)
	}
	if values := w.Result().Header.Values("X-Trace-Id"); len(values) != 1 || values[0] != parent[3:35] {
		t.Fatalf("response X-Trace-Id must be one correlation value: %q", values)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["trace_id"] != parent[3:35] || event["service"] != "edge" {
			t.Fatal(event)
		}
	}
}
func TestTraceInvalidAndStructuredSecrets(t *testing.T) {
	for _, v := range []string{"", strings.Repeat("x", 10000), "00-" + strings.Repeat("0", 32) + "-" + strings.Repeat("1", 16) + "-01", "ff-" + strings.Repeat("1", 32) + "-" + strings.Repeat("1", 16) + "-01"} {
		if validTraceparent(v) {
			t.Fatal("invalid accepted")
		}
	}
	t.Setenv("AURIGA_LOG_LEVEL", "DEBUG")
	var logs bytes.Buffer
	newLogLogger(&logs).Debug("account_id=alice password=secret-pass Authorization: Bearer secret-auth\n-----BEGIN PRIVATE KEY-----\nsecret-key\n-----END PRIVATE KEY-----")
	for _, secret := range []string{"secret-pass", "secret-auth", "secret-key"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("secret leaked")
		}
	}
	if !strings.Contains(logs.String(), "alice") || !strings.Contains(logs.String(), `"level":"DEBUG"`) {
		t.Fatal(logs.String())
	}
}

func traceWireRequest(t *testing.T, server *httptest.Server, request *http.Request) (*http.Response, *bufio.Reader, net.Conn) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := request.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	for {
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode >= 200 || response.StatusCode == http.StatusSwitchingProtocols {
			return response, reader, conn
		}
		_ = response.Body.Close()
	}
}

func assertSingleTraceResponse(t *testing.T, header http.Header, parent string) {
	t.Helper()
	values := header.Values("traceparent")
	if len(values) != 1 || !validTraceparent(values[0]) || values[0][3:35] != parent[3:35] {
		t.Fatalf("wire response must contain one valid same-trace parent: %q", values)
	}
	if values := header.Values("X-Trace-Id"); len(values) != 1 || values[0] != parent[3:35] {
		t.Fatalf("wire response must contain one X-Trace-Id: %q", values)
	}
}

func TestTraceProxyResponseWire(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const child = "00-4bf92f3577b34da6a3ce929d0e0e4736-1d8271a3a565c037-01"
	const businessID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	for _, test := range []struct {
		name       string
		parents    []string
		wantParent string
		status     int
		earlyHints bool
	}{
		{"missing", nil, parent, 200, false},
		{"java-child", []string{child}, child, 200, false},
		{"malformed", []string{"not-a-traceparent"}, parent, 200, false},
		{"conflicting-trace", []string{"00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-1d8271a3a565c037-01"}, parent, 200, false},
		{"multiple-spans", []string{parent, child}, parent, 200, false},
		{"duplicate-values", []string{child, child}, parent, 200, false},
		{"comma-list", []string{parent + ", " + child}, parent, 200, false},
		{"upstream-error", []string{child}, child, 503, false},
		{"early-hints", []string{child}, child, 200, true},
	} {
		for _, segments := range []int{1, 2} {
			t.Run(test.name+"/segments="+string(rune('0'+segments)), func(t *testing.T) {
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("traceparent") != parent || r.Header.Get("X-Request-Id") != businessID {
						t.Error("request trace or business request id changed")
					}
					if test.earlyHints {
						w.Header().Set("Link", "</fixture.css>; rel=preload")
						w.WriteHeader(http.StatusEarlyHints)
					}
					for _, value := range test.parents {
						w.Header().Add("traceparent", value)
					}
					w.Header().Add("X-Trace-Id", "untrusted-upstream-id")
					w.Header().Add("X-Trace-Id", parent[3:35])
					w.Header().Set("X-Request-Id", r.Header.Get("X-Request-Id"))
					w.Header().Add("X-Multi", "first")
					w.Header().Add("X-Multi", "second")
					w.Header().Add("Set-Cookie", "fixture-a=1; Path=/; HttpOnly")
					w.Header().Add("Set-Cookie", "fixture-b=2; Path=/; HttpOnly")
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(test.status)
					_, _ = io.WriteString(w, "{\"fixture\":true}\n")
				}))
				t.Cleanup(backend.Close)
				target := backend.URL
				var entry *httptest.Server
				for range segments {
					cfg, sessions, routes, tickets, signer, _ := testEnv(t)
					cfg.Upstreams["iam"] = target
					cfg.PublicPathPrefixes = []string{"/api/iam/v1/login/"}
					entry = httptest.NewServer(newTestGateway(t, cfg, sessions, routes, tickets, signer).Handler())
					t.Cleanup(entry.Close)
					target = entry.URL
				}
				request := httptest.NewRequest("GET", entry.URL+"/api/iam/v1/login/providers", nil)
				request.Host = "portal.example.com"
				request.Header.Set("traceparent", parent)
				request.Header.Set("X-Request-Id", businessID)
				response, _, _ := traceWireRequest(t, entry, request)
				assertSingleTraceResponse(t, response.Header, parent)
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || string(body) != "{\"fixture\":true}\n" || response.StatusCode != test.status || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("X-Request-Id") != businessID || len(response.Header.Values("X-Multi")) != 2 || len(response.Header.Values("Set-Cookie")) != 2 || response.Header.Get("traceparent") != test.wantParent {
					t.Fatalf("proxy changed body/status/business headers: status=%d body=%q error=%v headers=%v", response.StatusCode, body, err, response.Header)
				}
			})
		}
	}
}

func traceProxyHandler(proxy *traceReverseProxy, parent string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("traceparent", parent)
		w.Header().Set("X-Trace-Id", parent[3:35])
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyTraceparent, parent)))
	})
}

func TestTraceProxyCallbackAndStreaming(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const child = "00-4bf92f3577b34da6a3ce929d0e0e4736-1d8271a3a565c037-01"
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("traceparent", child)
		w.Header().Set("X-Trace-Id", child[3:35])
		w.Header().Set("Set-Cookie", "fixture=1")
		w.Header().Set("Trailer", "X-End")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, "first\n")
		_ = http.NewResponseController(w).Flush()
		<-release
		_, _ = io.WriteString(w, "second\n")
		w.Header().Set("X-End", "complete")
	}))
	t.Cleanup(backend.Close)
	target, _ := url.Parse(backend.URL)
	proxy := newReverseProxy(target, backend.Client().Transport, false)
	proxy.ModifyResponse = func(response *http.Response) error {
		response.Header.Del("Set-Cookie")
		response.Header.Set("X-Route-Callback", "preserved")
		return nil
	}
	entry := httptest.NewServer(traceProxyHandler(proxy, parent))
	t.Cleanup(entry.Close)
	response, _, _ := traceWireRequest(t, entry, httptest.NewRequest("GET", entry.URL+"/stream", nil))
	assertSingleTraceResponse(t, response.Header, parent)
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "first\n" {
		t.Fatalf("first stream chunk was buffered or changed: %q %v", first, err)
	}
	close(release)
	rest, err := io.ReadAll(reader)
	_ = response.Body.Close()
	if err != nil || string(rest) != "second\n" || response.StatusCode != 202 || response.Trailer.Get("X-End") != "complete" || response.Header.Get("X-Route-Callback") != "preserved" || len(response.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("stream/trailer/route callback changed: %q %v %v", rest, err, response)
	}
}

func TestTraceProxyErrorsAndLocalReject(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	t.Run("transport-error", func(t *testing.T) {
		backend := httptest.NewServer(http.NotFoundHandler())
		backend.Close()
		target, _ := url.Parse(backend.URL)
		entry := httptest.NewServer(traceProxyHandler(newReverseProxy(target, backend.Client().Transport, false), parent))
		t.Cleanup(entry.Close)
		response, _, _ := traceWireRequest(t, entry, httptest.NewRequest("GET", entry.URL+"/error", nil))
		assertSingleTraceResponse(t, response.Header, parent)
		if response.StatusCode != http.StatusBadGateway {
			t.Fatal("upstream transport failure mapping changed")
		}
		_ = response.Body.Close()
	})
	t.Run("response-callback-reject", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusEarlyHints)
			w.Header().Set("traceparent", parent)
			w.WriteHeader(200)
		}))
		t.Cleanup(backend.Close)
		target, _ := url.Parse(backend.URL)
		proxy := newReverseProxy(target, backend.Client().Transport, false)
		proxy.ModifyResponse = func(*http.Response) error { return errors.New("fixture redirect rejected") }
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("X-Error-Callback", "preserved")
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "rejected")
		}
		entry := httptest.NewServer(traceProxyHandler(proxy, parent))
		t.Cleanup(entry.Close)
		response, _, _ := traceWireRequest(t, entry, httptest.NewRequest("GET", entry.URL+"/error", nil))
		assertSingleTraceResponse(t, response.Header, parent)
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 503 || string(body) != "rejected" || response.Header.Get("X-Error-Callback") != "preserved" {
			t.Fatal("route-specific rejection/error callback changed")
		}
	})
	t.Run("local-path-reject-and-request-validation", func(t *testing.T) {
		cfg, sessions, routes, tickets, signer, _ := testEnv(t)
		g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
		entry := httptest.NewServer(g.Handler())
		t.Cleanup(entry.Close)
		request := httptest.NewRequest("GET", entry.URL+"/../denied", nil)
		request.Host = "portal.example.com"
		request.Header.Add("traceparent", parent)
		request.Header.Add("traceparent", parent)
		response, _, _ := traceWireRequest(t, entry, request)
		actual := response.Header.Get("traceparent")
		if response.StatusCode != 400 || !validTraceparent(actual) || actual == parent {
			t.Fatalf("local rejection or ambiguous request trace validation changed: %d %q", response.StatusCode, actual)
		}
		assertSingleTraceResponse(t, response.Header, actual)
		_ = response.Body.Close()
	})
}

func TestTraceProxyUpgradeWire(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const child = "00-4bf92f3577b34da6a3ce929d0e0e4736-1d8271a3a565c037-01"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, writer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = writer.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: fixture\r\ntraceparent: " + child + "\r\nX-Trace-Id: " + child[3:35] + "\r\n\r\n")
		_ = writer.Flush()
		data := make([]byte, 4)
		if _, err := io.ReadFull(writer, data); err == nil {
			_, _ = writer.Write(data)
			_ = writer.Flush()
		}
	}))
	t.Cleanup(backend.Close)
	target, _ := url.Parse(backend.URL)
	entry := httptest.NewServer(traceProxyHandler(newReverseProxy(target, backend.Client().Transport, false), parent))
	t.Cleanup(entry.Close)
	request := httptest.NewRequest("GET", entry.URL+"/upgrade", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "fixture")
	response, reader, conn := traceWireRequest(t, entry, request)
	assertSingleTraceResponse(t, response.Header, parent)
	if response.StatusCode != 101 || response.Header.Get("Upgrade") != "fixture" {
		t.Fatal("protocol upgrade changed")
	}
	_, _ = conn.Write([]byte("ping"))
	echo := make([]byte, 4)
	if _, err := io.ReadFull(reader, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("upgraded bidirectional bytes changed: %q %v", echo, err)
	}
}
