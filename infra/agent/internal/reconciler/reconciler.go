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
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxFailAttempts = 5

// StatusSink receives status updates after each reconcile step.
type StatusSink func(st *nodev1.InstanceStatus)

// Reconciler applies desired specs via a Driver, one goroutine per instance.
type Reconciler struct {
	Store  *state.Store
	Driver runtime.Driver
	Log    *slog.Logger
	Sink   StatusSink

	mu      sync.Mutex
	workers map[string]chan workItem
	sem     chan struct{}
	maxPrep int
	wg      sync.WaitGroup
	closed  bool
}

type workItem struct {
	kind string // apply | delete | tick
	spec *nodev1.InstanceSpec
	del  *nodev1.DeleteInstance
}

// New creates a reconciler. maxConcurrentPrepare defaults to 4.
func New(store *state.Store, driver runtime.Driver, log *slog.Logger, maxConcurrentPrepare int) *Reconciler {
	if maxConcurrentPrepare < 1 {
		maxConcurrentPrepare = 4
	}
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		Store:   store,
		Driver:  driver,
		Log:     log,
		workers: make(map[string]chan workItem),
		sem:     make(chan struct{}, maxConcurrentPrepare),
		maxPrep: maxConcurrentPrepare,
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
func (r *Reconciler) ApplySpec(_ context.Context, spec *nodev1.InstanceSpec) error {
	return r.Apply(spec)
}

// Apply queues an ApplySpec (persists desired first).
func (r *Reconciler) Apply(spec *nodev1.InstanceSpec) error {
	if spec == nil || spec.InstanceId == "" {
		return fmt.Errorf("missing instance_id")
	}
	if err := r.Store.PutDesired(spec); err != nil {
		return err
	}
	r.enqueue(spec.InstanceId, workItem{kind: "apply", spec: spec})
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
			r.enqueue(id, workItem{kind: "apply", spec: spec})
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
	for _, rec := range recs {
		if rec.Status != nil {
			out = append(out, rec.Status)
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
		switch item.kind {
		case "delete":
			r.handleDelete(ctx, id, item.del)
		default:
			r.handleApply(ctx, id, item.spec)
		}
	}
}

func (r *Reconciler) handleApply(ctx context.Context, id string, spec *nodev1.InstanceSpec) {
	if spec == nil {
		var err error
		spec, err = r.Store.GetDesired(id)
		if err != nil || spec == nil {
			return
		}
	}
	rec, _ := r.Store.GetActual(id)
	if rec.Status != nil &&
		rec.Status.ObservedGeneration == spec.Generation &&
		rec.Status.Phase == desiredPhase(spec) &&
		(rec.Status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING || rec.Status.Ready) {
		// After process restart the driver may be empty — re-drive if needed.
		st, _ := r.Driver.Inspect(ctx, id)
		need := spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING && !st.Running
		need = need || (spec.Desired == nodev1.DesiredState_DESIRED_STATE_STOPPED && st.Running)
		if !need {
			r.emit(rec.Status)
			return
		}
	}
	// Spec changed while still desired=running → PendingRestart (workspace decides when to restart).
	// desired=stopped falls through and stops the instance.
	if rec.Status != nil &&
		spec.Desired == nodev1.DesiredState_DESIRED_STATE_RUNNING &&
		rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING &&
		rec.Status.ObservedGeneration > 0 &&
		rec.Status.ObservedGeneration < spec.Generation &&
		needsRestart(rec.Status, spec) {
		st := cloneStatus(rec.Status)
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_PENDING_RESTART
		st.RestartReasons = restartReasons(spec)
		st.UpdatedAt = timestamppb.Now()
		r.persist(st, rec.Attempts)
		return
	}

	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	st := &nodev1.InstanceStatus{
		InstanceId: id,
		Phase:      nodev1.InstancePhase_INSTANCE_PHASE_PREPARING,
		UpdatedAt:  timestamppb.Now(),
	}
	r.persist(st, rec.Attempts)

	drvSpec := &runtime.Spec{
		InstanceID: id,
		Generation: spec.Generation,
		Desired:    desiredString(spec),
	}
	if err := r.Driver.Ensure(ctx, drvSpec, &runtime.Rendered{}); err != nil {
		r.fail(id, rec.Attempts, "agent.ensure_failed", err.Error(), true)
		return
	}

	if spec.GetStorage() != nil && spec.GetStorage().SchemaVersion > 0 {
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_MIGRATING
		st.UpdatedAt = timestamppb.Now()
		r.persist(st, rec.Attempts)
	}

	switch spec.Desired {
	case nodev1.DesiredState_DESIRED_STATE_RUNNING:
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_STARTING
		st.UpdatedAt = timestamppb.Now()
		r.persist(st, rec.Attempts)
		if err := r.Driver.Start(ctx, id); err != nil {
			r.fail(id, rec.Attempts, "agent.start_failed", err.Error(), true)
			return
		}
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
		st.Ready = true
		st.ObservedGeneration = spec.Generation
		st.StartedAt = timestamppb.Now()
		st.UpdatedAt = timestamppb.Now()
		r.persist(st, 0)
	case nodev1.DesiredState_DESIRED_STATE_STOPPED:
		_ = r.Driver.Stop(ctx, id, 5*time.Second)
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_STOPPED
		st.Ready = false
		st.ObservedGeneration = spec.Generation
		st.UpdatedAt = timestamppb.Now()
		r.persist(st, 0)
	default:
		st.ObservedGeneration = spec.Generation
		st.Phase = nodev1.InstancePhase_INSTANCE_PHASE_STOPPED
		st.UpdatedAt = timestamppb.Now()
		r.persist(st, 0)
	}
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
	r.persist(st, rec.Attempts)
	_ = r.Driver.Stop(ctx, id, 5*time.Second)
	_ = r.Driver.Remove(ctx, id)
	if del != nil && del.PurgeData {
		_ = r.Store.DeleteDesired(id)
	} else {
		_ = r.Store.DeleteDesired(id)
	}
	st = &nodev1.InstanceStatus{
		InstanceId: id,
		Phase:      nodev1.InstancePhase_INSTANCE_PHASE_ABSENT,
		UpdatedAt:  timestamppb.Now(),
	}
	r.emit(st)
}

func (r *Reconciler) fail(id string, attempts uint32, code, msg string, retryable bool) {
	attempts++
	st := &nodev1.InstanceStatus{
		InstanceId: id,
		Phase:      nodev1.InstancePhase_INSTANCE_PHASE_FAILED,
		UpdatedAt:  timestamppb.Now(),
		Error: &nodev1.InstanceError{
			Code:      code,
			Message:   msg,
			Retryable: retryable && attempts < maxFailAttempts,
			Attempts:  attempts,
		},
	}
	r.persist(st, attempts)
}

func (r *Reconciler) persist(st *nodev1.InstanceStatus, attempts uint32) {
	_ = r.Store.PutActual(state.ActualRecord{Status: st, Attempts: attempts})
	r.emit(st)
}

func (r *Reconciler) emit(st *nodev1.InstanceStatus) {
	if r.Sink != nil && st != nil {
		r.Sink(st)
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
