package main

import (
	"context"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestSyntheticProtocolJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		b := `{"model":"auriga-d7-synthetic","messages":[{"role":"user","content":"arbitrary complete history"}],"max_tokens":2`
		if stream {
			b += `,"stream":true`
		}
		b += `}`
		r, e := modelprotocol.DecodeRequest(t.Context(), strings.NewReader(b), syntheticProtocol())
		if e != nil {
			t.Fatal(e)
		}
		wire := r.Value()
		wire.Model = syntheticUpstream
		x, e := syntheticTransport(t.Context(), relay.Submission{Target: admin.RelayTarget{BaseURL: syntheticURL, UpstreamModel: syntheticUpstream}, Request: wire, PublicModel: syntheticModel}, nil)
		if e != nil {
			t.Fatal(e)
		}
		select {
		case _, open := <-x.Stopped:
			if open {
				t.Fatal("proof open")
			}
		default:
			t.Fatal("generation unfinished")
		}
		limits := modelprotocol.Limits{MaxBytes: 1 << 20, MaxEvents: 32, MaxEventBytes: modelprotocol.EventLimit}
		var result modelprotocol.Result
		if stream {
			result, e = modelprotocol.Stream(t.Context(), x.Body, r, limits, "request", func(_ context.Context, b []byte) (int, error) { return len(b), nil })
		} else {
			result, e = modelprotocol.Complete(t.Context(), x.Body, r, limits)
		}
		if e != nil || !result.Completed || !result.UsageKnown || result.Usage.PromptTokens != 32 || result.Usage.CompletionTokens != 2 {
			t.Fatalf("synthetic result %+v %v", result, e)
		}
	}
}
func TestSyntheticRejectsRealModelsAndUnsupportedCapabilities(t *testing.T) {
	for _, b := range []string{
		`{"model":"real","messages":[{"role":"user","content":"x"}],"max_tokens":2}`,
		`{"model":"auriga-d7-synthetic","messages":[{"role":"user","content":"x"}],"max_tokens":1}`,
		`{"model":"auriga-d7-synthetic","messages":[{"role":"user","content":"x"}],"max_tokens":2,"temperature":1}`,
	} {
		if _, e := modelprotocol.DecodeRequest(t.Context(), strings.NewReader(b), syntheticProtocol()); e == nil {
			t.Fatal("unsafe synthetic input accepted")
		}
	}
	if _, e := syntheticTransport(t.Context(), relay.Submission{Target: admin.RelayTarget{BaseURL: "https://example.com"}}, nil); e == nil {
		t.Fatal("real target accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if x, e := syntheticTransport(ctx, relay.Submission{}, nil); e == nil || x.Body != nil {
		t.Fatal("cancelled generation accepted")
	}
	if syntheticProtocol().VerifyUsage(t.Context(), api.TokenUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}) {
		t.Fatal("arbitrary usage trusted")
	}
}
