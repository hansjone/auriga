package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

const persistentProfile = `- id: auriga-dsh
  name: auriga-dsh/shared
- id: ports
  name: file:///run/auriga/shared-ports.mjs
- id: webserver
  name: '@deepseek-ai/dsh-host-webserver'
- id: tools
  name: '@deepseek-ai/dsh-tools'
  config: {mode: native}
- id: skills
  name: '@deepseek-ai/dsh-skill-filesystem'
  config: {providerName: auriga-shared, includeDefaultRoots: false, customSkillDirs: [/run/auriga/skills-active/current]}
`

type persistentQuota struct{ fail bool }

func (q *persistentQuota) Ensure(_ context.Context, _ string, bytes, inodes int64) error {
	if q.fail || bytes != storage.MemberBytes || inodes != storage.MemberInodes {
		return errors.New("kernel quota readback failed")
	}
	return nil
}

func persistentIntent(member int) *nodev1.SharedDshRuntimeIntent {
	id := func(n string) string { return "00000000-0000-4000-8000-0000000000" + n }
	part := "03"
	if member == 2 {
		part = "04"
	}
	return &nodev1.SharedDshRuntimeIntent{SchemaVersion: 1, OperationId: id(part),
		Binding: &nodev1.SharedDshBinding{TenantId: id("01"), MemberId: id(part), PlacementId: id(part),
			PhysicalInstanceId: id("02"), Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1},
		Action: "prepare-empty", CancelWaitSeconds: 30, ArchiveRetentionSeconds: 2592000, WorkspacePolicy: "empty", SessionPolicy: "discard"}
}

func persistentView(command *nodev1.SharedDshRuntimeIntent) *nodev1.SharedDshResourceView {
	return &nodev1.SharedDshResourceView{SchemaVersion: 1, Binding: proto.Clone(command.Binding).(*nodev1.SharedDshBinding),
		ViewId: command.OperationId, Source: "platform", AuthorizationSetDigest: "sha256:" + strings.Repeat("a", 64)}
}

func persistentFixture(t *testing.T) (*runtime.PersistentPools, *releaseEngine, *persistentQuota) {
	t.Helper()
	engine, docker, release := newReleaseEngine(t)
	docker.DataRoot = t.TempDir()
	quota := &persistentQuota{}
	layout := &storage.PoolLayout{Root: filepath.Join(docker.DataRoot, "pools"), Quota: quota}
	pools := &runtime.SharedPools{Layout: layout, Supervisor: docker}
	return &runtime.PersistentPools{Pools: pools, Driver: docker, UIDs: storage.NewUIDTable(release.dir),
		Template: &nodev1.InstanceSpec{Generation: 1, Dsh: release.dsh, Resources: &nodev1.Resources{MemoryMb: 512, CpuMillis: 500, Pids: 128}},
		Profile:  []byte(persistentProfile), Ports: []byte("// Synthetic installation module; host is a fixture, no DSH process is executed.\n"),
		Resources: &runtime.EmptyPoolResources{Root: layout.Root}}, engine, quota
}

