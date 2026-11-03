package reconciler

import (
	"context"
	"errors"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type healthRetry struct {
	failures int
	after    time.Time
}

// StartHealthChecks retains a trigger independently of ingress/observer targets:
// a failed repair withdraws those targets but must not disable later recovery.
func (r *Reconciler) StartHealthChecks(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	r.mu.Lock()
	if r.closed || r.healthStarted {
		r.mu.Unlock()
		return
	}
	r.healthStarted, r.healthContext, r.healthInterval = true, ctx, interval
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.done:
				return
			case <-ticker.C:
				records, err := r.Store.ListActual()
				if err != nil {
					continue
				}
				for _, rec := range records {
					if rec.Status != nil {
						r.RequestHealth(rec.Status.InstanceId)
					}
				}
			}
		}
	}()
}

// RequestHealth coalesces observer hints and maintenance ticks. No stale bool or
// sampled endpoint is accepted as readiness evidence, and no queue writer waits.
func (r *Reconciler) RequestHealth(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.healthContext.Err() != nil || r.healthPending[id] || time.Now().Before(r.healthRetries[id].after) {
		return
	}
	if _, ok := r.Driver.(runtime.HealthChecker); !ok {
		return
	}
	ch, ok := r.workers[id]
	if !ok {
		ch = make(chan workItem, 8)
		r.workers[id] = ch
		r.wg.Add(1)
		go r.loop(id, ch)
	}
	select {
	case ch <- workItem{kind: "health", healthContext: r.healthContext, healthEpoch: r.healthEpochs[id]}:
		r.healthPending[id] = true
	default:
	}
}

func (r *Reconciler) cancelHealth(id string) {
	r.mu.Lock()
	r.cancelHealthLocked(id)
	r.mu.Unlock()
}

func (r *Reconciler) cancelHealthLocked(id string) {
	r.healthEpochs[id]++
	if cancel := r.healthCancels[id]; cancel != nil {
		cancel()
	}
	delete(r.healthRetries, id)
}

// Desired writes and cancellation epochs use the same mutex as this commit.
// Publishing a stale observation can therefore never overwrite a newer command.
func (r *Reconciler) persistHealth(ctx context.Context, epoch uint64, spec *nodev1.InstanceSpec, st *nodev1.InstanceStatus, attempts uint32) bool {
	r.mu.Lock()
	latest, err := r.Store.GetDesired(st.InstanceId)
	if err != nil || r.closed || ctx.Err() != nil || epoch != r.healthEpochs[st.InstanceId] || !proto.Equal(spec, latest) {
		r.mu.Unlock()
		return false
	}
	err = r.Store.PutActual(state.ActualRecord{Status: st, Attempts: attempts})
	r.mu.Unlock()
	if err != nil {
		r.Log.Warn("persist runtime health failed", "instance_id", st.InstanceId, "error", err)
		return false
	}
	r.emit(st)
	return true
}

func (r *Reconciler) handleHealth(id string, parent context.Context, epoch uint64) {
	defer func() {
		r.mu.Lock()
		delete(r.healthPending, id)
		delete(r.healthCancels, id)
		r.mu.Unlock()
	}()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	r.mu.Lock()
	if r.closed || epoch != r.healthEpochs[id] {
		r.mu.Unlock()
		return
	}
	r.healthCancels[id] = cancel
	r.mu.Unlock()
	spec, err := r.Store.GetDesired(id)
	if err != nil || spec == nil || spec.Desired != nodev1.DesiredState_DESIRED_STATE_RUNNING {
		return
	}
	rec, err := r.Store.GetActual(id)
	if err != nil || rec.Status == nil || rec.Status.ObservedGeneration < 1 {
		return
	}
	switch rec.Status.Phase {
	case nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART, nodev1.InstancePhase_INSTANCE_PHASE_FAILED:
	default:
		return
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, r.StartTimeout)
	defer waitCancel()
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-waitCtx.Done():
		return
	}
	// A newer desired may remain pending while the ORIGINAL execution is checked.
	// Stop/delete/apply cancel active work before their queued worker can run.
	current := func() bool {
		if ctx.Err() != nil {
			return false
		}
		r.mu.Lock()
		validEpoch := !r.closed && epoch == r.healthEpochs[id]
		r.mu.Unlock()
		if !validEpoch {
			return false
		}
		latest, loadErr := r.Store.GetDesired(id)
		return loadErr == nil && proto.Equal(spec, latest)
	}
	ctx = telemetry.Start(ctx, "", id, rec.Status.ObservedGeneration, r.Log)
	checker := r.Driver.(runtime.HealthChecker)
	actual, checkErr := checker.CheckHealth(ctx, id)
	if !current() {
		return
	}
	if checkErr == nil && actual.Generation != rec.Status.ObservedGeneration {
		checkErr = errors.New("runtime health execution generation mismatch")
	}
	if checkErr == nil && actual.Ready && actual.Running {
		r.mu.Lock()
		delete(r.healthRetries, id)
		r.mu.Unlock()
		if !rec.Status.Ready || rec.Status.Error != nil {
			st := cloneStatus(rec.Status)
			st.Ready, st.Error, st.UpdatedAt = true, nil, timestamppb.Now()
			if st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
				st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
				if len(st.RestartReasons) != 0 {
					st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
				}
			}
			r.persistHealth(ctx, epoch, spec, st, 0)
		}
		return
	}
	st := cloneStatus(rec.Status)
	st.Ready, st.UpdatedAt = false, timestamppb.Now()
	if !r.persistHealth(ctx, epoch, spec, st, rec.Attempts) || !current() {
		return
	}
	// Maintenance repairs an already running process; unexpected exits remain
	// visible and are handled by normal lifecycle commands, never periodic starts.
	if checkErr == nil && actual.Running {
		// Keep the health phase deadline inside the continuous qualification.
		// A definite Engine outcome followed by health timeout must leave the
		// original qualification able to clear its own completed intent.
		run := func(ctx context.Context) error {
			phase, phaseCancel := context.WithTimeout(ctx, r.StartTimeout)
			defer phaseCancel()
			return r.Driver.Start(phase, id)
		}
		if r.Activation != nil {
			checkErr = r.Activation.Recover(ctx, id, rec.Status.ObservedGeneration, run)
		} else {
			checkErr = run(ctx)
		}
		if !current() {
			return
		}
		if checkErr == nil {
			st.Ready, st.Error, st.UpdatedAt = true, nil, timestamppb.Now()
			if st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
				st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
				if len(st.RestartReasons) != 0 {
					st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
				}
			}
			r.persistHealth(ctx, epoch, spec, st, 0)
			r.mu.Lock()
			delete(r.healthRetries, id)
			r.mu.Unlock()
			return
		}
	}
	r.mu.Lock()
	if r.closed || epoch != r.healthEpochs[id] || ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	retry := r.healthRetries[id]
	retry.failures = min(retry.failures+1, 7)
	delay := min(r.healthInterval*time.Duration(1<<uint(retry.failures-1)), time.Minute)
	retry.after = time.Now().Add(delay)
	r.healthRetries[id] = retry
	r.mu.Unlock()
	st.Error = &nodev1.InstanceError{Code: "agent.start_failed", Message: "Runtime health recovery could not be completed", Retryable: true, Attempts: uint32(retry.failures)}
	st.UpdatedAt = timestamppb.Now()
	r.persistHealth(ctx, epoch, spec, st, uint32(retry.failures))
}
