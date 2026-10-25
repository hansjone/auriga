// Package relay composes the device-only D7 execution core. It has no HTTP
// listener, network transport, retries, or authority replacement interfaces.
package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	ai "github.com/hansjone/auriga/infra/gen/auriga/ai/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Policy resolves trusted configuration by tenant, model ID and captured
// versions. Never infer a tokenizer from the public alias. Capacity must match
// the fixed model exactly; VerifyUsage must attest the final provider usage.
type Policy func(context.Context, string, string, admin.RelayVersions) (modelprotocol.Policy, error)

// Exchange.Stopped closes only when ALL upstream execution has actually stopped.
// Close/cancellation/Transport return alone is not this evidence. Body.Close
// must interrupt Read promptly. On error Transport must still return any body
// and stop evidence it owns, including when submission is ambiguous.
type Exchange struct {
	Body    io.ReadCloser
	Stopped <-chan struct{}
}

// Submission carries only a fixed server target and detached validated request.
// Request.Model is already replaced with Target.UpstreamModel. PublicModel is
// used solely to normalize the provider response into the public D2 protocol.
type Submission struct {
	Target      admin.RelayTarget
	Request     api.ChatCompletionRequest
	PublicModel string
	RequestID   string
}

// Transport is one trusted, synchronous submission, never a retry/failover loop.
// It must return promptly on ctx cancellation, enforce ConnectTimeoutSeconds,
// and use ONLY target.BaseURL/UpstreamModel and the borrowed secret. It must not
// retain secret after return. The core owns first-byte/idle/total limits. A real
// outbound adapter must additionally enforce DNS/redirect/SSRF policy; D7 uses
// fake transport only. Response bodies use the validated public model alias.
type Transport func(context.Context, Submission, []byte) (Exchange, error)

type Config struct {
	Gate                                                          *modelgate.Gate
	Reader                                                        *admin.RelayReader
	Cipher                                                        *admin.Cipher
	Ledger                                                        *budget.Ledger
	Policy                                                        Policy
	Transport                                                     Transport
	Limits                                                        modelprotocol.Limits
	RecheckInterval, CheckTimeout, StopTimeout, AccountingTimeout time.Duration
}

type Core struct {
	c       Config
	mu      sync.Mutex
	active  map[string]int64
	pending map[string][]<-chan struct{}
}

func New(c Config) (*Core, error) {
	if c.Gate == nil || c.Reader == nil || c.Cipher == nil || c.Ledger == nil || c.Policy == nil || c.Transport == nil ||
		c.Limits.MaxBytes <= 0 || c.Limits.MaxBytes >= 9007199254740991 || c.Limits.MaxEvents <= 0 || c.Limits.MaxEventBytes <= 0 || c.Limits.MaxEventBytes > modelprotocol.EventLimit ||
		c.RecheckInterval <= 0 || c.RecheckInterval > time.Second || c.CheckTimeout <= 0 || c.CheckTimeout > time.Second ||
		c.StopTimeout <= 0 || c.StopTimeout > 5*time.Second || c.AccountingTimeout <= 0 || c.AccountingTimeout > 5*time.Second {
		return nil, fault("common.validation_failed")
	}
	return &Core{c: c, active: map[string]int64{}, pending: map[string][]<-chan struct{}{}}, nil
}

// Output contains no raw ticket/key/prompt. Pending means the runtime must hand
// off Ref to recovery (or scan durable requests if Admit's COMMIT was ambiguous).
// Result's completion is intended only for the authenticated caller's response.
type Output struct {
	Ref         budget.Ref
	Reservation *budget.Reservation
	Result      modelprotocol.Result
	Pending     bool
	// Closed channel only, never a sent value, is live supervisor stop proof.
	// It is intentionally not persisted; restart alone provides no stop proof.
	StopProof <-chan struct{}
}

func (o Output) String() string   { return "AI relay result" }
func (o Output) GoString() string { return o.String() }

func fault(code string) error { return &budget.Error{Code: code} }
func safe(err error) error {
	if err == nil {
		return nil
	}
	var b *budget.Error
	var p *modelprotocol.Error
	if errors.As(err, &b) {
		return &budget.Error{Code: b.Code, OriginalRequestID: b.OriginalRequestID, Outcome: b.Outcome}
	}
	if errors.As(err, &p) {
		return &modelprotocol.Error{Code: p.Code, Message: p.Message}
	}
	switch err.Error() {
	case "common.validation_failed", "common.unauthenticated", "common.forbidden", "ai.model_not_allowed", "ai.budget_exceeded", "ai.budget_unavailable":
		return fault(err.Error())
	}
	return fault("ai.budget_unavailable")
}

