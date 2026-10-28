package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaysupervisor"
	"github.com/jackc/pgx/v5"
)

func startDevice(t *testing.T, f *deviceFixture) (*deviceRuntime, string) {
	t.Helper()
	r, e := newDeviceRuntime(t.Context(), f.cfg)
	if e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("tcp", f.cfg.Listen)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- r.serve(ctx, l, f.cfg.MaxConnections) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(4 * time.Second):
			t.Error("runtime did not stop")
		}
	})
	return r, "http://" + l.Addr().String()
}
func deviceRequest(t *testing.T, base, raw, method, path, body, key string) (int, string) {
	t.Helper()
	req, e := http.NewRequest(method, base+path, strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Authorization", "Bearer "+raw)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	r, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = r.Body.Close() }()
	b, e := io.ReadAll(r.Body)
	if e != nil {
		t.Fatal(e)
	}
	return r.StatusCode, string(b)
}
func syntheticBody(stream bool) string {
	return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"full input"}],"max_tokens":2,"stream":%t}`, syntheticModel, stream)
}
func TestDeviceRuntimeSocketRPC(t *testing.T) {
	f := newDeviceFixture(t)
	r, base := startDevice(t, f)
	if r.server.WriteTimeout != 0 || r.server.Handler != r.handler {
		t.Fatal("SSE deadline or raw URI mounting violated")
	}
	if f.cfg.IAM.Target == f.cfg.Org.Target {
		t.Fatal("fixture must exercise distinct RPC destinations")
	}
	code, b := deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", syntheticBody(false), "one")
	if code != 200 || !strings.Contains(b, `"prompt_tokens":32`) {
		t.Fatalf("JSON: %d %s", code, b)
	}
	code, b = deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", syntheticBody(false), "one")
	if code != 409 || !strings.Contains(b, "ai.replay_unavailable") {
		t.Fatalf("replay: %d %s", code, b)
	}
	code, b = deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", syntheticBody(true), "two")
	if code != 200 || !strings.Contains(b, "data: [DONE]") || !strings.Contains(b, `"completion_tokens":2`) {
		t.Fatalf("SSE: %d %s", code, b)
	}
	for _, path := range []string{"/ai/v1/models", "/api/ai/v1/me/usage"} {
		code, b = deviceRequest(t, base, f.control.raw, "GET", path, "", "")
		if code != 200 {
			t.Fatalf("query %s: %d %s", path, code, b)
		}
	}
	var usage, outbox, requests int
	if e := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1),(SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1),(SELECT count(*) FROM ai.budget_request WHERE tenant_id=$1)`, f.control.tenant).Scan(&usage, &outbox, &requests)
	}); e != nil {
		t.Fatal(e)
	}
	if usage != 2 || outbox != 2 || requests != 2 {
		t.Fatalf("duplicate execution/accounting: %d %d %d", usage, outbox, requests)
	}
	if f.control.calls.Load() < 2 {
		t.Fatal("no actual DeviceAccess RPC")
	}
}
func TestDeviceRuntimeAuthorityAndSyntheticFence(t *testing.T) {
	f := newDeviceFixture(t)
	_, base := startDevice(t, f)
	for _, tc := range []struct {
		name string
		set  func(bool)
		want int
	}{
		{"revoked", f.control.revoked.Store, 401}, {"expired", f.control.expired.Store, 401}, {"org failed", f.control.orgFailed.Store, 503}, {"wrong tenant", f.control.wrongTenant.Store, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.set(true)
			code, b := deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", syntheticBody(false), "")
			tc.set(false)
			if code != tc.want {
				t.Fatalf("authority: %d %s", code, b)
			}
		})
	}
	code, _ := deviceRequest(t, base, f.control.raw+"wrong-tenant", "GET", "/ai/v1/models", "", "")
	if code != 401 {
		t.Fatal("unverified identity accepted", code)
	}
	code, _ = deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", strings.ReplaceAll(syntheticBody(false), syntheticModel, "real-model"), "")
	if code != 403 {
		t.Fatal("real model accepted", code)
	}
	if e := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `INSERT INTO ai.model(tenant_id,id,provider_id,document,grants,revision) SELECT tenant_id,'configured-real',provider_id,jsonb_set(document,'{id}','"configured-real"'),grants,revision FROM ai.model WHERE tenant_id=$1 AND id=$2`, f.control.tenant, syntheticModel)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	code, b := deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", strings.ReplaceAll(syntheticBody(false), syntheticModel, "configured-real"), "")
	if code != 503 || !strings.Contains(b, "ai.budget_unavailable") {
		t.Fatal("configured real model received synthetic policy", code, b)
	}
	if e := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `UPDATE ai.model SET document=jsonb_set(document,'{price,inputMicrosPerMillionTokens}','0') WHERE tenant_id=$1 AND id=$2`, f.control.tenant, syntheticModel)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	code, b = deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", syntheticBody(false), "")
	if code != 503 || !strings.Contains(b, "ai.budget_unavailable") {
		t.Fatal("arbitrary synthetic price accepted", code, b)
	}
	if e := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `UPDATE ai.model SET document=jsonb_set(document,'{price,inputMicrosPerMillionTokens}','1000000') WHERE tenant_id=$1 AND id=$2`, f.control.tenant, syntheticModel)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	service, e := os.ReadFile(f.cfg.IAM.ServiceTokenFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(f.cfg.IAM.ServiceTokenFile, []byte("rotated-fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	code, _ = deviceRequest(t, base, f.control.raw, "GET", "/ai/v1/models", "", "")
	if code != 403 {
		t.Fatal("RPC reused stale service JWT", code)
	}
	if e = os.WriteFile(f.cfg.IAM.ServiceTokenFile, service, 0600); e != nil {
		t.Fatal(e)
	}
	// A socket trap is a concrete potential upstream. Pinning rejects the changed
	// target before dispatch; no model socket connection may reach it.
	trap, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = trap.Close() }()
	if e = f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		_, e := tx.Exec(t.Context(), `UPDATE ai.provider SET document=jsonb_set(document,'{baseUrl}',to_jsonb($2::text)) WHERE tenant_id=$1`, f.control.tenant, "http://"+trap.Addr().String())
		return e
	}); e != nil {
		t.Fatal(e)
	}
	code, _ = deviceRequest(t, base, f.control.raw, "POST", "/ai/v1/chat/completions", syntheticBody(false), "")
	if code == 200 {
		t.Fatal("changed real target accepted")
	}
	if e = trap.(*net.TCPListener).SetDeadline(time.Now().Add(50 * time.Millisecond)); e != nil {
		t.Fatal(e)
	}
	if conn, e := trap.Accept(); e == nil {
		_ = conn.Close()
		t.Fatal("model network connection occurred")
	}
}
func TestDeviceRuntimeRawSocketAndLimits(t *testing.T) {
	f := newDeviceFixture(t)
	f.cfg.HeaderMillis = 50
	f.cfg.ShutdownMillis = 1000
	f.cfg.ReadMillis = 80
	f.cfg.MaxConnections = 2
	f.cfg.MaxInFlight = 1
	_, base := startDevice(t, f)
	addr := strings.TrimPrefix(base, "http://")
	for _, line := range []string{"GET /ai/v1/%6dodels", "GET /ai/v1/./models", "HEAD /ai/v1/models"} {
		c, e := net.DialTimeout("tcp", addr, time.Second)
		if e != nil {
			t.Fatal(e)
		}
		_ = c.SetDeadline(time.Now().Add(time.Second))
		_, e = fmt.Fprintf(c, "%s HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer %s\r\nConnection: close\r\n\r\n", line, f.control.raw)
		if e != nil {
			t.Fatal(e)
		}
		r, e := http.ReadResponse(bufio.NewReader(c), nil)
		if e != nil {
			t.Fatal(e)
		}
		_ = r.Body.Close()
		_ = c.Close()
		if r.StatusCode != 404 {
			t.Fatal("raw request rewritten", line, r.StatusCode)
		}
	}
	slow, e := net.DialTimeout("tcp", addr, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = slow.Close() }()
	_ = slow.SetDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(slow, "GET /ai/v1/models HTTP/1.1\r\nHost:")
	if _, e = io.ReadAll(slow); e != nil {
		if n, ok := e.(net.Error); ok && n.Timeout() {
			t.Fatal("slow header not bounded")
		}
	}
	body, e := net.DialTimeout("tcp", addr, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = body.Close() }()
	_ = body.SetDeadline(time.Now().Add(time.Second))
	_, _ = fmt.Fprintf(body, "POST /ai/v1/chat/completions HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: 99\r\n\r\n{", f.control.raw)
	// Header/body parser occupies the only in-flight slot before any DB access.
	response, e := http.ReadResponse(bufio.NewReader(body), nil)
	if e != nil {
		t.Fatal(e)
	}
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("slow body not rejected")
	}
}
func TestDeviceListenerConnectionLimit(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = l.Close() }()
	bounded := &deviceListener{Listener: l, slots: make(chan struct{}, 1)}
	first, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = first.Close() }()
	accepted, e := bounded.Accept()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = accepted.Close() }()
	done := make(chan error, 1)
	go func() { _, e := bounded.Accept(); done <- e }()
	second, e := net.Dial("tcp", l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = second.Close() }()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	if _, e = second.Read(make([]byte, 1)); e == nil {
		t.Fatal("extra connection retained")
	} else if n, ok := e.(net.Error); ok && n.Timeout() {
		t.Fatal("extra connection was not closed")
	}
	_ = l.Close()
	if e := <-done; e == nil {
		t.Fatal("closed listener accepted a connection")
	}
}
func TestDeviceShutdownRetainsUnhandedReservation(t *testing.T) {
	f := newDeviceFixture(t)
	f.cfg.ShutdownMillis = 20
	r, e := newDeviceRuntime(t.Context(), f.cfg)
	if e != nil {
		t.Fatal(e)
	}
	finish, ok := r.supervisor.Reserve()
	defer r.closeResources()
	if !ok {
		t.Fatal("reserve")
	}
	if e = r.shutdownRuntime(); e == nil || !strings.Contains(e.Error(), "occupied=1") {
		t.Fatal("false clean shutdown", e)
	}
	finish(&relaysupervisor.Pending{Faulted: true})
	if r.supervisor.Stats().Occupied != 1 || r.supervisor.Stats().Pending != 1 {
		t.Fatal("late handoff lost after timeout")
	}
	if _, ok = r.supervisor.Reserve(); ok {
		t.Fatal("shutdown admission open")
	}
}

