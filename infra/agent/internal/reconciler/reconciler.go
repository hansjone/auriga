// Package reconciler drives instance actual state toward desired specs.
package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxFailAttempts = 5

// StatusSink receives an owned snapshot after each reconcile step.
type StatusSink func(st *nodev1.InstanceStatus)

// Reconciler applies desired specs via a Driver, one goroutine per instance.
type Reconciler struct {
	Store      *state.Store
	Driver     runtime.Driver
	Activation *runtime.ReleaseActivation
	Log        *slog.Logger
	Sink       StatusSink
	// PrepareTimeout bounds Ensure (artifact fetch, image pull, render) and
	// StartTimeout bounds Start, so a stuck download fails instead of hanging.
	PrepareTimeout time.Duration
	StartTimeout   time.Duration

	mu      sync.Mutex
	workers map[string]chan workItem
	sem     chan struct{}
	maxPrep int
	wg      sync.WaitGroup
	closed  bool
}

type workItem struct {
	kind    string // apply | delete | tick
	spec    *nodev1.InstanceSpec
	del     *nodev1.DeleteInstance
	command *nodev1.Command
	result  chan *nodev1.CommandResult
	parent  string
}

// New creates a reconciler. maxConcurrentPrepare defaults to 4.
func New(store *state.Store, driver runtime.Driver, log *slog.Logger, maxConcurrentPrepare int) *Reconciler {
	if maxConcurrentPrepare < 1 {
		maxConcurrentPrepare = 4
	}
	if log == nil {
		log = slog.Default()
	}
	var activation *runtime.ReleaseActivation
	if d, ok := driver.(interface {
		ReleaseCoordinator() *runtime.ReleaseActivation
	}); ok {
		activation = d.ReleaseCoordinator()
	}
	return &Reconciler{
		Activation:     activation,
		Store:          store,
		Driver:         driver,
		Log:            log,
		workers:        make(map[string]chan workItem),
		sem:            make(chan struct{}, maxConcurrentPrepare),
		maxPrep:        maxConcurrentPrepare,
		PrepareTimeout: 10 * time.Minute,
		StartTimeout:   2 * time.Minute,
	}
}

// Close stops all instance workers.
func (r *Reconciler) Close() {
	r.mu.Lock()
	r.closed = true
	for id, ch := range r.workers {
		close(ch)
		delete(r.workers, id)
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// ApplySpec persists desired and queues reconcile (Session / tests).
func (r *Reconciler) ApplySpec(ctx context.Context, spec *nodev1.InstanceSpec) error {
	return r.apply(spec, telemetry.Current(ctx).Value)
}

// Apply queues an ApplySpec (persists desired first).
func (r *Reconciler) Apply(spec *nodev1.InstanceSpec) error {
	return r.apply(spec, "")
}

func (r *Reconciler) apply(spec *nodev1.InstanceSpec, parent string) error {
	if spec == nil || spec.InstanceId == "" {
		return fmt.Errorf("missing instance_id")
	}
	spec = proto.Clone(spec).(*nodev1.InstanceSpec)
	if err := r.Store.PutDesiredWithTrace(spec, parent); err != nil {
		return err
	}
	r.enqueue(spec.InstanceId, workItem{kind: "apply", spec: spec, parent: r.Store.StartupParent(spec.InstanceId, spec.Generation)})
	return nil
}

// Delete removes an instance (Session API).
func (r *Reconciler) Delete(_ context.Context, instanceID string, generation int64, purgeData bool) error {
	return r.DeleteInstance(&nodev1.DeleteInstance{
		InstanceId: instanceID,
		Generation: generation,
		PurgeData:  purgeData,
	})
}

// DeleteInstance queues a delete message.
func (r *Reconciler) DeleteInstance(del *nodev1.DeleteInstance) error {
	if del == nil || del.InstanceId == "" {
		return fmt.Errorf("missing instance_id")
	}
	r.enqueue(del.InstanceId, workItem{kind: "delete", del: del})
	return nil
}

// ReconcileAll re-queues every desired instance (used after reconnect / restart).
func (r *Reconciler) ReconcileAll(_ context.Context) error {
	ids, err := r.Store.ListDesiredIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		spec, err := r.Store.GetDesired(id)
		if err != nil {
			return err
		}
		if spec != nil {
			r.enqueue(id, workItem{kind: "apply", spec: spec, parent: r.Store.StartupParent(id, spec.Generation)})
		}
	}
	return nil
}

// Inventory builds the full InstanceStatus list for Session.
func (r *Reconciler) Inventory() ([]*nodev1.InstanceStatus, error) {
	recs, err := r.Store.ListActual()
	if err != nil {
		return nil, err
	}
	out := make([]*nodev1.InstanceStatus, 0, len(recs))
	inspectionCtx, cancel := context.WithTimeout(context.Background(), r.StartTimeout)
	defer cancel()
	for _, rec := range recs {
		if rec.Status != nil {
			status := cloneStatus(rec.Status)
			if status.Ready {
				actual, inspectErr := r.Driver.Inspect(inspectionCtx, status.InstanceId)
				status.Ready = inspectErr == nil && inspectionCtx.Err() == nil && actual.Running && actual.Ready
			}
			out = append(out, status)
		}
	}
	ids, err := r.Store.ListDesiredIDs()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, st := range out {
		have[st.InstanceId] = true
	}
	for _, id := range ids {
		if have[id] {
			continue
		}
		out = append(out, &nodev1.InstanceStatus{
			InstanceId: id,
			Phase:      nodev1.InstancePhase_INSTANCE_PHASE_ABSENT,
			UpdatedAt:  timestamppb.Now(),
		})
	}
	return out, nil
}

func (r *Reconciler) enqueue(id string, item workItem) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
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
	case ch <- item:
	default:
		// drop oldest-style: non-blocking best effort then blocking
		go func() { ch <- item }()
	}
}

