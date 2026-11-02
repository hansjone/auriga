package conn

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/telemetry"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

// Only scheduling and the external runtime are gated. recvLoop, handleControl,
// ApplySpec, desired/status persistence and completion all use production code.
type startupReceiverStream struct {
	nodev1.NodeControlService_SessionClient
	frames chan *nodev1.SessionResponse
	polls  chan struct{}
}

func (s *startupReceiverStream) Recv() (*nodev1.SessionResponse, error) {
	s.polls <- struct{}{}
	frame, ok := <-s.frames
	if !ok {
		return nil, io.EOF
	}
	return frame, nil
}

type startupReceiverGate struct {
	entered chan struct{}
	release chan struct{}
	used    atomic.Bool
}

func (h *startupReceiverGate) Enabled(context.Context, slog.Level) bool { return true }
func (h *startupReceiverGate) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *startupReceiverGate) WithGroup(string) slog.Handler            { return h }
func (h *startupReceiverGate) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "control message received" && h.used.CompareAndSwap(false, true) {
		close(h.entered)
		<-h.release
	}
	return nil
}

type startupInFlightDriver struct {
	runtime.Driver
	started chan telemetry.Span
	release chan struct{}
	used    atomic.Bool
}

func (d *startupInFlightDriver) Start(ctx context.Context, id string) error {
	if telemetry.Current(ctx).Generation == 8 && d.used.CompareAndSwap(false, true) {
		d.started <- telemetry.Current(ctx)
		<-d.release
	}
	return d.Driver.Start(ctx, id)
}

func TestReceiverOverlapKeepsStartupObservationMonotonic(t *testing.T) {
	t.Run("late-frame-then-completion", func(t *testing.T) { testReceiverOverlap(t, false) })
	t.Run("late-frame-then-rebind", func(t *testing.T) { testReceiverOverlap(t, true) })
}

