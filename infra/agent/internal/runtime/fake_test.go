package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

func TestFakeDriverLifecycle(t *testing.T) {
	f := runtime.NewFake()
	ctx := context.Background()
	spec := &runtime.Spec{InstanceID: "x", Generation: 1, Desired: "running"}
	if err := f.Ensure(ctx, spec, &runtime.Rendered{}); err != nil {
		t.Fatal(err)
	}
	if err := f.Start(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	st, err := f.Inspect(ctx, "x")
	if err != nil || !st.Running || !st.Ready {
		t.Fatalf("%+v %v", st, err)
	}
	if err := f.Stop(ctx, "x", time.Second); err != nil {
		t.Fatal(err)
	}
	f.FailNextStart = true
	_ = f.Ensure(ctx, spec, &runtime.Rendered{})
	if err := f.Start(ctx, "x"); err == nil {
		t.Fatal("expected start failure")
	}
}