// Minimal bounded pre-read is routing only. DecodeRequest must subsequently
// reject duplicate/unknown fields, malformed history and unsupported features.
func route(source io.Reader) ([]byte, string, error) {
	if source == nil {
		return nil, "", fault("common.validation_failed")
	}
	b, err := io.ReadAll(io.LimitReader(source, modelprotocol.RequestLimit+1))
	var v struct {
		Model string `json:"model"`
	}
	if err != nil || len(b) > modelprotocol.RequestLimit || json.Unmarshal(b, &v) != nil || v.Model == "" {
		return nil, "", fault("common.validation_failed")
	}
	return b, v.Model, nil
}

func fingerprint(r *modelprotocol.Request) ([32]byte, error) {
	b, err := json.Marshal(r.Value()) // Generated value canonicalizes optional fields; maps are sorted.
	return sha256.Sum256(b), err
}

func streaming(r *modelprotocol.Request) bool { v := r.Value(); return v.GetStream() }

func admission(call *modelgate.Call, req *modelprotocol.Request) budget.TrustedSnapshot {
	i, s := call.Identity(), call.Snapshot()
	v, p, q := s.Versions(), s.Constraints().Price, i.Limits()
	a := &ai.BudgetAdmission{TenantId: i.Tenant(), AccountId: i.Account(), ModelId: s.ModelID(), ProviderId: v.ProviderID, CredentialId: v.CredentialID,
		Currency: p.Currency, PriceVersion: p.Version, InputTokenUpperBound: req.InputBound(), MaxOutputTokens: req.Value().MaxTokens,
		Origin: &ai.BudgetAdmission_DeviceId{DeviceId: i.Device()}, TokenExpiresAt: timestamppb.New(call.Deadline())}
	if q.DailyTokens.Limited {
		a.DailyTokens = &q.DailyTokens.Value
	}
	if q.MonthlyBudgetMicros.Limited {
		a.MonthlyBudgetMicros = &q.MonthlyBudgetMicros.Value
	}
	return budget.TrustedSnapshot{Admission: a, Price: p, ValidUntil: call.Deadline()}
}

func (c *Core) check(ctx context.Context, call *modelgate.Call, inFlight bool) error {
	checkCtx, cancel := context.WithTimeout(ctx, c.c.CheckTimeout)
	defer cancel()
	return c.c.Gate.Recheck(checkCtx, call, inFlight)
}

