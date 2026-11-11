package reconciler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

type stageDriver struct {
	*runtime.Fake
	timeout bool
}

func (d *stageDriver) Ensure(ctx context.Context, s *runtime.Spec, r *runtime.Rendered) error {
	telemetry.Stage(ctx, "image_pull")
	telemetry.Stage(ctx, "image_ready")
	telemetry.Stage(ctx, "container_created")
	return d.Fake.Ensure(ctx, s, r)
}
func (d *stageDriver) Start(ctx context.Context, id string) error {
	telemetry.Stage(ctx, "container_started")
	telemetry.Stage(telemetry.Child(ctx), "first_health_attempt")
	if d.timeout {
		d.timeout = false
		return context.DeadlineExceeded
	}
	telemetry.Stage(ctx, "listen_detected")
	telemetry.Stage(ctx, "plugin_healthy")
	return d.Fake.Start(ctx, id)
}
func (d *stageDriver) CheckHealth(_ context.Context, id string) (runtime.Status, error) {
	return runtime.Status{InstanceID: id, Generation: 7, Running: true}, nil
}
func TestStartupStagesTimeoutThenSameGenerationRecovery(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	spec := &nodev1.InstanceSpec{InstanceId: "stages-a", Generation: 7, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if err := store.PutDesiredWithTrace(spec, parent); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	d := &stageDriver{Fake: runtime.NewFake(), timeout: true}
	r := New(store, d, slog.New(slog.NewJSONHandler(&logs, nil)), 1)
	defer r.Close()
	ctx := r.startupContext(context.Background(), store.StartupParent(spec.InstanceId, 7), spec.InstanceId, 7)
	r.handleApply(ctx, spec.InstanceId, spec)
	actual, err := store.GetActual(spec.InstanceId)
	if err != nil || actual.Status.Ready {
		t.Fatal(actual, err)
	}
	r.handleHealth(spec.InstanceId, context.Background(), 0)
	actual, err = store.GetActual(spec.InstanceId)
	if err != nil || !actual.Status.Ready {
		t.Fatal(actual, err)
	}
	decoder := json.NewDecoder(&logs)
	var stages []string
	var total float64
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event["stage"] == nil {
			continue
		}
		stages = append(stages, event["stage"].(string))
		if event["trace_id"] != parent[3:35] || event["instance_id"] != spec.InstanceId || event["generation"] != float64(7) || event["elapsed_ms"].(float64) < 0 || event["cumulative_ms"].(float64) < total {
			t.Fatal(event)
		}
		total = event["cumulative_ms"].(float64)
	}
	want := []string{"accepted", "image_pull", "image_ready", "container_created", "container_started", "first_health_attempt", "timeout", "container_started", "first_health_attempt", "listen_detected", "plugin_healthy", "ready"}
	if len(stages) != len(want) {
		t.Fatal(stages)
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatal(stages)
		}
	}
	completed := r.startupContext(context.Background(), store.StartupParent(spec.InstanceId, 7), spec.InstanceId, 7)
	if telemetry.Current(completed).Value[3:35] == parent[3:35] {
		t.Fatal("maintenance reused completed startup")
	}
}

func TestStartupContextWithoutMatchingTimingEmitsNoStages(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	spec := &nodev1.InstanceSpec{InstanceId: "new-generation", Generation: 8, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
	if err := store.PutDesiredWithTrace(spec, ""); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	r := New(store, runtime.NewFake(), slog.New(slog.NewJSONHandler(&logs, nil)), 1)
	defer r.Close()
	ctx := r.startupContext(context.Background(), "", spec.InstanceId, 7)
	telemetry.Stage(ctx, "ready")
	if logs.Len() != 0 {
		t.Fatal("stale execution created unowned stages", logs.String())
	}
	ctx = r.startupContext(context.Background(), store.StartupParent(spec.InstanceId, 8), spec.InstanceId, 8)
	telemetry.Stage(ctx, "accepted")
	if logs.Len() == 0 {
		t.Fatal("current startup lost its clock")
	}
}
