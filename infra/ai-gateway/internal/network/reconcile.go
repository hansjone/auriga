package network

import (
	"context"
	"errors"
	"math/rand/v2"
	"sort"
	"sync"
	"time"
)

const kickWorkers = 128
const kickRoundBudget = 1500 * time.Millisecond

type ReconcileConfig struct {
	Interval        time.Duration
	RequestTimeout  time.Duration
	MaxKickAttempts int
	// Query failures get a bounded grace; explicit inactive decisions never do.
	FailureGrace time.Duration
	OnError      func(Binding, error)
	// One complete, account-filtered CONNZ snapshot per round, not per CID.
	Snapshot func(context.Context) (map[uint64]*ConnInfo, error)
}

type reconcileState struct {
	queries             map[string]tenantQuery
	snapshotFailedSince time.Time
	snapshotDegraded    bool
}

type tenantQuery struct {
	next     time.Time
	failures int
}

// Reconcile has one owner per redeemer. IAM and CONNZ are each batched once per
// round. Failed KICKs retain their revocation latch; query errors have a separate
// bounded grace. Polling, IAM retries and KICK retries are jittered.
func (r *IAMRedeemer) Reconcile(ctx context.Context, cfg ReconcileConfig, kick func(context.Context, Binding) error) error {
	if cfg.Interval <= 0 || cfg.Interval > 5*time.Second || cfg.RequestTimeout <= 0 || cfg.RequestTimeout > 20*time.Second || cfg.MaxKickAttempts < 1 || cfg.MaxKickAttempts > 5 || kick == nil || cfg.Snapshot == nil {
		return errors.New("network: invalid reconciliation configuration")
	}
	if cfg.FailureGrace == 0 {
		cfg.FailureGrace = 1500 * time.Millisecond
	}
	if cfg.FailureGrace <= 0 || cfg.FailureGrace > 60*time.Second {
		return errors.New("network: invalid authority failure grace")
	}
	state := reconcileState{}
	timer := time.NewTimer(jitter(cfg.Interval))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		r.reconcileRound(ctx, cfg, kick, &state)
		timer.Reset(jitter(cfg.Interval))
	}
}

func jitter(base time.Duration) time.Duration {
	spread := base / 5
	if spread < 1 {
		return base
	}
	return base - spread + time.Duration(rand.Int64N(int64(2*spread+1)))
}

func queryBackoff(base time.Duration, failures int) time.Duration {
	for i := 1; i < failures && base < 500*time.Millisecond; i++ {
		base *= 2
	}
	if base > 500*time.Millisecond {
		base = 500 * time.Millisecond
	}
	return jitter(base)
}

