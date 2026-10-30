package main

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/team"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	org "github.com/hansjone/auriga/infra/gen/auriga/org/v1"
)

type finalizationWork struct {
	usage  atomic.Bool
	checks atomic.Int64
}

// A logical work budget makes a slow final history replay deterministic, without
// sleeps or changing the provider's 5s idle/30s total deadlines. Once usage has
// reached the caller, excessive validation checks wait for Core's own deadline.
// An incremental validator only has the final event left and stays below 64.
type finalizationContext struct {
	context.Context
	work *finalizationWork
}

func (c finalizationContext) Err() error {
	if c.work.usage.Load() && c.work.checks.Add(1) > 64 {
		<-c.Done()
	}
	return c.Context.Err()
}

func TestDeviceUpstreamFinalizationWorkIsBounded(t *testing.T) {
	f := largeUpstreamFixture(t)
	cipher, err := admin.LoadCipher(f.cfg.MasterKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := admin.NewRelayReader(f.db, "USD", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	iamConn, err := team.DialRPC(team.RPCConfig(f.cfg.IAM))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = iamConn.Close() })
	orgConn, err := team.DialRPC(team.RPCConfig(f.cfg.Org))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = orgConn.Close() })
	authority, err := modelgate.NewAuthority(iam.NewDeviceAccessServiceClient(iamConn), org.NewMemberServiceClient(orgConn), org.NewAuthorizationServiceClient(orgConn), org.NewQuotaServiceClient(orgConn), "USD", time.Now)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := modelgate.New(authority, reader)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := budget.New(f.db, "USD", nil)
	if err != nil {
		t.Fatal(err)
	}
	transport, policy, err := deviceTransportPolicy(f.db, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	work := new(finalizationWork)
	core, err := relay.New(relay.Config{Gate: gate, Reader: reader, Cipher: cipher, Ledger: ledger, Policy: policy, Transport: func(ctx context.Context, s relay.Submission, secret []byte) (relay.Exchange, error) {
		return transport(finalizationContext{Context: ctx, work: work}, s, secret)
	}, Limits: deviceProtocolLimits(f.cfg), RecheckInterval: 100 * time.Millisecond, CheckTimeout: time.Second, StopTimeout: 100 * time.Millisecond, AccountingTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	result, err := core.Execute(t.Context(), f.control.raw, strings.NewReader(strings.Replace(syntheticBody(true), `"max_tokens":2`, `"max_tokens":8192`, 1)), "", func(_ context.Context, b []byte) (int, error) {
		n, e := output.Write(b)
		if strings.Contains(string(b), `"completion_tokens":5000`) {
			work.usage.Store(true)
		}
		return n, e
	})
	if err != nil || !result.Result.Completed || result.Pending || !strings.Contains(output.String(), "data: [DONE]") || work.checks.Load() > 64 {
		t.Fatalf("terminal replay exceeded work budget: bytes=%d content_frames=%d checks=%d completed=%t pending=%t error=%v", output.Len(), strings.Count(output.String(), `"content":"x"`), work.checks.Load(), result.Result.Completed, result.Pending, err)
	}
}
