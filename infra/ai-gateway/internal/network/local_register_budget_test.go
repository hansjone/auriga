package network

import (
	"context"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
)

type delayedRegistrationMailbox struct {
	fakeMailboxRPC
	delay     time.Duration
	remaining chan time.Duration
}

func (f *delayedRegistrationMailbox) LocalRegister(ctx context.Context, req *collab.NetworkMailboxServiceLocalRegisterRequest) (*collab.NetworkMailboxServiceLocalRegisterResponse, error) {
	if deadline, ok := ctx.Deadline(); ok {
		f.remaining <- time.Until(deadline)
	}
	select {
	case <-time.After(f.delay):
		return f.fakeMailboxRPC.LocalRegister(ctx, req)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestLocalRegistrationHasIndependentBoundedBusinessBudget(t *testing.T) {
	for _, parentBudget := range []time.Duration{0, 50 * time.Millisecond} {
		t.Run(parentBudget.String(), func(t *testing.T) {
			_, cfg, _ := runtimeFixture(t)
			f := &delayedRegistrationMailbox{fakeMailboxRPC: fakeMailboxRPC{response: []byte(`{"proof":"unchanged"}`)}, delay: 350 * time.Millisecond, remaining: make(chan time.Duration, 1)}
			cfg.Collab = startMailboxRPC(t, f)
			run, err := StartRuntime(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(run.Close)
			ctx := t.Context()
			if parentBudget > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, parentBudget)
				defer cancel()
			}
			data, ae := run.mailbox.client.Call(ctx, liveBinding{Tenant: testTenant, Realm: "1", Node: "1"}, "local_register", []byte(validLocalRegister))
			if parentBudget == 0 {
				if ae != nil || string(data) != `{"proof":"unchanged"}` {
					t.Fatalf("registration was cut off by the %dms reconciliation budget: %v", cfg.RequestTimeoutMillis, ae)
				}
			} else if ae == nil || ae.Code != "network.overloaded" || !ae.Retryable || len(data) != 0 {
				t.Fatalf("registration escaped the earlier parent deadline: data=%q error=%v", data, ae)
			}
			select {
			case remaining := <-f.remaining:
				if remaining <= 0 || remaining > 20*time.Second || parentBudget == 0 && remaining < 19*time.Second {
					t.Fatalf("unexpected registration deadline: %s", remaining)
				}
			case <-time.After(time.Second):
				t.Fatal("registration never reached the fixed RPC target")
			}
		})
	}
}