func (r *Reconciler) loop(id string, ch chan workItem) {
	defer r.wg.Done()
	for item := range ch {
		ctx := context.Background()
		generation := int64(0)
		if item.spec != nil {
			generation = item.spec.Generation
		}
		if generation == 0 && item.command != nil {
			if spec, err := r.Store.GetDesired(id); err == nil && spec != nil {
				generation = spec.Generation
				item.parent = r.Store.StartupParent(id, generation)
			}
		}
		// A same-generation queued replay must not reopen a completed startup.
		if r.Store.StartupComplete(id, generation) {
			item.parent = ""
		}
		ctx = telemetry.Start(ctx, item.parent, id, generation, r.Log)
		switch item.kind {
		case "restart":
			item.result <- r.handleRestart(ctx, item.command)
		case "delete":
			r.handleDelete(ctx, id, item.del)
		default:
			r.handleApply(ctx, id, item.spec)
		}
	}
}

func (r *Reconciler) handleApply(ctx context.Context, id string, spec *nodev1.InstanceSpec) {
	r.applySpec(ctx, id, spec, false)
}

// replaceStoppedExecution is used only after an authorized restart has durably
// stopped an older execution. Its target must pass normal Ensure/activation;
// ordinary recovery continues to preserve the original execution without rebuild.
func (r *Reconciler) applySpec(ctx context.Context, id string, spec *nodev1.InstanceSpec, replaceStoppedExecution bool) {
	if spec == nil {
		var err error
		spec, err = r.Store.GetDesired(id)
		if err != nil || spec == nil {
			return
		}
	}
	spec = proto.Clone(spec).(*nodev1.InstanceSpec)
	if telemetry.Current(ctx).Generation != spec.Generation || telemetry.Current(ctx).Instance != id {
		ctx = telemetry.Start(ctx, r.Store.StartupParent(id, spec.Generation), id, spec.Generation, r.Log)
	}
	rec, actualErr := r.Store.GetActual(id)
	if actualErr != nil {
		r.fail(id, spec.Generation, 0, "agent.ensure_failed", actualErr.Error(), false)
		return
	}
	if r.Activation != nil && rec.Status == nil {
		execution, err := r.Store.GetExecution(id)
		if err != nil {
			r.fail(id, spec.Generation, rec.Attempts, "agent.ensure_failed", err.Error(), false)
			return
		}
		if execution != nil {
			rec.Status = &nodev1.InstanceStatus{InstanceId: id, ObservedGeneration: execution.Generation, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, UpdatedAt: timestamppb.Now()}
		}
	}
	if r.Activation != nil && spec.Desired == nodev1.DesiredState_DESIRED_STATE_STOPPED {
		execution, err := r.Store.GetExecution(id)
		if err != nil {
			r.fail(id, spec.Generation, rec.Attempts, "agent.ensure_failed", err.Error(), false)
			return
		}
		if execution != nil {
			if err := r.Driver.Stop(ctx, id, 5*time.Second); err != nil {
				r.fail(id, spec.Generation, rec.Attempts, "agent.start_failed", err.Error(), true)
				return
			}
			_ = r.persist(&nodev1.InstanceStatus{InstanceId: id, ObservedGeneration: spec.Generation, Phase: nodev1.InstancePhase_INSTANCE_PHASE_STOPPED, UpdatedAt: timestamppb.Now()}, 0)
			return
		}
	}
	if !replaceStoppedExecution && rec.Status != nil &&
		rec.Status.ObservedGeneration == spec.Generation &&
		(spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING || rec.Status.Phase == desiredPhase(spec)) {
		// Persisted ready is not evidence after an agent restart. Rediscover and
		// health-check a still-running process without Ensure replacing it.
		recoveryCtx, cancel := context.WithTimeout(ctx, r.StartTimeout)
		st, err := r.Driver.Inspect(recoveryCtx, id)
		if err == nil {
			err = recoveryCtx.Err()
		}
		if err != nil {
			cancel()
			r.failRecovery(ctx, rec, "runtime inspection failed")
			return
		}
		if spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING && (st.Running || r.Activation != nil) {
			if r.Activation == nil && st.Ready && rec.Status.Ready &&
				(rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART) {
				r.emit(rec.Status)
			} else {
				r.recoverRunning(ctx, rec, st.Ready && rec.Status.Ready)
			}
			cancel()
			return
		}
		cancel()
		if spec.Desired == nodev1.DesiredState_DESIRED_STATE_STOPPED && !st.Running {
			r.emit(rec.Status)
			return
		}
	}
	// Spec changed while still desired=running → PendingRestart (workspace decides when to restart).
	// desired=stopped falls through and stops the instance.
	if !replaceStoppedExecution && rec.Status != nil &&
		spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING &&
		(rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING ||
			rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART ||
			(rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED && len(rec.Status.RestartReasons) != 0)) &&
		rec.Status.ObservedGeneration > 0 &&
		rec.Status.ObservedGeneration < spec.Generation &&
		needsRestart(rec.Status, spec) {
		st := cloneStatus(rec.Status)
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
		if len(st.RestartReasons) == 0 {
			st.RestartReasons = restartReasons(spec)
		}
		st.Error = nil
		st.UpdatedAt = timestamppb.Now()
		pending := rec
		pending.Status = st
		recoveryCtx, cancel := context.WithTimeout(ctx, r.StartTimeout)
		actual, err := r.Driver.Inspect(recoveryCtx, id)
		if err == nil {
			err = recoveryCtx.Err()
		}
		switch {
		case err != nil || !actual.Running:
			r.failRecovery(ctx, pending, "pending runtime inspection failed")
		case !actual.Ready || r.Activation != nil:
			r.recoverRunning(ctx, pending, actual.Ready && pending.Status.Ready)
		default:
			st.Ready = true
			_ = r.persist(st, rec.Attempts)
		}
		cancel()
		return
	}

	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	// Every report for this attempt carries the generation being applied;
	// workspace drops reports for any other generation as stale.
	st := &nodev1.InstanceStatus{
		InstanceId:         id,
		Phase:              nodev1.InstancePhase_INSTANCE_PHASE_PREPARING,
		ObservedGeneration: spec.Generation,
		UpdatedAt:          timestamppb.Now(),
	}
	if err := r.persist(st, rec.Attempts); err != nil {
		return
	}

	drvSpec := &runtime.Spec{
		Full:       spec,
		InstanceID: id,
		Generation: spec.Generation,
		Desired:    desiredString(spec),
	}
	code := "agent.ensure_failed"
	run := func(activationCtx context.Context) error {
		telemetry.Log(activationCtx, slog.LevelInfo, "preparing instance")
		prepCtx, cancelPrep := context.WithTimeout(activationCtx, r.PrepareTimeout)
		err := r.Driver.Ensure(prepCtx, drvSpec, &runtime.Rendered{})
		cancelPrep()
		if err != nil {
			return err
		}
		if spec.GetStorage() != nil && spec.Storage.SchemaVersion > 0 {
			st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_MIGRATING
			st.UpdatedAt = timestamppb.Now()
			if err := r.persist(st, rec.Attempts); err != nil {
				return err
			}
		}
		if spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING {
			code = "agent.start_failed"
			st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_STARTING
			st.UpdatedAt = timestamppb.Now()
			if err := r.persist(st, rec.Attempts); err != nil {
				return err
			}
			startCtx, cancelStart := context.WithTimeout(activationCtx, r.StartTimeout)
			err := r.Driver.Start(startCtx, id)
			cancelStart()
			return err
		}
		return r.Driver.Stop(activationCtx, id, 5*time.Second)
	}
	var err error
	if r.Activation != nil {
		err = r.Activation.Activate(ctx, spec, run)
	} else {
		err = run(ctx)
	}
	if err != nil {
		telemetry.Log(ctx, slog.LevelWarn, "runtime activation failed", "error", telemetry.Mask(err.Error()))
		r.fail(id, spec.Generation, rec.Attempts, code, err.Error(), true)
		return
	}
	switch spec.Desired {
	case nodev1.DesiredState_DESIRED_STATE_RUNNING:
		telemetry.Log(ctx, slog.LevelInfo, "instance started")
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
		st.Ready = true
		st.ObservedGeneration = spec.Generation
		st.StartedAt = timestamppb.Now()
		st.UpdatedAt = timestamppb.Now()
		_ = r.persist(st, 0)
	case nodev1.DesiredState_DESIRED_STATE_STOPPED:
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_STOPPED
		st.Ready = false
		st.ObservedGeneration = spec.Generation
		st.UpdatedAt = timestamppb.Now()
		_ = r.persist(st, 0)
	default:
		st.ObservedGeneration = spec.Generation
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_STOPPED
		st.UpdatedAt = timestamppb.Now()
		_ = r.persist(st, 0)
	}
}

