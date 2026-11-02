package runtime

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/hansjone/auriga/infra/agent/internal/state"
)

// RecoverInterruptedRuntime performs no Engine mutation. The operator must stop
// the original agent and affected instances first. Empty expectation is inspect
// only; apply rechecks both Engine evidence and the exact offline-state digest.
func (d *Docker) RecoverInterruptedRuntime(ctx context.Context, store *state.Store, instance, stoppedAgent, expect string) (state.RecoverySnapshot, error) {
	var zero state.RecoverySnapshot
	if d.Activation == nil || d.Activation.Store != store || !containerIDPattern.MatchString(stoppedAgent) {
		return zero, fmt.Errorf("recovery requires original store and exact stopped agent container ID")
	}
	snapshot, err := store.InspectInterruptedExecution(instance)
	if err != nil {
		return zero, err
	}
	verify := func(ctx context.Context, snapshot state.RecoverySnapshot) error {
		agent, err := d.Client.ContainerInspect(ctx, stoppedAgent)
		if err != nil {
			return err
		}
		if agent == nil || agent["Id"] != stoppedAgent || !definitelyStopped(agent) {
			return fmt.Errorf("original agent container is not definitely stopped")
		}
		cfg, _ := agent["Config"].(map[string]any)
		labels, _ := cfg["Labels"].(map[string]any)
		if labels["com.docker.compose.service"] != "agent" {
			return fmt.Errorf("stopped container is not the original compose agent")
		}
		mounts, _ := agent["Mounts"].([]any)
		bound := false
		for _, raw := range mounts {
			m, _ := raw.(map[string]any)
			if m["Type"] == "bind" && m["Source"] == filepath.Clean(d.DataRoot) && m["Destination"] == filepath.Clean(d.DataRoot) {
				bound = true
			}
		}
		if !bound {
			return fmt.Errorf("stopped agent does not bind the original data root")
		}
		known := map[string]state.ExecutionRecord{}
		present := map[string]bool{}
		for _, r := range snapshot.Executions {
			known[r.ContainerID] = r
			if r.Candidate.Partition != snapshot.Candidate.Partition {
				continue
			}
			if r.InstanceID == instance {
				image, candidate, err := d.Activation.verify(r.DSH)
				if err != nil {
					return err
				}
				if candidate != snapshot.Candidate || !executionMatchesImage(&r, image, candidate) {
					return fmt.Errorf("original signed execution does not match committed recovery candidate")
				}
			}
			insp, err := d.Client.ContainerInspect(ctx, r.ContainerID)
			if err != nil {
				return err
			}
			if insp == nil {
				byName, err := d.Client.ContainerInspect(ctx, containerName(r.InstanceID))
				if err != nil {
					return err
				}
				if byName != nil {
					return fmt.Errorf("actual execution name was rebound")
				}
				continue
			}
			insp, err = d.verifyContainer(ctx, &r)
			if err != nil {
				return err
			}
			present[r.ContainerID] = true
			if !definitelyStopped(insp) {
				return fmt.Errorf("affected actual container is not definitely stopped")
			}
		}
		all, err := d.Client.ContainerList(ctx, true, map[string][]string{"label": {"auriga.managed=true"}})
		if err != nil {
			return err
		}
		for _, item := range all {
			id, _ := item["Id"].(string)
			r, ok := known[id]
			labels, _ := item["Labels"].(map[string]any)
			if !ok || labels[labelManaged] != "true" || labels[labelInstance] != r.InstanceID || labels[labelGeneration] != fmt.Sprint(r.Generation) || (r.Candidate.Partition == snapshot.Candidate.Partition && !present[id]) {
				return fmt.Errorf("unrecorded or changed managed container prevents protected recovery")
			}
			delete(present, id)
		}
		if len(present) != 0 {
			return fmt.Errorf("managed container inventory changed during recovery")
		}
		return ctx.Err()
	}
	if err := verify(ctx, snapshot); err != nil {
		return zero, err
	}
	if expect != "" {
		if err := store.RecoverInterruptedExecution(ctx, instance, expect, verify); err != nil {
			return zero, err
		}
	}
	return snapshot, nil
}

func definitelyStopped(insp map[string]any) bool {
	s, _ := insp["State"].(map[string]any)
	running, ok := s["Running"].(bool)
	return ok && !running && s["Restarting"] == false && s["Paused"] == false && s["Dead"] == false && (s["Status"] == "exited" || s["Status"] == "created")
}
