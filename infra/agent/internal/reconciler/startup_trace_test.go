package reconciler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

type traceLog struct {
	sync.Mutex
	bytes.Buffer
}

func (w *traceLog) Write(p []byte) (int, error) { w.Lock(); defer w.Unlock(); return w.Buffer.Write(p) }

type traceDriver struct {
	runtime.Driver
	entered chan telemetry.Span
	release chan struct{}
}

func (d *traceDriver) Start(ctx context.Context, _ string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.entered <- telemetry.Current(ctx)
	<-d.release
	return fmt.Errorf("health fixture rejected password=sensitive")
}
func TestAsyncStartupConcurrentReloadAndCanceledSession(t *testing.T) {
	directory := t.TempDir()
	store, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	parents := []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"}
	for i, parent := range parents {
		spec := &nodev1.InstanceSpec{InstanceId: fmt.Sprint("instance-", i), Generation: 9, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
		if err := store.PutDesiredWithTrace(spec, parent); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	driver := &traceDriver{Driver: runtime.NewFake(), entered: make(chan telemetry.Span, 2), release: make(chan struct{})}
	var logs traceLog
	rec := reconciler.New(store, driver, slog.New(slog.NewJSONHandler(&logs, nil)), 2)
	var release sync.Once
	defer func() { release.Do(func() { close(driver.release) }); rec.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rec.ReconcileAll(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case span := <-driver.entered:
			index := 0
			if span.Instance == "instance-1" {
				index = 1
			}
			if !telemetry.Valid(span.Value) || span.Value[3:35] != parents[index][3:35] || span.Parent != parents[index][36:52] || span.Generation != 9 {
				t.Errorf("wrong startup span %+v", span)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("async start inherited cancellation or stalled")
		}
	}
	release.Do(func() { close(driver.release) })
	rec.Close()
	decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
	failures := 0
	for decoder.More() {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event["msg"] == "runtime activation failed" {
			failures++
			if event["trace_id"] == "" || event["error"] == "health fixture rejected password=sensitive" {
				t.Fatal("untraced or unmasked failure", event)
			}
		}
	}
	if failures != 2 {
		t.Fatalf("failures=%d", failures)
	}
}