func (r *Reconciler) recoverRunning(ctx context.Context, rec state.ActualRecord, alreadyReady bool) {
	previous := rec.Status
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		r.failRecovery(ctx, rec, "runtime health recovery timed out")
		return
	}
	st := cloneStatus(previous)
	st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_STARTING
	st.Ready = false
	st.Error = nil
	st.UpdatedAt = timestamppb.Now()
	if !alreadyReady {
		if err := r.persist(st, rec.Attempts); err != nil {
			return
		}
	}
	run := func(ctx context.Context) error {
		startCtx, cancel := context.WithTimeout(ctx, r.StartTimeout)
		defer cancel()
		return r.Driver.Start(startCtx, st.InstanceId)
	}
	var err error
	if r.Activation != nil {
		err = r.Activation.Recover(ctx, st.InstanceId, st.ObservedGeneration, run)
	} else {
		err = run(ctx)
	}
	if err != nil {
		r.failRecovery(ctx, rec, "runtime health recovery failed")
		return
	}
	st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
	if previous.Phase == nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART || len(previous.RestartReasons) != 0 {
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
	}
	st.Ready = true
	st.UpdatedAt = timestamppb.Now()
	// Start may merely rediscover an existing container. Preserve its start time.
	_ = r.persist(st, 0)
}

