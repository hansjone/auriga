package relay

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func syntheticPolicy() modelprotocol.Policy {
	return modelprotocol.Policy{Model: "synthetic", ContextWindow: 4096, MaxOutputTokens: 512,
		Tools: true, Temperature: true, TopP: true, Stop: true,
		// The synthetic provider defines every complete effective request as 32
		// input tokens, including templates, history, tools and sampling options.
		// This rule has no meaning for any production model or tokenizer.
		MeasureInput: func(context.Context, api.ChatCompletionRequest) (int64, error) { return 32, nil },
		VerifyUsage:  func(_ context.Context, u api.TokenUsage) bool { return u.PromptTokens == 32 && u.CompletionTokens == 2 },
	}
}

func TestSyntheticResponseFixtures(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, choice := range []string{textChoice, toolChoice, filterChoice} {
			request := strings.Replace(plainRequest, `"max_tokens":`, toolsRequest+`"max_tokens":`, 1)
			if choice == textChoice {
				request = plainRequest
			}
			body := completion(choice)
			if stream {
				request = strings.Replace(request, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
				body = sse(choice)
			}
			r, err := modelprotocol.DecodeRequest(t.Context(), strings.NewReader(request), syntheticPolicy())
			if err != nil {
				t.Fatal(err)
			}
			var result modelprotocol.Result
			limits := modelprotocol.Limits{MaxBytes: 65536, MaxEvents: 32, MaxEventBytes: 8192}
			if stream {
				result, err = modelprotocol.Stream(t.Context(), io.NopCloser(strings.NewReader(body)), r, limits, "fixture", func(_ context.Context, b []byte) (int, error) { return len(b), nil })
			} else {
				result, err = modelprotocol.Complete(t.Context(), io.NopCloser(strings.NewReader(body)), r, limits)
			}
			if err != nil || !result.Completed || !result.UsageKnown {
				t.Fatalf("response fixture stream=%v: %v", stream, err)
			}
		}
	}
}

func TestConfigurationFailsClosed(t *testing.T) {
	base := Config{Gate: new(modelgate.Gate), Reader: new(admin.RelayReader), Cipher: new(admin.Cipher), Ledger: new(budget.Ledger),
		Policy: func(context.Context, string, string, admin.RelayVersions) (modelprotocol.Policy, error) {
			return syntheticPolicy(), nil
		},
		Transport: func(context.Context, Submission, []byte) (Exchange, error) {
			t.Fatal("configuration dispatched")
			return Exchange{}, nil
		},
		Limits: modelprotocol.Limits{MaxBytes: 1024, MaxEvents: 10, MaxEventBytes: 512}, RecheckInterval: time.Second, CheckTimeout: time.Second, StopTimeout: time.Second, AccountingTimeout: time.Second}
	for _, change := range []func(*Config){func(c *Config) { c.Policy = nil }, func(c *Config) { c.Transport = nil }, func(c *Config) { c.Gate = nil }, func(c *Config) { c.Ledger = nil }, func(c *Config) { c.Reader = nil }, func(c *Config) { c.Cipher = nil }, func(c *Config) { c.Limits.MaxBytes = 0 }, func(c *Config) { c.RecheckInterval = 2 * time.Second }, func(c *Config) { c.CheckTimeout = 2 * time.Second }, func(c *Config) { c.StopTimeout = 0 }, func(c *Config) { c.AccountingTimeout = 0 }} {
		cfg := base
		change(&cfg)
		_, err := New(cfg)
		expectCode(t, err, "common.validation_failed")
	}
}

const plainRequest = `{"model":"synthetic","messages":[{"role":"user","content":"private prompt"}],"max_tokens":16}`

func TestRoutingAndFingerprint(t *testing.T) {
	p := syntheticPolicy()
	for _, raw := range []string{plainRequest, " \n" + plainRequest + "\t"} {
		b, alias, err := route(bytes.NewBufferString(raw))
		if err != nil || alias != "synthetic" {
			t.Fatalf("route: %q %v", alias, err)
		}
		r, err := modelprotocol.DecodeRequest(t.Context(), bytes.NewReader(b), p)
		if err != nil {
			t.Fatal(err)
		}
		h, err := fingerprint(r)
		if err != nil {
			t.Fatal(err)
		}
		r2, err := modelprotocol.ValidateRequest(t.Context(), r.Value(), p)
		if err != nil {
			t.Fatal(err)
		}
		h2, err := fingerprint(r2)
		if err != nil || h != h2 {
			t.Fatal("canonical request digest changed")
		}
	}
	for _, raw := range []string{
		strings.Repeat(" ", modelprotocol.RequestLimit+1) + plainRequest,
		strings.Replace(plainRequest, `"max_tokens":16`, `"unknown":1,"max_tokens":16`, 1),
		strings.Replace(plainRequest, `"model":"synthetic"`, `"model":"other","model":"synthetic"`, 1),
	} {
		b, _, err := route(strings.NewReader(raw))
		if err == nil {
			_, err = modelprotocol.DecodeRequest(t.Context(), bytes.NewReader(b), p)
		}
		if err == nil {
			t.Fatal("unsafe route bypassed strict decoding")
		}
	}
}
