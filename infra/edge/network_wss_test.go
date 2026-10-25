package main

import (
	"crypto/sha1"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewNetworkWSSDisabled(t *testing.T) {
	n, err := newNetworkWSS(NetworkWSSConfig{Enabled: false}, "portal.example")
	if err != nil || n != nil {
		t.Fatalf("disabled: n=%v err=%v", n, err)
	}
}

func TestNewNetworkWSSRequiresUpstream(t *testing.T) {
	_, err := newNetworkWSS(NetworkWSSConfig{Enabled: true}, "portal.example")
	if err == nil {
		t.Fatal("expected upstream required")
	}
}

func TestNetworkWSSRejectsNonUpgrade(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be hit")
	}))
	t.Cleanup(up.Close)
	n, err := newNetworkWSS(NetworkWSSConfig{
		Enabled:        true,
		UpstreamURL:    up.URL,
		MaxConcurrent:  2,
		AllowedOrigins: []string{"https://portal.example"},
	}, "portal.example")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, DefaultNetworkWSSPath, nil)
	req.Header.Set("Origin", "https://portal.example")
	rr := httptest.NewRecorder()
	n.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "edge.upgrade_required") {
		t.Fatalf("body=%s", rr.Body.String())
	}
}

func TestNetworkWSSRejectsBadOrigin(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be hit")
	}))
	t.Cleanup(up.Close)
	n, err := newNetworkWSS(NetworkWSSConfig{
		Enabled:        true,
		UpstreamURL:    up.URL,
		AllowedOrigins: []string{"https://portal.example"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, DefaultNetworkWSSPath, nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	n.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNetworkWSSProxiesUpgrade(t *testing.T) {
	var sawUpgrade atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "no upgrade", 400)
			return
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("leaked auth headers: cookie=%q auth=%q", r.Header.Get("Cookie"), r.Header.Get("Authorization"))
		}
		sawUpgrade.Store(true)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		_ = rw.Flush()
	}))
	t.Cleanup(up.Close)

	n, err := newNetworkWSS(NetworkWSSConfig{
		Enabled:        true,
		UpstreamURL:    up.URL,
		AllowedOrigins: []string{"https://portal.example"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	client := up.Client()
	// Drive through NetworkWSS via a local listener that uses hijack-capable ResponseWriter.
	ln := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.ServeHTTP(w, r)
	}))
	t.Cleanup(ln.Close)

	req, err := http.NewRequest(http.MethodGet, ln.URL+DefaultNetworkWSSPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Origin", "https://portal.example")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")))
	req.Header.Set("Cookie", "auriga_session=secret")
	req.Header.Set("Authorization", "Bearer leak")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if !sawUpgrade.Load() {
		t.Fatal("upstream did not see upgrade")
	}
}

func TestNetworkWSSConcurrencyLimit(t *testing.T) {
	var inFlight atomic.Int64
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		defer inFlight.Add(-1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", 500)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		_ = rw.Flush()
		<-release
	}))
	t.Cleanup(up.Close)

	n, err := newNetworkWSS(NetworkWSSConfig{
		Enabled:        true,
		UpstreamURL:    up.URL,
		MaxConcurrent:  1,
		AllowedOrigins: []string{"https://portal.example"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewServer(http.HandlerFunc(n.ServeHTTP))
	t.Cleanup(edge.Close)

	doUpgrade := func() *http.Response {
		req, _ := http.NewRequest(http.MethodGet, edge.URL+DefaultNetworkWSSPath, nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Origin", "https://portal.example")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef")))
		resp, err := edge.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp := doUpgrade()
		if resp.StatusCode != http.StatusSwitchingProtocols {
			t.Errorf("first status=%d", resp.StatusCode)
		}
		// keep body open until release
		time.Sleep(50 * time.Millisecond)
		_ = resp.Body.Close()
	}()

	// Wait until first is in flight
	deadline := time.Now().Add(2 * time.Second)
	for inFlight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if inFlight.Load() == 0 {
		close(release)
		wg.Wait()
		t.Fatal("first upgrade never reached upstream")
	}

	resp2 := doUpgrade()
	body, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second want 503, got %d body=%s", resp2.StatusCode, body)
	}
	if !strings.Contains(string(body), "edge.overloaded") {
		t.Fatalf("body=%s", body)
	}
	close(release)
	wg.Wait()
}

func TestGatewayRoutesNetworkWSSOnPortalOnly(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		_ = rw.Flush()
	}))
	t.Cleanup(up.Close)

	cfg, sessions, routes, tickets, signer, _ := testEnv(t)
	cfg.NetworkWSS = NetworkWSSConfig{
		Enabled:        true,
		UpstreamURL:    up.URL,
		AllowedOrigins: []string{"https://" + cfg.PortalHost},
	}
	g := newTestGateway(t, cfg, sessions, routes, tickets, signer)
	h := g.Handler()

	req := httptest.NewRequest(http.MethodGet, DefaultNetworkWSSPath, nil)
	req.Host = cfg.AdminHost
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Origin", "https://"+cfg.PortalHost)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("admin host status=%d body=%s", rr.Code, rr.Body.String())
	}
}