func testReceiverOverlap(t *testing.T, rebindBeforeReady bool) {
	t.Helper()
	directory := t.TempDir()
	store, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	gate := &startupReceiverGate{entered: make(chan struct{}), release: make(chan struct{})}
	driver := &startupInFlightDriver{Driver: runtime.NewFake(), started: make(chan telemetry.Span, 1), release: make(chan struct{})}
	discard := slog.New(slog.NewJSONHandler(io.Discard, nil))
	rec := reconciler.New(store, driver, discard, 1)
	session := New(Config{Store: store, Reconciler: rec, Logger: slog.New(gate)})
	oldStream := &startupReceiverStream{frames: make(chan *nodev1.SessionResponse, 1), polls: make(chan struct{}, 16)}
	newStream := &startupReceiverStream{frames: make(chan *nodev1.SessionResponse, 1), polls: make(chan struct{}, 16)}
	oldDone, newDone := make(chan error, 1), make(chan error, 1)
	readySaved, readyRelease := make(chan struct{}), make(chan struct{})
	var readyUsed atomic.Bool
	rec.Sink = func(status *nodev1.InstanceStatus) {
		if status.ObservedGeneration == 8 && status.Ready && readyUsed.CompareAndSwap(false, true) {
			close(readySaved)
			<-readyRelease
		}
	}
	var oldOnce, startOnce, readyOnce, oldCloseOnce, newCloseOnce sync.Once
	var receivers sync.WaitGroup
	unblockOld := func() { oldOnce.Do(func() { close(gate.release) }) }
	unblockStart := func() { startOnce.Do(func() { close(driver.release) }) }
	unblockQueue := func() { readyOnce.Do(func() { close(readyRelease) }) }
	closeOld := func() { oldCloseOnce.Do(func() { close(oldStream.frames) }) }
	closeNew := func() { newCloseOnce.Do(func() { close(newStream.frames) }) }
	defer func() {
		unblockOld()
		unblockStart()
		unblockQueue()
		closeOld()
		closeNew()
		receivers.Wait()
		rec.Close()
		_ = store.Close()
	}()
	frame := func(generation int64, parent string) *nodev1.SessionResponse {
		return &nodev1.SessionResponse{Body: &nodev1.SessionResponse_Apply{Apply: &nodev1.ApplySpec{
			Spec: &nodev1.InstanceSpec{InstanceId: "instance-overlap", Generation: generation, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}, Traceparent: parent,
		}}}
	}
	await := func(signal <-chan struct{}, description string) {
		t.Helper()
		select {
		case <-signal:
		case <-time.After(3 * time.Second):
			t.Fatal("fixture stalled: " + description)
		}
	}
	finish := func(done <-chan error, description string) {
		t.Helper()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "session closed by server") {
				t.Fatal(description, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("receiver did not finish: " + description)
		}
	}
	const traceA = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const traceB = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
	oldStream.frames <- frame(7, traceA)
	closeOld()
	receivers.Add(1)
	go func() { defer receivers.Done(); oldDone <- session.recvLoop(context.Background(), oldStream) }()
	await(gate.entered, "old receiver has acquired g7 before persistence")
	receivers.Add(1)
	go func() { defer receivers.Done(); newDone <- session.recvLoop(context.Background(), newStream) }()
	await(newStream.polls, "new receiver waiting")
	newStream.frames <- frame(8, traceB)
	await(newStream.polls, "new g8 persisted and next Recv entered")
	select {
	case span := <-driver.started:
		if !telemetry.Valid(span.Value) || span.Generation != 8 || span.Value[3:35] != traceB[3:35] || span.Parent != traceB[36:52] {
			t.Errorf("wrong in-flight context: %+v", span)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("g8 did not enter actual Start")
	}
	unblockOld()
	finish(oldDone, "old g7 receiver")
	desired, err := store.GetDesired("instance-overlap")
	if err != nil || desired == nil || desired.Generation != 7 {
		t.Fatal("test must retain existing late desired write semantics", desired, err)
	}
	if got := store.StartupParent("instance-overlap", 8); got != traceB {
		t.Errorf("late g7 lost in-flight g8 cause: %q", got)
	}
	activeReplays := []string(nil)
	if rebindBeforeReady {
		activeReplays = []string{traceA, "", strings.Repeat("invalid", 100)}
	}
	for _, parent := range activeReplays {
		newStream.frames <- frame(8, parent)
		await(newStream.polls, "g8 unrelated/missing/invalid observation processed")
		if got := store.StartupParent("instance-overlap", 8); got != traceB {
			t.Errorf("g8 cause rebound by %q: %q", parent, got)
		}
	}
	unblockStart()
	await(readySaved, "actual g8 Ready is persisted before Sink barrier")
	if !store.StartupComplete("instance-overlap", 8) || store.StartupParent("instance-overlap", 8) != "" {
		t.Error("in-flight g8 completion watermark lost")
	}
	for _, parent := range []string{traceB, traceA, "", strings.Repeat("invalid", 100)} {
		newStream.frames <- frame(8, parent)
		await(newStream.polls, "completed g8 replay processed")
		if !store.StartupComplete("instance-overlap", 8) || store.StartupParent("instance-overlap", 8) != "" {
			t.Error("completed g8 cause resurrected")
		}
	}
	closeNew()
	finish(newDone, "new receiver")
	unblockQueue()
	rec.Close()
	if !store.StartupComplete("instance-overlap", 8) {
		t.Error("older queued execution reduced completion watermark")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reloaded.Close() }()
	restarted := reconciler.New(reloaded, runtime.NewFake(), discard, 1)
	defer restarted.Close()
	restartedSession := New(Config{Store: reloaded, Reconciler: restarted, Logger: discard})
	replay := &startupReceiverStream{frames: make(chan *nodev1.SessionResponse, 1), polls: make(chan struct{}, 16)}
	replay.frames <- frame(8, traceB)
	close(replay.frames)
	if err := restartedSession.recvLoop(context.Background(), replay); err == nil {
		t.Error("closed replay stream should end")
	}
	restarted.Close()
	if !reloaded.StartupComplete("instance-overlap", 8) || reloaded.StartupParent("instance-overlap", 8) != "" {
		t.Error("reload/redelivery resurrected cause")
	}
}
