package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// EmptyPoolResources admits only an explicitly empty platform view received on
// NodeControl. Nonempty views require the signed-resource materializer adapter.
type EmptyPoolResources struct{ Root string }

func (v *EmptyPoolResources) Verify(_ context.Context, pool string, views []*nodev1.SharedDshResourceView) error {
	for _, view := range views {
		if view == nil || view.Source != "platform" || len(view.Resources) != 0 {
			return fmt.Errorf("shared resource materializer is required")
		}
	}
	root, err := (&storage.PoolLayout{Root: v.Root}).Path(pool, "host", "platform-skills", "active")
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("empty platform view has unexpected mounted resources")
	}
	return nil
}

func (v *EmptyPoolResources) Cleanup(ctx context.Context, pool string) error {
	return v.Verify(ctx, pool, nil)
}

// PersistentPools joins real directory receipts, authoritative platform views,
// signed runtime activation, immutable configuration and a single actual process.
type PersistentPools struct {
	Pools            *SharedPools
	Driver           *Docker
	UIDs             *storage.UIDTable
	Template         *nodev1.InstanceSpec
	Profile, Ports   []byte
	DedicatedProfile []byte
	Resources        PoolResourceVerifier
	Publish          func(string, Endpoint)
	Unpublish        func(string)
	Report           func(*nodev1.SessionRequest)
	Memory           func(context.Context, string) (*nodev1.SharedDshPoolMemorySample, error)
	TaskAdmission    *PoolTaskAdmission
	ConfigureHost    func(context.Context, *nodev1.SharedDshBinding, int) (string, error)
	// Ownership boundary, like storage.Layout.Chown. Production defaults to
	// strict Lchown; synthetic non-root Engine fixtures record this operation.
	Chown  func(string, int, int) error
	mu     sync.Mutex
	views  map[string]*nodev1.SharedDshResourceView
	active map[string]bool
}

type poolProcess struct {
	Container string
	PID       int64
	StartedAt time.Time
}

func (m *PersistentPools) ResetViews() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.views = map[string]*nodev1.SharedDshResourceView{}
	m.active = map[string]bool{}
}

func (m *PersistentPools) View(ctx context.Context, view *nodev1.SharedDshResourceView) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if view == nil || !validPoolBinding(view.Binding) {
		return fmt.Errorf("invalid shared view")
	}
	check := CheckPoolViews([]*nodev1.SharedDshBinding{view.Binding}, []*nodev1.SharedDshResourceView{view}, time.Now().Unix())
	if check != nil && !errors.Is(check, observer.ErrPoolResourceExpired) {
		return check
	}
	if m.views == nil {
		m.views = map[string]*nodev1.SharedDshResourceView{}
	}
	// W deliberately resends stable views. Do not repeat all downloads and
	// Engine probes for each identical member frame; Watch still revalidates
	// signatures/leases independently, and reconnect clears this admission cache.
	if check == nil && m.active[view.Binding.PhysicalInstanceId] && proto.Equal(m.views[view.Binding.PlacementId], view) {
		return nil
	}
	m.views[view.Binding.PlacementId] = proto.Clone(view).(*nodev1.SharedDshResourceView)
	bindings, err := m.Pools.Bindings(view.Binding.PhysicalInstanceId)
	if err != nil || len(bindings) == 0 {
		return err
	}
	if check != nil {
		_, views, err := m.snapshot(view.Binding.PhysicalInstanceId)
		if err != nil {
			return err
		}
		watch := observer.PoolWatch{Pools: m.Pools, Verifier: m.Resources, Validate: CheckPoolViews}
		reports, impact, err := watch.Check(ctx, view.ViewId, bindings, views, time.Now())
		if m.Unpublish != nil {
			delete(m.active, view.Binding.PhysicalInstanceId)
			m.Unpublish(view.Binding.PhysicalInstanceId)
		}
		for _, report := range reports {
			m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshResourceReport{SharedDshResourceReport: report}})
		}
		if impact != nil {
			m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshGuardianImpact{SharedDshGuardianImpact: impact}})
		}
		if err != nil {
			return err
		}
		return check
	}
	return m.ensure(ctx, bindings[0])
}

func (m *PersistentPools) Prepare(ctx context.Context, command *nodev1.SharedDshRuntimeIntent) (*nodev1.SharedDshRuntimeReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, err := m.Pools.Apply(ctx, command)
	if command.Action == "stop-pool" {
		delete(m.active, command.Binding.PhysicalInstanceId)
		if m.Unpublish != nil {
			m.Unpublish(command.Binding.PhysicalInstanceId)
		}
		return receipt, err
	}
	if err != nil || receipt.Outcome != "confirmed" {
		return receipt, err
	}
	if err := m.ensure(ctx, command.Binding); err != nil {
		// The durable preparation remains retriable, while process admission stays closed.
		receipt.Outcome = "unconfirmed"
		return receipt, err
	}
	return receipt, nil
}

