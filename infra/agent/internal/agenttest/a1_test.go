package agenttest_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
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

func TestSessionHeartbeatReconnectAndReconcile(t *testing.T) {
	fw, err := testutil.StartFakeWorkspace(testutil.FakeWorkspaceOpts{
		HeartbeatInterval: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fw.Close()

	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	_, err = enroll.Join(context.Background(), enroll.Options{AllowInsecure: true,
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
	rec.Sink = sess.StatusSink()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- sess.Run(ctx) }()

	if err := testutil.WaitFor(5*time.Second, func() bool { return sess.Connected() }); err != nil {
		t.Fatal("connect:", err)
	}
	if err := testutil.WaitFor(3*time.Second, func() bool {
		_, _, usage := fw.Stats()
		return usage >= 1
	}); err != nil {
		t.Fatal("heartbeat:", err)
	}

	fw.PushApply(&nodev1.InstanceSpec{
		InstanceId: "inst_01",
		TenantId:   "t_default",
		OwnerId:    "u_alice",
		Generation: 1,
		Runtime:    nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
		Desired:    nodev1.DesiredState_DESIRED_STATE_RUNNING,
	})
	if err := testutil.WaitFor(3*time.Second, func() bool {
		rec, _ := store.GetActual("inst_01")
		return rec.Status != nil && rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_RUNNING && rec.Status.Ready
	}); err != nil {
		t.Fatal("reconcile running:", err)
	}

	helloBefore, _, _ := fw.Stats()

	fw.PushApply(&nodev1.InstanceSpec{
		InstanceId: "inst_01",
		TenantId:   "t_default",
		OwnerId:    "u_alice",
		Generation: 2,
		Runtime:    nodev1.RuntimeKind_RUNTIME_KIND_DOCKER,
		Desired:    nodev1.DesiredState_DESIRED_STATE_STOPPED,
	})
	if err := testutil.WaitFor(3*time.Second, func() bool {
		rec, _ := store.GetActual("inst_01")
		return rec.Status != nil && rec.Status.Phase == nodev1.InstancePhase_INSTANCE_PHASE_STOPPED
	}); err != nil {
		t.Fatal("reconcile stopped:", err)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Logf("session ended: %v", err)
	}

	fw.SetDropNextSession(true)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
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
	go func() { _ = sess2.Run(ctx2) }()

	if err := testutil.WaitFor(5*time.Second, func() bool {
		hello, _, _ := fw.Stats()
		// DropNextSession fails before Hello; successful reconnect adds one Hello.
		return hello > helloBefore
	}); err != nil {
		t.Fatal("reconnect hello:", err)
	}
	if err := testutil.WaitFor(3*time.Second, sess2.Connected); err != nil {
		t.Fatal("reconnect connected:", err)
	}
	cancel2()
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
