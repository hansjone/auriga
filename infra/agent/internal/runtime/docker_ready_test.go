package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

const readyBody = `{"status":"ok","ready":true,"protocol":1,"pluginVersion":"0.0.0"}`

func readyDocker(t *testing.T, endpoint string) *runtime.Docker {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Running": true},
				"NetworkSettings": map[string]any{"Ports": map[string]any{"7860/tcp": []any{map[string]string{"HostPort": port}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(engine.Close)
	client, err := runtime.DialEngine(engine.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return runtime.NewDockerWithClient(client, "")
}

func TestDockerStartWaitsForRealHealth(t *testing.T) {
	var attempts atomic.Int64
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_auriga/health" {
			t.Errorf("unexpected health path %s", r.URL.Path)
		}
		switch attempts.Add(1) {
		case 1:
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
		case 2:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"ok","ready":false,"protocol":1,"pluginVersion":"0.0.0"}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, readyBody)
		}
	}))
	t.Cleanup(health.Close)
	d := readyDocker(t, health.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.Start(ctx, "ready-test"); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() < 3 {
		t.Fatal("start succeeded before transient error and not-ready health recovered")
	}
	if endpoint, err := d.Endpoint("ready-test"); err != nil || endpoint.URL != health.URL {
		t.Fatalf("healthy endpoint not published: %+v %v", endpoint, err)
	}
}

func TestDockerStartWaitsAcrossConnectionRefused(t *testing.T) {
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	_ = reservation.Close()
	d := readyDocker(t, "http://"+address)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Start(ctx, "refused-test") }()
	select {
	case err := <-done:
		t.Fatalf("start returned while health port refused connections: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, readyBody) }), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestDockerStartFailedRetryDoesNotKeepPriorReadyEndpoint(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ready.Load() {
			_, _ = io.WriteString(w, readyBody)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(health.Close)
	d := readyDocker(t, health.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.Start(ctx, "retry-test"); err != nil {
		t.Fatal(err)
	}
	ready.Store(false)
	ctx, cancelRetry := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelRetry()
	if err := d.Start(ctx, "retry-test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry: %v", err)
	}
	if _, err := d.Endpoint("retry-test"); err == nil {
		t.Fatal("failed retry retained old ingress endpoint")
	}
	status, err := d.Inspect(context.Background(), "retry-test")
	if err != nil || status.Ready {
		t.Fatalf("failed retry still reports ready: %+v %v", status, err)
	}
}

func TestDockerStartRejectsFalseHealthUntilContextDeadline(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"status":          {http.StatusServiceUnavailable, readyBody},
		"other-success":   {http.StatusAccepted, readyBody},
		"empty":           {http.StatusOK, ""},
		"html":            {http.StatusOK, "<html>ok</html>"},
		"not-ready":       {http.StatusOK, `{"status":"ok","ready":false,"protocol":1,"pluginVersion":"x"}`},
		"bad-status":      {http.StatusOK, `{"status":"error","ready":true,"protocol":1,"pluginVersion":"x"}`},
		"protocol":        {http.StatusOK, `{"status":"ok","ready":true,"protocol":2,"pluginVersion":"x"}`},
		"missing-version": {http.StatusOK, `{"status":"ok","ready":true,"protocol":1}`},
		"oversized":       {http.StatusOK, `{"status":"ok","ready":true,"protocol":1,"pluginVersion":"` + strings.Repeat("x", 32*1024) + `"}`},
		"trailing":        {http.StatusOK, readyBody + `{}`},
		"extra":           {http.StatusOK, strings.TrimSuffix(readyBody, "}") + `,"other":true}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(health.Close)
			d := readyDocker(t, health.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := d.Start(ctx, "unready-test"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("false health accepted or deadline lost: %v", err)
			}
			if _, err := d.Endpoint("unready-test"); err == nil {
				t.Fatal("failed start published endpoint")
			}
		})
	}
}

func TestDockerStartHealthRedirectAndCancellation(t *testing.T) {
	var redirected atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		_, _ = io.WriteString(w, readyBody)
	}))
	t.Cleanup(other.Close)
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	t.Cleanup(health.Close)
	d := readyDocker(t, health.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := d.Start(ctx, "redirect-test"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("redirect: %v", err)
	}
	if redirected.Load() != 0 {
		t.Fatal("health followed redirect")
	}

	entered := make(chan struct{}, 1)
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	t.Cleanup(blocked.Close)
	d = readyDocker(t, blocked.URL)
	ctx, cancelProbe := context.WithCancel(context.Background())
	defer cancelProbe()
	done := make(chan error, 1)
	go func() { done <- d.Start(ctx, "cancel-test") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("health request not made")
	}
	cancelProbe()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("start ignored cancellation")
	}
}
