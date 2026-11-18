package conn

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const uplinkInstance = "queued-uplink"

var uplinkRelease = state.ReleaseCandidate{
	Partition: state.ReleasePartition{Purpose: "dsh_runtime", Name: "dsh", OS: "linux", Arch: "amd64"},
	Sequence:  1, BodyDigest: "sha256:" + strings.Repeat("c", 64),
}

// The controllable driver models Docker's lease-aware inspection. Session,
// the Reconciler, command replay and both bbolt records run production code.
type uplinkLeaseDriver struct {
	*runtime.Fake
	store         *state.Store
	now, deadline atomic.Int64
	stops         atomic.Int32
	barrier       atomic.Pointer[uplinkInspectionBarrier]
}

type uplinkInspectionBarrier struct {
	id      string
	entered chan struct{}
	resume  chan struct{}
}

func (d *uplinkLeaseDriver) Inspect(ctx context.Context, id string) (runtime.Status, error) {
	record, err := d.store.GetExecution(id)
	if err != nil || record == nil {
		return runtime.Status{}, err
	}
	if err := d.store.CheckReleaseExecution(record.Candidate); err != nil {
		return runtime.Status{}, err
	}
	if barrier := d.barrier.Load(); barrier != nil && id == barrier.id {
		close(barrier.entered)
		select {
		case <-ctx.Done():
			return runtime.Status{}, ctx.Err()
		case <-barrier.resume:
		}
	}
	ready := func() bool { return id != uplinkInstance || d.now.Load() < d.deadline.Load() }
	return runtime.Status{InstanceID: id, ContainerID: record.ContainerID, Generation: record.Generation,
		Running: true, Ready: ready(), RecheckReady: ready}, nil
}

func (d *uplinkLeaseDriver) Stop(ctx context.Context, id string, grace time.Duration) error {
	d.stops.Add(1)
	return d.Fake.Stop(ctx, id, grace)
}

func uplinkFixture(t *testing.T) (*Session, *uplinkLeaseDriver, *nodev1.InstanceStatus) {
	t.Helper()
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	driver := &uplinkLeaseDriver{Fake: runtime.NewFake(), store: store}
	driver.deadline.Store(10)
	runUplinkRelease(t, store, uplinkRelease, false)
	putUplinkExecution(t, store, uplinkInstance, "a", uplinkRelease)
	status := &nodev1.InstanceStatus{
		InstanceId: uplinkInstance, ObservedGeneration: 7, Phase: nodev1.InstancePhase_INSTANCE_PHASE_RUNNING,
		UpdatedAt: timestamppb.New(time.Unix(100, 0)), Ready: true,
		UnifiedGuardian: &resourcev1.UnifiedGuardianState{Generation: 7, SelectionRevision: 3, State: "active"},
		UnifiedResourceReports: []*resourcev1.UnifiedInstallationReport{
			{Generation: 7, ObservedGeneration: 7, SelectionRevision: 3, State: "applied"},
		},
	}
	if err := store.PutActual(state.ActualRecord{Status: status}); err != nil {
		t.Fatal(err)
	}
	rec := reconciler.New(store, driver, nil, 1)
	rec.Activation = &runtime.ReleaseActivation{Store: store}
	t.Cleanup(rec.Close)
	return New(Config{Store: store, Reconciler: rec}), driver, status
}

