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

	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

const readyBody = `{"status":"ok","ready":true,"protocol":1,"pluginVersion":"0.0.0"}`

func readyDocker(t *testing.T, endpoint string, beforeInspectResponse ...func()) *runtime.Docker {
	t.Helper()
	return readyDockerWithPortMetadata(t, endpoint, map[string]any{"7860/tcp": []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": "0"}}}, "7860/tcp", beforeInspectResponse...)
}

func readyDockerWithPortMetadata(t *testing.T, endpoint string, bindings any, publishedKey string, beforeInspectResponse ...func()) *runtime.Docker {
	t.Helper()
	var fixtureInstance atomic.Value
	fixtureInstance.Store("")
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
			for _, notify := range beforeInspectResponse {
				notify()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Running": true},
				"Id": fixtureContainerID, "Image": fixtureConfigID, "Config": map[string]any{"Labels": map[string]string{"auriga.managed": "true", "auriga.instance": fixtureInstance.Load().(string), "auriga.generation": "7"}, "ExposedPorts": map[string]any{"7860/tcp": map[string]any{}, "8888/tcp": map[string]any{}}},
				"HostConfig":      map[string]any{"PortBindings": bindings},
				"NetworkSettings": map[string]any{"Ports": map[string]any{publishedKey: []any{map[string]string{"HostPort": port}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(engine.Close)
	client, err := runtime.DialEngine(engine.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d := runtime.NewDockerWithClient(client, "")
	d.Policy.AllowAnyImage = true
	readyInstances.Store(d, &fixtureInstance)
	return d
}

func TestDockerStartUsesActualUnambiguousContainerPort(t *testing.T) {
	for _, key := range []string{"7860/tcp", "8888/tcp"} {
		t.Run(key, func(t *testing.T) {
			var probes atomic.Int64
			health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/_auriga/health" {
					t.Errorf("unexpected health path %s", r.URL.Path)
				}
				probes.Add(1)
				_, _ = io.WriteString(w, readyBody)
			}))
			t.Cleanup(health.Close)
			d := readyDockerWithPortMetadata(t, health.URL, map[string]any{key: []any{map[string]string{"HostIp": "127.0.0.1", "HostPort": "0"}}}, key)
			// This checks actual port selection, not a one-second installation
			// SLA: startDocker also creates trust material and durably seeds state.
			ctx := t.Context()
			if deadline, ok := t.Deadline(); ok {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, deadline.Add(-time.Second))
				defer cancel()
			}
			if err := startDocker(t, d, ctx, "actual-port-test"); err != nil {
				t.Fatal(err)
			}
			if probes.Load() == 0 {
				t.Fatal("actual container port never reached real health")
			}
			if endpoint, err := d.Endpoint("actual-port-test"); err != nil || endpoint.URL != health.URL {
				t.Fatalf("actual container port not recovered: %+v %v", endpoint, err)
			}
		})
	}
}

func TestDockerStartRejectsMissingAmbiguousOrInvalidContainerPort(t *testing.T) {
	for name, bindings := range map[string]any{
		"missing":           nil,
		"empty":             map[string]any{},
		"multiple":          map[string]any{"7860/tcp": []any{map[string]string{"HostPort": "0"}}, "8888/tcp": []any{map[string]string{"HostPort": "0"}}},
		"zero":              map[string]any{"0/tcp": []any{map[string]string{"HostPort": "0"}}},
		"overflow":          map[string]any{"65536/tcp": []any{map[string]string{"HostPort": "0"}}},
		"negative":          map[string]any{"-1/tcp": []any{map[string]string{"HostPort": "0"}}},
		"path":              map[string]any{"7860/tcp/../8888": []any{map[string]string{"HostPort": "0"}}},
		"noncanonical":      map[string]any{"07860/tcp": []any{map[string]string{"HostPort": "0"}}},
		"empty-binding":     map[string]any{"7860/tcp": []any{}},
		"multiple-bindings": map[string]any{"7860/tcp": []any{map[string]string{"HostPort": "0"}, map[string]string{"HostPort": "0"}}},
		"invalid-binding":   map[string]any{"7860/tcp": []any{"invalid"}},
		"no-tcp":            map[string]any{"7860/udp": []any{map[string]string{"HostPort": "0"}}},
		"wrong-type":        "7860/tcp",
	} {
		t.Run(name, func(t *testing.T) {
			var probes atomic.Int64
			health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { probes.Add(1); _, _ = io.WriteString(w, readyBody) }))
			t.Cleanup(health.Close)
			d := readyDockerWithPortMetadata(t, health.URL, bindings, "7860/tcp")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := startDocker(t, d, ctx, "invalid-port-test"); err == nil {
				t.Fatal("invalid container port metadata accepted")
			}
			if _, err := d.Endpoint("invalid-port-test"); err == nil || probes.Load() != 0 {
				t.Fatal("invalid port metadata reached health or published endpoint")
			}
		})
	}
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
	if err := startDocker(t, d, ctx, "ready-test"); err != nil {
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
	go func() { done <- startDocker(t, d, ctx, "refused-test") }()
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
	// Establish the healthy baseline independently of the failed retry's
	// production deadline below; lazy trust/state setup is part of startDocker.
	ctx := t.Context()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-time.Second))
		defer cancel()
	}
	if err := startDocker(t, d, ctx, "retry-test"); err != nil {
		t.Fatal(err)
	}
	ready.Store(false)
	ctx, cancelRetry := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelRetry()
	if err := startDocker(t, d, ctx, "retry-test"); !errors.Is(err, context.DeadlineExceeded) {
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
			if err := startDocker(t, d, ctx, "unready-test"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("false health accepted or deadline lost: %v", err)
			}
			if _, err := d.Endpoint("unready-test"); err == nil {
				t.Fatal("failed start published endpoint")
			}
		})
	}
}

