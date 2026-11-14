package modelgate

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

type SharedRequest struct {
	TenantID, SessionID, TaskID, CallID, Capability string
	Body                                            []byte
}

// SharedCall is constructed after online authorization. Relay implementations
// must use Binding.MemberID, never the shared physical instance's owner key.
type SharedCall struct {
	Binding        sharedauth.Binding
	TaskID, CallID string
	Body           []byte
}
type SharedUsage struct {
	InputTokens, OutputTokens int64
	Known                     bool
}
type SharedRelay interface {
	Stream(context.Context, SharedCall, func([]byte) error) (SharedUsage, error)
}

// SharedLedger is a required durable port. Begin atomically reserves by complete
// binding+callId and rejects fingerprint conflicts; false means already begun
// (including ambiguous/cancelled outcomes) and MUST NOT invoke the relay again.
// Finish must durably settle or quarantine unknown usage, idempotently. Existing
// budget/relay metering implements this port at joint wiring; no memory ledger
// or fallback account is used in production here.
type SharedLedger interface {
	Begin(context.Context, sharedauth.Binding, string, [32]byte) (bool, error)
	Finish(context.Context, sharedauth.Binding, string, SharedUsage, error) error
}
type SharedProvider struct {
	gate   *sharedauth.Gate
	relay  SharedRelay
	ledger SharedLedger
}

func NewSharedProvider(g *sharedauth.Gate, r SharedRelay, l SharedLedger) (*SharedProvider, error) {
	if g == nil || r == nil || l == nil {
		return nil, sharedauth.ErrInvalid
	}
	return &SharedProvider{g, r, l}, nil
}
func (p *SharedProvider) Invoke(ctx context.Context, q SharedRequest, emit func([]byte) error) error {
	if !sharedauth.UUID(q.CallID) || len(q.Body) == 0 || len(q.Body) > 4*1024*1024 || emit == nil {
		return sharedauth.ErrInvalid
	}
	c, e := p.gate.Verify(ctx, q.Capability, q.TenantID, q.SessionID, q.TaskID, sharedauth.Model, "model.invoke")
	if e != nil {
		return e
	}
	body := append([]byte(nil), q.Body...)
	fresh, e := p.ledger.Begin(ctx, c.Binding, q.CallID, sha256.Sum256(body))
	if e != nil {
		return e
	}
	if !fresh {
		return fault("common.conflict")
	}
	run, cancel := context.WithDeadline(ctx, time.Unix(c.ExpiresAt, 0))
	defer cancel()
	// Recheck during silent streams as well as before every delivered chunk.
	done := make(chan struct{})
	defer close(done)
	watchDone := make(chan struct{})
	defer func() { cancel(); <-watchDone }()
	go func() {
		defer close(watchDone)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-run.Done():
				return
			case <-tick.C:
				if p.gate.Recheck(run, c, "model.invoke") != nil {
					cancel()
					return
				}
			}
		}
	}()
	usage, runErr := p.relay.Stream(run, SharedCall{c.Binding, c.TaskID, q.CallID, body}, func(chunk []byte) error {
		if run.Err() != nil {
			return run.Err()
		}
		if e := p.gate.Recheck(run, c, "model.invoke"); e != nil {
			cancel()
			return e
		}
		return emit(append([]byte(nil), chunk...))
	})
	if runErr == nil {
		runErr = run.Err()
	}
	if runErr == nil {
		runErr = p.gate.Recheck(run, c, "model.invoke")
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 {
		usage = SharedUsage{}
		runErr = sharedauth.ErrUnavailable
	}
	// Caller cancellation cannot suppress settlement. No automatic model retries.
	settle, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	if e := p.ledger.Finish(settle, c.Binding, q.CallID, usage, runErr); e != nil {
		return sharedauth.ErrUnavailable
	}
	return runErr
}
