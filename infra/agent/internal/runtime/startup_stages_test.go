package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
)

func TestDockerStartupStagesAndContainerTrace(t *testing.T) {
	e, d, f := newReleaseEngine(t)
	spec := releaseSpec(f, "startup-stages", 7)
	var logs bytes.Buffer
	ctx := telemetry.Start(context.Background(), "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", spec.InstanceId, spec.Generation, slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx = telemetry.WithStartup(ctx, time.Now(), "propagated")
	if err := executeRelease(ctx, d, spec, t.TempDir(), t.TempDir()); err != nil {
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
	found := false
	for _, c := range e.containers {
		config, _ := c["Config"].(map[string]any)
		env, _ := config["Env"].([]any)
		for _, v := range env {
			if strings.HasPrefix(v.(string), "TRACEPARENT=00-4bf92f3577b34da6a3ce929d0e0e4736-") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("container missing startup trace")
	}
}