// Execute takes the original device AI ticket, a bounded body, an optional
// idempotency key and a context-aware stream sink. Request-body read deadlines
// belong to the HTTP runtime. No externally supplied identity or quota is used.
func (c *Core) Execute(ctx context.Context, raw string, source io.Reader, key string, sink modelprotocol.Sink) (out Output, err error) {
	started := time.Now()
	b, alias, err := route(source)
	if err != nil {
		return out, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, c.c.CheckTimeout)
	call, err := c.c.Gate.Prepare(checkCtx, raw, alias)
	cancel()
	if err != nil {
		return out, safe(err)
	}
	ctx, cancel = context.WithDeadline(ctx, call.Deadline())
	defer cancel()
	s := call.Snapshot()
	i := call.Identity()
	p, err := c.c.Policy(ctx, i.Tenant(), s.ModelID(), s.Versions())
	if err != nil || p.VerifyUsage == nil || p.Model != alias || p.ContextWindow != s.Constraints().ContextWindow || p.MaxOutputTokens != s.Constraints().MaxOutputTokens {
		return out, fault("ai.budget_unavailable")
	}
	req, err := modelprotocol.DecodeRequest(ctx, bytes.NewReader(b), p)
	if err != nil {
		return out, safe(err)
	}
	if streaming(req) && sink == nil {
		return out, fault("common.validation_failed")
	}
	stream := &streamOutput{sink: sink, limits: c.c.Limits}
	if streaming(req) {
		sink = stream.consume
	}
	h, err := fingerprint(req)
	if err != nil {
		return out, fault("common.validation_failed")
	}
	if err = c.check(ctx, call, false); err != nil {
		return out, safe(err)
	}
	r, err := c.c.Ledger.Admit(ctx, admission(call, req), budget.Request{Method: "POST", Path: "/ai/v1/chat/completions", Key: key, Fingerprint: h})
	if err != nil {
		var be *budget.Error
		out.Pending = !errors.As(err, &be) // Unknown COMMIT: no permission to execute.
		return out, safe(err)
	}
	out.Ref = budget.Ref{TenantID: i.Tenant(), AccountID: i.Account(), RequestID: r.Admission.RequestId}
	out.Reservation = r
	submitted := false
	err = c.c.Reader.WithCredential(ctx, s, c.c.Cipher, func(target admin.RelayTarget, secret []byte) error {
		execCtx, stop := context.WithDeadline(ctx, started.Add(time.Duration(target.TotalTimeoutSeconds)*time.Second))
		defer stop()
		provider := i.Tenant() + "/" + s.Versions().ProviderID
		if !c.acquire(provider, target.MaxConcurrency) {
			return fault("common.rate_limited")
		}
		// Before submission this invocation alone owns the local slot.
		release := true
		defer func() {
			if release {
				c.release(provider)
			} else {
				c.awaitStop(provider, out.StopProof)
			}
		}()
		if e := c.check(execCtx, call, false); e != nil {
			return safe(e)
		}
		if execCtx.Err() != nil {
			return fault("ai.upstream_timeout")
		}
		dispatch, e := c.c.Ledger.MarkDispatching(execCtx, out.Ref, r.Revision)
		if e != nil {
			return safe(e)
		}
		out.Reservation = dispatch
		// Final online check after the durable CAS. Failure freezes dispatching;
		// it never restores a dispatch right or fabricates a zero-usage refund.
		if e = c.check(execCtx, call, false); e != nil {
			return safe(e)
		}
		if execCtx.Err() != nil {
			return fault("ai.upstream_timeout")
		}
		submitted = true // Invocation may submit even if it returns an error.
		out.Result, release, out.StopProof, e = c.run(execCtx, call, target, secret, req, out.Ref.RequestID, sink)
		finish := budget.Finish{Outcome: "unknown", ExecutionStopped: release}
		if e == nil && out.Result.Completed && out.Result.UsageKnown && release {
			u := out.Result.Usage
			finish.Outcome = "completed"
			finish.Usage = &budget.FinalUsage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, Price: s.Constraints().Price}
		}
		accountCtx, accountCancel := context.WithTimeout(context.Background(), c.c.AccountingTimeout)
		defer accountCancel()
		settled, accountErr := c.c.Ledger.Settle(accountCtx, out.Ref, dispatch.Revision, finish)
		if accountErr != nil {
			out.Pending = true
			return fault("ai.budget_unavailable")
		}
		out.Reservation = settled
		out.Pending = settled.State == "unknown" || !release
		if e != nil {
			return e
		}
		if finish.Outcome != "completed" {
			return fault("ai.budget_unavailable")
		}
		return nil
	})
	if !submitted {
		c.recoverUnsubmitted(&out)
	}
	if streaming(req) {
		if writeErr := stream.finish(ctx, out.Ref.RequestID, err); writeErr != nil && err == nil {
			err = &modelprotocol.Error{Code: "ai.stream_interrupted", Message: "The model response stream was interrupted"}
		}
	}
	if out.Pending && err == nil {
		err = fault("ai.budget_unavailable")
	}
	return out, safe(err)
}

// Only this invocation's fresh reservation can reach this path. No Transport
// was invoked. Reading a dispatching row never authorizes another submission.
func (c *Core) recoverUnsubmitted(out *Output) {
	ctx, cancel := context.WithTimeout(context.Background(), c.c.AccountingTimeout)
	defer cancel()
	r, err := c.c.Ledger.GetReservation(ctx, out.Ref)
	if err == nil && (r.State == "reserved" || r.State == "dispatching") {
		r, err = c.c.Ledger.Recover(ctx, out.Ref, r.Revision, budget.RecoveryEvidence{NeverSubmitted: true, ExecutionStopped: true})
	}
	if err != nil {
		out.Pending = true
		return
	}
	out.Reservation = r
	out.Pending = r.State == "unknown"
}

// This process-local provider scheduler is NOT cross-gateway budget authority.
// Missing proof conservatively keeps its slot; later closed live proof is
// reclaimed lazily at the next local admission, without any background worker.
func (c *Core) acquire(provider string, maximum int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	proofs := c.pending[provider]
	kept := proofs[:0]
	for _, proof := range proofs {
		select {
		case _, open := <-proof:
			if !open {
				c.active[provider]--
				continue
			}
		default:
		}
		kept = append(kept, proof)
	}
	if len(kept) == 0 {
		delete(c.pending, provider)
	} else {
		c.pending[provider] = kept
	}
	if maximum < 1 || c.active[provider] >= maximum {
		return false
	}
	c.active[provider]++
	return true
}

func (c *Core) awaitStop(provider string, proof <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[provider] = append(c.pending[provider], proof)
}
func (c *Core) release(provider string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active[provider]--
	if c.active[provider] == 0 {
		delete(c.active, provider)
	}
}
