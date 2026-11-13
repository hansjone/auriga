package runtime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUnifiedR6ReconcileAllAfterAgentRestartKeepsInstanceAndSkills(t *testing.T) {
	manager, spec, driver, stops := runtime.NewUnifiedRestartFixture(t)
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.PutDesired(spec); err != nil {
		t.Fatal(err)
	}
	manager.Specs = store
	current, err := manager.Prepare(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Ensure(t.Context(), &runtime.Spec{InstanceID: spec.InstanceId, Generation: spec.Generation}, nil); err != nil {
		t.Fatal(err)
	}
	if err := driver.Start(t.Context(), spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	reports, guardian, err := manager.Observe(t.Context(), spec.InstanceId)
	if err != nil {
		t.Fatal(err)
	}
	before := &nodev1.InstanceStatus{InstanceId: spec.InstanceId, ObservedGeneration: spec.Generation, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true, StartedAt: timestamppb.Now(), UnifiedResourceReports: reports, UnifiedGuardian: guardian}
	if err := store.PutActual(state.ActualRecord{Status: before}); err != nil {
		t.Fatal(err)
	}
	contentPath := filepath.Join(current, "example-skill", "SKILL.md")
	content, err := os.ReadFile(contentPath)
	if err != nil {
		t.Fatal(err)
	}
	stopCount := stops()
	restarted := runtime.NewUnifiedManager(manager.Layout, driver, store, t.TempDir())
	restarted.Protocol, restarted.Client, restarted.Origin, restarted.Now = manager.Protocol, manager.Client, manager.Origin, manager.Now
	preparing := &runtime.Preparing{Unified: restarted, Inner: driver, Specs: store}
	reconciler := reconciler.New(store, preparing, nil, 1)
	if err := reconciler.ReconcileAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	reconciler.Close()
	actual, err := driver.Inspect(t.Context(), spec.InstanceId)
	if err != nil || !actual.Running || stops() != stopCount {
		t.Fatalf("agent restart stopped original runtime: %+v %v", actual, err)
	}
	after, err := store.GetActual(spec.InstanceId)
	if err != nil || !proto.Equal(before, after.Status) {
		t.Fatalf("agent restart replaced execution/status: %+v %v", after, err)
	}
	got, err := os.ReadFile(contentPath)
	if err != nil || string(got) != string(content) {
		t.Fatal("agent restart changed shared bytes", err)
	}
	_, guardian, err = restarted.Observe(t.Context(), spec.InstanceId)
	if err != nil || guardian.State != "active" || guardian.NewSessionRequired {
		t.Fatalf("agent restart invalidated existing sessions: %+v %v", guardian, err)
	}
}
