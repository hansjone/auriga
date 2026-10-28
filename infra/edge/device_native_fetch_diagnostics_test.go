package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Observe only the native-fetch fixture's network-submit hop. Error types and
// booleans identify the failed stage without logging credentials or peer data.
type nativeFetchTraceTransport struct {
	base http.RoundTripper
	logf func(string, ...any)
}

func (tr nativeFetchTraceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	started := time.Now()
	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) { tr.logf("native-fetch hop connect-start elapsed=%s", time.Since(started)) },
		ConnectDone: func(_, _ string, err error) {
			tr.logf("native-fetch hop connect-done elapsed=%s error-type=%T", time.Since(started), err)
		},
		TLSHandshakeStart: func() { tr.logf("native-fetch hop tls-start elapsed=%s", time.Since(started)) },
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			tr.logf("native-fetch hop tls-done elapsed=%s verified-chains=%d error-type=%T", time.Since(started), len(state.VerifiedChains), err)
		},
		GotConn: func(info httptrace.GotConnInfo) {
			tr.logf("native-fetch hop got-connection elapsed=%s reused=%v", time.Since(started), info.Reused)
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			tr.logf("native-fetch hop request-written elapsed=%s error-type=%T", time.Since(started), info.Err)
		},
		GotFirstResponseByte: func() { tr.logf("native-fetch hop response-first-byte elapsed=%s", time.Since(started)) },
	}
	response, err := tr.base.RoundTrip(r.Clone(httptrace.WithClientTrace(r.Context(), trace)))
	tr.logf("native-fetch hop roundtrip-done elapsed=%s response=%v error-type=%T", time.Since(started), response != nil, err)
	return response, err
}

func TestDeviceNativeNodeFetchHopDiagnostics(t *testing.T) {
	for _, wrongCA := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrong-ca=%v", wrongCA), func(t *testing.T) {
			var calls atomic.Int32
			server, hop := networkSubmitTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(202) }))
			if wrongCA {
				hop.CAFile = createAccessPKI(t).hop.CAFile
			}
			target, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			base, err := newIAMHopTransport(hop, target)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(base.CloseIdleConnections)
			var log strings.Builder
			var logMu sync.Mutex
			tr := nativeFetchTraceTransport{base, func(format string, args ...any) {
				logMu.Lock()
				defer logMu.Unlock()
				_, _ = fmt.Fprintf(&log, format+"\n", args...)
			}}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer must-not-be-logged")
			response, err := tr.RoundTrip(req)
			if response != nil {
				_ = response.Body.Close()
			}
			if wrongCA != (err != nil) || wrongCA && calls.Load() != 0 || !wrongCA && calls.Load() != 1 {
				t.Fatalf("wrong-ca=%v err=%v calls=%d", wrongCA, err, calls.Load())
			}
			logMu.Lock()
			defer logMu.Unlock()
			for _, stage := range []string{"connect-start", "connect-done", "tls-start", "tls-done", "roundtrip-done"} {
				if !strings.Contains(log.String(), stage) {
					t.Errorf("missing %s: %s", stage, &log)
				}
			}
			if wrongCA && !strings.Contains(log.String(), "*tls.CertificateVerificationError") {
				t.Errorf("missing TLS error type: %s", &log)
			}
			for _, forbidden := range []string{"must-not-be-logged", server.URL, "BEGIN CERTIFICATE"} {
				if strings.Contains(log.String(), forbidden) {
					t.Error("hop diagnostics disclosed request or certificate data")
				}
			}
		})
	}
}

// Cancel when the child has reported a precise stage, rather than sleeping or
// relying on a short wall-clock deadline on a loaded Windows runner.
type nativeFetchCancelCapture struct {
	mu     sync.Mutex
	data   bytes.Buffer
	marker string
	cancel context.CancelFunc
}

func (capture *nativeFetchCancelCapture) Write(p []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	n, err := capture.data.Write(p)
	if strings.Contains(capture.data.String(), capture.marker) {
		capture.cancel()
	}
	return n, err
}

func TestDeviceNativeNodeFetchInterruptedDiagnostics(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"request-sent", "response-headers"} {
		t.Run(phase, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "response-headers" {
					w.Header().Set("Content-Length", "1000")
					w.WriteHeader(202)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			capture := &nativeFetchCancelCapture{marker: `"stage":"` + phase + `"`, cancel: cancel}
			cmd := exec.CommandContext(ctx, node, "testdata/device_native_fetch.mjs", server.URL)
			cmd.Stdout, cmd.Stderr = capture, capture
			err := cmd.Run()
			log := capture.data.String()
			if err == nil || ctx.Err() != context.Canceled || !strings.Contains(log, capture.marker) {
				t.Fatalf("interrupt missing: process=%v context=%v\n%s", err, ctx.Err(), log)
			}
			if strings.Contains(log, `"stage":"response-body"`) || strings.Contains(log, `"stage":"complete"`) {
				t.Fatalf("stalled response marked complete: %s", log)
			}
		})
	}
}

func TestDeviceNativeNodeFetchDiagnostics(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"success", "connect", "tls", "body", "assertion"} {
		t.Run(failure, func(t *testing.T) {
			const secret = "response-secret-must-not-be-logged"
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Set-Cookie", secret)
				if failure == "body" {
					w.Header().Set("Content-Length", "1000")
					w.WriteHeader(202)
					_, _ = w.Write([]byte(secret))
					return
				}
				if failure == "assertion" {
					w.WriteHeader(500)
					_, _ = w.Write([]byte(secret))
					return
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
					w.WriteHeader(403)
					_, _ = w.Write([]byte(`{"code":"common.forbidden"}`))
					return
				}
				w.WriteHeader(202)
			})
			server := httptest.NewServer(handler)
			if failure == "tls" {
				server.Close()
				server = httptest.NewTLSServer(handler)
			}
			t.Cleanup(server.Close)
			if failure == "connect" {
				server.Close()
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "testdata/device_native_fetch.mjs", server.URL)
			output, runErr := cmd.CombinedOutput()
			log := string(output)
			if (failure == "success") != (runErr == nil) {
				t.Fatalf("result=%v\n%s", runErr, log)
			}
			required := []string{`"stage":"startup"`, `"stage":"fetch-start"`, `"stage":"connect-start"`, `"stage":"exit"`}
			switch failure {
			case "success":
				required = append(required, `"stage":"connected"`, `"stage":"response-body"`, `"stage":"complete"`, `"stage":"before-exit"`, "native-fetch=4 rejected=8")
			case "connect", "tls":
				required = append(required, `"stage":"connect-error"`, `"stage":"failure"`)
			case "body":
				required = append(required, `"stage":"response-headers"`, `"stage":"request-error"`, `"stage":"failure"`)
			case "assertion":
				required = append(required, `"stage":"response-body"`, `"stage":"failure"`, `"name":"AssertionError"`, `"code":"ERR_ASSERTION"`)
			}
			for _, marker := range required {
				if !strings.Contains(log, marker) {
					t.Errorf("missing %s\n%s", marker, log)
				}
			}
			for _, forbidden := range []string{secret, "Bearer ai-device", "auriga_admin_session=browser", "BEGIN CERTIFICATE", server.URL} {
				if strings.Contains(log, forbidden) {
					t.Error("diagnostics disclosed response/credential/address data")
				}
			}
			if failure == "body" && strings.Contains(log, `"stage":"response-body"`) {
				t.Error("incomplete body was marked complete")
			}
			if failure != "success" && strings.Contains(log, `"stage":"complete"`) {
				t.Error("failure was marked complete")
			}
		})
	}
}