func (r *IAMRedeemer) reconcileRound(ctx context.Context, cfg ReconcileConfig, kick func(context.Context, Binding) error, state *reconcileState) {
	start := time.Now()
	grace := cfg.FailureGrace
	if grace == 0 {
		grace = 1500 * time.Millisecond
	}
	bindings := r.snapshot()
	if len(bindings) == 0 {
		return
	}
	inspect, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	conns, snapshotErr := cfg.Snapshot(inspect)
	cancel()
	if ctx.Err() != nil {
		return
	}
	if snapshotErr == nil {
		state.snapshotFailedSince = time.Time{}
		state.snapshotDegraded = false
	} else {
		if state.snapshotFailedSince.IsZero() {
			state.snapshotFailedSince = start
		}
		if !state.snapshotDegraded && time.Since(state.snapshotFailedSince) >= grace {
			state.snapshotDegraded = true
			if cfg.OnError != nil {
				cfg.OnError(Binding{ServerID: r.cfg.ServerID}, errors.New("network: CONNZ outage grace exhausted; kicking known revoked CIDs"))
			}
		}
	}
	eligible := make([]liveBinding, 0, len(bindings))
	for _, b := range bindings {
		if start.Before(b.CheckAfter) {
			continue
		}
		if !start.Before(b.NotAfter) {
			r.forget(b.Ingress)
			continue
		}
		if snapshotErr == nil {
			ci := conns[b.ClientID]
			if ci == nil {
				r.forget(b.Ingress)
				continue
			}
			if ci.Account != b.Account || ci.AuthorizedUser != b.Ingress {
				r.forget(b.Ingress)
				if cfg.OnError != nil {
					cfg.OnError(b.Binding, ErrNotOwned)
				}
				continue
			}
		}
		eligible = append(eligible, b)
	}
	var status map[nodeIdentity]bool
	var failures map[string]error
	if state.queries == nil {
		state.queries = make(map[string]tenantQuery)
	}
	previous := state.queries
	queries := make([]liveBinding, 0, len(eligible))
	retained := make(map[string]tenantQuery)
	for _, b := range eligible {
		q := state.queries[b.Tenant]
		retained[b.Tenant] = q
		if !b.Revoked && !time.Now().Before(q.next) {
			queries = append(queries, b)
		}
	}
	state.queries = retained
	if len(queries) > 0 {
		status, failures = r.activeBatch(ctx, queries, cfg.RequestTimeout)
		if ctx.Err() != nil {
			return
		}
		for _, b := range queries {
			delete(state.queries, b.Tenant)
		}
		for tenant := range failures {
			q := previous[tenant]
			q.failures++
			q.next = time.Now().Add(queryBackoff(cfg.Interval, q.failures))
			state.queries[tenant] = q
		}
	}
	pending := make([]liveBinding, 0)
	for _, b := range eligible {
		active, queried := status[nodeIdentity{b.Tenant, b.Realm, b.Node}]
		queryErr := failures[b.Tenant]
		queried = queried || queryErr != nil
		if !b.Revoked && queried {
			if queryErr == nil {
				b.Failures = 0
				b.FailedSince = time.Time{}
				b.Revoked = !active
			} else {
				b.Failures++
				if b.FailedSince.IsZero() {
					b.FailedSince = time.Now()
				}
			}
		}
		if !b.Revoked && (!b.FailedSince.IsZero() && time.Since(b.FailedSince) >= grace) {
			b.Revoked = true
			// Spread outage-driven closes; authoritative active=false bypasses this
			// delay. New ticket redemption remains fail-closed throughout the grace.
			b.RetryAfter = time.Now().Add(time.Duration(rand.Int64N(int64(250 * time.Millisecond))))
		}
		r.remember(b)
		// A complete snapshot proves account/ingress ownership for the entire round.
		// CIDs cannot be reused within a server generation, so per-KICK CONNZ is redundant.
		// During a prolonged CONNZ outage, authenticated callout bindings still
		// identify the server generation/CID. NoReconnect prevents generation
		// changes and CIDs are not reused within a generation. Never clear the
		// revocation latch because control-plane inspection is unavailable.
		if b.Revoked && (snapshotErr == nil || state.snapshotDegraded) && b.Attempts < cfg.MaxKickAttempts && !time.Now().Before(b.RetryAfter) {
			pending = append(pending, b)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Attempts < pending[j].Attempts })
	kickCtx, stop := context.WithTimeout(ctx, max(kickRoundBudget, 2*cfg.RequestTimeout))
	defer stop()
	jobs := make(chan liveBinding)
	var workers sync.WaitGroup
	for i := 0; i < min(kickWorkers, len(pending)); i++ {
		workers.Go(func() {
			for b := range jobs {
				if kickCtx.Err() != nil {
					continue
				}
				attempt, cancel := context.WithTimeout(kickCtx, cfg.RequestTimeout)
				err := kick(attempt, b.Binding)
				cancel()
				// Count every invoked KICK, including cancellation at the round
				// boundary; otherwise late requests could retry forever.
				b.Attempts++
				if err == nil || errors.Is(err, ErrNoSuchConn) {
					r.forget(b.Ingress)
					continue
				}
				if errors.Is(err, ErrNotOwned) {
					b.Attempts = cfg.MaxKickAttempts
				}
				b.RetryAfter = time.Now().Add(queryBackoff(cfg.Interval, b.Attempts))
				r.remember(b)
				if b.Attempts == cfg.MaxKickAttempts && cfg.OnError != nil {
					cfg.OnError(b.Binding, err)
				}
			}
		})
	}
	for _, b := range pending {
		if kickCtx.Err() != nil {
			break
		}
		select {
		case jobs <- b:
		case <-kickCtx.Done():
		}
	}
	close(jobs)
	workers.Wait()
}

func (r *IAMRedeemer) forget(ingress string) {
	guard := r.connectionGuard(ingress)
	if guard == nil {
		return
	}
	guard.Lock()
	r.mu.Lock()
	delete(r.bindings, ingress)
	delete(r.guards, ingress)
	onForget := r.onForget
	r.mu.Unlock()
	guard.Unlock()
	if onForget != nil {
		onForget(ingress)
	}
}

func (r *IAMRedeemer) remember(b liveBinding) {
	guard := r.connectionGuard(b.Ingress)
	if guard == nil {
		return
	}
	guard.Lock()
	defer guard.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.bindings[b.Ingress]; ok {
		b.Incarnation, b.ChannelNonce, b.Epoch = current.Incarnation, current.ChannelNonce, current.Epoch
		r.bindings[b.Ingress] = b
	}
}
