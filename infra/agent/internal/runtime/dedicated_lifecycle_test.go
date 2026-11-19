package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentruntime "github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// Synthetic authority for scheduling/containment tests only. The separate
// public SDK fixture executes the exact preparation source with real rc.3
// init/load/compose/normalizer/resolution; neither test boots a DSH process.
type dedicatedLifecycleAuthority struct {
	mu                   sync.Mutex
	view                 *nodev1.SharedDshResourceView
	cache, relay, digest string
	restart              atomic.Bool
	host                 atomic.Int32
	oldHost              atomic.Bool
}

func (a *dedicatedLifecycleAuthority) Refresh(_ context.Context, b *nodev1.SharedDshBinding) (*nodev1.SharedDshResourceView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !proto.Equal(b, a.view.Binding) {
		return nil, errors.New("revoked")
	}
	return proto.Clone(a.view).(*nodev1.SharedDshResourceView), nil
}
func (a *dedicatedLifecycleAuthority) Verify(_ context.Context, _ string, views []*nodev1.SharedDshResourceView) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(views) != 1 || !proto.Equal(views[0], a.view) {
		return errors.New("view superseded")
	}
	value, err := sharednode.HostValue(a.view)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "view": value, "resources": []any{}})
	if err != nil {
		return err
	}
	a.digest = testDigest(raw)
	return os.WriteFile(filepath.Join(a.relay, "dedicated-resources.json"), raw, 0644)
}
func (*dedicatedLifecycleAuthority) Cleanup(context.Context, string) error { return nil }
func (a *dedicatedLifecycleAuthority) DedicatedIntent(_ string, v *nodev1.SharedDshResourceView) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !proto.Equal(v, a.view) {
		return "", "", errors.New("view superseded")
	}
	return a.cache, a.digest, nil
}

func dedicatedLifecycleFixture(t *testing.T) (*agentruntime.PersistentPools, *releaseEngine, *dedicatedLifecycleAuthority, *nodev1.SharedDshRuntimeIntent) {
	t.Helper()
	m, engine, _ := persistentFixture(t)
	profile, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "nas", "dedicated-runtime-profile.example.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m.DedicatedProfile = profile
	m.Chown = func(path string, uid, gid int) error {
		if uid != gid || (uid != 0 && uid != 200000) {
			t.Fatal("unexpected uid", uid, gid)
		}
		_, err := os.Stat(path)
		return err
	}
	command := persistentIntent(1)
	command.Binding.Mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
	root := filepath.Join(m.Pools.Layout.Root, command.Binding.PhysicalInstanceId, "control")
	a := &dedicatedLifecycleAuthority{view: persistentView(command), cache: filepath.Join(root, "dedicated-artifacts"), relay: filepath.Join(root, "host-relay")}
	for _, dir := range []string{a.cache, a.relay} {
		if err = os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	m.Resources = a
	m.ConfigureHost = func(context.Context, *nodev1.SharedDshBinding, int) (string, error) { return a.relay, nil }
	engine.mu.Lock()
	engine.preparation = func(body map[string]any) error {
		engine.mu.Lock()
		for _, other := range engine.containers {
			if other["running"] == true && other != nil {
				labels, _ := other["Labels"].(map[string]any)
				if labels["auriga.dedicated-preparation"] == nil {
					engine.mu.Unlock()
					return errors.New("helper overlapped actual DSH")
				}
			}
		}
		engine.mu.Unlock()
		return syntheticDedicatedPrepared(body)
	}
	engine.afterStart = func() {
		engine.mu.Lock()
		id := engine.starts[len(engine.starts)-1]
		labels, _ := engine.containers[id]["Labels"].(map[string]any)
		helper := labels["auriga.dedicated-preparation"] != nil
		engine.mu.Unlock()
		if !helper {
			if !a.oldHost.Load() {
				a.host.Add(1)
			}
			a.restart.Store(false)
		}
	}
	engine.dedicatedHost = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_auriga/dedicated-profile" {
			_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 1, "physicalInstanceId": command.Binding.PhysicalInstanceId, "profile": "auriga.dedicated.v1", "capabilities": map[string]any{"pluginInstall": true, "privateSkills": true, "includeDefaultRoots": true, "toolPresentation": "native"}})
			return
		}
		a.mu.Lock()
		view := proto.Clone(a.view).(*nodev1.SharedDshResourceView)
		digest := a.digest
		a.mu.Unlock()
		binding, _ := sharednode.HostValue(view.Binding)
		status, reason := "ready", "verified"
		if a.restart.Load() {
			status, reason = "restart-required", "resource.restart_required"
			w.WriteHeader(503)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 1, "binding": binding, "viewId": view.ViewId, "intentDigest": digest, "status": status, "reason": reason, "hostInstanceId": fmt.Sprintf("00000000-0000-4000-8000-%012d", a.host.Load())})
	}
	engine.mu.Unlock()
	return m, engine, a, command
}

