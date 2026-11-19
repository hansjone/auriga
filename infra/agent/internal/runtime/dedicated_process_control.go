package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

// The fixed prepare → main-start transaction and out-of-band guardian cleanup
// share this boundary. Waiting honors the existing caller deadline. It is not
// a new scheduler, task slot or container budget.
type poolProcessControl struct {
	gate  chan struct{}
	users int
}

func (d *Docker) lockPoolProcess(ctx context.Context, id string) (func(), error) {
	if err := storage.ValidateInstanceID(id); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.poolControls == nil {
		d.poolControls = map[string]*poolProcessControl{}
	}
	gate := d.poolControls[id]
	if gate == nil {
		gate = &poolProcessControl{gate: make(chan struct{}, 1)}
		d.poolControls[id] = gate
	}
	gate.users++
	d.mu.Unlock()
	drop := func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		gate.users--
		if gate.users == 0 {
			delete(d.poolControls, id)
		}
	}
	select {
	case gate.gate <- struct{}{}:
		return func() { <-gate.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func (d *Docker) preparationForCleanup(id string) (*dedicatedLaunchState, *dedicatedHelperJournal, error) {
	if !poolUUID.MatchString(id) || !filepath.IsAbs(d.DataRoot) {
		return nil, nil, nil
	}
	root := filepath.Join(d.DataRoot, "pools")
	file, err := (&storage.PoolLayout{Root: root}).Path(id, "control", "dedicated-launch-state")
	if err != nil {
		return nil, nil, err
	}
	if err = storage.CheckNoSymlink(root, file); err != nil {
		return nil, nil, err
	}
	var launch dedicatedLaunchState
	if err = unifiedRead(file, &launch); os.IsNotExist(err) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	if launch.Binding == nil || launch.Binding.PhysicalInstanceId != id || !validPoolBinding(launch.Binding) || !poolUUID.MatchString(launch.AttemptID) {
		return nil, nil, fmt.Errorf("invalid dedicated cleanup binding")
	}
	base, err := (&storage.PoolLayout{Root: root}).Path(id, "control", "dedicated-preparations", launch.AttemptID)
	if err != nil {
		return nil, nil, err
	}
	if launch.Meta != filepath.Join(base, "input") || launch.Output != filepath.Join(base, "output") {
		return nil, nil, fmt.Errorf("dedicated cleanup paths changed")
	}
	if launch.Candidate != (state.ReleaseCandidate{}) {
		if d.Activation == nil || d.Activation.Store == nil {
			return nil, nil, fmt.Errorf("dedicated cleanup release state unavailable")
		}
		if err = d.Activation.Store.CheckReleaseExecution(launch.Candidate); err != nil && !errors.Is(err, state.ErrReleaseRollback) && !errors.Is(err, state.ErrReleaseConflict) {
			return nil, nil, err
		}
	}
	var journal dedicatedHelperJournal
	if err = unifiedRead(filepath.Join(base, "helper-state"), &journal); os.IsNotExist(err) {
		return &launch, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	if journal.AttemptID != launch.AttemptID || journal.ImageID != launch.ImageID || journal.Candidate != launch.Candidate {
		return nil, nil, fmt.Errorf("dedicated helper cleanup journal mismatch")
	}
	return &launch, &journal, nil
}

func (d *Docker) inspectDedicatedCleanup(ctx context.Context, id string, launch *dedicatedLaunchState, journal *dedicatedHelperJournal) (map[string]any, error) {
	ref := "auriga-ded-prepare-" + launch.AttemptID
	if journal != nil && journal.ContainerID != "" {
		if !containerIDPattern.MatchString(journal.ContainerID) {
			return nil, fmt.Errorf("invalid dedicated helper CID")
		}
		ref = journal.ContainerID
	}
	actual, err := d.Client.ContainerInspect(ctx, ref)
	if err != nil || actual == nil {
		return actual, err
	}
	cfg, _ := actual["Config"].(map[string]any)
	labels, _ := cfg["Labels"].(map[string]any)
	if !containerIDPattern.MatchString(stringValue(actual["Id"])) || (journal != nil && journal.ContainerID != "" && actual["Id"] != journal.ContainerID) || actual["Image"] != launch.ImageID || labels[labelInstance] != id || labels[labelManaged] != "true" || labels["auriga.dedicated-preparation"] != launch.AttemptID || !sameStrings(cfg["Cmd"], []string{"/run/auriga/dedicated-prepare.mjs"}) {
		return nil, fmt.Errorf("dedicated helper cleanup ownership mismatch")
	}
	mounts, _ := actual["Mounts"].([]any)
	owned := false
	for _, row := range mounts {
		mount, _ := row.(map[string]any)
		if mount["Destination"] == "/run/auriga" && mount["Source"] == launch.Meta && mount["Type"] == "bind" && mount["RW"] == false {
			owned = true
		}
	}
	if !owned {
		return nil, fmt.Errorf("dedicated helper cleanup input mismatch")
	}
	if journal != nil {
		raw, err := dedicatedRead(filepath.Join(d.DataRoot, "pools"), filepath.Join(launch.Meta, "dedicated-input.json"), 1<<20)
		if err != nil || udigest(raw) != journal.InputDigest {
			return nil, fmt.Errorf("dedicated helper cleanup seal mismatch")
		}
	}
	return actual, nil
}

// A persisted unresolved release intent wins even over an absent/stopped
// container: an earlier Engine Create/Start may still commit late. Only the
// existing explicit protected recovery can clear that intent.
func (d *Docker) dedicatedHelperStopped(ctx context.Context, id string) error {
	launch, journal, err := d.preparationForCleanup(id)
	if err != nil || launch == nil {
		return err
	}
	actual, err := d.inspectDedicatedCleanup(ctx, id, launch, journal)
	if err != nil {
		return err
	}
	if actual != nil {
		return fmt.Errorf("dedicated preparation container not confirmed absent")
	}
	return nil
}

func (d *Docker) stopDedicatedHelper(ctx context.Context, id string) error {
	launch, journal, err := d.preparationForCleanup(id)
	if err != nil || launch == nil {
		return err
	}
	actual, err := d.inspectDedicatedCleanup(ctx, id, launch, journal)
	if err != nil || actual == nil {
		return err
	}
	cid := stringValue(actual["Id"])
	if err = d.Client.ContainerStop(ctx, cid, 5); err != nil {
		return err
	}
	actual, err = d.inspectDedicatedCleanup(ctx, id, launch, journal)
	if err != nil {
		return err
	}
	if actual != nil {
		live, _ := actual["State"].(map[string]any)
		if live["Running"] != false || live["Pid"] != float64(0) {
			return fmt.Errorf("dedicated helper stop unconfirmed")
		}
		if err = d.Client.ContainerRemove(ctx, cid, false); err != nil {
			return err
		}
	}
	return d.dedicatedHelperStopped(ctx, id)
}
