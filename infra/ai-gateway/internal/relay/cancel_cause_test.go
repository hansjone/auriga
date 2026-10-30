package relay

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
)

// Hold back parent Done propagation while keeping its immutable deadline.
// This isolates the ordering where an online check returns an opaque error at
// the deadline before the parent's cancellation reaches run's child context.
type delayedDeadline struct {
	context.Context
	deadline time.Time
}

func (c delayedDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestRecheckDeadlineBeforeParentCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := delayedDeadline{Context: t.Context(), deadline: time.Now().Add(time.Second)}
		c := Core{c: Config{RecheckInterval: time.Second, CheckTimeout: time.Second, StopTimeout: time.Second,
			Transport: func(ctx context.Context, _ Submission, _ []byte) (Exchange, error) {
				<-ctx.Done()
				stopped := make(chan struct{})
				close(stopped)
				return Exchange{Stopped: stopped}, ctx.Err()
			}}}
		// A nil call produces an immediate opaque Gate error without PG/RPC.
		// The classification must depend on the execution deadline, not the
		// particular safe error returned by Recheck (which hides RPC timeouts).
		r := cancellationRequest(t)
		_, stopped, _, err := c.run(ctx, nil, admin.RelayTarget{FirstByteTimeoutSeconds: 5}, nil, r, "unit", nil)
		expectCode(t, err, "ai.upstream_timeout")
		if !stopped || ctx.Err() != nil || time.Now().Before(ctx.deadline) {
			t.Fatal("did not isolate recheck before parent cancellation")
		}
	})
}

func TestCancellationCauseFirstWins(t *testing.T) {
	for _, mode := range []string{"client", "recheck", "client-before-total", "recheck-before-total", "total", "first-byte"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				interval := time.Hour
				if strings.HasPrefix(mode, "recheck") {
					interval = 100 * time.Millisecond
				}
				first := int64(1)
				if mode == "total" || strings.HasSuffix(mode, "before-total") {
					first = 5
				}
				if mode == "first-byte" {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
				}
				c := Core{c: Config{RecheckInterval: interval, CheckTimeout: time.Second, StopTimeout: time.Second,
					Transport: func(ctx context.Context, _ Submission, _ []byte) (Exchange, error) {
						if strings.HasPrefix(mode, "client") {
							cancel()
						}
						<-ctx.Done()
						// Delay observation until both competing timers have fired.
						// Virtual time makes this deterministic, without wall sleeps.
						<-time.After(2 * time.Second)
						stopped := make(chan struct{})
						close(stopped)
						return Exchange{Stopped: stopped}, ctx.Err()
					}}}
				_, stopped, _, err := c.run(ctx, nil, admin.RelayTarget{FirstByteTimeoutSeconds: first}, nil, cancellationRequest(t), "unit", nil)
				want := "ai.upstream_timeout"
				if strings.HasPrefix(mode, "client") || strings.HasPrefix(mode, "recheck") {
					want = "ai.stream_interrupted"
				}
				expectCode(t, err, want)
				if !stopped {
					t.Fatal("lost actual stop proof")
				}
			})
		})
	}
}

func TestIdleCancellationCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		c := Core{c: Config{RecheckInterval: time.Hour, StopTimeout: time.Second,
			Limits: modelprotocol.Limits{MaxBytes: 1024, MaxEvents: 10, MaxEventBytes: 512},
			Transport: func(ctx context.Context, _ Submission, _ []byte) (Exchange, error) {
				pr, pw := io.Pipe()
				stopped := make(chan struct{})
				go func() {
					defer close(stopped)
					defer func() { _ = pw.Close() }()
					<-time.After(500 * time.Millisecond)
					_, _ = pw.Write([]byte("{"))
					<-ctx.Done()
				}()
				return Exchange{Body: pr, Stopped: stopped}, nil
			}}}
		_, stopped, _, err := c.run(t.Context(), nil, admin.RelayTarget{FirstByteTimeoutSeconds: 1, IdleTimeoutSeconds: 1}, nil, cancellationRequest(t), "unit", nil)
		expectCode(t, err, "ai.upstream_timeout")
		if !stopped || time.Since(started) != 1500*time.Millisecond {
			t.Fatal("idle timer did not replace first-byte timer", stopped, time.Since(started))
		}
	})
}

func cancellationRequest(t *testing.T) *modelprotocol.Request {
	t.Helper()
	r, err := modelprotocol.DecodeRequest(t.Context(), strings.NewReader(plainRequest), syntheticPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
