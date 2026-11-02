package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
)

func TestHealthWaitKeepsFailureClassificationAndTrace(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	for name, fixture := range map[string]struct {
		status         int
		body, category string
	}{
		"not-ready": {200, `{"status":"ok","ready":false,"protocol":1,"pluginVersion":"fixture"}`, "not_ready"},
		"status":    {503, `password=sensitive`, "http_status"},
		"invalid":   {200, `{"secret":"sensitive"}`, "invalid_response"},
		"bad-json":  {200, `sensitive`, "invalid_response"},
	} {
		t.Run(name, func(t *testing.T) {
			seen := make(chan string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- r.Header.Get("traceparent")
				w.WriteHeader(fixture.status)
				_, _ = io.WriteString(w, fixture.body)
			}))
			defer server.Close()
			var logs bytes.Buffer
			ctx := telemetry.Start(context.Background(), parent, "instance-a", 7, slog.New(slog.NewJSONHandler(&logs, nil)))
			ctx, cancel := context.WithTimeout(ctx, 180*time.Millisecond)
			defer cancel()
			err := waitInstanceHealth(ctx, server.URL)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout identity changed: %v", err)
			}
			var diagnostic *healthWaitError
			if !errors.As(err, &diagnostic) || diagnostic.last.Category != fixture.category || diagnostic.last.HTTPStatus != fixture.status || diagnostic.attempts < 1 {
				t.Fatalf("lost last health diagnostic: %v", err)
			}
			select {
			case requestParent := <-seen:
				if !telemetry.Valid(requestParent) || requestParent[3:35] != parent[3:35] || requestParent[36:52] == telemetry.Current(ctx).Value[36:52] {
					t.Fatal("health did not create client child")
				}
			default:
				t.Fatal("no real health request")
			}
			text := logs.String()
			if strings.Contains(text, "sensitive") || !strings.Contains(text, parent[3:35]) || !strings.Contains(text, fixture.category) || !strings.Contains(text, "instance-a") {
				t.Fatal("wrong or unsafe structured health log", text)
			}
		})
	}
}

func TestHealthWaitKeepsTransportExceptionAndSuccess(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String()
	_ = listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err = waitInstanceHealth(ctx, endpoint)
	var diagnostic *healthWaitError
	if !errors.As(err, &diagnostic) || diagnostic.last.Category != "transport_error" || diagnostic.last.Err == "" {
		t.Fatalf("lost transport exception: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","ready":true,"protocol":1,"pluginVersion":"fixture"}`)
	}))
	defer server.Close()
	success, cancelSuccess := context.WithTimeout(context.Background(), time.Second)
	defer cancelSuccess()
	if err := waitInstanceHealth(success, server.URL); err != nil {
		t.Fatal("trace changed readiness", err)
	}
}