func (r *Reconciler) failRecovery(ctx context.Context, rec state.ActualRecord, message string) {
	telemetry.Log(ctx, slog.LevelWarn, "runtime health recovery failed", "error", telemetry.Mask(message))
	attempts := rec.Attempts + 1
	st := cloneStatus(rec.Status)
	st.Phase, st.Ready = nodev1.InstancePhase_INSTANCE_PHASE_FAILED, false
	st.UpdatedAt = timestamppb.Now()
	st.Error = &nodev1.InstanceError{Code: "agent.start_failed", Message: message, Retryable: attempts < maxFailAttempts, Attempts: attempts}
	_ = r.persist(st, attempts)
}

func (r *Reconciler) handleDelete(ctx context.Context, id string, del *nodev1.DeleteInstance) {
	rec, _ := r.Store.GetActual(id)
	st := &nodev1.InstanceStatus{
		InstanceId: id,
		Phase:      nodev1.InstancePhase_INSTANCE_PHASE_DELETING,
		UpdatedAt:  timestamppb.Now(),
	}
	if rec.Status != nil {
		st.ObservedGeneration = rec.Status.ObservedGeneration
	}
	_ = r.persist(st, rec.Attempts)
	if err := r.Driver.Stop(ctx, id, 5*time.Second); err != nil {
		r.fail(id, st.ObservedGeneration, rec.Attempts, "agent.start_failed", err.Error(), true)
		return
	}
	if err := r.Driver.Remove(ctx, id); err != nil {
		r.fail(id, st.ObservedGeneration, rec.Attempts, "agent.ensure_failed", err.Error(), true)
		return
	}
	if err := r.Store.DeleteDesired(id); err != nil {
		r.fail(id, st.ObservedGeneration, rec.Attempts, "agent.ensure_failed", err.Error(), true)
		return
	}
	_ = del
	st = &nodev1.InstanceStatus{
		InstanceId: id,
		Phase:      nodev1.InstancePhase_INSTANCE_PHASE_ABSENT,
		UpdatedAt:  timestamppb.Now(),
	}
	r.emit(st)
}

