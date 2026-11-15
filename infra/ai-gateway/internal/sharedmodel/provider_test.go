package sharedmodel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

type modelAuthority struct {
	binding sharedauth.Binding
	checks  atomic.Int32
}

func (a *modelAuthority) Resolve(context.Context, sharedauth.Lookup) (sharedauth.Snapshot, error) {
	a.checks.Add(1)
	return sharedauth.Snapshot{Binding: a.binding, CredentialVersion: 1, Active: true, Scopes: []string{"model.invoke"}}, nil
}

type modelRelayFunc func(context.Context, SharedCall, func([]byte) error) (SharedUsage, error)

func (f modelRelayFunc) Stream(ctx context.Context, call SharedCall, emit func([]byte) error) (SharedUsage, error) {
	return f(ctx, call, emit)
}

type modelLedger struct {
	started  bool
	finished int
	usage    SharedUsage
}

func (l *modelLedger) Begin(context.Context, sharedauth.Binding, string, [32]byte) (bool, error) {
	if l.started {
		return false, nil
	}
	l.started = true
	return true, nil
}
func (l *modelLedger) Finish(_ context.Context, _ sharedauth.Binding, _ string, usage SharedUsage, _ error) error {
	l.finished++
	l.usage = usage
	return nil
}

func TestModelStreamOutlivesAdmissionCapability(t *testing.T) {
	binding := sharedauth.Binding{TenantID: uuid.NewString(), MemberID: uuid.NewString(), PlacementID: uuid.NewString(), PhysicalInstanceID: uuid.NewString(), Mode: "shared", Epoch: 1, Revision: 1, SessionID: uuid.NewString(), PlatformSessionID: uuid.NewString(), AuthGeneration: 1}
	authority := &modelAuthority{binding: binding}
	var now atomic.Int64
	now.Store(time.Now().Unix())
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := sharedauth.New(authority, map[string]ed25519.PublicKey{"model": public}, func() time.Time { return time.Unix(now.Load(), 0) })
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	token, err := gate.Issue(context.Background(), sharedauth.Lookup{TenantID: binding.TenantID, SessionID: binding.SessionID, TaskID: taskID, Audience: sharedauth.Model}, []string{"model.invoke"}, time.Minute, "model", private)
	if err != nil {
		t.Fatal(err)
	}
	ledger := &modelLedger{}
	chunks := 0
	relay := modelRelayFunc(func(ctx context.Context, _ SharedCall, emit func([]byte) error) (SharedUsage, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < MaxCallDuration-5*time.Second {
			t.Error("stream deadline inherited the short credential TTL")
		}
		checks := authority.checks.Load()
		now.Add(61)
		// Run the watcher after admission expiry while the online authority
		// remains active. This avoids a real one-minute wait in the regression.
		select {
		case <-ctx.Done():
			return SharedUsage{}, ctx.Err()
		case <-time.After(350 * time.Millisecond):
		}
		if authority.checks.Load() <= checks {
			t.Error("stream stopped checking online authority")
		}
		checksBeforeChunks := authority.checks.Load()
		for i := 0; i < 100; i++ {
			if err := emit([]byte("long output")); err != nil {
				return SharedUsage{}, err
			}
		}
		if authority.checks.Load()-checksBeforeChunks >= 100 {
			t.Error("each fast chunk performed an authority RPC")
		}
		return SharedUsage{InputTokens: 2, OutputTokens: 3, Known: true}, nil
	})
	provider, err := NewSharedProvider(gate, relay, ledger)
	if err != nil {
		t.Fatal(err)
	}
	request := SharedRequest{TenantID: binding.TenantID, SessionID: binding.SessionID, TaskID: taskID, CallID: uuid.NewString(), Capability: token, Body: []byte("prompt")}
	if err := provider.Invoke(context.Background(), request, func([]byte) error { chunks++; return nil }); err != nil {
		t.Fatal(err)
	}
	if chunks != 100 || ledger.finished != 1 || !ledger.usage.Known || ledger.usage.OutputTokens != 3 {
		t.Fatal("long stream did not complete and settle once")
	}
	if _, err := gate.Verify(context.Background(), token, binding.TenantID, binding.SessionID, taskID, sharedauth.Model, "model.invoke"); !errors.Is(err, sharedauth.ErrExpired) {
		t.Fatal("expired capability admitted a new request", err)
	}
	// Renew the admission token for the same call. The public error must stay
	// identifiable and the relay/settlement must not run for the duplicate.
	renewed, err := gate.Issue(context.Background(), sharedauth.Lookup{TenantID: binding.TenantID, SessionID: binding.SessionID, TaskID: taskID, Audience: sharedauth.Model}, []string{"model.invoke"}, time.Minute, "model", private)
	if err != nil {
		t.Fatal(err)
	}
	request.Capability = renewed
	if err := provider.Invoke(context.Background(), request, func([]byte) error { return nil }); !errors.Is(err, sharedauth.ErrConflict) {
		t.Fatal("duplicate has no typed conflict", err)
	}
	if ledger.finished != 1 {
		t.Fatal("duplicate settled again")
	}
}

func TestModelCallKeySurvivesBindingChanges(t *testing.T) {
	binding := sharedauth.Binding{TenantID: uuid.NewString(), MemberID: uuid.NewString(), SessionID: uuid.NewString()}
	callID := uuid.NewString()
	key := CallKey(binding, callID)
	updated := binding
	updated.Epoch++
	updated.Revision++
	updated.AuthGeneration++
	updated.PlatformSessionID = uuid.NewString()
	updated.SessionID = uuid.NewString()
	updated.PlacementID = uuid.NewString()
	updated.PhysicalInstanceID = uuid.NewString()
	if CallKey(updated, callID) != key {
		t.Fatal("binding change reset call deduplication")
	}
	updated.MemberID = uuid.NewString()
	if CallKey(updated, callID) == key {
		t.Fatal("different member shares a call key")
	}
	if CallKey(binding, uuid.NewString()) == key {
		t.Fatal("different call shares a call key")
	}
}
