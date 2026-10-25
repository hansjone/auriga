package agenttest_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/conn"
	"github.com/hansjone/auriga/infra/agent/internal/enroll"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestJoinAndRejectBadTokenAndBadFingerprint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	t.Run("bad_token", func(t *testing.T) {
		fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{})
		if err != nil {
			t.Fatal(err)
		}
		defer fw.Close()
		_, err = enroll.Join(context.Background(), enroll.Options{AllowInsecure: true,
			Server:        fw.URL(),
			Token:         "wrong.token",
			CAFingerprint: fw.CA.Fingerprint(),
			AgentDir:      filepath.Join(t.TempDir(), "agent"),
			AgentVersion:  "test",
			Logger:        logger,
		})
		if err == nil {
			t.Fatal("expected bad token rejection")
		}
	})

	t.Run("bad_fingerprint", func(t *testing.T) {
		fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{})
		if err != nil {
			t.Fatal(err)
		}
		defer fw.Close()
		_, err = enroll.Join(context.Background(), enroll.Options{AllowInsecure: true,
			Server:        fw.URL(),
			Token:         fw.Token(),
			CAFingerprint: "0000000000000000000000000000000000000000000000000000000000000000",
			AgentDir:      filepath.Join(t.TempDir(), "agent"),
			AgentVersion:  "test",
			Logger:        logger,
		})
		if err == nil {
			t.Fatal("expected bad fingerprint rejection")
		}
	})

	t.Run("ok", func(t *testing.T) {
		fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{})
		if err != nil {
			t.Fatal(err)
		}
		defer fw.Close()
		agentDir := filepath.Join(t.TempDir(), "agent")
		mat, err := enroll.Join(context.Background(), enroll.Options{AllowInsecure: true,
			Server:        fw.URL(),
			Token:         fw.Token(),
			CAFingerprint: fw.CA.Fingerprint(),
			AgentDir:      agentDir,
			AgentVersion:  "test",
			Logger:        logger,
		})
		if err != nil {
			t.Fatal(err)
		}
		if mat.NodeID != "n_test01" {
			t.Fatalf("node id: %s", mat.NodeID)
		}
		if _, err := os.Stat(filepath.Join(agentDir, identity.KeyFileName)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestExpiredCertificateRejected(t *testing.T) {
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{RejectExpired: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()

	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	mat, err := enroll.Join(context.Background(), enroll.Options{AllowInsecure: true,
		Server:        fw.URL(),
		Token:         fw.Token(),
		CAFingerprint: fw.CA.Fingerprint(),
		AgentDir:      agentDir,
		AgentVersion:  "test",
		Logger:        logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	expiredPEM, err := fw.CA.IssueNode(mat.NodeID, &mat.PrivateKey.PublicKey, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	mat.CertificatePEM = expiredPEM
	mat.CertificateExpiresAt = time.Now().Add(-time.Hour)
	if err := identity.Save(agentDir, mat); err != nil {
		t.Fatal(err)
	}

	store, err := state.Open(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	driver := runtime.NewFake()
	rec := reconciler.New(store, driver, logger, 2)
	defer rec.Close()

	sess := conn.New(conn.Config{AllowInsecure: true,
		AgentDir:        agentDir,
		AgentVersion:    "test",
		CAFingerprint:   fw.CA.Fingerprint(),
		Logger:          logger,
		Reconciler:      rec,
		Store:           store,
		MinBackoff:      10 * time.Millisecond,
		MaxBackoff:      50 * time.Millisecond,
		RenewCheckEvery: 24 * time.Hour,
		WelcomeTimeout:  time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = sess.Run(ctx)
	if sess.Connected() {
		t.Fatal("expired cert must not establish session")
	}
}

type sessionEvent struct {
	epoch   int64
	request *nodev1.SessionRequest
	closed  bool
	err     error
}

type observedSessionStream struct {
	grpc.ServerStream
	epoch  int64
	events chan<- sessionEvent
}

func (s *observedSessionStream) RecvMsg(msg any) error {
	if err := s.ServerStream.RecvMsg(msg); err != nil {
		return err
	}
	req := proto.Clone(msg.(*nodev1.SessionRequest)).(*nodev1.SessionRequest)
	select {
	case s.events <- sessionEvent{epoch: s.epoch, request: req}:
		return nil
	case <-s.Context().Done():
		return s.Context().Err()
	}
}

func TestSessionHeartbeatReconnectAndReconcile(t *testing.T) {
	// This bounds the integration test, including startup and disk persistence.
	// It is not a reconnect or reconcile latency SLA; teardown is bounded below.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events := make(chan sessionEvent, 128)
	var epoch atomic.Int64
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{
		HeartbeatInterval: 40 * time.Millisecond,
		SessionInterceptor: func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			e := epoch.Add(1)
			err := handler(srv, &observedSessionStream{ServerStream: stream, epoch: e, events: events})
			select {
			case events <- sessionEvent{epoch: e, closed: true, err: err}:
			case <-ctx.Done():
			}
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()

	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	_, err = enroll.Join(ctx, enroll.Options{AllowInsecure: true,
		Server:        fw.URL(),
		Token:         fw.Token(),
		CAFingerprint: fw.CA.Fingerprint(),
		AgentDir:      agentDir,
		AgentVersion:  "test",
		Logger:        logger,
	})
	if err != nil {
		t.Fatal(err)
	}

	store, err := state.Open(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	driver := runtime.NewFake()
	rec := reconciler.New(store, driver, logger, 2)
	defer rec.Close()

	sess := conn.New(conn.Config{AllowInsecure: true,
		AgentDir:          agentDir,
		AgentVersion:      "test",
		CAFingerprint:     fw.CA.Fingerprint(),
		Logger:            logger,
		Reconciler:        rec,
		Store:             store,
		HeartbeatInterval: 40 * time.Millisecond,
		MinBackoff:        20 * time.Millisecond,
		MaxBackoff:        100 * time.Millisecond,
		WelcomeTimeout:    5 * time.Second,
		RenewCheckEvery:   24 * time.Hour,
		StableFor:         time.Millisecond,
	})
	var activeSession atomic.Pointer[conn.Session]
	activeSession.Store(sess)
	rec.Sink = func(st *nodev1.InstanceStatus) { activeSession.Load().StatusSink()(st) }

	start := func(s *conn.Session) func() {
		runCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- s.Run(runCtx) }()
		var once sync.Once
		return func() {
			once.Do(func() {
				stop()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("session ended unexpectedly: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("session did not stop")
				}
			})
		}
	}
	stop := start(sess)
	defer stop()

	var trace []string
	wait := func(label string, match func(sessionEvent) bool) sessionEvent {
		t.Helper()
		for {
			select {
			case ev := <-events:
				detail := fmt.Sprintf("epoch=%d closed=%v err=%v", ev.epoch, ev.closed, ev.err)
				if ev.request != nil {
					detail += fmt.Sprintf(" request=%T", ev.request.Body)
					if st := ev.request.GetStatus(); st != nil {
						detail += fmt.Sprintf(" status=%v", st)
						if st.Error != nil || st.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED {
							t.Fatalf("%s: reconcile failed: %s; trace=%v", label, detail, trace)
						}
					}
				}
				trace = append(trace, detail)
				if match(ev) {
					return ev
				}
			case <-ctx.Done():
				desired, desiredErr := store.GetDesired("inst_01")
				actual, actualErr := store.GetActual("inst_01")
				t.Fatalf("%s: %v; trace=%v; desired=%v (%v); actual=%+v (%v)", label, ctx.Err(), trace, desired, desiredErr, actual, actualErr)
			}
		}
	}
	heartbeat := func(e int64, s *conn.Session) *nodev1.Inventory {
		t.Helper()
		wait("Hello", func(ev sessionEvent) bool { return ev.epoch == e && ev.request.GetHello() != nil })
		inventory := wait("Inventory", func(ev sessionEvent) bool { return ev.epoch == e && ev.request.GetInventory() != nil })
		// Usage is sent only after this stream's Welcome has been consumed.
		wait("heartbeat after Welcome", func(ev sessionEvent) bool { return ev.epoch == e && ev.request.GetUsage() != nil })
		if !s.Connected() {
			t.Fatalf("epoch %d: heartbeat without connected session; trace=%v", e, trace)
		}
		return inventory.request.GetInventory()
	}
	assertState := func(e, generation int64, phase nodev1.InstancePhase, ready bool) {
		t.Helper()
		wait("reconcile status uplink", func(ev sessionEvent) bool {
			st := ev.request.GetStatus()
			return ev.epoch == e && st.GetInstanceId() == "inst_01" && st.GetObservedGeneration() == generation && st.GetPhase() == phase && st.GetReady() == ready
		})
		actual, err := store.GetActual("inst_01")
		if err != nil || actual.Status == nil || actual.Status.ObservedGeneration != generation || actual.Status.Phase != phase || actual.Status.Ready != ready {
			t.Fatalf("persisted status differs from uplink: %+v, %v", actual, err)
		}
		running, err := driver.Inspect(ctx, "inst_01")
		if err != nil || running.Running != ready || running.Ready != ready {
			t.Fatalf("runtime differs from uplink: %+v, %v", running, err)
		}
	}
	heartbeat(1, sess)

	fw.PushApply(&nodev1.InstanceSpec{
		InstanceId: "inst_01",
		TenantId:   "t_default",
		OwnerId:    "u_alice",
		Generation: 1,
		Runtime:    nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
		Desired:    nodev1.DesiredState_DESIRED_STATE_RUNNING,
	})
	assertState(1, 1, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, true)

	fw.PushApply(&nodev1.InstanceSpec{
		InstanceId: "inst_01",
		TenantId:   "t_default",
		OwnerId:    "u_alice",
		Generation: 2,
		Runtime:    nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
		Desired:    nodev1.DesiredState_DESIRED_STATE_STOPPED,
	})
	assertState(1, 2, nodev1.InstancePhase_INSTANCE_PHASE_STOPPED, false)
	stop()
	wait("old server stream closed", func(ev sessionEvent) bool { return ev.epoch == 1 && ev.closed })
	if sess.Connected() {
		t.Fatal("old session still connected after stopping")
	}

	fw.SetDropNextSession(true)
	sess2 := conn.New(conn.Config{AllowInsecure: true,
		AgentDir:          agentDir,
		AgentVersion:      "test",
		CAFingerprint:     fw.CA.Fingerprint(),
		Logger:            logger,
		Reconciler:        rec,
		Store:             store,
		HeartbeatInterval: 40 * time.Millisecond,
		MinBackoff:        20 * time.Millisecond,
		MaxBackoff:        80 * time.Millisecond,
		WelcomeTimeout:    5 * time.Second,
		RenewCheckEvery:   24 * time.Hour,
		StableFor:         time.Millisecond,
	})
	activeSession.Store(sess2)
	stop2 := start(sess2)
	defer stop2()
	dropped := wait("forced drop", func(ev sessionEvent) bool { return ev.epoch == 2 && ev.closed })
	if status.Code(dropped.err) != codes.Unavailable {
		t.Fatalf("expected forced Unavailable: %v", dropped.err)
	}
	inventory := heartbeat(3, sess2)
	if len(inventory.Instances) != 1 || inventory.Instances[0].InstanceId != "inst_01" ||
		inventory.Instances[0].ObservedGeneration != 2 || inventory.Instances[0].Phase != nodev1.InstancePhase_INSTANCE_PHASE_STOPPED || inventory.Instances[0].Ready {
		t.Fatalf("new stream lost persisted stopped generation: %v", inventory)
	}
	// A fresh request on the new stream must still reach reconcile and return
	// its exact generation. Old counters or a stale status cannot satisfy this.
	fw.PushApply(&nodev1.InstanceSpec{
		InstanceId: "inst_01", TenantId: "t_default", OwnerId: "u_alice", Generation: 3,
		Runtime: nodev1.RuntimeKind_RUNTIME_KIND_DOCKER, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING,
	})
	assertState(3, 3, nodev1.InstancePhase_INSTANCE_PHASE_RUNNING, true)
	stop2()
	wait("new server stream closed", func(ev sessionEvent) bool { return ev.epoch == 3 && ev.closed })
}

func TestReconcilerPhasesAndRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	store, err := state.Open(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.Default()
	driver := runtime.NewFake()
	rec := reconciler.New(store, driver, logger, 2)

	spec := &nodev1.InstanceSpec{
		InstanceId: "inst_r1",
		Generation: 1,
		Desired:    nodev1.DesiredState_DESIRED_STATE_RUNNING,
		Runtime:    nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
	}
	if err := rec.Apply(spec); err != nil {
		t.Fatal(err)
	}
	if err := testutil.WaitFor(2*time.Second, func() bool {
		a, _ := store.GetActual("inst_r1")
		return a.Status != nil && a.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING
	}); err != nil {
		t.Fatal(err)
	}
	rec.Close()
	_ = store.Close()

	store2, err := state.Open(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }()
	got, err := store2.GetDesired("inst_r1")
	if err != nil || got == nil || got.Generation != 1 {
		t.Fatalf("desired not persisted: %#v %v", got, err)
	}
	actual, err := store2.GetActual("inst_r1")
	if err != nil || actual.Status == nil || actual.Status.Phase != nodev1.InstancePhase_INSTANCE_PHASE_RUNNING {
		t.Fatalf("actual not persisted: %#v %v", actual, err)
	}

	driver2 := runtime.NewFake()
	rec2 := reconciler.New(store2, driver2, logger, 2)
	defer rec2.Close()
	if err := rec2.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := testutil.WaitFor(2*time.Second, func() bool {
		st, _ := driver2.Inspect(context.Background(), "inst_r1")
		return st.Running && st.Ready
	}); err != nil {
		t.Fatal("driver not restored after restart:", err)
	}

	driver2.FailNextStart = true
	_ = driver2.Remove(context.Background(), "inst_r1")
	_ = store2.DeleteDesired("inst_r1")
	if err := rec2.Apply(&nodev1.InstanceSpec{
		InstanceId: "inst_fail",
		Generation: 1,
		Desired:    nodev1.DesiredState_DESIRED_STATE_RUNNING,
	}); err != nil {
		t.Fatal(err)
	}
	if err := testutil.WaitFor(2*time.Second, func() bool {
		a, _ := store2.GetActual("inst_fail")
		return a.Status != nil && a.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_FAILED
	}); err != nil {
		t.Fatal("expected failed phase:", err)
	}
}

func TestWrongServerCertRejected(t *testing.T) {
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()
	other, err := testutil.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_, err = enroll.Join(context.Background(), enroll.Options{AllowInsecure: true,
		Server:        fw.URL(),
		Token:         fw.Token(),
		CAFingerprint: other.Fingerprint(),
		AgentDir:      filepath.Join(dir, "agent"),
		AgentVersion:  "test",
		Logger:        slog.Default(),
	})
	if err == nil {
		t.Fatal("expected fingerprint mismatch")
	}
}