func syntheticDedicatedPrepared(body map[string]any) error {
	host := body["HostConfig"].(map[string]any)
	mounts := host["Mounts"].([]any)
	paths := map[string]string{}
	for _, value := range mounts {
		mount := value.(map[string]any)
		paths[mount["Target"].(string)] = mount["Source"].(string)
	}
	meta, output, home := paths["/run/auriga"], paths["/run/auriga/dedicated-prepared"], paths["/home/dsh/.host"]
	raw, err := os.ReadFile(filepath.Join(meta, "dedicated-input.json"))
	if err != nil {
		return err
	}
	var input map[string]any
	if err = json.Unmarshal(raw, &input); err != nil {
		return err
	}
	logical, err := os.ReadFile(filepath.Join(meta, "dedicated-profile.json"))
	if err != nil {
		return err
	}
	var rows []map[string]any
	if err = json.Unmarshal(logical, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		if name := row["name"].(string); strings.HasPrefix(name, "@deepseek-ai/") {
			row["name"] = "file:///opt/dsh/node_modules/" + name + "/lib/index.js"
		}
	}
	config, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	profile := filepath.Join(home, ".dsh", "profiles", "auriga-dedicated")
	if err = os.MkdirAll(profile, 0700); err != nil {
		return err
	}
	controls := []map[string]any{}
	for _, name := range []string{"package.json", "cordis.patch.yml", "pnpm-workspace.yaml"} {
		file := filepath.Join(profile, name)
		if _, err = os.Stat(file); os.IsNotExist(err) {
			if err = os.WriteFile(file, []byte("synthetic preserved profile "+name), 0600); err != nil {
				return err
			}
		}
		bytes, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		controls = append(controls, map[string]any{"path": "/home/dsh/.host/.dsh/profiles/auriga-dedicated/" + name, "size": len(bytes), "digest": testDigest(bytes)})
	}
	proof := map[string]any{}
	for key, value := range input {
		proof[key] = value
	}
	proof["inputDigest"], proof["configDigest"], proof["configBytes"], proof["profileFiles"], proof["startedBundles"] = testDigest(raw), testDigest(config), len(config), controls, []any{}
	manifest, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(output, "config.json"), config, 0600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, "manifest.json"), manifest, 0600)
}

