package network

import (
	"context"
	"testing"
	"time"

	collab "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
)

type delayedDeliveryMailbox struct{ delayedRegistrationMailbox }

func (f *delayedDeliveryMailbox) wait(ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		f.remaining <- time.Until(deadline)
	}
	select {
	case <-time.After(f.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *delayedDeliveryMailbox) Fetch(ctx context.Context, req *collab.NetworkMailboxServiceFetchRequest) (*collab.NetworkMailboxServiceFetchResponse, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return f.fakeMailboxRPC.Fetch(ctx, req)
}

func (f *delayedDeliveryMailbox) Receipt(ctx context.Context, req *collab.NetworkMailboxServiceReceiptRequest) (*collab.NetworkMailboxServiceReceiptResponse, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return f.fakeMailboxRPC.Receipt(ctx, req)
}

func TestDurableDeliveryHasIndependentBoundedBusinessBudget(t *testing.T) {
	for _, operation := range []string{"fetch", "receipt"} {
		for _, delay := range []time.Duration{300 * time.Millisecond, time.Second} {
			for _, parentBudget := range []time.Duration{0, 50 * time.Millisecond} {
				t.Run(operation+"/"+delay.String()+"/"+parentBudget.String(), func(t *testing.T) {
					_, cfg, _ := runtimeFixture(t)
					f := &delayedDeliveryMailbox{delayedRegistrationMailbox{fakeMailboxRPC: fakeMailboxRPC{response: []byte(`{"proof":"unchanged"}`)}, delay: delay, remaining: make(chan time.Duration, 1)}}
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
					data, ae := run.mailbox.client.Call(ctx, liveBinding{Tenant: testTenant, Realm: "1", Node: "1"}, operation, []byte(`{}`))
					if parentBudget == 0 {
						if ae != nil || string(data) != `{"proof":"unchanged"}` {
							t.Fatalf("%s inherited the reconciliation deadline: %v", operation, ae)
						}
					} else if ae == nil || ae.Code != "network.overloaded" || !ae.Retryable || len(data) != 0 {
						t.Fatalf("%s escaped the earlier parent deadline: data=%q error=%v", operation, data, ae)
					}
					select {
					case remaining := <-f.remaining:
						if remaining <= 0 || remaining > 20*time.Second || parentBudget == 0 && remaining < 19*time.Second {
							t.Fatalf("unexpected business deadline: %s", remaining)
						}
					case <-time.After(time.Second):
						t.Fatal("delivery never reached collab")
					}
				})
			}
		}
	}
}