func putUplinkExecution(t *testing.T, store *state.Store, id, idDigit string, release state.ReleaseCandidate) {
	t.Helper()
	envelope := &nodev1.RuntimeReleaseEnvelope{
		Purpose: release.Partition.Purpose, Name: release.Partition.Name, Os: release.Partition.OS, Arch: release.Partition.Arch,
		ReleaseSequence: release.Sequence, ImageDigest: release.BodyDigest,
	}
	err := store.PutExecution(state.ExecutionRecord{
		Version: 2, InstanceID: id, Generation: 7, Phase: "started",
		ContainerID: strings.Repeat(idDigit, 64), ConfigID: release.BodyDigest, EngineImageID: release.BodyDigest, ProofDigest: release.BodyDigest,
		DSH: &nodev1.DshRuntime{ReleaseEnvelope: envelope}, Candidate: release,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func runUplinkRelease(t *testing.T, store *state.Store, release state.ReleaseCandidate, unresolved bool) {
	t.Helper()
	err := store.WithReleaseExecution(t.Context(), release, func(context.Context) error { return nil }, func(_ context.Context, execution *state.ReleaseExecution) error {
		if !unresolved {
			return nil
		}
		finish, err := execution.BeginAction()
		if err != nil {
			return err
		}
		finish(false)
		return nil
	})
	if unresolved && errors.Is(err, state.ErrReleaseUnresolved) {
		return
	}
	if unresolved || err != nil {
		t.Fatalf("unexpected release execution result (unresolved=%t): %v", unresolved, err)
	}
}

type blockedUplinkStream struct {
	nodev1.NodeControlService_SessionClient
	ctx     context.Context
	blocked chan struct{}
	resume  chan struct{}
	frames  chan *nodev1.SessionRequest
}

func (s *blockedUplinkStream) Send(msg *nodev1.SessionRequest) error {
	if msg.GetUsage() != nil {
		close(s.blocked)
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-s.resume:
		}
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.frames <- msg:
		return nil
	}
}

func startBlockedUplink(t *testing.T, s *Session) (<-chan *nodev1.SessionRequest, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	stream := &blockedUplinkStream{ctx: ctx, blocked: make(chan struct{}), resume: make(chan struct{}), frames: make(chan *nodev1.SessionRequest, 8)}
	done := make(chan error, 1)
	go func() { done <- s.sendLoop(ctx, stream, time.Hour, nil) }()
	var once sync.Once
	resume := func() { once.Do(func() { close(stream.resume) }) }
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("send loop returned %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("send loop failed to stop")
		}
	})
	s.ReportUsage(&nodev1.NodeUsage{})
	select {
	case <-stream.blocked:
	case <-time.After(3 * time.Second):
		t.Fatal("sender did not block on the preceding frame")
	}
	return stream.frames, resume
}

func nextUplinkFrame(t *testing.T, frames <-chan *nodev1.SessionRequest) *nodev1.SessionRequest {
	t.Helper()
	select {
	case msg := <-frames:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("missing uplink frame")
		return nil
	}
}

func (d *uplinkLeaseDriver) blockInspection(t *testing.T, id string) func() {
	t.Helper()
	barrier := &uplinkInspectionBarrier{id: id, entered: make(chan struct{}), resume: make(chan struct{})}
	d.barrier.Store(barrier)
	var once sync.Once
	resume := func() { once.Do(func() { close(barrier.resume) }) }
	t.Cleanup(resume)
	return resume
}

func (d *uplinkLeaseDriver) waitForInspection(t *testing.T) {
	t.Helper()
	select {
	case <-d.barrier.Load().entered:
	case <-time.After(3 * time.Second):
		t.Fatal("uplink did not reach the blocked inspection")
	}
}

func TestSessionRechecksInventoryBeforeSend(t *testing.T) {
	const later = "zz-uplink"
	for _, scenario := range []string{"expired", "renewed", "same-generation-replacement", "already-replaced", "no-longer-applied", "partition-intent", "partition-advanced"} {
		t.Run(scenario, func(t *testing.T) {
			s, driver, status := uplinkFixture(t)
			otherRelease := uplinkRelease
			otherRelease.Partition.Arch = "arm64"
			runUplinkRelease(t, driver.store, otherRelease, false)
			putUplinkExecution(t, driver.store, later, "c", otherRelease)
			other := proto.Clone(status).(*nodev1.InstanceStatus)
			other.InstanceId = later
			if err := driver.store.PutActual(state.ActualRecord{Status: other}); err != nil {
				t.Fatal(err)
			}
			if scenario == "already-replaced" {
				putUplinkExecution(t, driver.store, uplinkInstance, "b", uplinkRelease)
			}
			resume := driver.blockInspection(t, later)
			stream := &blockedUplinkStream{ctx: t.Context(), frames: make(chan *nodev1.SessionRequest, 1)}
			done := make(chan error, 1)
			go func() { done <- s.sendInventory(stream) }()
			defer func() {
				resume()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(3 * time.Second):
					t.Error("inventory send did not finish")
				}
			}()
			driver.waitForInspection(t)
			setUplinkScenario(t, driver, scenario)
			if scenario == "no-longer-applied" || scenario == "same-generation-replacement" {
				current := proto.Clone(status).(*nodev1.InstanceStatus)
				if scenario == "no-longer-applied" {
					current.UnifiedResourceReports[0].State = "applying"
				}
				if err := driver.store.PutActual(state.ActualRecord{Status: current}); err != nil {
					t.Fatal(err)
				}
			}
			resume()
			list := nextUplinkFrame(t, stream.frames).GetInventory().GetInstances()
			if len(list) != 2 || list[0].GetInstanceId() != uplinkInstance || !proto.Equal(list[1], other) {
				t.Fatal("inventory lost the other verified instance", list)
			}
			wantReady := scenario == "renewed" || scenario == "no-longer-applied"
			wantApplied := scenario == "renewed"
			got := list[0]
			if got.GetReady() != wantReady || (len(got.GetUnifiedResourceReports()) == 1) != wantApplied ||
				!proto.Equal(got.GetUpdatedAt(), status.GetUpdatedAt()) || got.GetObservedGeneration() != status.GetObservedGeneration() {
				t.Fatal("inventory did not recheck the first instance before sending", got)
			}
		})
	}
}

