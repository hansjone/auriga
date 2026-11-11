package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
)

func TestDockerStartupStagesAndContainerTrace(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	spec := releaseSpec(f, "startup-stages", 7)
	var logs bytes.Buffer
	ctx := telemetry.Start(context.Background(), "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", spec.InstanceId, spec.Generation, slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx = telemetry.WithStartup(ctx, time.Now(), "propagated")
	home, meta := t.TempDir(), t.TempDir()
	if err := d.Activation.Activate(ctx, spec, func(ctx context.Context) error {
		s := &runtime.Spec{Full: spec, InstanceID: spec.InstanceId, Generation: spec.Generation, Image: spec.Dsh.Image, HomeBind: home, MetaBind: meta, HostUID: 200001, ListenPort: 7860, Env: map[string]string{"TRACEPARENT": "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"}}
		if err := d.Ensure(ctx, s, nil); err != nil {
			return err
		}
		return d.Start(ctx, spec.InstanceId)
	}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&logs)
	for _, want := range []string{"image_pull", "image_ready", "container_created", "container_started", "first_health_attempt", "listen_detected", "plugin_healthy"} {
		found := false
		for decoder.More() {
			var event map[string]any
			if err := decoder.Decode(&event); err != nil {
				t.Fatal(err)
			}
			if event["stage"] != nil {
				if event["stage"] != want {
					t.Fatal(event, want)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatal("missing", want)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, c := range e.containers {
		env, _ := c["Env"].([]any)
		for _, v := range env {
			if strings.HasPrefix(v.(string), "TRACEPARENT=") {
				count++
				if v != "TRACEPARENT="+telemetry.Current(ctx).Value {
					t.Fatal("spec overrode trusted trace", v)
				}
			}
		}
	}
	if count != 1 {
		t.Fatalf("container TRACEPARENT count=%d", count)
	}
	t.Log("validated top-level Env: exactly one trusted TRACEPARENT overrides spec")
}
