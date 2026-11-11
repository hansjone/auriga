package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
)

func TestStartupListenDetectedBeforePluginHealthy(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok","ready":true,"protocol":1,"pluginVersion":"fixture"}`)
	}))
	defer server.Close()
	var logs bytes.Buffer
	ctx := telemetry.Start(context.Background(), "", "listen-a", 7, slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx = telemetry.WithStartup(ctx, time.Now(), "generated")
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := waitInstanceHealth(ctx, server.URL); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&logs)
	var stages []string
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if stage, ok := event["stage"].(string); ok {
			stages = append(stages, stage)
			if stage == "listen_detected" && event["http_status"] != float64(503) {
				t.Fatal(event)
			}
		}
	}
	want := []string{"first_health_attempt", "listen_detected", "plugin_healthy"}
	if len(stages) != len(want) {
		t.Fatal(stages)
	}
	for i := range want {
		if stages[i] != want[i] {
			t.Fatal(stages)
		}
	}
}