func TestDockerStartHealthRedirectAndCancellation(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		var probes atomic.Int64
		var redirected atomic.Int64
		processedRedirect := make(chan struct{}, 1)
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			redirected.Add(1)
			_, _ = io.WriteString(w, readyBody)
		}))
		t.Cleanup(other.Close)
		health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/_auriga/health" {
				t.Errorf("unexpected health path %s", r.URL.Path)
			}
			// A second request proves Start consumed the first 302 and kept
			// waiting for health, independently of activation/startup duration.
			if probes.Add(1) == 2 {
				processedRedirect <- struct{}{}
			}
			http.Redirect(w, r, other.URL, http.StatusFound)
		}))
		t.Cleanup(health.Close)
		d := readyDocker(t, health.URL)
		// No installation/startup SLA: strict 100ms HTTP deadline coverage is
		// exercised directly through production ProbeHealth below.
		seedDockerRecord(t, d, "redirect-test", 7)
		ctx, cancel := context.WithCancel(t.Context())
		const startupGuard = 30 * time.Second
		const completionGuard = 5 * time.Second
		guard, stopGuard := context.WithTimeout(t.Context(), startupGuard)
		defer stopGuard()
		done := make(chan error, 1)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			done <- startDocker(t, d, ctx, "redirect-test")
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-finished:
			case <-time.After(completionGuard):
				t.Error("redirect Start did not stop during cleanup")
			}
		})
		select {
		case <-processedRedirect:
		case err := <-done:
			t.Fatalf("Start returned before rejecting redirect and probing again: %v", err)
		case <-guard.Done():
			t.Fatal("Start did not reach repeated original health within startup guard")
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("redirect cancellation lost: %v", err)
			}
		case <-time.After(completionGuard):
			t.Fatal("redirect Start ignored cancellation")
		}
		if redirected.Load() != 0 {
			t.Fatal("health followed redirect")
		}
		if probes.Load() < 2 {
			t.Fatal("original redirect was not processed before cancellation")
		}
		if _, err := d.Endpoint("redirect-test"); err == nil {
			t.Fatal("redirect published endpoint")
		}
		status, err := d.Inspect(t.Context(), "redirect-test")
		if err != nil || !status.Running || status.Ready {
			t.Fatalf("redirect must remain running without Ready: %+v %v", status, err)
		}
	})

	t.Run("observer-health-phase", func(t *testing.T) {
		var probes, redirected atomic.Int64
		bodyEntered := make(chan struct{}, 1)
		bodyStopped := make(chan error, 1)
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			redirected.Add(1)
			_, _ = io.WriteString(w, readyBody)
		}))
		t.Cleanup(other.Close)
		health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/_auriga/health" {
				t.Errorf("unexpected health path %s", r.URL.Path)
			}
			if probes.Add(1) == 1 {
				http.Redirect(w, r, other.URL, http.StatusFound)
				return
			}
			_, _ = io.WriteString(w, `{"status":"ok",`)
			w.(http.Flusher).Flush()
			select {
			case bodyEntered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			select {
			case bodyStopped <- r.Context().Err():
			default:
			}
		}))
		t.Cleanup(health.Close)
		t.Cleanup(health.CloseClientConnections)
		base, err := url.Parse(health.URL)
		if err != nil {
			t.Fatal(err)
		}
		target := observer.InstanceTarget{BaseURL: base, Process: observer.ProcessRunning}
		// The real 100ms deadline covers only production HTTP probing: first
		// reject an actual 302, then cancel a blocked body on the same deadline.
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		deadline, _ := ctx.Deadline()
		redirect := observer.ProbeHealth(ctx, nil, target)
		if redirect.Ready || redirect.HTTPOK || !redirect.ProcessOK || redirect.Err != "health status 302" {
			t.Fatalf("production health did not reject actual redirect: %+v", redirect)
		}
		blocked := observer.ProbeHealth(ctx, nil, target)
		// A canceled caller context alone is insufficient evidence: the HTTP
		// client's 3s fallback must not be what finally releases this body.
		if !time.Now().Before(deadline.Add(time.Second)) {
			t.Fatal("100ms health deadline did not stop body before HTTP fallback")
		}
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) || blocked.Ready || blocked.HTTPOK || !blocked.ProcessOK || blocked.Err != "invalid health response" {
			t.Fatalf("health body did not stop on 100ms deadline: %+v %v", blocked, ctx.Err())
		}
		select {
		case <-bodyEntered:
		default:
			t.Fatal("deadline expired before actual health body; phase was not tested")
		}
		select {
		case err := <-bodyStopped:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("deadline did not close HTTP request: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP body remained active after health deadline")
		}
		if probes.Load() != 2 || redirected.Load() != 0 {
			t.Fatalf("health phase requests: original=%d redirected=%d", probes.Load(), redirected.Load())
		}
	})

	t.Run("cancel", func(t *testing.T) {
		type completion struct {
			err error
			at  time.Time
		}
		entered := make(chan struct{}, 1)
		stopped := make(chan completion, 1)
		blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/_auriga/health" {
				t.Errorf("unexpected health path %s", r.URL.Path)
			}
			// Flush incomplete JSON so cancellation must interrupt a body read.
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"status":"ok",`)
			w.(http.Flusher).Flush()
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			select {
			case stopped <- completion{err: r.Context().Err(), at: time.Now()}:
			default:
			}
		}))
		t.Cleanup(blocked.Close)
		var lastInspectResponse atomic.Value
		d := readyDocker(t, blocked.URL, func() { lastInspectResponse.Store(time.Now()) })
		seedDockerRecord(t, d, "cancel-test", 7)
		t.Cleanup(blocked.CloseClientConnections)
		ctx, cancelProbe := context.WithCancel(t.Context())
		// This guard only bounds test scheduling. The cancellation under test is
		// explicit and occurs after the handler flushes the blocked response body.
		const startupGuard = 30 * time.Second
		const cleanupGuard = 5 * time.Second
		guard, stopGuard := context.WithTimeout(t.Context(), startupGuard)
		defer stopGuard()
		done := make(chan completion, 1)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			err := startDocker(t, d, ctx, "cancel-test")
			done <- completion{err: err, at: time.Now()}
		}()
		t.Cleanup(func() {
			cancelProbe()
			select {
			case <-finished:
			case <-time.After(cleanupGuard):
				t.Error("Start did not stop during cleanup")
			}
		})
		select {
		case <-entered:
		case result := <-done:
			t.Fatalf("Start returned before blocked health body: %v", result.err)
		case <-guard.Done():
			t.Fatal("health body not entered within scheduling guard")
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("probe canceled before explicit cancellation: %v", err)
		}
		select {
		case result := <-stopped:
			t.Fatalf("HTTP body request stopped before explicit cancellation: %v", result.err)
		default:
		}
		select {
		case result := <-done:
			t.Fatalf("Start returned before explicit cancellation: %v", result.err)
		default:
		}
		// Health cannot begin before the last actual Engine inspection reply.
		// Anchor the earliest possible 3s fallback here, after activation and
		// startup, rather than impose a 3s bound on the entire Start operation.
		inspectedAt, ok := lastInspectResponse.Load().(time.Time)
		if !ok {
			t.Fatal("health entered without actual Engine inspection")
		}
		fallbackNotBefore := inspectedAt.Add(3 * time.Second)
		// Unlike the startup/cleanup guard, this is the cancellation response
		// requirement. Both completion events must occur within the same 1s
		// window and before the client's own timeout could have closed HTTP.
		const cancellationResponseLimit = time.Second
		canceledAt := time.Now()
		responseDeadline := canceledAt.Add(cancellationResponseLimit)
		response := time.NewTimer(cancellationResponseLimit)
		defer response.Stop()
		cancelProbe()
		assertCancellation := func(stage string, result completion) {
			if !errors.Is(result.err, context.Canceled) {
				t.Fatalf("%s cancellation lost: %v", stage, result.err)
			}
			if result.at.Before(canceledAt) || !result.at.Before(responseDeadline) || !result.at.Before(fallbackNotBefore) {
				t.Fatalf("%s completion does not prove business cancellation before HTTP fallback: response=%s", stage, result.at.Sub(canceledAt))
			}
		}
		select {
		case result := <-done:
			assertCancellation("Start", result)
		case <-response.C:
			t.Fatal("Start did not react to business cancellation within 1s; HTTP fallback is not cancellation evidence")
		}
		select {
		case result := <-stopped:
			assertCancellation("HTTP body", result)
		case <-response.C:
			t.Fatal("HTTP body request did not react to business cancellation within 1s")
		}
		if _, err := d.Endpoint("cancel-test"); err == nil {
			t.Fatal("canceled start published endpoint")
		}
		status, err := d.Inspect(t.Context(), "cancel-test")
		if err != nil || !status.Running || status.Ready {
			t.Fatalf("canceled start must remain running without Ready: %+v %v", status, err)
		}
	})
}