func TestSessionReadsReportsAfterBlockedInspection(t *testing.T) {
	for _, scenario := range []string{"unchanged", "applying", "same-generation-replacement", "partition-intent", "partition-advanced"} {
		t.Run(scenario, func(t *testing.T) {
			s, driver, status := uplinkFixture(t)
			report := proto.Clone(status.GetUnifiedResourceReports()[0]).(*resourcev1.UnifiedInstallationReport)
			report.Identity = &resourcev1.UnifiedResourceIdentity{PackageId: "another-plugin"}
			status.UnifiedResourceReports = append(status.UnifiedResourceReports, report)
			if err := driver.store.PutActual(state.ActualRecord{Status: status}); err != nil {
				t.Fatal(err)
			}
			resumeInspect := driver.blockInspection(t, uplinkInstance)
			frames, resume := startBlockedUplink(t, s)
			s.StatusSink()(status)
			resume()
			if nextUplinkFrame(t, frames).GetUsage() == nil {
				t.Fatal("the blocked frame did not go first")
			}
			driver.waitForInspection(t)
			current := proto.Clone(status).(*nodev1.InstanceStatus)
			if scenario == "applying" {
				current.UnifiedResourceReports[0].State = "applying"
			}
			setUplinkScenario(t, driver, scenario)
			if err := driver.store.PutActual(state.ActualRecord{Status: current}); err != nil {
				t.Fatal(err)
			}
			resumeInspect()
			got := nextUplinkFrame(t, frames).GetStatus()
			want := status.GetUnifiedResourceReports()
			switch scenario {
			case "applying":
				want = want[1:]
			case "same-generation-replacement", "partition-intent", "partition-advanced":
				want = nil
			}
			wantReady := scenario == "unchanged" || scenario == "applying"
			if got == nil || got.GetReady() != wantReady ||
				!proto.Equal(got.GetUpdatedAt(), status.GetUpdatedAt()) || len(got.GetUnifiedResourceReports()) != len(want) {
				t.Fatal("uplink did not bind READY and reports to the post-Docker snapshot", got)
			}
			for i, report := range want {
				if !proto.Equal(got.GetUnifiedResourceReports()[i], report) {
					t.Fatal("uplink dropped a current report", got)
				}
			}
		})
	}
}

func TestSessionRechecksQueuedReadyStatus(t *testing.T) {
	for _, scenario := range []string{"valid", "expired", "renewed", "same-generation-replacement", "no-longer-applied"} {
		t.Run(scenario, func(t *testing.T) {
			s, driver, status := uplinkFixture(t)
			frames, resume := startBlockedUplink(t, s)
			s.StatusSink()(status)
			if len(s.statusCh) != 1 {
				t.Fatal("READY was not queued while the previous frame blocked")
			}
			setUplinkScenario(t, driver, scenario)
			var successor *nodev1.InstanceStatus
			switch scenario {
			case "same-generation-replacement":
				successor = proto.Clone(status).(*nodev1.InstanceStatus)
				successor.UpdatedAt = timestamppb.New(status.UpdatedAt.AsTime().Add(time.Second))
				if err := s.cfg.Store.PutActual(state.ActualRecord{Status: successor}); err != nil {
					t.Fatal(err)
				}
				s.StatusSink()(successor)
			case "no-longer-applied":
				current := proto.Clone(status).(*nodev1.InstanceStatus)
				current.UnifiedGuardian.State = "private_only"
				current.UnifiedResourceReports = nil
				if err := s.cfg.Store.PutActual(state.ActualRecord{Status: current}); err != nil {
					t.Fatal(err)
				}
			}
			resume()
			if nextUplinkFrame(t, frames).GetUsage() == nil {
				t.Fatal("the blocked frame did not go first")
			}
			got := nextUplinkFrame(t, frames).GetStatus()
			wantApplied := scenario == "valid" || scenario == "renewed"
			wantReady := wantApplied || scenario == "no-longer-applied"
			if got == nil || got.GetReady() != wantReady || (len(got.GetUnifiedResourceReports()) == 1) != wantApplied ||
				got.GetObservedGeneration() != status.GetObservedGeneration() || !proto.Equal(got.GetUpdatedAt(), status.GetUpdatedAt()) {
				t.Fatal("queued READY did not follow live execution", got)
			}
			if successor != nil {
				if got := nextUplinkFrame(t, frames).GetStatus(); !proto.Equal(got, successor) {
					t.Fatal("late rejection reordered or invalidated the replacement's READY", got)
				}
			}
			if !status.GetReady() || len(status.GetUnifiedResourceReports()) != 1 {
				t.Fatal("transport edited its caller's status")
			}
		})
	}
}

