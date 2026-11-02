package state

import (
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestStartupCauseReloadReplayAndTerminalBoundary(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	spec := &nodev1.InstanceSpec{InstanceId: "instance-a", Generation: 7, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
	before, _ := proto.MarshalOptions{Deterministic: true}.Marshal(spec)
	if err := store.PutDesiredWithTrace(spec, parent); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if store.StartupParent(spec.InstanceId, 7) != parent {
		t.Fatal("reload lost cause")
	}
	redelivery := "00-4bf92f3577b34da6a3ce929d0e0e4736-1111111111111111-01"
	if err := store.PutDesiredWithTrace(spec, redelivery); err != nil {
		t.Fatal(err)
	}
	if store.StartupParent(spec.InstanceId, 7) != redelivery {
		t.Fatal("redelivery lost its actual dispatch parent")
	}
	if err := store.PutDesiredWithTrace(spec, "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"); err != nil {
		t.Fatal(err)
	}
	if store.StartupParent(spec.InstanceId, 7) != redelivery {
		t.Fatal("unrelated operation rebound generation")
	}
	parent = redelivery
	loaded, _ := store.GetDesired(spec.InstanceId)
	after, _ := proto.MarshalOptions{Deterministic: true}.Marshal(loaded)
	if string(before) != string(after) {
		t.Fatal("observation changed business spec")
	}
	if err := store.PutActual(ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: spec.InstanceId, ObservedGeneration: 7, Phase: nodev1.InstancePhase_INSTANCE_PHASE_STOPPED}}); err != nil {
		t.Fatal(err)
	}
	if store.StartupParent(spec.InstanceId, 7) != parent {
		t.Fatal("restart checkpoint completed startup")
	}
	if err := store.PutActual(ActualRecord{Status: &nodev1.InstanceStatus{InstanceId: spec.InstanceId, ObservedGeneration: 7, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, Ready: true}}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutDesiredWithTrace(spec, parent); err != nil {
		t.Fatal(err)
	}
	if store.StartupParent(spec.InstanceId, 7) != "" {
		t.Fatal("replay resurrected completed cause")
	}
	spec.Generation++
	if err := store.PutDesiredWithTrace(spec, "invalid"); err != nil {
		t.Fatal("invalid observation rejected business spec", err)
	}
	if store.StartupParent(spec.InstanceId, 8) != "" {
		t.Fatal("invalid parent persisted")
	}
	if !store.StartupComplete(spec.InstanceId, 7) {
		t.Fatal("new desired generation forgot completed older queued work")
	}
	if err := store.DeleteDesired(spec.InstanceId); err != nil {
		t.Fatal(err)
	}
	if store.StartupParent(spec.InstanceId, 7) != "" {
		t.Fatal("delete retained cause")
	}
}
