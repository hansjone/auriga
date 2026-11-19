package runtime

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func engineMemoryInteger(value any) (int64, error) {
	n, ok := value.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
		return 0, fmt.Errorf("invalid Engine memory counter")
	}
	return int64(n), nil
}

// PoolMemory samples the entire signed, current container. Generic Stats' MB
// projection, node totals and configuration estimates are not admission data.
func (d *Docker) PoolMemory(ctx context.Context, pool string) (*nodev1.SharedDshPoolMemorySample, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if d.Activation == nil || d.Client == nil {
		return nil, fmt.Errorf("pool memory runtime unavailable")
	}
	record, err := d.observationRecord(pool)
	if err != nil || record == nil || record.Phase != "started" || record.Generation > 9007199254740991 {
		return nil, fmt.Errorf("pool memory execution unavailable")
	}
	before, err := d.verifyContainer(ctx, record)
	if err != nil {
		return nil, err
	}
	process, err := containerProcess(before)
	if err != nil {
		return nil, err
	}
	var saved poolProcess
	if err := unifiedRead(filepath.Join(d.DataRoot, "pools", pool, "control", "process-state"), &saved); err != nil || saved.Container != record.ContainerID || saved.PID != process.pid || !saved.StartedAt.Equal(process.startedAt) {
		return nil, fmt.Errorf("pool memory process lifetime changed")
	}
	state, _ := before["State"].(map[string]any)
	if state["Running"] != true {
		return nil, fmt.Errorf("pool memory process stopped")
	}
	configured, _ := before["HostConfig"].(map[string]any)
	hardLimit, err := engineMemoryInteger(configured["Memory"])
	if err != nil || hardLimit <= 0 {
		return nil, fmt.Errorf("pool memory hard limit unavailable")
	}
	stats, err := d.Client.ContainerStats(ctx, record.ContainerID)
	if err != nil || stats == nil {
		return nil, fmt.Errorf("pool memory sample unavailable")
	}
	memory, _ := stats["memory_stats"].(map[string]any)
	used, usedErr := engineMemoryInteger(memory["usage"])
	limit, limitErr := engineMemoryInteger(memory["limit"])
	read, _ := stats["read"].(string)
	observed, timeErr := time.Parse(time.RFC3339Nano, read)
	now := time.Now()
	if usedErr != nil || limitErr != nil || limit <= 0 || limit != hardLimit || timeErr != nil || observed.Unix() < 0 || observed.After(now) || observed.Unix()+30 <= now.Unix() {
		return nil, fmt.Errorf("pool memory sample invalid or stale")
	}
	after, err := d.verifyContainer(ctx, record)
	if err != nil {
		return nil, err
	}
	afterProcess, err := containerProcess(after)
	afterState, _ := after["State"].(map[string]any)
	afterConfig, _ := after["HostConfig"].(map[string]any)
	afterLimit, afterLimitErr := engineMemoryInteger(afterConfig["Memory"])
	current, currentErr := d.observationRecord(pool)
	if err != nil || afterProcess != process || afterState["Running"] != true || afterLimitErr != nil || afterLimit != hardLimit || currentErr != nil || current == nil || current.ContainerID != record.ContainerID || current.Generation != record.Generation || current.Phase != "started" || current.ProofDigest != record.ProofDigest || current.EngineImageID != record.EngineImageID || current.ConfigID != record.ConfigID {
		return nil, fmt.Errorf("pool changed during memory sampling")
	}
	return &nodev1.SharedDshPoolMemorySample{SchemaVersion: 1, PhysicalInstanceId: pool,
		ObservedGeneration: record.Generation, ObservedAt: observed.Unix(), ValidUntil: observed.Unix() + 30,
		MemoryUsedBytes: used, MemoryLimitBytes: limit}, nil
}