func TestSessionRechecksQueuedRestartSuccess(t *testing.T) {
	for _, scenario := range []string{"valid", "expired", "renewed", "same-generation-replacement", "partition-intent", "partition-advanced"} {
		t.Run(scenario, func(t *testing.T) {
			s, driver, _ := uplinkFixture(t)
			const commandID = "finished-restart"
			command := &nodev1.Command{CommandId: commandID, InstanceId: uplinkInstance, Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}}
			request, err := proto.MarshalOptions{Deterministic: true}.Marshal(command)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.cfg.Store.PutRestartCommand(commandID, state.RestartRecord{Request: request, Generation: 7, Stopped: true, Complete: true}); err != nil {
				t.Fatal(err)
			}
			frames, resume := startBlockedUplink(t, s)
			incoming := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_Command{Command: command}}
			if err := s.handleControl(t.Context(), incoming); err != nil {
				t.Fatal(err)
			}
			if err := testutil.WaitFor(3*time.Second, func() bool { return len(s.resultCh) == 1 }); err != nil {
				t.Fatal("the original result was not queued", err)
			}
			var resumeInspect func()
			if scenario == "partition-intent" || scenario == "partition-advanced" {
				// The replay has already checked readiness to queue the result.
				// Block the separate inspection immediately before sending it.
				resumeInspect = driver.blockInspection(t, uplinkInstance)
			} else {
				setUplinkScenario(t, driver, scenario)
			}
			resume()
			if nextUplinkFrame(t, frames).GetUsage() == nil {
				t.Fatal("the blocked frame did not go first")
			}
			if resumeInspect != nil {
				driver.waitForInspection(t)
				setUplinkScenario(t, driver, scenario)
				resumeInspect()
			}
			got := nextUplinkFrame(t, frames).GetResult()
			wantOK := scenario == "valid" || scenario == "renewed"
			if got == nil || got.GetOk() != wantOK || got.GetCommandId() != commandID || got.GetInstanceId() != uplinkInstance {
				t.Fatal("queued Restart result did not follow live execution", got)
			}
			if !wantOK && !got.GetError().GetRetryable() {
				t.Fatal("stale result should be retryable", got)
			}
			if scenario == "expired" {
				driver.deadline.Store(20)
				if err := s.handleControl(t.Context(), incoming); err != nil {
					t.Fatal(err)
				}
				if got := nextUplinkFrame(t, frames).GetResult(); !got.GetOk() || got.GetCommandId() != commandID {
					t.Fatal("eligible replay did not reuse completion checkpoint", got)
				}
			}
			checkpoint, err := s.cfg.Store.GetRestartCommand(commandID)
			if err != nil || checkpoint == nil || !checkpoint.Complete || !checkpoint.Stopped || driver.stops.Load() != 0 {
				t.Fatal("uplink validation changed the Restart action", checkpoint, err, driver.stops.Load())
			}
		})
	}
}

func setUplinkScenario(t *testing.T, driver *uplinkLeaseDriver, scenario string) {
	t.Helper()
	switch scenario {
	case "expired":
		driver.now.Store(10)
	case "renewed":
		driver.deadline.Store(20)
		driver.now.Store(10)
	case "same-generation-replacement":
		putUplinkExecution(t, driver.store, uplinkInstance, "b", uplinkRelease)
	case "partition-intent":
		// Keep the same high-water mark to isolate the effect of the new intent.
		runUplinkRelease(t, driver.store, uplinkRelease, true)
		if err := driver.store.CheckReleaseExecution(uplinkRelease); !errors.Is(err, state.ErrReleaseUnresolved) {
			t.Fatalf("missing durable intent: %v", err)
		}
	case "partition-advanced":
		next := uplinkRelease
		next.Sequence++
		next.BodyDigest = "sha256:" + strings.Repeat("d", 64)
		runUplinkRelease(t, driver.store, next, false)
		if err := driver.store.CheckReleaseExecution(uplinkRelease); !errors.Is(err, state.ErrReleaseRollback) {
			t.Fatalf("missing durable high-water advance: %v", err)
		}
	}
}
