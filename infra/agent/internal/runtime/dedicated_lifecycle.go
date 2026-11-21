package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	agentstate "github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// The gate is durable and separate from Active: existing ordinary roots may
// drain normally, but no new root/helper/process can obtain a second budget.
func (m *SharedPools) dedicatedMaintenance(b *nodev1.SharedDshBinding, begin bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, path, err := m.load(b.PhysicalInstanceId)
	if err != nil {
		return err
	}
	if b.Mode != nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED || len(s.Bindings) != 1 || !proto.Equal(s.Bindings[b.MemberId], b) || s.Contaminated || poolStopPending(s) || m.stopping[b.PhysicalInstanceId] > 0 || memberOperationBlocked(s, b) || proto.Equal(s.Quiesced[b.MemberId], b) {
		return fmt.Errorf("dedicated maintenance binding unavailable")
	}
	if s.DedicatedMaintenance != nil && !proto.Equal(s.DedicatedMaintenance, b) {
		return fmt.Errorf("dedicated maintenance binding changed")
	}
	if begin && s.DedicatedMaintenance == nil {
		s.DedicatedMaintenance = proto.Clone(b).(*nodev1.SharedDshBinding)
		if err = savePool(path, s); err != nil {
			return err
		}
	}
	if s.DedicatedMaintenance == nil {
		return fmt.Errorf("dedicated maintenance fence missing")
	}
	if len(s.Active) != 0 || m.inflight[b.PhysicalInstanceId+"/"+b.MemberId] {
		return fmt.Errorf("dedicated ordinary/Job Finish unconfirmed")
	}
	return nil
}

type dedicatedLaunchState struct {
	Binding                                                                *nodev1.SharedDshBinding
	Candidate                                                              agentstate.ReleaseCandidate
	AttemptID, IntentDigest, RestartedIntentDigest, PreviousHost, Phase    string
	ContainerID, Meta, Output, Cache, ProfileDigest, ConfigDigest, ImageID string
}

func (m *PersistentPools) dedicatedLaunchPath(pool string) (string, error) {
	return m.Pools.Layout.Path(pool, "control", "dedicated-launch-state")
}

