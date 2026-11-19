package reconciler

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
)

type resourceObserver interface {
	ResourceObservations(context.Context, string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error)
	ResourceState(string) ([]*resourcev1.UnifiedInstallationReport, *resourcev1.UnifiedGuardianState, error)
}

func (r *Reconciler) guardResources(ctx context.Context, spec *nodev1.InstanceSpec) (bool, bool) {
	guard, ok := r.Driver.(interface {
		GuardResources(context.Context, *nodev1.InstanceSpec) error
	})
	if !ok {
		if len(spec.GetUnifiedResources()) > 0 {
			r.fail(spec.GetInstanceId(), spec.GetGeneration(), 0, "agent.ensure_failed", "trusted unified guardian unavailable", true)
			return false, false
		}
		return true, false
	}
	if err := guard.GuardResources(ctx, spec); err != nil {
		if errors.Is(err, runtime.ErrUnifiedPrivateOnly) {
			// Isolation already stopped and cleared shared content. Continue the
			// desired RUNNING reconciliation so private-only recovery is automatic.
			return true, true
		} else {
			r.fail(spec.GetInstanceId(), spec.GetGeneration(), 0, "agent.ensure_failed", err.Error(), true)
		}
		return false, false
	}
	return true, false
}

func (r *Reconciler) enrichResources(ctx context.Context, status *nodev1.InstanceStatus) {
	observer, ok := r.Driver.(resourceObserver)
	if !ok {
		return
	}
	var reports []*resourcev1.UnifiedInstallationReport
	var guardian *resourcev1.UnifiedGuardianState
	var err error
	if status.GetReady() {
		reports, guardian, err = observer.ResourceObservations(ctx, status.GetInstanceId())
	} else {
		reports, guardian, err = observer.ResourceState(status.GetInstanceId())
	}
	status.UnifiedResourceReports, status.UnifiedGuardian = reports, guardian
	if err != nil {
		status.Ready = false
		status.Error = &nodev1.InstanceError{Code: "agent.ensure_failed", Message: "native resource observation unavailable", Retryable: true}
	}
	filterAppliedReports(status)
}

func (r *Reconciler) enrichPersistedResources(status *nodev1.InstanceStatus) {
	if observer, ok := r.Driver.(resourceObserver); ok {
		reports, guardian, err := observer.ResourceState(status.GetInstanceId())
		status.UnifiedResourceReports, status.UnifiedGuardian = reports, guardian
		if err != nil {
			status.Ready = false
			status.Error = &nodev1.InstanceError{Code: "agent.ensure_failed", Message: "trusted resource state unavailable", Retryable: true}
		}
	}
	filterAppliedReports(status)
}

func filterAppliedReports(status *nodev1.InstanceStatus) {
	guardian := status.GetUnifiedGuardian()
	status.UnifiedResourceReports = slices.DeleteFunc(status.UnifiedResourceReports, func(report *resourcev1.UnifiedInstallationReport) bool {
		return report.GetState() == "applied" && (!status.GetReady() || status.GetObservedGeneration() <= 0 ||
			guardian.GetState() != "active" || guardian.GetGeneration() != uint64(status.GetObservedGeneration()) ||
			report.GetObservedGeneration() != uint64(status.GetObservedGeneration()) ||
			report.GetGeneration() != uint64(status.GetObservedGeneration()) ||
			report.GetSelectionRevision() != guardian.GetSelectionRevision())
	})
}

// READY for a signed runtime needs the current committed endpoint, not just
// a successful Start or a cached resource observation.
func (r *Reconciler) checkCommittedReady(ctx context.Context, status *nodev1.InstanceStatus) error {
	if r.Activation == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	actual, err := r.Driver.Inspect(ctx, status.InstanceId)
	if err != nil {
		return err
	}
	if !actual.Running || !actual.Ready || actual.Generation != status.ObservedGeneration {
		return errors.New("committed runtime endpoint unavailable")
	}
	return ctx.Err()
}

func (r *Reconciler) guardReady(ctx context.Context, status *nodev1.InstanceStatus) {
	if !status.Ready || r.checkCommittedReady(ctx, status) == nil {
		return
	}
	markEndpointUnavailable(status)
}

func markEndpointUnavailable(status *nodev1.InstanceStatus) {
	status.Ready = false
	status.Phase = nodev1.InstancePhase_INSTANCE_PHASE_FAILED
	status.Error = &nodev1.InstanceError{Code: "agent.start_failed", Message: "committed runtime endpoint unavailable", Retryable: true}
	filterAppliedReports(status)
}

func (r *Reconciler) commitStatus(ctx context.Context, st *nodev1.InstanceStatus, save func() error) error {
	r.guardReady(ctx, st)
	if err := save(); err != nil {
		return err
	}
	durableReady := st.Ready
	r.guardReady(ctx, st)
	if r.SyncEndpoint != nil {
		if accepted := r.SyncEndpoint(st); st.Ready && !accepted {
			markEndpointUnavailable(st)
		}
		// Ingress synchronization may have waited on the Engine or bridge.
		r.guardReady(ctx, st)
	}
	if durableReady && !st.Ready {
		if err := save(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.emit(st)
	return nil
}
