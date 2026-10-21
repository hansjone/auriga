package runtime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthWaitNeverUsesEnvironmentProxy(t *testing.T) {
	var proxyRequests atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyRequests.Add(1)
		_, _ = io.WriteString(w, `{"status":"ok","ready":true,"protocol":1,"pluginVersion":"test"}`)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := waitInstanceHealth(ctx, "http://192.0.2.10:9"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected health result: %v", err)
	}
	if proxyRequests.Load() != 0 {
		t.Fatal("health used HTTP_PROXY")
	}
}
