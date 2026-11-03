package bifrostbackend

import (
	"encoding/json"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestCatalogCapabilityRefreshToPublicDirectory(t *testing.T) {
	provider := "019a0000-0000-7000-8000-000000000001"
	pool := api.RelayPool{Id: "auto", Routes: []api.RelayRoute{{ProviderId: provider, Model: "small-fallback", Weight: 0}, {ProviderId: provider, Model: "large", Weight: 1}}, Capabilities: &api.RelayPoolCapabilities{SourceProviderId: provider, SourceModel: "large", ContextWindowMode: "inherit", MaxOutputTokensMode: "inherit"}}
	refresh := func(body string) {
		t.Helper()
		// catalogFixture executes fetchProviderModels over a real random-port
		// HTTP server, including parsing/sanitization, rather than mocking it.
		page := catalogFixture(t, "openai", body)
		for _, model := range page.Items {
			if model.Id == pool.Capabilities.SourceModel {
				pool.Capabilities.Upstream = &api.RelayPoolUpstreamCapabilities{ProviderId: provider, Model: model}
			}
		}
		var err error
		pool, err = resolvePoolCapabilities(pool)
		if err != nil {
			t.Fatal(err)
		}
	}
	refresh(`{"data":[{"id":"large","context_window":2000000,"max_output_tokens":128000,"api_key":"not-public"},{"id":"small-fallback","context_window":32768,"max_output_tokens":4096}]}`)
	if pool.ContextWindow != 2000000 || pool.MaxOutputTokens != 128000 {
		t.Fatal("source metadata was shrunk to fallback or guessed limits")
	}
	refresh(`{"data":[{"id":"large","context_window":4000000,"max_output_tokens":256000}]}`)
	var reopened api.RelayPool
	// Include mandatory configuration fields as a real persisted document does.
	pool.Enabled = true
	pool.Price = api.Price{Currency: "USD", Version: "fixture", InputMicrosPerMillionTokens: 1, OutputMicrosPerMillionTokens: 1}
	pool.Price.EffectiveAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(pool)
	if err != nil || json.Unmarshal(raw, &reopened) != nil {
		t.Fatal("capability configuration did not survive serialization", err)
	}
	entry := publicModel(reopened)
	if entry["context_window"] != int64(4000000) || entry["max_output_tokens"] != int64(256000) || len(entry) != 6 {
		t.Fatalf("refreshed directory=%v", entry)
	}
	recommended := int64(1000000)
	pool.Capabilities.RecommendedContextWindow = &recommended
	refresh(`{"data":[{"id":"large","max_output_tokens":128000}]}`)
	if pool.ContextWindow != recommended || pool.Capabilities.Upstream.Model.ContextWindow != nil {
		t.Fatal("successful refresh retained a field removed by upstream")
	}
	pool.Capabilities.ContextWindowMode = "manual"
	pool.ContextWindow = 750000
	refresh(`{"data":[{"id":"large","context_window":2000000,"max_output_tokens":128000}]}`)
	if pool.ContextWindow != 750000 {
		t.Fatal("refresh overwrote explicit administrator context")
	}
}

func TestCapabilityPolicyRejectsInvalidConfigurationWithoutMutation(t *testing.T) {
	provider := "019a0000-0000-7000-8000-000000000001"
	base := api.RelayPool{Id: "auto", Routes: []api.RelayRoute{{ProviderId: provider, Model: "large", Weight: 1}}, Capabilities: &api.RelayPoolCapabilities{SourceProviderId: provider, SourceModel: "large", ContextWindowMode: "inherit", MaxOutputTokensMode: "inherit"}}
	for _, mutate := range []func(*api.RelayPoolCapabilities){
		func(p *api.RelayPoolCapabilities) { p.ContextWindowMode = "unknown" },
		func(p *api.RelayPoolCapabilities) { n := int64(1); p.RecommendedContextWindow = &n },
		func(p *api.RelayPoolCapabilities) { n := int64(-1); p.RecommendedMaxOutputTokens = &n },
	} {
		pool := base
		policy := *base.Capabilities
		pool.Capabilities = &policy
		mutate(pool.Capabilities)
		if _, err := resolvePoolCapabilities(pool); err != invalid {
			t.Fatal("invalid capability policy accepted", err)
		}
	}
	base.Capabilities.SourceModel = "removed"
	base.Capabilities.Upstream = &api.RelayPoolUpstreamCapabilities{ProviderId: provider, Model: api.RelayProviderModel{Id: "removed"}}
	resolved, err := resolvePoolCapabilities(base)
	if err != nil || resolved.Capabilities.SourceModel != "large" || resolved.Capabilities.Upstream != nil || base.Capabilities.Upstream == nil || base.Capabilities.SourceModel != "removed" {
		t.Fatal("source removal reused its snapshot or mutated the caller")
	}
}
