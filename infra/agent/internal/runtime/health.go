package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
)

type healthWaitError struct {
	cause    error
	last     observer.HealthResult
	attempts int
	elapsed  time.Duration
}

func (e *healthWaitError) Error() string {
	return fmt.Sprintf("%v; attempts=%d elapsed_ms=%d last=%s http_status=%d response=%s detail=%s", e.cause, e.attempts, e.elapsed.Milliseconds(), e.last.Category, e.last.HTTPStatus, e.last.Response, e.last.Err)
}
func (e *healthWaitError) Unwrap() error { return e.cause }

// A successful Engine start means only that a process exists. The existing
// Start context bounds the entire readiness wait; each HTTP attempt is shorter.
func waitInstanceHealth(ctx context.Context, endpoint string) error {
	ctx = telemetry.Child(ctx)
	started := time.Now()
	attempts := 0
	listening := false
	last := observer.HealthResult{Category: "not_attempted"}
	failure := func(cause error) error {
		err := &healthWaitError{cause: cause, last: last, attempts: attempts, elapsed: time.Since(started)}
		telemetry.Log(ctx, slog.LevelWarn, "instance health wait failed", "attempts", attempts, "elapsed_ms", err.elapsed.Milliseconds(), "category", last.Category, "http_status", last.HTTPStatus, "health_response", last.Response, "health_span_id", last.SpanID, "error", telemetry.Mask(err.Error()))
		return err
	}
	telemetry.Log(ctx, slog.LevelInfo, "instance health wait started")
	base, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		ResponseHeaderTimeout: 2 * time.Second,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	target := observer.InstanceTarget{ID: telemetry.Current(ctx).Instance, BaseURL: base, Process: observer.ProcessRunning}
	for {
		if err := ctx.Err(); err != nil {
			return failure(err)
		}
		if attempts == 0 {
			telemetry.Stage(ctx, "first_health_attempt")
		}
		result := observer.ProbeHealth(ctx, client, target)
		if !listening && result.HTTPStatus > 0 {
			listening = true
			telemetry.Stage(ctx, "listen_detected", "http_status", result.HTTPStatus)
		}
		attempts++
		if ctx.Err() == nil || attempts == 1 {
			last = result
		}
		if result.Ready {
			telemetry.Stage(ctx, "plugin_healthy")
			telemetry.Log(ctx, slog.LevelInfo, "instance health ready", "attempts", attempts, "elapsed_ms", time.Since(started).Milliseconds(), "health_response", result.Response, "health_span_id", result.SpanID)
			return ctx.Err()
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return failure(ctx.Err())
		case <-timer.C:
		}
	}
}