func (r *Reconciler) fail(id string, generation int64, attempts uint32, code, msg string, retryable bool) {
	attempts++
	st := &nodev1.InstanceStatus{
		InstanceId:         id,
		Phase:              nodev1.InstancePhase_INSTANCE_PHASE_FAILED,
		ObservedGeneration: generation,
		UpdatedAt:          timestamppb.Now(),
		Error: &nodev1.InstanceError{
			Code:      code,
			Message:   msg,
			Retryable: retryable && attempts < maxFailAttempts,
			Attempts:  attempts,
		},
	}
	_ = r.persist(st, attempts)
}

func (r *Reconciler) persist(st *nodev1.InstanceStatus, attempts uint32) error {
	if err := r.Store.PutActual(state.ActualRecord{Status: st, Attempts: attempts}); err != nil {
		r.Log.Warn("persist runtime status failed", "instance_id", st.InstanceId, "error", err)
		return err
	}
	r.emit(st)
	return nil
}

func (r *Reconciler) emit(st *nodev1.InstanceStatus) {
	if r.Sink != nil && st != nil {
		// The worker reuses st in later transitions. Copy before handing it to
		// another goroutine; cloning at stream.Send would already be too late.
		r.Sink(cloneStatus(st))
	}
}

func desiredPhase(spec *nodev1.InstanceSpec) nodev1.InstancePhase {
	if spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING {
		return nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
	}
	return nodev1.InstancePhase_INSTANCE_PHASE_STOPPED
}

func desiredString(spec *nodev1.InstanceSpec) string {
	if spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING {
		return "running"
	}
	return "stopped"
}

func needsRestart(prev *nodev1.InstanceStatus, spec *nodev1.InstanceSpec) bool {
	_ = prev
	// A1 heuristic: any generation bump while running requires restart decision from workspace.
	return spec.Generation > 0
}

func restartReasons(_ *nodev1.InstanceSpec) []nodev1.RestartReason {
	return []nodev1.RestartReason{nodev1.RestartReason_RESTART_REASON_CONFIG}
}

func cloneStatus(st *nodev1.InstanceStatus) *nodev1.InstanceStatus {
	if st == nil {
		return nil
	}
	return proto.Clone(st).(*nodev1.InstanceStatus)
}