func (m *PersistentPools) snapshot(pool string) ([]*nodev1.SharedDshBinding, []*nodev1.SharedDshResourceView, error) {
	bindings, err := m.Pools.Bindings(pool)
	if err != nil {
		return nil, nil, err
	}
	views := make([]*nodev1.SharedDshResourceView, len(bindings))
	for i, binding := range bindings {
		view := m.views[binding.PlacementId]
		if view == nil || !proto.Equal(view.Binding, binding) {
			return nil, nil, fmt.Errorf("fresh complete platform resource snapshot required")
		}
		views[i] = view
	}
	return bindings, views, nil
}

func (m *PersistentPools) ensure(ctx context.Context, binding *nodev1.SharedDshBinding) (result error) {
	return m.ensureMode(ctx, binding, nil)
}

func (m *PersistentPools) ensureMode(ctx context.Context, binding *nodev1.SharedDshBinding, restart *dedicatedReceipt) (result error) {
	defer func() {
		if result != nil {
			delete(m.active, binding.PhysicalInstanceId)
			if m.Unpublish != nil {
				m.Unpublish(binding.PhysicalInstanceId)
			}
		}
	}()
	if m.Driver == nil || m.Driver.Activation == nil || m.UIDs == nil || m.Template == nil || m.Resources == nil {
		return fmt.Errorf("shared runtime installation is incomplete")
	}
	pool := binding.PhysicalInstanceId
	layout := m.Pools.Layout
	meta, err := layout.Path(pool, "control", "meta")
	if err != nil {
		return err
	}
	skills, err := layout.Path(pool, "host", "platform-skills", "active")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(skills, 0755); err != nil {
		return err
	}
	// The read-only /run/auriga meta bind carries the nested platform-skills
	// mountpoint. runc cannot create it inside a read-only parent at start.
	if err = os.MkdirAll(filepath.Join(meta, "skills-active", "current"), 0755); err != nil {
		return err
	}
	bindings, views, err := m.snapshot(pool)
	if err != nil {
		return err
	}
	if err = m.Pools.AuthorizePoolStart(ctx, pool, views, m.Resources); err != nil {
		return err
	}
	uid, err := m.UIDs.Allocate(pool)
	if err != nil {
		return err
	}
	var relay string
	if m.ConfigureHost != nil {
		relay, err = m.ConfigureHost(ctx, binding, uid)
		if err != nil {
			return err
		}
	}
	members, err := layout.Path(pool, "members")
	if err != nil {
		return err
	}
	// A restarted Agent has a fresh mount namespace. Attach the node-owned pool
	// filesystem before touching any member path; mounting is idempotent.
	if backend, ok := layout.Quota.(interface {
		MountPool(context.Context, string) (storage.PoolMount, error)
	}); ok {
		if _, err = backend.MountPool(ctx, pool); err != nil {
			return err
		}
	}
	for _, member := range bindings {
		path, err := layout.Workspace(pool, member.TenantId, member.MemberId, member.Epoch)
		if err != nil {
			return err
		}
		if err = m.sharePoolDirectory(path, members, uid); err != nil {
			return err
		}
	}
	home, err := storage.SafeJoin(members, ".host")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(home, 0700); err != nil {
		return err
	}
	if err = layout.Quota.Ensure(ctx, home, storage.MemberBytes, storage.MemberInodes); err != nil {
		return err
	}
	if err = m.chown(home, uid, uid); err != nil {
		return err
	}
	// A separate durable, quota-enforced Host trust ledger. The third mount of
	// this same filesystem exposes only public message cache at its fixed path;
	// control and private node ledgers remain outside the filesystem.
	trust := filepath.Join(members, "message-trust")
	if err = os.MkdirAll(trust, 0700); err != nil {
		return err
	}
	if err = layout.Quota.Ensure(ctx, trust, storage.MemberBytes, storage.MemberInodes); err != nil {
		return err
	}
	if err = m.chown(trust, uid, uid); err != nil {
		return err
	}
	// New files inherit the already confirmed Host home project quota. Host
	// atomically copies legacy ledgers here while retaining the original files;
	// never allocate another project or move an old inode across this boundary.
	for _, name := range []string{"message-preferences", "message-inboxes"} {
		path, err := storage.SafeJoin(members, ".host", name)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(path, 0700); err != nil {
			return err
		}
		if err = m.chown(path, uid, uid); err != nil {
			return err
		}
	}
	full := proto.Clone(m.Template).(*nodev1.InstanceSpec)
	if full.Dsh == nil || full.Generation <= 0 || full.Secrets != nil || full.OwnerId != "" || len(full.Plugins) != 0 || len(full.UnifiedResources) != 0 || len(full.GetProfile().GetEnv()) != 0 || len(full.GetProfile().GetPatches()) != 0 {
		return fmt.Errorf("shared runtime template contains personal state or lacks signed runtime")
	}
	full.InstanceId, full.TenantId = pool, binding.TenantId
	port := full.GetProfile().GetListenPort()
	if port == 0 {
		port = 7860
	}
	mode := "shared"
	if binding.Mode == nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED {
		return m.ensureDedicated(ctx, binding, full, uid, members, skills, relay, port, restart)
	}
	digest, err := renderer.RenderPoolMode(m.Profile, m.Ports, pool, binding.TenantId, mode, meta, port)
	if err != nil {
		return err
	}
	container := &PoolContainer{Members: members, Skills: skills, Meta: meta, Relay: relay, ProfileDigest: digest}
	if backend, ok := layout.Quota.(interface {
		MountPool(context.Context, string) (storage.PoolMount, error)
	}); ok {
		fs, err := backend.MountPool(ctx, pool)
		if err != nil {
			return err
		}
		container.Volume, container.Device = "auriga-pool-"+pool, fs.Device
	}
	path, err := m.Driver.poolContainerPath(pool)
	if err != nil {
		return err
	}
	var existing PoolContainer
	if err = unifiedRead(path, &existing); err == nil {
		if existing != *container {
			return fmt.Errorf("pool container storage or profile changed")
		}
	} else if os.IsNotExist(err) {
		if err = unifiedAtomic(path, container); err != nil {
			return err
		}
	} else {
		return err
	}
	trustedNow := time.Now().UTC()
	if clock, ok := m.Resources.(interface{ Now() time.Time }); ok {
		trustedNow = clock.Now()
	}
	policy := map[string]any{"schemaVersion": 1, "sharedPool": true, "newSessionRequired": false,
		"allowPersonalPlugins": false, "trustedTime": trustedNow.Format(time.RFC3339Nano)}
	var expires int64
	for _, view := range views {
		for _, resource := range view.Resources {
			if expires == 0 || resource.ExpiresAt < expires {
				expires = resource.ExpiresAt
			}
		}
	}
	if expires != 0 {
		policy["leaseExpiresAt"] = time.Unix(expires, 0).UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	if err = unifiedAtomicMode(filepath.Join(meta, "resource-session-policy.json"), raw, 0644); err != nil {
		return err
	}
	record, err := m.Driver.Activation.Store.GetExecution(pool)
	if err != nil {
		return err
	}
	processFile, err := layout.Path(pool, "control", "process-state")
	if err != nil {
		return err
	}
	initial := record == nil || record.Phase == "created"
	if record == nil {
		err = m.Driver.Activation.Activate(ctx, full, func(ctx context.Context) error {
			spec := &Spec{Full: full, InstanceID: pool, Generation: full.Generation, Image: full.Dsh.Image,
				CPUMillis: full.GetResources().GetCpuMillis(), MemoryMB: full.GetResources().GetMemoryMb(), PIDs: full.GetResources().GetPids(),
				HostUID: uid, HomeBind: members, MetaBind: meta, ListenPort: port, Pool: container,
				Env: map[string]string{"AURIGA_MODE": mode, "AURIGA_INSTANCE_ID": pool, "AURIGA_TENANT_ID": binding.TenantId,
					"HOME": "/home/dsh/.host", "DSH_HOME": "/home/dsh/.host/.dsh", "DSH_TELEMETRY_DISABLED": "1", "DSH_TELEMETRY_MODE": "DISABLED"}}
			if err := m.Driver.Ensure(ctx, spec, &Rendered{Digest: digest}); err != nil {
				return err
			}
			return m.Driver.Start(ctx, pool)
		})
	} else {
		if record.Generation != full.Generation || !proto.Equal(record.DSH, full.Dsh) {
			return fmt.Errorf("running pool release requires explicit maintenance")
		}
		// Reconnect recovers only a process which is already running. A stopped pool
		// goes through W's explicit guardian/recovery protocol.
		actual, inspectErr := m.Driver.verifyContainer(ctx, record)
		if inspectErr != nil {
			return inspectErr
		}
		live, _ := actual["State"].(map[string]any)
		if live["Running"] != true && record.Phase != "created" {
			return fmt.Errorf("pool stopped; explicit recovery required")
		}
		if record.Phase == "started" {
			var saved poolProcess
			if err := unifiedRead(processFile, &saved); err != nil {
				return err
			}
			process, err := containerProcess(actual)
			if err != nil || saved.Container != record.ContainerID || saved.PID != process.pid || !saved.StartedAt.Equal(process.startedAt) {
				return fmt.Errorf("pool process lifetime changed")
			}
		}
		err = m.Driver.Activation.Recover(ctx, pool, record.Generation, func(ctx context.Context) error { return m.Driver.Start(ctx, pool) })
	}
	if err != nil {
		return err
	}
	record, err = m.Driver.Activation.Store.GetExecution(pool)
	if err != nil {
		return err
	}
	actual, err := m.Driver.verifyContainer(ctx, record)
	if err != nil {
		return err
	}
	process, err := containerProcess(actual)
	if err != nil {
		return err
	}
	if initial {
		if err := unifiedAtomic(processFile, poolProcess{Container: record.ContainerID, PID: process.pid, StartedAt: process.startedAt}); err != nil {
			return err
		}
	}
	endpoint, err := m.Driver.Endpoint(pool)
	if err != nil {
		return err
	}
	if m.Publish != nil {
		m.Publish(pool, endpoint)
	}
	if m.active == nil {
		m.active = map[string]bool{}
	}
	m.active[pool] = true
	return nil
}

func (m *PersistentPools) chown(path string, uid, gid int) error {
	if m.Chown != nil {
		return m.Chown(path, uid, gid)
	}
	return storage.Lchown(path, uid, gid)
}

func (m *PersistentPools) sharePoolDirectory(path, root string, uid int) error {
	if err := storage.AssertUnderRoot(root, path); err != nil {
		return err
	}
	for parent := filepath.Dir(path); parent != filepath.Dir(root); parent = filepath.Dir(parent) {
		if err := os.Chmod(parent, 0755); err != nil {
			return err
		}
	}
	return m.chown(path, uid, uid)
}

func (m *PersistentPools) Watch(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(m.Pools.Layout.Root)
	if err != nil {
		return
	}
	watch := observer.PoolWatch{Pools: m.Pools, Verifier: m.Resources, Validate: CheckPoolViews}
	for _, entry := range entries {
		pool := entry.Name()
		if !entry.IsDir() || !poolUUID.MatchString(pool) {
			continue
		}
		// Blocked pools no longer receive resource views. Their durable guardian
		// receipts must still replay after a disconnect or process restart.
		impacts, impactErr := m.Pools.GuardianImpacts(pool)
		if impactErr == nil {
			for _, impact := range impacts {
				m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshGuardianImpact{SharedDshGuardianImpact: impact}})
			}
		}
		bindings, views, err := m.refreshPoolSnapshot(ctx, pool)
		if err != nil || len(bindings) == 0 {
			if err != nil {
				m.closePoolIngress(pool)
			}
			continue
		}
		operation := views[0].ViewId
		if m.Memory != nil {
			if sample, err := m.Memory(ctx, pool); err == nil {
				m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshPoolMemorySample{SharedDshPoolMemorySample: sample}})
			}
		}
		if bindings[0].Mode == nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED {
			m.watchDedicated(ctx, bindings, views)
			continue
		}
		reports, impact, checkErr := watch.Check(ctx, operation, bindings, views, time.Now())
		if checkErr != nil && m.Unpublish != nil {
			delete(m.active, pool)
			m.Unpublish(pool)
		}
		for _, report := range reports {
			m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshResourceReport{SharedDshResourceReport: report}})
		}
		if impact != nil {
			m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshGuardianImpact{SharedDshGuardianImpact: impact}})
		}
		actual, err := m.Driver.CheckHealth(ctx, pool)
		if err != nil || !actual.Ready {
			delete(m.active, pool)
			if m.Unpublish != nil {
				m.Unpublish(pool)
			}
			if err == nil && !actual.Running {
				impact, err := m.Pools.Trip(ctx, pool, operation, "crash")
				if err == nil {
					m.report(&nodev1.SessionRequest{Body: &nodev1.SessionRequest_SharedDshGuardianImpact{SharedDshGuardianImpact: impact}})
				}
			}
		}
	}
}

func (m *PersistentPools) report(frame *nodev1.SessionRequest) {
	if m.Report != nil {
		m.Report(frame)
	}
}
