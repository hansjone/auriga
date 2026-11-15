package sharedmodel

import (
	"context"
	"crypto/sha256"
	"sync"
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

// SharedLedger is a required durable port. Begin atomically reserves by stable
// tenant/member/callId and rejects binding or fingerprint conflicts; false means already begun
// (including ambiguous/cancelled outcomes) and MUST NOT invoke the relay again.
// Finish must durably settle or quarantine unknown usage, idempotently.
// PostgresSharedLedger owns call attribution and dispatch deduplication, while
// the existing relay budget ledger owns monetary reservations and charging.
type SharedLedger interface {
	Begin(context.Context, sharedauth.Binding, string, [32]byte) (bool, error)
	Finish(context.Context, sharedauth.Binding, string, SharedUsage, error) error
}
type SharedProvider struct {
	gate   *sharedauth.Gate
	relay  SharedRelay
	ledger SharedLedger
}

// Match the relay's existing maximum call lifetime, independent of capability
// TTL. A caller's shorter deadline and cooperative cancellation still win.
const MaxCallDuration = 150 * time.Second

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
		return sharedauth.ErrConflict
	}
	run, cancel := context.WithTimeout(ctx, MaxCallDuration)
	defer cancel()
	// The watcher always checks online. Fast chunks reuse only this invocation's
	// latest success for at most one watcher interval, avoiding per-token RPCs.
	const checkInterval = 250 * time.Millisecond
	var checkMu sync.Mutex
	var checkedAt time.Time
	var checkErr error
	checkStream := func(force bool) error {
		checkMu.Lock()
		defer checkMu.Unlock()
		if force || checkedAt.IsZero() || time.Since(checkedAt) >= checkInterval {
			checkErr = p.gate.RecheckModelStream(run, c)
			checkedAt = time.Now()
		}
		return checkErr
	}
	done := make(chan struct{})
	defer close(done)
	watchDone := make(chan struct{})
	defer func() { cancel(); <-watchDone }()
	go func() {
		defer close(watchDone)
		tick := time.NewTicker(checkInterval)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-run.Done():
				return
			case <-tick.C:
				if checkStream(true) != nil {
					cancel()
					return
				}
			}
		}
	}()
	usage := SharedUsage{Known: true}
	runErr := p.gate.Recheck(run, c, "model.invoke")
	if runErr == nil {
		usage, runErr = p.relay.Stream(run, SharedCall{c.Binding, c.TaskID, q.CallID, body}, func(chunk []byte) error {
			if run.Err() != nil {
				return run.Err()
			}
			if e := checkStream(false); e != nil {
				cancel()
				return e
			}
			return emit(append([]byte(nil), chunk...))
		})
	}
	if runErr == nil {
		runErr = run.Err()
	}
	if runErr == nil {
		runErr = checkStream(true)
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.InputTokens > 9007199254740991 || usage.OutputTokens > 9007199254740991 {
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
