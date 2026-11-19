package runtime_test

import (
	"context"
	"testing"
	"time"
)

func TestPoolMemoryUsesSignedEngineBytesAndCurrentProcess(t *testing.T) {
	for _, scenario := range []string{"fresh", "80-percent", "missing-usage", "missing-limit", "zero-limit", "fractional", "unsafe", "stale", "future", "missing-read", "different-limit", "changed-process", "changed-during-read", "changed-limit-during-read"} {
		t.Run(scenario, func(t *testing.T) {
			m, engine, _ := persistentFixture(t)
			command := persistentIntent(1)
			if err := m.View(t.Context(), persistentView(command)); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Prepare(t.Context(), command); err != nil {
				t.Fatal(err)
			}
			const limit = int64(512 * 1024 * 1024)
			memory := map[string]any{"usage": limit * 79 / 100, "limit": limit}
			stats := map[string]any{"read": time.Now().UTC().Format(time.RFC3339Nano), "memory_stats": memory}
			engine.mu.Lock()
			switch scenario {
			case "80-percent":
				memory["usage"] = limit - limit/5
			case "missing-usage":
				delete(memory, "usage")
			case "missing-limit":
				delete(memory, "limit")
			case "zero-limit":
				memory["limit"] = 0
			case "fractional":
				memory["usage"] = 1.5
			case "unsafe":
				memory["usage"] = int64(9007199254740992)
			case "stale":
				stats["read"] = time.Now().Add(-31 * time.Second).UTC().Format(time.RFC3339Nano)
			case "future":
				stats["read"] = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
			case "missing-read":
				delete(stats, "read")
			case "different-limit":
				memory["limit"] = limit / 2
			case "changed-process":
				engine.mode = "process-lifetime"
			case "changed-during-read":
				engine.beforeStats = func() { engine.mu.Lock(); engine.mode = "process-lifetime"; engine.mu.Unlock() }
			case "changed-limit-during-read":
				engine.beforeStats = func() {
					engine.mu.Lock()
					defer engine.mu.Unlock()
					for _, body := range engine.containers {
						body["HostConfig"].(map[string]any)["Memory"] = float64(limit / 2)
					}
				}
			}
			engine.memoryStats = stats
			engine.mu.Unlock()
			sample, err := m.Driver.PoolMemory(context.Background(), command.Binding.PhysicalInstanceId)
			if scenario != "fresh" && scenario != "80-percent" {
				if err == nil || sample != nil {
					t.Fatal("invalid Engine data became a valid sample", sample, err)
				}
				return
			}
			if err != nil || sample.MemoryUsedBytes != memory["usage"] || sample.MemoryLimitBytes != limit || sample.ObservedGeneration != 1 || sample.PhysicalInstanceId != command.Binding.PhysicalInstanceId || sample.ValidUntil-sample.ObservedAt != 30 {
				t.Fatal("exact signed Engine sample lost", sample, err)
			}
		})
	}
}
