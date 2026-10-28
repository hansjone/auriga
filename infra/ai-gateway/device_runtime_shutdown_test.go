package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relayhttp"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaysupervisor"
)

func TestDeviceShutdownKeepsRecoveryAvailableForActiveHTTP(t *testing.T) {
	// No recovery I/O occurs: this checks the real Supervisor admission lifetime
	// with a real HTTP request, independently of PostgreSQL availability.
	sup, err := relaysupervisor.New(new(budget.Ledger), 1, time.Second, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var finished, closed, closedEarly atomic.Bool
	r := &deviceRuntime{handler: new(relayhttp.Handler), supervisor: sup, cancel: cancel, shutdown: time.Second}
	r.closeResources = func() {
		closedEarly.Store(!finished.Load() || ctx.Err() == nil)
		closed.Store(true)
	}
	r.server = &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }}
	r.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-req.Context().Done():
		}
		// A request may still be reading its body when shutdown starts. Its
		// recovery reservation must remain available until HTTP has drained.
		finish, ok := sup.Reserve()
		if !ok || req.Context().Err() != nil || closed.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			finish(nil)
			w.WriteHeader(http.StatusOK)
		}
		finished.Store(true)
	})
	r.server.RegisterOnShutdown(unblock)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- r.server.Serve(l) }()
	t.Cleanup(func() {
		unblock()
		cancel()
		_ = r.server.Close()
		drain, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = sup.Drain(drain)
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})
	client := &http.Client{Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	result := make(chan int, 1)
	go func() {
		res, err := client.Get("http://" + l.Addr().String())
		if err != nil {
			result <- 0
			return
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		result <- res.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler did not start")
	}
	if err := r.shutdownRuntime(); err != nil {
		t.Fatal(err)
	}
	if status := <-result; status != http.StatusOK {
		t.Fatalf("active HTTP lost recovery admission: status=%d", status)
	}
	if !closed.Load() || closedEarly.Load() || !sup.Stats().Closed {
		t.Fatal("dependencies did not close after HTTP completion and cancellation")
	}
}
