package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
)

func TestUnifiedR6HostClockAcrossAgentProcesses(t *testing.T) {
	if os.Getenv("AURIGA_TEST_HOST_CLOCK_CHILD") == "1" {
		m := NewUnifiedManager(nil, nil, nil, "missing-test-directory")
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Boot string
			Mono int64
		}{m.BootID, m.mono()})
		os.Exit(0)
	}
	m := NewUnifiedManager(nil, nil, nil, t.TempDir())
	before := m.mono()
	command := exec.Command(os.Args[0], "-test.run=^TestUnifiedR6HostClockAcrossAgentProcesses$")
	command.Env = append(os.Environ(), "AURIGA_TEST_HOST_CLOCK_CHILD=1")
	raw, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	var child struct {
		Boot string
		Mono int64
	}
	if err := json.Unmarshal(raw, &child); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	if !uUUID.MatchString(m.BootID) || child.Boot != m.BootID || before < 1 || child.Mono < before || child.Mono > m.mono() {
		t.Fatalf("agent process changed host clock: parent=%s/%d child=%+v", m.BootID, before, child)
	}
}

func TestUnifiedR6AgentRestartPreservesRunningSharedView(t *testing.T) {
	f := newUnifiedFixture(t)
	clock := NewUnifiedManager(nil, nil, nil, t.TempDir())
	f.manager.BootID, f.manager.Mono = clock.BootID, clock.Mono
	ctx := context.Background()
	current, err := f.manager.Prepare(ctx, f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Ensure(ctx, &Spec{InstanceID: f.source.spec.InstanceId, Generation: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Start(ctx, f.source.spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	stops := f.driver.stops
	restarted := NewUnifiedManager(f.manager.Layout, f.driver, f.source, t.TempDir())
	restarted.Protocol, restarted.Client, restarted.Origin, restarted.Now = f.manager.Protocol, f.manager.Client, f.manager.Origin, f.manager.Now
	if err := restarted.Guard(ctx, f.source.spec); err != nil {
		t.Fatal("agent restart isolated active resources", err)
	}
	actual, err := f.driver.Inspect(ctx, f.source.spec.InstanceId)
	if err != nil || !actual.Running || f.driver.stops != stops {
		t.Fatalf("restart stopped instance: %+v %v", actual, err)
	}
	raw, err := os.ReadFile(filepath.Join(current, "example-skill", "SKILL.md"))
	if err != nil || string(raw) != unifiedSkillText {
		t.Fatal("shared view changed on agent restart", err)
	}
	_, guardian, err := restarted.Observe(ctx, f.source.spec.InstanceId)
	if err != nil || guardian.NewSessionRequired {
		t.Fatalf("agent restart invalidated sessions: %+v %v", guardian, err)
	}
}

func TestUnifiedR6PrivateStateOutsideContainerMounts(t *testing.T) {
	f := newUnifiedFixture(t)
	root, current, err := f.manager.paths(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := f.manager.Layout.For(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(root) != paths.Root || filepath.Base(root) != "resource-state" {
		t.Fatalf("node state lies in container meta/home mount: %s", root)
	}
	if current != filepath.Join(root, "skills-active", "current") {
		t.Fatal(current)
	}
}

func TestUnifiedR6SelectionLockSurvivesIsolationAndGeneration(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		t.Run(fmt.Sprint(isolated), func(t *testing.T) {
			f := newUnifiedFixture(t)
			if _, err := f.manager.Prepare(context.Background(), f.source.spec); err != nil {
				t.Fatal(err)
			}
			if isolated {
				f.manager.BootID = unifiedNonce()
				_ = f.manager.Guard(context.Background(), f.source.spec)
			}
			next := proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
			next.Generation++
			next.UnifiedResources[0].Enabled = false
			next.UnifiedResources[0].InstallationRevision++
			next.UnifiedResources[0].Identity.ManifestDigest = "sha256:" + strings.Repeat("0", 64)
			next.UnifiedResources[0].DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED.Enum()
			f.source.spec = next
			if _, err := f.manager.Prepare(context.Background(), next); err == nil {
				t.Fatal("same selection changed intent after generation/isolation")
			}
			s, _, _, err := f.manager.load(next.InstanceId)
			if err != nil || s.State == "active" {
				t.Fatalf("conflicting selection remains active: %+v %v", s, err)
			}
		})
	}
}

func TestUnifiedR6CacheCollectionPreservesProofsAndCurrentBytes(t *testing.T) {
	f := newUnifiedFixture(t)
	root, _, err := f.manager.paths(f.source.spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	obsolete := filepath.Join(root, "cache", fmt.Sprintf("%064x", 1), "payload.tgz")
	proof := filepath.Join(root, "migration-evidence", "proof.json")
	for _, file := range []string{obsolete, proof} {
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("preserved evidence fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.manager.Prepare(t.Context(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(obsolete); !os.IsNotExist(err) {
		t.Fatal("unpinned payload not collected", err)
	}
	if _, err := os.Stat(proof); err != nil {
		t.Fatal("migration evidence collected", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cache", f.ref.Identity.ManifestDigest[len("sha256:"):], "manifest.json")); err != nil {
		t.Fatal("current original bytes collected", err)
	}
}

func TestUnifiedR6VoluntaryDisableDoesNotPermanentlyGateSessions(t *testing.T) {
	f := newUnifiedFixture(t)
	if _, err := f.manager.Prepare(t.Context(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	next := proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
	next.Generation++
	next.ResourceSelectionRevision++
	next.UnifiedResources[0].InstallationRevision++
	next.UnifiedResources[0].Enabled = false
	next.UnifiedResources[0].DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED.Enum()
	f.source.spec = next
	f.catalogEmpty = true
	if _, err := f.manager.Prepare(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	_, guardian, err := f.manager.Observe(t.Context(), next.InstanceId)
	if err != nil || guardian.NewSessionRequired {
		t.Fatalf("voluntary disable permanently gated private sessions: %+v %v", guardian, err)
	}
}

func TestUnifiedR6ConfirmedTombstoneCollectionDoesNotStopHealthyView(t *testing.T) {
	f := newUnifiedFixture(t)
	tombstone := proto.Clone(f.ref).(*resourcev1.UnifiedResourceRef)
	tombstone.Identity.PackageId = "019a0000-0000-7000-8000-000000000077"
	tombstone.Envelope.Statement.Name = "removed-skill"
	tombstone.Enabled = false
	tombstone.DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_REMOVED.Enum()
	f.source.spec.UnifiedResources = append(f.source.spec.UnifiedResources, tombstone)
	current, err := f.manager.Prepare(t.Context(), f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.manager.Observe(t.Context(), f.source.spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	before := f.driver.stops
	f.source.spec = proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
	f.source.spec.Generation++
	f.source.spec.UnifiedResources = f.source.spec.UnifiedResources[:1]
	if err := f.manager.Guard(t.Context(), f.source.spec); err != nil {
		t.Fatal("confirmed tombstone collection blocked healthy renewal", err)
	}
	if f.driver.stops != before {
		t.Fatal("tombstone collection stopped healthy shared runtime")
	}
	if _, err := os.Stat(filepath.Join(current, "example-skill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedR6AuthoritySafetyDisablePreservesSelectedBytes(t *testing.T) {
	f := newUnifiedFixture(t)
	if _, err := f.manager.Prepare(t.Context(), f.source.spec); err != nil {
		t.Fatal(err)
	}
	next := proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
	next.Generation++
	next.UnifiedResources[0].InstallationRevision++
	next.UnifiedResources[0].Enabled = false
	next.UnifiedResources[0].DesiredState = resourcev1.UnifiedResourceDesiredState_UNIFIED_RESOURCE_DESIRED_STATE_DISABLED.Enum()
	f.source.spec = next
	_ = f.manager.Guard(t.Context(), next)
	if _, err := f.manager.Prepare(t.Context(), next); err != nil {
		t.Fatal("authority safety disable blocked", err)
	}
	f.catalogEmpty = true
	reports, _, err := f.manager.Observe(t.Context(), next.InstanceId)
	if err != nil || len(reports) != 1 || reports[0].State != "disabled" || reports[0].InstallationRevision != 2 {
		t.Fatalf("wrong authority disable: %+v %v", reports, err)
	}
}

func TestUnifiedR6MountRejectsAnyPrivateStateExposure(t *testing.T) {
	d := &Docker{DataRoot: t.TempDir()}
	id := "instance"
	root := filepath.Join(d.DataRoot, "instances", id, "resource-state")
	view := map[string]any{"Source": filepath.Join(root, "skills-active", "current"), "Destination": "/home/dsh/.dsh/auriga/skills-active/current", "Type": "bind", "RW": false}
	for _, source := range []string{root, filepath.Dir(root), filepath.Join(root, "cache")} {
		inspection := map[string]any{"Mounts": []any{view, map[string]any{"Source": source, "Destination": "/run/leak", "Type": "bind", "RW": false}},
			"Config":     map[string]any{"Entrypoint": []string{"node", renderer.ContainerResourceSupervisor}, "Cmd": []string{renderer.ContainerResourcePolicy, "node", "dsh.mjs"}},
			"HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": "no"}}}
		if err := d.verifyUnifiedMount(inspection, id); err == nil || !strings.Contains(err.Error(), "node-private resource state") {
			t.Fatalf("private state reachable from container: %s", source)
		}
		inspection["Mounts"] = []any{view}
		if err := d.verifyUnifiedMount(inspection, id); err != nil {
			t.Fatal("normal current-only mount rejected", err)
		}
	}
}

func TestUnifiedR6ObservationMismatchIsolatesImmediately(t *testing.T) {
	f := newUnifiedFixture(t)
	ctx := context.Background()
	current, err := f.manager.Prepare(ctx, f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Ensure(ctx, &Spec{InstanceID: f.source.spec.InstanceId, Generation: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.driver.Start(ctx, f.source.spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	f.override = true
	if _, _, err := f.manager.Observe(ctx, f.source.spec.InstanceId); err == nil {
		t.Fatal("catalog disagreement accepted")
	}
	actual, err := f.driver.Inspect(ctx, f.source.spec.InstanceId)
	if err != nil || actual.Running {
		t.Fatalf("observation mismatch left runtime executing: %+v %v", actual, err)
	}
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		t.Fatal("observation mismatch retained shared content", err)
	}
}

func TestUnifiedR6FailedDisableDoesNotLeaveOldSharedView(t *testing.T) {
	f := newUnifiedFixture(t)
	ctx := context.Background()
	current, err := f.manager.Prepare(ctx, f.source.spec)
	if err != nil {
		t.Fatal(err)
	}
	next := proto.Clone(f.source.spec).(*nodev1.InstanceSpec)
	next.Generation++
	next.ResourceSelectionRevision++
	next.UnifiedResources[0].Enabled = false
	// A malformed new intent must still remove the old active view.
	next.UnifiedResources[0].DesiredState = nil
	f.source.spec = next
	_, _ = f.manager.Prepare(ctx, next)
	entries, err := os.ReadDir(current)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed disable left old shared content enabled", err)
	}
}

func TestUnifiedR6ClockDoesNotUseWallTimeForMonotonic(t *testing.T) {
	m := NewUnifiedManager(nil, nil, nil, t.TempDir())
	before := m.mono()
	m.Now = func() time.Time { return time.Unix(0, 0) }
	if m.mono() < before {
		t.Fatal("wall correction changed monotonic counter")
	}
}

// NewUnifiedRestartFixture exposes only the real fixture's public runtime ports
// to the external reconciler integration test; no production test hook is added.
func NewUnifiedRestartFixture(t *testing.T) (*UnifiedManager, *nodev1.InstanceSpec, Driver, func() int) {
	t.Helper()
	f := newUnifiedFixture(t)
	clock := NewUnifiedManager(nil, nil, nil, t.TempDir())
	f.manager.BootID, f.manager.Mono = clock.BootID, clock.Mono
	return f.manager, f.source.spec, f.driver, func() int { return f.driver.stops }
}
