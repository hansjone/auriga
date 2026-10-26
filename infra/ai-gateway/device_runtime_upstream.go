package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/upstream"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

type deviceUpstreamConfig struct {
	AllowedHosts []string              `json:"allowedHosts"`
	Models       []deviceUpstreamModel `json:"models"`
}

// These are protected deployment assertions, never request parameters. Reserving
// all context headroom is valid only for a provider enforcing this total context
// limit (including templates/history/tools) and returning authoritative usage.
type deviceUpstreamModel struct {
	Tenant               string `json:"tenant"`
	Model                string `json:"model"`
	ProviderID           string `json:"providerId"`
	UpstreamModel        string `json:"upstreamModel"`
	ContextWindow        int64  `json:"contextWindow"`
	MaxOutputTokens      int64  `json:"maxOutputTokens"`
	ContextLimitEnforced bool   `json:"contextLimitEnforced"`
	TrustUsage           bool   `json:"trustUsage"`
}

func upstreamProtocol(m deviceUpstreamModel) modelprotocol.Policy {
	return modelprotocol.Policy{Model: m.Model, ContextWindow: m.ContextWindow, MaxOutputTokens: m.MaxOutputTokens,
		MeasureInput: func(ctx context.Context, r api.ChatCompletionRequest) (int64, error) {
			if ctx.Err() != nil || !m.ContextLimitEnforced || r.MaxTokens < 1 || r.MaxTokens > m.MaxOutputTokens {
				return 0, errors.New("common.validation_failed")
			}
			return m.ContextWindow - r.MaxTokens, nil
		},
		VerifyUsage: func(ctx context.Context, u api.TokenUsage) bool {
			return ctx.Err() == nil && m.TrustUsage && u.PromptTokens >= 0 && u.CompletionTokens >= 0 && u.CompletionTokens <= m.MaxOutputTokens && u.PromptTokens <= m.ContextWindow-u.CompletionTokens && u.TotalTokens == u.PromptTokens+u.CompletionTokens
		},
	}
}

func deviceUpstreamPolicy(db *store.Store, config deviceUpstreamConfig) relay.Policy {
	return func(ctx context.Context, tenant, model string, v admin.RelayVersions) (modelprotocol.Policy, error) {
		deny := errors.New("ai.budget_unavailable")
		for _, m := range config.Models {
			if m.Tenant != tenant || m.Model != model {
				continue
			}
			// Match the captured model revision and provider, then let Gate's
			// final rechecks fence provider/credential changes before dispatch.
			err := db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
				var raw []byte
				if err := tx.QueryRow(ctx, `SELECT document FROM ai.model WHERE tenant_id=$1 AND id=$2 AND revision=$3 AND provider_id=$4`, tenant, model, v.ModelRevision, v.ProviderID).Scan(&raw); err != nil {
					return deny
				}
				var configured api.Model
				if json.Unmarshal(raw, &configured) != nil || configured.Id != model || configured.ProviderId != v.ProviderID || configured.ProviderId != m.ProviderID || configured.UpstreamModel != m.UpstreamModel || configured.Revision != v.ModelRevision || !configured.Enabled || configured.ContextWindow != m.ContextWindow || configured.MaxOutputTokens != m.MaxOutputTokens {
					return deny
				}
				return nil
			})
			if err != nil {
				return modelprotocol.Policy{}, deny
			}
			return upstreamProtocol(m), nil
		}
		return modelprotocol.Policy{}, deny
	}
}

func deviceTransportPolicy(db *store.Store, c deviceConfig) (relay.Transport, relay.Policy, error) {
	if c.Upstream == nil {
		return syntheticTransport, syntheticPolicy(db, c.SyntheticTenant), nil
	}
	client, err := upstream.New(upstream.Config{AllowedHosts: c.Upstream.AllowedHosts, MaxResponseBytes: 1 << 20, MaxEventBytes: modelprotocol.EventLimit})
	if err != nil {
		return nil, nil, deviceError()
	}
	return client.Transport, deviceUpstreamPolicy(db, *c.Upstream), nil
}
