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

// The controllable driver models Docker's lease-aware inspection. Session,
// the Reconciler, command replay and both bbolt records run production code.
type uplinkLeaseDriver struct {
	*runtime.Fake
	store         *state.Store
	now, deadline atomic.Int64
	stops         atomic.Int32
	blockID       string
	inspecting    chan struct{}
	resumeInspect chan struct{}
}

func (d *uplinkLeaseDriver) Inspect(ctx context.Context, id string) (runtime.Status, error) {
	record, err := d.store.GetExecution(id)
	if err != nil || record == nil {
		return runtime.Status{}, err
	}
	if id == d.blockID {
		close(d.inspecting)
		select {
		case <-ctx.Done():
			return runtime.Status{}, ctx.Err()
		case <-d.resumeInspect:
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
	putUplinkExecution(t, store, uplinkInstance, "a")
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

func putUplinkExecution(t *testing.T, store *state.Store, id, idDigit string) {
	t.Helper()
	digest := "sha256:" + strings.Repeat("c", 64)
	envelope := &nodev1.RuntimeReleaseEnvelope{
		Purpose: "dsh_runtime", Name: "dsh", Os: "linux", Arch: "amd64", ReleaseSequence: 1, ImageDigest: digest,
	}
	err := store.PutExecution(state.ExecutionRecord{
		Version: 2, InstanceID: id, Generation: 7, Phase: "started",
		ContainerID: strings.Repeat(idDigit, 64), ConfigID: digest, EngineImageID: digest, ProofDigest: digest,
		DSH: &nodev1.DshRuntime{ReleaseEnvelope: envelope},
		Candidate: state.ReleaseCandidate{
			Partition: state.ReleasePartition{Purpose: envelope.Purpose, Name: envelope.Name, OS: envelope.Os, Arch: envelope.Arch},
			Sequence:  envelope.ReleaseSequence, BodyDigest: digest,
		},
	})
	if err != nil {
		t.Fatal(err)
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
	d.blockID = id
	d.inspecting, d.resumeInspect = make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(d.resumeInspect) }) }
	t.Cleanup(resume)
	return resume
}

func (d *uplinkLeaseDriver) waitForInspection(t *testing.T) {
	t.Helper()
	select {
	case <-d.inspecting:
	case <-time.After(3 * time.Second):
		t.Fatal("uplink did not reach the blocked inspection")
	}
}

func TestSessionRechecksInventoryBeforeSend(t *testing.T) {
	const later = "zz-uplink"
	for _, scenario := range []string{"expired", "renewed", "same-generation-replacement", "already-replaced", "no-longer-applied"} {
		t.Run(scenario, func(t *testing.T) {
			s, driver, status := uplinkFixture(t)
			putUplinkExecution(t, driver.store, later, "c")
			other := proto.Clone(status).(*nodev1.InstanceStatus)
			other.InstanceId = later
			if err := driver.store.PutActual(state.ActualRecord{Status: other}); err != nil {
				t.Fatal(err)
			}
			if scenario == "already-replaced" {
				putUplinkExecution(t, driver.store, uplinkInstance, "b")
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
			s.nowAtDequeue(t, driver, scenario)
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
	for _, scenario := range []string{"unchanged", "applying", "same-generation-replacement"} {
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
			s.nowAtDequeue(t, driver, scenario)
			if err := driver.store.PutActual(state.ActualRecord{Status: current}); err != nil {
				t.Fatal(err)
			}
			resumeInspect()
			got := nextUplinkFrame(t, frames).GetStatus()
			want := status.GetUnifiedResourceReports()
			switch scenario {
			case "applying":
				want = want[1:]
			case "same-generation-replacement":
				want = nil
			}
			if got == nil || got.GetReady() != (scenario != "same-generation-replacement") ||
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
			s.nowAtDequeue(t, driver, scenario)
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
	for _, scenario := range []string{"valid", "expired", "renewed", "same-generation-replacement"} {
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
			s.nowAtDequeue(t, driver, scenario)
			resume()
			if nextUplinkFrame(t, frames).GetUsage() == nil {
				t.Fatal("the blocked frame did not go first")
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

func (s *Session) nowAtDequeue(t *testing.T, driver *uplinkLeaseDriver, scenario string) {
	t.Helper()
	switch scenario {
	case "expired":
		driver.now.Store(10)
	case "renewed":
		driver.deadline.Store(20)
		driver.now.Store(10)
	case "same-generation-replacement":
		putUplinkExecution(t, s.cfg.Store, uplinkInstance, "b")
	}
}
