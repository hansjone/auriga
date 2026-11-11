package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestStartupStagesShareClockAndOrigin(t *testing.T) {
	var logs bytes.Buffer
	ctx := Start(context.Background(), "", "instance-a", 7, slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx = WithStartup(ctx, time.Now().Add(-time.Second), "generated")
	Stage(ctx, "accepted")
	Stage(Child(ctx), "image_ready")
	Stage(ctx, "ready")
	decoder := json.NewDecoder(&logs)
	var trace string
	var cumulative float64
	for _, stage := range []string{"accepted", "image_ready", "ready"} {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event["stage"] != stage || event["instance_id"] != "instance-a" || event["generation"] != float64(7) || event["trace.origin"] != "generated" {
			t.Fatal(event)
		}
		if trace == "" {
			trace = event["trace_id"].(string)
		}
		if event["trace_id"] != trace || event["elapsed_ms"].(float64) < 0 || event["cumulative_ms"].(float64) < 1000 || event["cumulative_ms"].(float64) < cumulative {
			t.Fatal(event)
		}
		cumulative = event["cumulative_ms"].(float64)
	}
}

func TestParentOnlyLogOmitsUnknownOrigin(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	Log(WithParent(context.Background(), ""), slog.LevelInfo, "no startup")
	var event map[string]any
	if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
		t.Fatal(err)
	}
	if _, exists := event["trace.origin"]; exists {
		t.Fatal("unknown origin was serialized", event)
	}
}