func (m *PersistentPools) ensureDedicated(ctx context.Context, b *nodev1.SharedDshBinding, full *nodev1.InstanceSpec, uid int, members, skills, relay string, port uint32, restart *dedicatedReceipt) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if len(m.DedicatedProfile) == 0 || relay == "" {
		return fmt.Errorf("dedicated native profile/Host configuration required")
	}
	unlock, err := m.Driver.lockPoolProcess(ctx, b.PhysicalInstanceId)
	if err != nil {
		return err
	}
	defer unlock()
	bindings, views, err := m.snapshot(b.PhysicalInstanceId)
	if err != nil || len(bindings) != 1 || !proto.Equal(bindings[0], b) {
		return fmt.Errorf("dedicated singleton view required")
	}
	intent, ok := m.Resources.(interface {
		DedicatedIntent(string, *nodev1.SharedDshResourceView) (string, string, error)
	})
	refresh, refreshOK := m.Resources.(interface {
		Refresh(context.Context, *nodev1.SharedDshBinding) (*nodev1.SharedDshResourceView, error)
	})
	if !ok || !refreshOK {
		return fmt.Errorf("dedicated current intent authority required")
	}
	cache, intentDigest, err := intent.DedicatedIntent(b.PhysicalInstanceId, views[0])
	if err != nil {
		return err
	}
	path, err := m.dedicatedLaunchPath(b.PhysicalInstanceId)
	if err != nil {
		return err
	}
	var previous dedicatedLaunchState
	if err = unifiedRead(path, &previous); err != nil && !os.IsNotExist(err) {
		return err
	}
	record, err := m.Driver.Activation.Store.GetExecution(b.PhysicalInstanceId)
	if err != nil {
		return err
	}
	if previous.Binding != nil {
		if !proto.Equal(previous.Binding, b) || previous.Phase != "running" || record == nil || record.ContainerID != previous.ContainerID {
			return fmt.Errorf("dedicated previous execution/preparation unconfirmed")
		}
		actual, err := m.Driver.verifyContainer(ctx, record)
		if err != nil {
			return err
		}
		process, err := containerProcess(actual)
		if err != nil {
			return err
		}
		file, _ := m.Pools.Layout.Path(b.PhysicalInstanceId, "control", "process-state")
		var saved poolProcess
		if err = unifiedRead(file, &saved); err != nil || saved.Container != record.ContainerID || saved.PID != process.pid || !saved.StartedAt.Equal(process.startedAt) {
			return fmt.Errorf("dedicated process lifetime changed")
		}
		if restart == nil {
			if err = m.writeDedicatedPolicy(previous.Meta, views[0]); err != nil {
				return err
			}
			return m.Driver.Activation.Recover(ctx, b.PhysicalInstanceId, record.Generation, func(ctx context.Context) error { return m.Driver.Start(ctx, b.PhysicalInstanceId) })
		}
		if previous.RestartedIntentDigest == intentDigest || restart.Status != "restart-required" || restart.IntentDigest != intentDigest || restart.ViewID != views[0].ViewId || restart.HostInstanceID == previous.PreviousHost {
			return fmt.Errorf("dedicated restart already attempted or superseded")
		}
	} else if record != nil {
		return fmt.Errorf("dedicated existing process lacks its launch evidence")
	}
	if record != nil && (record.Generation != full.Generation || !proto.Equal(record.DSH, full.Dsh)) {
		return fmt.Errorf("dedicated original signed runtime changed")
	}
	m.closePoolIngress(b.PhysicalInstanceId)
	if err = m.Pools.dedicatedMaintenance(b, true); err != nil {
		return err
	}
	view := proto.Clone(views[0]).(*nodev1.SharedDshResourceView)
	fence := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Pools.dedicatedMaintenance(b, false); err != nil {
			return err
		}
		latest, err := refresh.Refresh(ctx, b)
		if err != nil || !proto.Equal(latest, view) {
			return fmt.Errorf("dedicated preparation resource view superseded")
		}
		if err = m.Resources.Verify(ctx, b.PhysicalInstanceId, []*nodev1.SharedDshResourceView{latest}); err != nil {
			return err
		}
		currentCache, current, err := intent.DedicatedIntent(b.PhysicalInstanceId, latest)
		if err != nil || current != intentDigest || currentCache != cache {
			return fmt.Errorf("dedicated preparation intent superseded")
		}
		return nil
	}
	if err = fence(); err != nil {
		return err
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	random[6] = (random[6] & 15) | 64
	random[8] = (random[8] & 63) | 128
	attempt := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	base, err := m.Pools.Layout.Path(b.PhysicalInstanceId, "control", "dedicated-preparations", attempt)
	if err != nil {
		return err
	}
	meta, output := filepath.Join(base, "input"), filepath.Join(base, "output")
	if err = os.MkdirAll(base, 0700); err != nil {
		return err
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return err
	}
	if err = m.chown(output, uid, uid); err != nil {
		return err
	}
	state := dedicatedLaunchState{Binding: proto.Clone(b).(*nodev1.SharedDshBinding), AttemptID: attempt, IntentDigest: intentDigest, Phase: "draining", Meta: meta, Output: output, Cache: cache, RestartedIntentDigest: previous.RestartedIntentDigest}
	if restart != nil {
		state.PreviousHost = restart.HostInstanceID
		state.RestartedIntentDigest = intentDigest
	}
	if err = unifiedAtomic(path, state); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			state.Phase = "closing"
			_ = unifiedAtomic(path, state)
		}
	}()
	if record != nil {
		if err = m.Driver.stopMain(ctx, b.PhysicalInstanceId, 5*time.Second); err != nil {
			return err
		}
		if stopped, err := m.Driver.mainProcessStopped(ctx, b.PhysicalInstanceId); err != nil || !stopped {
			return fmt.Errorf("dedicated old process stop unconfirmed")
		}
	}
	container := &PoolContainer{Mode: "dedicated", Members: members, Skills: skills, Meta: meta, Relay: relay, DedicatedPrepared: output, DedicatedCache: cache}
	if backend, ok := m.Pools.Layout.Quota.(interface {
		MountPool(context.Context, string) (storage.PoolMount, error)
	}); ok {
		fs, err := backend.MountPool(ctx, b.PhysicalInstanceId)
		if err != nil {
			return err
		}
		container.Volume, container.Device = "auriga-pool-"+b.PhysicalInstanceId, fs.Device
	}
	err = m.Driver.Activation.Activate(ctx, full, func(active context.Context) error {
		if err := fence(); err != nil {
			return err
		}
		scope, err := requireActivation(active, b.PhysicalInstanceId)
		if err != nil {
			return err
		}
		state.Candidate = scope.candidate
		if err = unifiedAtomic(path, state); err != nil {
			return err
		}
		if err = m.Driver.Client.PullImage(active, full.Dsh.Image); err != nil {
			return err
		}
		image, err := m.Driver.Client.ImageInspect(active, full.Dsh.Image)
		if err != nil {
			return err
		}
		imageID, _ := image["Id"].(string)
		if err = scope.pinEngineImage(imageID); err != nil {
			return err
		}
		bindingValue, err := sharednode.HostValue(b)
		if err != nil {
			return err
		}
		bindingBytes, _ := json.Marshal(bindingValue)
		viewValue, err := sharednode.HostValue(view)
		if err != nil {
			return err
		}
		viewBytes, _ := json.Marshal(viewValue)
		intentBytes, err := dedicatedRead(m.Pools.Layout.Root, filepath.Join(relay, "dedicated-resources.json"), 1<<20)
		if err != nil {
			return err
		}
		input := renderer.DedicatedPreparation{SchemaVersion: 1, AttemptID: attempt, Binding: bindingBytes, View: viewBytes, IntentDigest: intentDigest, ImageID: imageID}
		digest, err := renderer.RenderDedicatedPool(m.DedicatedProfile, b.PhysicalInstanceId, meta, port, input, intentBytes)
		if err != nil {
			return err
		}
		container.ProfileDigest = digest
		state.ProfileDigest, state.ImageID, state.Phase = digest, imageID, "preparing"
		if err = unifiedAtomic(path, state); err != nil {
			return err
		}
		if err = m.writeDedicatedPolicy(meta, view); err != nil {
			return err
		}
		spec := &Spec{Full: full, InstanceID: b.PhysicalInstanceId, Generation: full.Generation, Image: full.Dsh.Image, CPUMillis: full.GetResources().GetCpuMillis(), MemoryMB: full.GetResources().GetMemoryMb(), PIDs: full.GetResources().GetPids(), HostUID: uid, HomeBind: members, MetaBind: meta, ListenPort: port, Pool: container,
			Env: map[string]string{"AURIGA_MODE": "dedicated", "AURIGA_INSTANCE_ID": b.PhysicalInstanceId, "AURIGA_TENANT_ID": b.TenantId, "HOME": "/home/dsh/.host", "DSH_HOME": "/home/dsh/.host/.dsh", "DSH_TELEMETRY_DISABLED": "1", "DSH_TELEMETRY_MODE": "DISABLED", "COREPACK_ENABLE_NETWORK": "0"}}
		if err = m.Driver.prepareDedicated(active, spec, attempt, output, cache, fence); err != nil {
			return err
		}
		if err = fence(); err != nil {
			return err
		}
		state.ConfigDigest, err = VerifyDedicatedPrepared(m.Pools.Layout.Root, meta, output, view, intentDigest, attempt, imageID)
		if err != nil {
			return err
		}
		// The helper is confirmed absent. Remove its write ownership and freeze
		// the output before the runtime sees the ONLY mount of this directory.
		for _, name := range []string{"config.json", "manifest.json"} {
			file := filepath.Join(output, name)
			if err = m.chown(file, 0, 0); err != nil {
				return err
			}
			if err = os.Chmod(file, 0444); err != nil {
				return err
			}
		}
		if err = m.chown(output, 0, 0); err != nil {
			return err
		}
		if err = os.Chmod(output, 0555); err != nil {
			return err
		}
		state.Phase = "activating"
		if err = unifiedAtomic(path, state); err != nil {
			return err
		}
		if err = fence(); err != nil {
			return err
		}
		if err = m.Driver.Ensure(active, spec, &Rendered{Digest: digest}); err != nil {
			return err
		}
		if err = m.Driver.Start(active, b.PhysicalInstanceId); err != nil {
			return err
		}
		return fence()
	})
	if err != nil {
		return err
	}
	record, err = m.Driver.Activation.Store.GetExecution(b.PhysicalInstanceId)
	if err != nil || record == nil {
		return fmt.Errorf("dedicated new execution unavailable")
	}
	actual, err := m.Driver.verifyContainer(ctx, record)
	if err != nil {
		return err
	}
	process, err := containerProcess(actual)
	if err != nil {
		return err
	}
	file, err := m.Pools.Layout.Path(b.PhysicalInstanceId, "control", "process-state")
	if err != nil {
		return err
	}
	if err = unifiedAtomic(file, poolProcess{Container: record.ContainerID, PID: process.pid, StartedAt: process.startedAt}); err != nil {
		return err
	}
	state.ContainerID, state.Phase = record.ContainerID, "running"
	return unifiedAtomic(path, state)
}

func (m *PersistentPools) writeDedicatedPolicy(meta string, view *nodev1.SharedDshResourceView) error {
	now := time.Now().UTC()
	if clock, ok := m.Resources.(interface{ Now() time.Time }); ok {
		now = clock.Now()
	}
	policy := map[string]any{"schemaVersion": 1, "sharedPool": true, "newSessionRequired": false, "allowPersonalPlugins": false, "trustedTime": now.Format(time.RFC3339Nano)}
	var expires int64
	for _, resource := range view.Resources {
		if expires == 0 || resource.ExpiresAt < expires {
			expires = resource.ExpiresAt
		}
	}
	if expires != 0 {
		policy["leaseExpiresAt"] = time.Unix(expires, 0).UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return unifiedAtomicMode(filepath.Join(meta, "resource-session-policy.json"), raw, 0644)
}
