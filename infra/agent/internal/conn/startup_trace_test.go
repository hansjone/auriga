package conn

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/reconciler"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/state"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

func TestControlApplyCarriesMessageCauseNotSessionCause(t *testing.T) {
	store, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	driver := runtime.NewFake()
	driver.FailNextStart = true
	rec := reconciler.New(store, driver, slog.New(slog.NewJSONHandler(io.Discard, nil)), 1)
	defer rec.Close()
	session := New(Config{Store: store, Reconciler: rec, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	message := &nodev1.SessionResponse{Body: &nodev1.SessionResponse_Apply{Apply: &nodev1.ApplySpec{Spec: &nodev1.InstanceSpec{InstanceId: "instance-a", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}, Traceparent: parent}}}
	if err := session.handleControl(ctx, message); err != nil {
		t.Fatal(err)
	}
	rec.Close()
	actual, err := store.GetActual("instance-a")
	if err != nil {
		t.Fatal(err)
	}
	if actual.Status == nil || actual.Status.GetError().GetAttempts() != 1 || store.StartupParent("instance-a", 1) != parent {
		t.Fatal("control cause lost or cancellation changed execution")
	}
	// Invalid optional metadata cannot reject a real desired write.
	invalid := &nodev1.InstanceSpec{InstanceId: "instance-b", Generation: 1, Desired: nodev1.DesiredState_DESIRED_STATE_RUNNING}
	if err := store.PutDesiredWithTrace(invalid, strings.Repeat("x", 10000)); err != nil {
		t.Fatal(err)
	}
	if store.StartupParent("instance-b", 1) != "" {
		t.Fatal("invalid parent retained")
	}
}
