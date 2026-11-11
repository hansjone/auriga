package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type startupClock struct {
	mu            sync.Mutex
	started, last time.Time
}

// WithStartup attaches an observation clock. It has no effect on deadlines or readiness.
// The durable acceptance time includes queueing and same-generation recovery waits.
func WithStartup(ctx context.Context, started time.Time, origin string) context.Context {
	if started.IsZero() {
		started = time.Now()
	}
	s := Current(ctx)
	s.startup = &startupClock{started: started, last: time.Now()}
	if origin != "" {
		s.Origin = origin
	}
	return context.WithValue(ctx, key{}, s)
}

// Stage measures the interval since the previous milestone and total since acceptance.
// Children share the clock, so health and container stages remain on one timeline.
func Stage(ctx context.Context, stage string, fields ...any) {
	s := Current(ctx)
	if s.startup == nil {
		return
	}
	c := s.startup
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	elapsed, total := max(now.Sub(c.last).Milliseconds(), 0), max(now.Sub(c.started).Milliseconds(), 0)
	c.last = now
	Log(ctx, slog.LevelInfo, "startup stage", append([]any{"stage", stage, "elapsed_ms", elapsed, "cumulative_ms", total}, fields...)...)
}

func Failed(ctx context.Context, err error) {
	stage := "failed"
	if errors.Is(err, context.DeadlineExceeded) {
		stage = "timeout"
	}
	Stage(ctx, stage, "error", Mask(err.Error()))
}