func TestDeviceShutdownLetsActiveHTTPFinishGracefully(t *testing.T) {
	f := newDeviceFixture(t)
	f.cfg.ShutdownMillis = 1000
	r, e := newDeviceRuntime(t.Context(), f.cfg)
	if e != nil {
		t.Fatal(e)
	}
	// Exercise the runtime's HTTP lifetime independently of the fixture's 100ms
	// relay read/write deadlines and the catalog RPC/query deadlines. Those can
	// expire while the old memberHook waits for the shutdown goroutine to run.
	entered := make(chan context.Context, 1)
	release, shuttingDown := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	r.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entered <- req.Context()
		select {
		case <-release:
		case <-req.Context().Done():
		}
		if req.Context().Err() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	r.server.RegisterOnShutdown(func() { close(shuttingDown) })
	t.Cleanup(func() {
		unblock()
		r.cancel()
		_ = r.server.Close()
		_ = r.supervisor.Drain(context.Background())
		r.closeResources()
	})
	l, e := net.Listen("tcp", f.cfg.Listen)
	if e != nil {
		t.Fatal(e)
	}
	served := make(chan error, 1)
	go func() { served <- r.server.Serve(l) }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)
	status := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("GET", "http://"+l.Addr().String()+"/ai/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+f.control.raw)
		res, e := client.Do(req)
		if e != nil {
			status <- 0
			return
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		status <- res.StatusCode
	}()
	var active context.Context
	select {
	case active = <-entered:
	case <-time.After(time.Second):
		t.Fatal("no active HTTP handler")
	}
	done := make(chan error, 1)
	go func() { done <- r.shutdownRuntime() }()
	select {
	case <-shuttingDown:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not start")
	}
	if !r.supervisor.Stats().Closed {
		t.Fatal("shutdown left recovery admission open")
	}
	// Inspect the actual request context: a cancellation-notification goroutine
	// might not have run yet, even when the context is already cancelled.
	if active.Err() != nil {
		t.Fatal("HTTP cancelled before its grace period", active.Err())
	}
	select {
	case e := <-done:
		t.Fatal("shutdown returned before active HTTP completed", e)
	default:
	}
	unblock()
	if code := <-status; code != 200 {
		t.Fatal("active HTTP lost graceful completion", code)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if e = <-served; !errors.Is(e, http.ErrServerClosed) {
		t.Fatal(e)
	}
}

func TestDeviceShutdownLetsActiveCatalogFinishGracefully(t *testing.T) {
	f := newDeviceFixture(t)
	f.cfg.ShutdownMillis = 1000
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	f.control.memberHook = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r, err := newDeviceRuntime(t.Context(), f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The member RPC proves the real handler has admitted this request. Release
	// it directly from the HTTP shutdown notification, without making its relay
	// and query deadlines wait for a polling loop or a second test-driver turn.
	r.server.RegisterOnShutdown(unblock)
	t.Cleanup(func() {
		unblock()
		r.cancel()
		_ = r.server.Close()
		_ = r.supervisor.Drain(context.Background())
		r.closeResources()
	})
	l, err := net.Listen("tcp", f.cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- r.server.Serve(l) }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(client.CloseIdleConnections)
	req, err := http.NewRequest("GET", "http://"+l.Addr().String()+"/ai/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.control.raw)
	type response struct {
		status int
		body   string
		err    error
	}
	completed := make(chan response, 1)
	go func() {
		res, err := client.Do(req)
		if err != nil {
			completed <- response{err: err}
			return
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(res.Body)
		completed <- response{status: res.StatusCode, body: string(body), err: err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("real handler did not enter member RPC")
	}
	done := make(chan error, 1)
	go func() { done <- r.shutdownRuntime() }()
	result := <-completed
	if result.err != nil || result.status != http.StatusOK || !strings.Contains(result.body, fmt.Sprintf(`"id":%q`, syntheticModel)) {
		t.Fatalf("active catalog lost graceful completion: status=%d body=%s err=%v", result.status, result.body, result.err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

func TestDeviceShutdownTimeoutDoesNotWaitForBorrowedPoolConnection(t *testing.T) {
	f := newDeviceFixture(t)
	f.cfg.ShutdownMillis = 20
	r, e := newDeviceRuntime(t.Context(), f.cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer r.closeResources()
	borrowed, release := make(chan struct{}), make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- f.db.Transaction(t.Context(), f.control.tenant, func(pgx.Tx) error { close(borrowed); <-release; return nil })
	}()
	<-borrowed
	original := r.closeResources
	var closeCalled atomic.Bool
	r.closeResources = func() { closeCalled.Store(true); f.db.Close(); original() }
	finish, _ := r.supervisor.Reserve()
	done := make(chan error, 1)
	go func() { done <- r.shutdownRuntime() }()
	select {
	case e = <-done:
		if e == nil {
			t.Error("unresolved drain reported clean")
		}
	case <-time.After(250 * time.Millisecond):
		t.Error("shutdown waited for pool.Close")
	}
	if closeCalled.Load() {
		t.Error("timed-out shutdown closed borrowed pool")
	}
	close(release)
	if e := <-txDone; e != nil {
		t.Fatal(e)
	}
	finish(nil)
}