func TestDedicatedActualCallerPrepareReadOnlyMountAndControlledRestart(t *testing.T) {
	m, engine, a, command := dedicatedLifecycleFixture(t)
	published := 0
	m.Publish = func(string, agentruntime.Endpoint) { published++ }
	if err := m.View(t.Context(), persistentView(command)); err != nil {
		t.Fatal(err)
	}
	if receipt, err := m.Prepare(t.Context(), command); err != nil || receipt.Outcome != "confirmed" {
		t.Fatal("actual dedicated launch caller", err)
	}
	if published != 0 {
		t.Fatal("preparation was advertised ready")
	}
	const task = "00000000-0000-4000-8000-000000000901"
	if err := m.Pools.AdmitTask(t.Context(), command.Binding, task, 1, 100); err == nil {
		t.Fatal("preparation gate opened before Host receipt")
	}
	m.Watch(t.Context())
	if published != 1 {
		t.Fatal("actual Host profile/receipt did not publish", published)
	}
	if err := m.Pools.AdmitTask(t.Context(), command.Binding, task, 1, 100); err != nil {
		t.Fatal(err)
	}
	before, err := m.Driver.Activation.Store.GetExecution(command.Binding.PhysicalInstanceId)
	if err != nil {
		t.Fatal(err)
	}
	a.restart.Store(true)
	m.Watch(t.Context())
	engine.mu.Lock()
	creates, running := engine.creates, engine.containers[before.ContainerID]["running"]
	engine.mu.Unlock()
	if creates != 2 || running != true {
		t.Fatal("unknown ordinary/Job finish was stopped or given parallel preparation", creates, running)
	}
	if err = m.Pools.AdmitTask(t.Context(), command.Binding, "00000000-0000-4000-8000-000000000902", 1, 100); err == nil {
		t.Fatal("maintenance admitted second root")
	}
	if err = m.Pools.CompleteTask(command.Binding, task); err != nil {
		t.Fatal(err)
	} // fixture supplies local terminal evidence; typed cross-end ACK is tested by F3.
	m.Watch(t.Context())
	after, err := m.Driver.Activation.Store.GetExecution(command.Binding.PhysicalInstanceId)
	if err != nil || before.ContainerID == after.ContainerID {
		t.Fatal("controlled restart did not replace exact process", err)
	}
	m.Watch(t.Context())
	if published < 2 {
		t.Fatal("new Host never became ready")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.creates != 4 || len(engine.containers) != 1 {
		t.Fatal("helper was left behind or a second budget was created", engine.creates, len(engine.containers))
	}
	body := engine.containers[after.ContainerID]
	host := body["HostConfig"].(map[string]any)
	prepared, cache := 0, 0
	for _, value := range host["Mounts"].([]any) {
		mount := value.(map[string]any)
		switch mount["Target"] {
		case "/run/auriga/dedicated-prepared":
			prepared++
			if mount["ReadOnly"] != true {
				t.Fatal("runtime can write prepared config")
			}
		case "/run/auriga/dedicated-resources":
			cache++
			if mount["ReadOnly"] != true {
				t.Fatal("runtime can write verified cache")
			}
		}
	}
	if prepared != 1 || cache != 1 {
		t.Fatal("actual runtime omitted closed mounts")
	}
}

func TestDedicatedPreparationUnknownTamperAndRevokeStayClosed(t *testing.T) {
	for _, scenario := range []string{"unknown-helper-start", "unknown-main-start", "tampered-output", "superseded-view", "changed-helper-id"} {
		t.Run(scenario, func(t *testing.T) {
			m, engine, a, command := dedicatedLifecycleFixture(t)
			engine.mu.Lock()
			prepare := engine.preparation
			if scenario == "unknown-main-start" {
				engine.beforeStart = func(id string) {
					engine.mu.Lock()
					defer engine.mu.Unlock()
					labels, _ := engine.containers[id]["Labels"].(map[string]any)
					if labels["auriga.dedicated-preparation"] == nil {
						engine.mode = "start-500"
					}
				}
			}
			engine.preparation = func(body map[string]any) error {
				if err := prepare(body); err != nil {
					return err
				}
				switch scenario {
				case "unknown-helper-start":
					engine.mu.Lock()
					engine.mode = "start-500"
					engine.mu.Unlock()
				case "changed-helper-id":
					engine.mu.Lock()
					engine.mode = "container-id"
					engine.mu.Unlock()
				case "superseded-view":
					a.mu.Lock()
					a.view.ViewId = "00000000-0000-4000-8000-000000000888"
					a.mu.Unlock()
				case "tampered-output":
					for _, value := range body["HostConfig"].(map[string]any)["Mounts"].([]any) {
						mount := value.(map[string]any)
						if mount["Target"] == "/run/auriga/dedicated-prepared" {
							return os.WriteFile(filepath.Join(mount["Source"].(string), "config.json"), []byte("[]"), 0600)
						}
					}
				}
				return nil
			}
			engine.mu.Unlock()
			if err := m.View(t.Context(), persistentView(command)); err != nil {
				t.Fatal(err)
			}
			if receipt, err := m.Prepare(t.Context(), command); err == nil || receipt.Outcome != "unconfirmed" {
				t.Fatal("unsafe helper became runtime", scenario, err)
			}
			if scenario == "unknown-helper-start" || scenario == "unknown-main-start" {
				if stopped, err := m.Driver.ResourceProcessStopped(t.Context(), command.Binding.PhysicalInstanceId); err == nil || stopped {
					t.Fatal("unresolved Engine action became an execution-tree stop proof", err)
				}
			}
			engine.mu.Lock()
			creates := engine.creates
			engine.mode = ""
			engine.mu.Unlock()
			_, _ = m.Prepare(t.Context(), command)
			engine.mu.Lock()
			defer engine.mu.Unlock()
			expected := 1
			if scenario == "unknown-main-start" {
				expected = 2
			}
			if creates != expected || engine.creates != creates {
				t.Fatal("unknown helper was replayed or main started", creates, engine.creates)
			}
		})
	}
}

func TestDedicatedGuardianSerializesWithHelperAndKnownRevokeCleansTree(t *testing.T) {
	m, engine, _, command := dedicatedLifecycleFixture(t)
	started, resume := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	engine.mu.Lock()
	prepare := engine.preparation
	engine.preparation = func(body map[string]any) error { close(started); <-resume; return prepare(body) }
	engine.mu.Unlock()
	if err := m.View(t.Context(), persistentView(command)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := m.Prepare(t.Context(), command); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("helper not started")
	}
	deadline, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if stopped, err := m.Driver.ResourceProcessStopped(deadline, command.Binding.PhysicalInstanceId); err == nil || stopped {
		t.Fatal("guardian bypassed in-flight helper", err)
	}
	trip := make(chan error, 1)
	go func() {
		impact, err := m.Pools.Trip(t.Context(), command.Binding.PhysicalInstanceId, "00000000-0000-4000-8000-000000000999", "resource-revoked")
		if err == nil && impact.State != "stopped" {
			err = errors.New("known finite helper revoke did not confirm cleanup")
		}
		trip <- err
	}()
	file := filepath.Join(m.Pools.Layout.Root, command.Binding.PhysicalInstanceId, "control", "runtime-state")
	for limit := time.Now().Add(3 * time.Second); ; {
		raw, err := os.ReadFile(file)
		var state struct{ Contaminated bool }
		if err == nil && json.Unmarshal(raw, &state) == nil && state.Contaminated {
			break
		}
		if time.Now().After(limit) {
			t.Fatal("guardian did not set durable admission fence")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-trip:
		t.Fatal("guardian reported while helper still in flight", err)
	default:
	}
	close(resume)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked preparation started main")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not drain")
	}
	select {
	case err := <-trip:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("guardian did not finish exact cleanup")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.creates != 1 || len(engine.containers) != 0 {
		t.Fatal("helper outlived stopped-tree proof or main was created", engine.creates, len(engine.containers))
	}
}

type dedicatedVolumeQuota struct {
	persistentQuota
	root string
}

func (q *dedicatedVolumeQuota) MountPool(_ context.Context, pool string) (storage.PoolMount, error) {
	return storage.PoolMount{Device: "/dev/loop-synthetic", Root: filepath.Join(q.root, pool, "members")}, nil
}

func TestDedicatedQuotaVolumeSubpathRequiredAndVerified(t *testing.T) {
	for _, scenario := range []string{"supported", "legacy-api", "ignored-subpath"} {
		t.Run(scenario, func(t *testing.T) {
			m, engine, _, command := dedicatedLifecycleFixture(t)
			m.Pools.Layout.Quota = &dedicatedVolumeQuota{root: m.Pools.Layout.Root}
			engine.mu.Lock()
			if scenario == "legacy-api" {
				engine.apiVersion = "1.44"
			}
			engine.preparation = func(body map[string]any) error {
				raw, _ := json.Marshal(body)
				var copy map[string]any
				_ = json.Unmarshal(raw, &copy)
				for _, value := range copy["HostConfig"].(map[string]any)["Mounts"].([]any) {
					mount := value.(map[string]any)
					if mount["Target"] == "/home/dsh/.host" {
						options, _ := mount["VolumeOptions"].(map[string]any)
						if options["Subpath"] != ".host" {
							return errors.New("whole-pool helper exposure")
						}
						mount["Source"] = filepath.Join(m.Pools.Layout.Root, command.Binding.PhysicalInstanceId, "members", ".host")
					}
				}
				return syntheticDedicatedPrepared(copy)
			}
			if scenario == "ignored-subpath" {
				engine.beforeInspect = func(id string) error {
					engine.mu.Lock()
					defer engine.mu.Unlock()
					body := engine.containers[id]
					labels, _ := body["Labels"].(map[string]any)
					if labels["auriga.dedicated-preparation"] != nil {
						for _, value := range body["HostConfig"].(map[string]any)["Mounts"].([]any) {
							mount := value.(map[string]any)
							if mount["Target"] == "/home/dsh/.host" {
								mount["VolumeOptions"].(map[string]any)["Subpath"] = ""
							}
						}
					}
					return nil
				}
			}
			engine.mu.Unlock()
			if err := m.View(t.Context(), persistentView(command)); err != nil {
				t.Fatal(err)
			}
			_, err := m.Prepare(t.Context(), command)
			if (scenario == "supported") != (err == nil) {
				t.Fatal("volume capability acceptance mismatch", scenario, err)
			}
			engine.mu.Lock()
			defer engine.mu.Unlock()
			expected := 2
			if scenario == "legacy-api" {
				expected = 0
			}
			if scenario == "ignored-subpath" {
				expected = 1
			}
			if engine.creates != expected {
				t.Fatal("unexpected process after volume gate", engine.creates, expected)
			}
		})
	}
}

func TestDedicatedRestartCannotReuseOldHostUUIDForReady(t *testing.T) {
	m, _, a, command := dedicatedLifecycleFixture(t)
	published := 0
	m.Publish = func(string, agentruntime.Endpoint) { published++ }
	if err := m.View(t.Context(), persistentView(command)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	m.Watch(t.Context())
	if published != 1 {
		t.Fatal("first Host not ready")
	}
	a.oldHost.Store(true)
	a.restart.Store(true)
	m.Watch(t.Context())
	m.Watch(t.Context())
	if published != 1 {
		t.Fatal("old Host UUID became proof for a new process")
	}
	if err := m.Pools.AdmitTask(t.Context(), command.Binding, "00000000-0000-4000-8000-000000000998", 1, 100); err == nil {
		t.Fatal("old Host readiness opened maintenance gate")
	}
}

func TestDedicatedCanceledStopWithdrawsReadyWithoutClaimingStopped(t *testing.T) {
	m, _, _, command := dedicatedLifecycleFixture(t)
	if err := m.View(t.Context(), persistentView(command)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	m.Watch(t.Context())
	if _, err := m.Driver.Endpoint(command.Binding.PhysicalInstanceId); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.Driver.Stop(ctx, command.Binding.PhysicalInstanceId, 0); err == nil {
		t.Fatal("canceled cleanup claimed completion")
	}
	if _, err := m.Driver.Endpoint(command.Binding.PhysicalInstanceId); err == nil {
		t.Fatal("expired control wait kept a ready endpoint")
	}
}