func TestPersistentPoolTwoMembersAndRestartReuseOneSignedProcess(t *testing.T) {
	m, engine, _ := persistentFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, second := persistentIntent(1), persistentIntent(2)
	if err := m.View(ctx, persistentView(first)); err != nil {
		t.Fatal(err)
	}
	if receipt, err := m.Prepare(ctx, first); err != nil || receipt.Outcome != "confirmed" {
		t.Fatalf("first start: %v", err)
	}
	if err := m.View(ctx, persistentView(second)); err != nil {
		t.Fatal(err)
	}
	if receipt, err := m.Prepare(ctx, second); err != nil || receipt.Outcome != "confirmed" {
		t.Fatalf("join: %v", err)
	}
	engine.mu.Lock()
	creates, starts := engine.creates, len(engine.starts)
	engine.mu.Unlock()
	if creates != 1 || starts != 1 {
		t.Fatalf("created=%d started=%d", creates, starts)
	}
	record, err := m.Driver.Activation.Store.GetExecution(first.Binding.PhysicalInstanceId)
	if err != nil {
		t.Fatal(err)
	}
	before := record.ContainerID
	workspace, err := m.Pools.Layout.Workspace(first.Binding.PhysicalInstanceId, first.Binding.TenantId, first.Binding.MemberId, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "retained.txt"), []byte("synthetic member data"), 0600); err != nil {
		t.Fatal(err)
	}
	// Rebuild the authority around the same durable ledger, execution DB and Engine.
	recoveredDriver := runtime.NewDockerWithClient(m.Driver.Client, m.Driver.DataRoot)
	recoveredDriver.Policy, recoveredDriver.Activation = m.Driver.Policy, m.Driver.Activation
	reconnected := &runtime.PersistentPools{Pools: &runtime.SharedPools{Layout: m.Pools.Layout, Supervisor: recoveredDriver},
		Driver: recoveredDriver, UIDs: storage.NewUIDTable(filepath.Dir(m.UIDs.Path)), Template: m.Template, Profile: m.Profile, Ports: m.Ports, Resources: m.Resources}
	if receipt, err := reconnected.Prepare(ctx, first); err == nil || receipt == nil || receipt.Outcome != "unconfirmed" {
		t.Fatal("reconnect accepted stale resource snapshots")
	}
	if err := reconnected.View(ctx, persistentView(first)); err == nil {
		t.Fatal("partial resource snapshot accepted")
	}
	if err := reconnected.View(ctx, persistentView(second)); err != nil {
		t.Fatal(err)
	}
	if _, err := reconnected.Prepare(ctx, first); err != nil {
		t.Fatal(err)
	}
	reconnected.Watch(ctx)
	after, err := m.Driver.Activation.Store.GetExecution(first.Binding.PhysicalInstanceId)
	if err != nil || after.ContainerID != before {
		t.Fatal("reconnect replaced execution")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.creates != 1 || len(engine.starts) != 1 || engine.containers[before]["running"] != true {
		t.Fatal("reopen or observation changed the process")
	}
	body := engine.containers[before]
	for _, value := range body["Env"].([]any) {
		text := value.(string)
		if strings.Contains(text, "TOKEN") || strings.Contains(text, "OWNER") || strings.Contains(text, "CREDENTIAL") {
			t.Fatal("personal material in process env")
		}
	}
	if _, err := os.ReadFile(filepath.Join(workspace, "retained.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentPoolQuotaSnapshotReleaseAndProfileGates(t *testing.T) {
	for _, gate := range []string{"quota", "snapshot", "personal", "profile", "release", "resource"} {
		t.Run(gate, func(t *testing.T) {
			m, engine, quota := persistentFixture(t)
			command := persistentIntent(1)
			view := persistentView(command)
			if gate == "quota" {
				quota.fail = true
			}
			if gate == "personal" {
				m.Template.Secrets = &nodev1.Secrets{}
			}
			if gate == "profile" {
				m.Profile = []byte(strings.Replace(persistentProfile, "auriga-dsh/shared", "auriga-dsh", 1))
			}
			if gate == "release" {
				m.Template.Dsh.ReleaseEnvelope.Signature.Value = "invalid"
			}
			if gate == "resource" {
				view.Resources = []*nodev1.SharedDshResource{{ResourceId: command.OperationId, LeaseId: command.OperationId, Digest: "sha256:" + strings.Repeat("a", 64), ExpiresAt: time.Now().Add(time.Hour).Unix()}}
			}
			if gate != "snapshot" {
				if err := m.View(context.Background(), view); err != nil {
					t.Fatal(err)
				}
			}
			if receipt, err := m.Prepare(context.Background(), command); err == nil && receipt != nil && receipt.Outcome == "confirmed" {
				t.Fatal("invalid runtime started")
			}
			engine.mu.Lock()
			defer engine.mu.Unlock()
			if engine.creates != 0 || len(engine.starts) != 0 {
				t.Fatal("gate failed after Engine mutation")
			}
		})
	}
}

type persistentFilesystem struct {
	*persistentQuota
	root string
}

func (s *persistentFilesystem) MountPool(_ context.Context, pool string) (storage.PoolMount, error) {
	root := filepath.Join(s.root, pool, "members")
	if err := os.MkdirAll(root, 0700); err != nil {
		return storage.PoolMount{}, err
	}
	return storage.PoolMount{Device: "/dev/loop7", Root: root}, nil
}

func TestPersistentExt4VolumeSharesTheDeviceAndRejectsRebinding(t *testing.T) {
	m, engine, quota := persistentFixture(t)
	m.Pools.Layout.Quota = &persistentFilesystem{persistentQuota: quota, root: m.Pools.Layout.Root}
	command := persistentIntent(1)
	if err := m.View(context.Background(), persistentView(command)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	name := "auriga-pool-" + command.Binding.PhysicalInstanceId
	engine.mu.Lock()
	volume := engine.volumes[name]
	if volume == nil || volume["Options"].(map[string]any)["device"] != "/dev/loop7" {
		t.Fatal("Engine did not mount the node-attested pool device")
	}
	volume["Options"].(map[string]any)["device"] = "/dev/loop8"
	engine.mu.Unlock()
	if receipt, err := m.Prepare(context.Background(), command); err == nil || receipt.Outcome != "unconfirmed" {
		t.Fatal("rebound volume was adopted")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.creates != 1 || len(engine.starts) != 1 {
		t.Fatal("recovery replaced the pool process")
	}
}

func TestPersistentPoolRejectsChangedProcessAndDoesNotRestartStoppedPool(t *testing.T) {
	for _, scenario := range []string{"changed", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			m, engine, _ := persistentFixture(t)
			command := persistentIntent(1)
			if err := m.View(context.Background(), persistentView(command)); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Prepare(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			engine.mu.Lock()
			if scenario == "changed" {
				engine.mode = "process-lifetime"
			} else {
				for _, container := range engine.containers {
					container["running"] = false
				}
			}
			engine.mu.Unlock()
			if _, err := m.Prepare(context.Background(), command); err == nil {
				t.Fatal("unexpected process was adopted")
			}
			engine.mu.Lock()
			defer engine.mu.Unlock()
			if len(engine.starts) != 1 || engine.creates != 1 {
				t.Fatal("pool was automatically replaced or restarted")
			}
		})
	}
}

func TestPersistentPoolExpiredAuthorityProducesActualStopReceipt(t *testing.T) {
	m, engine, _ := persistentFixture(t)
	command := persistentIntent(1)
	if err := m.View(context.Background(), persistentView(command)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	var impact *nodev1.SharedDshGuardianImpact
	m.Report = func(report *nodev1.SessionRequest) {
		if report.GetSharedDshGuardianImpact() != nil {
			impact = report.GetSharedDshGuardianImpact()
		}
	}
	view := persistentView(command)
	view.Resources = []*nodev1.SharedDshResource{{ResourceId: command.OperationId, LeaseId: command.OperationId, Digest: "sha256:" + strings.Repeat("a", 64), ExpiresAt: time.Now().Add(-time.Second).Unix()}}
	if err := m.View(context.Background(), view); err == nil {
		t.Fatal("expired authority accepted")
	}
	if impact == nil || impact.State != "stopped" || impact.Reason != "resource-expired" {
		t.Fatal("missing actual guardian stop proof")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	for _, container := range engine.containers {
		if container["running"] == true {
			t.Fatal("expired pool remains running")
		}
	}
}
