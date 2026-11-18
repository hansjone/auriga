package reconciler

import (
	"context"
	"slices"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

// UplinkExecution identifies an execution across the Session queue. It does
// not authorize sending anything: the live proof is checked at dequeue.
type UplinkExecution struct {
	instanceID  string
	containerID string
	generation  int64
}

// CaptureUplinkExecution snapshots identity inside the instance worker before
// another lifecycle operation can replace the container at the same generation.
func (r *Reconciler) CaptureUplinkExecution(id string, generation int64) UplinkExecution {
	if r.Activation == nil {
		return UplinkExecution{}
	}
	record, err := r.Store.GetExecution(id)
	if err != nil || record == nil || record.Phase != "started" || record.Generation != generation {
		return UplinkExecution{}
	}
	return UplinkExecution{instanceID: id, containerID: record.ContainerID, generation: generation}
}

func (r *Reconciler) uplinkReadyStatus(ctx context.Context, id string, expected UplinkExecution) *nodev1.InstanceStatus {
	if expected.instanceID != id || expected.containerID == "" || ctx.Err() != nil {
		return nil
	}
	actual, err := r.Store.GetActual(id)
	if err != nil || !actual.Status.GetReady() || actual.Status.GetObservedGeneration() != expected.generation {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	live, err := runtime.InspectReadOnly(ctx, r.Driver, id)
	if err != nil || ctx.Err() != nil || !live.Ready || !live.Running ||
		live.ContainerID != expected.containerID || live.Generation != expected.generation {
		return nil
	}
	return actual.Status
}

// StatusForUplink rechecks READY and applied immediately before sending. It
// keeps the original timestamp and never writes durable state or local ingress.
func (r *Reconciler) StatusForUplink(ctx context.Context, status *nodev1.InstanceStatus, expected UplinkExecution) *nodev1.InstanceStatus {
	if r.Activation == nil || !status.GetReady() {
		return status
	}
	if status.GetObservedGeneration() == expected.generation {
		if current := r.uplinkReadyStatus(ctx, status.GetInstanceId(), expected); current != nil {
			return filterQueuedApplied(status, current)
		}
	}
	status = cloneStatus(status)
	markEndpointUnavailable(status)
	return status
}

func filterQueuedApplied(status, current *nodev1.InstanceStatus) *nodev1.InstanceStatus {
	stale := func(report *resourcev1.UnifiedInstallationReport) bool {
		if report.GetState() != "applied" {
			return false
		}
		guardian := current.GetUnifiedGuardian()
		return guardian.GetState() != "active" || guardian.GetGeneration() != report.GetObservedGeneration() ||
			guardian.GetSelectionRevision() != report.GetSelectionRevision() ||
			!slices.ContainsFunc(current.GetUnifiedResourceReports(), func(observed *resourcev1.UnifiedInstallationReport) bool {
				return observed.GetState() == "applied" && observed.GetGeneration() == report.GetGeneration() &&
					observed.GetObservedGeneration() == report.GetObservedGeneration() &&
					observed.GetSelectionRevision() == report.GetSelectionRevision() &&
					observed.GetInstallationRevision() == report.GetInstallationRevision() &&
					proto.Equal(observed.GetIdentity(), report.GetIdentity())
			})
	}
	if slices.ContainsFunc(status.GetUnifiedResourceReports(), stale) {
		status = cloneStatus(status)
		status.UnifiedResourceReports = slices.DeleteFunc(status.UnifiedResourceReports, stale)
	}
	return status
}

// ResultForUplink rechecks a successful Restart before it is sent. The durable
// completion is an action checkpoint and remains available to an idempotent replay.
func (r *Reconciler) ResultForUplink(ctx context.Context, result *nodev1.CommandResult, expected UplinkExecution) *nodev1.CommandResult {
	if r.Activation == nil || !result.GetOk() {
		return result
	}
	record, err := r.Store.GetRestartCommand(result.GetCommandId())
	if err == nil && record != nil && record.Complete && record.Generation == expected.generation &&
		r.uplinkReadyStatus(ctx, result.GetInstanceId(), expected) != nil {
		return result
	}
	return commandFailure(&nodev1.Command{CommandId: result.GetCommandId(), InstanceId: result.GetInstanceId()}, "agent.start_failed", true)
}
