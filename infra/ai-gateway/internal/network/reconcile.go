package network

import (
	"context"
	"errors"
	"sync"
	"time"
)

type ReconcileConfig struct {
	Interval        time.Duration
	RequestTimeout  time.Duration
	MaxKickAttempts int
	// Called on exhausted retries or ownership conflicts; contains no credentials.
	OnError func(Binding, error)
	// Inspect also reclaims healthy connections that have disconnected, without
	// waiting for their JWT to expire. Called only after the CONNECT grace period.
	Inspect func(context.Context, Binding) error
}

// Reconcile checks known connections only, with bounded memory, concurrency,
// per-request deadlines and KICK attempts. IAM errors fail closed as well.
// A failed KICK never clears the revoked latch. Broker JWT expiry remains the
// final fallback when the system control account cannot close a socket.
func (r *IAMRedeemer) Reconcile(ctx context.Context, cfg ReconcileConfig, kick func(context.Context, Binding) error) error {
	if cfg.Interval <= 0 || cfg.RequestTimeout <= 0 || cfg.MaxKickAttempts < 1 || cfg.MaxKickAttempts > 5 || kick == nil {
		return errors.New("network: invalid reconciliation configuration")
	}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		jobs := make(chan liveBinding)
		var workers sync.WaitGroup
		for range 16 {
			workers.Go(func() {
				for b := range jobs {
					r.reconcileOne(ctx, cfg, kick, b)
				}
			})
		}
		for _, b := range r.snapshot() {
			select {
			case jobs <- b:
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}
		}
		close(jobs)
		workers.Wait()
	}
}

func (r *IAMRedeemer) reconcileOne(ctx context.Context, cfg ReconcileConfig, kick func(context.Context, Binding) error, b liveBinding) {
	now := time.Now()
	if now.Before(b.CheckAfter) {
		return
	}
	remove := false
	if cfg.Inspect != nil {
		inspect, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
		err := cfg.Inspect(inspect, b.Binding)
		cancel()
		if ctx.Err() != nil {
			return
		}
		remove = errors.Is(err, ErrNoSuchConn)
		if errors.Is(err, ErrNotOwned) {
			b.Revoked = true
			b.Attempts = cfg.MaxKickAttempts
			remove = true
			if cfg.OnError != nil {
				cfg.OnError(b.Binding, err)
			}
		}
	}
	if !now.Before(b.NotAfter) {
		remove = true
	} else if !remove && !b.Revoked {
		check, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
		active, err := r.active(check, b)
		cancel()
		if ctx.Err() != nil {
			return
		}
		b.Revoked = err != nil || !active
	}
	if !remove && b.Revoked && b.Attempts < cfg.MaxKickAttempts {
		attempt, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
		err := kick(attempt, b.Binding)
		cancel()
		if ctx.Err() != nil {
			return
		}
		b.Attempts++
		remove = err == nil || errors.Is(err, ErrNoSuchConn)
		if errors.Is(err, ErrNotOwned) {
			b.Attempts = cfg.MaxKickAttempts
		}
		if !remove && b.Attempts == cfg.MaxKickAttempts && cfg.OnError != nil {
			cfg.OnError(b.Binding, err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// A fresh ingress is never reused, so an older snapshot cannot overwrite a
	// reconnect or a different connection that happens to have the same node.
	if _, ok := r.bindings[b.Ingress]; ok {
		if remove {
			delete(r.bindings, b.Ingress)
		} else {
			r.bindings[b.Ingress] = b
		}
	}
}
