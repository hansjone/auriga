package reconciler

import (
	"context"
	"errors"

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
}
