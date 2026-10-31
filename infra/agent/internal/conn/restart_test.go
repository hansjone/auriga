package conn

import (
	"context"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/testutil"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestControlSessionReturnsRestartResultAndRejectsUnsupportedCommand(t *testing.T) {
	s, workspace, _ := sessionFixture(t, nil)
	rec := reconciler.New(s.cfg.Store, runtime.NewFake(), s.cfg.Logger, 1)
	t.Cleanup(rec.Close)
	s.cfg.Reconciler = rec
	rec.Sink = s.StatusSink()
	if err := rec.Apply(&nodev1.InstanceSpec{InstanceId: "session-restart", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}); err != nil {
		t.Fatal(err)
	}
	if err := testutil.WaitFor(time.Second, func() bool { a, e := s.cfg.Store.GetActual("session-restart"); return e == nil && a.Status.GetReady() }); err != nil {
		t.Fatal(err)
	}
	stop := runSession(t, s)
	defer stop()
	if err := testutil.WaitFor(time.Second, s.Connected); err != nil {
		t.Fatal(err)
	}
	for _, command := range []*nodev1.Command{
		{CommandId: "wire-restart", InstanceId: "session-restart", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}},
		{CommandId: "wire-unsupported", InstanceId: "session-restart", Kind: &nodev1.Command_Snapshot{Snapshot: &nodev1.SnapshotCommand{}}},
	} {
		workspace.PushCommand(command)
		select {
		case result := <-workspace.Results():
			if result.CommandId != command.CommandId || result.InstanceId != command.InstanceId {
				t.Fatalf("result identity mismatch: %v", result)
			}
			if command.GetRestart() != nil {
				if !result.Ok {
					t.Fatalf("restart result failed: %v", result)
				}
			} else if result.Ok || result.Error.GetCode() != "agent.policy_denied" {
				t.Fatalf("unsupported command accepted: %v", result)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("command result was not sent on control stream")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rec.Command(ctx, &nodev1.Command{CommandId: "cancelled", InstanceId: "session-restart", Kind: &nodev1.Command_Restart{Restart: &nodev1.RestartCommand{}}}).Ok {
		t.Fatal("cancelled command accepted")
	}
}
