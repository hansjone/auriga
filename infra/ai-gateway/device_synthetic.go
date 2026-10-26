package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

const syntheticModel = "auriga-d7-synthetic"
const syntheticProvider = "01900000-0000-7000-8000-000000000004"
const syntheticCredential = "01900000-0000-7000-8000-000000000005"
const syntheticURL = "https://synthetic.invalid/v1"
const syntheticUpstream = "auriga-d7-memory-v1"

func syntheticPrice() api.Price {
	return api.Price{Currency: "USD", Version: "d7-fake-v1", EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), InputMicrosPerMillionTokens: 1000000, OutputMicrosPerMillionTokens: 2000000}
}
func syntheticProtocol() modelprotocol.Policy {
	return modelprotocol.Policy{Model: syntheticModel, ContextWindow: 64, MaxOutputTokens: 2,
		MeasureInput: func(ctx context.Context, r api.ChatCompletionRequest) (int64, error) {
			// This deliberately fake protocol maps every complete validated history
			// to one 32-token input symbol, irrespective of its byte length.
			if ctx.Err() != nil || r.MaxTokens != 2 {
				return 0, errors.New("common.validation_failed")
			}
			return 32, nil
		}, VerifyUsage: func(ctx context.Context, u api.TokenUsage) bool {
			return ctx.Err() == nil && u.PromptTokens == 32 && u.CompletionTokens == 2 && u.TotalTokens == 34
		}}
}
func syntheticPolicy(db *store.Store, tenant string) relay.Policy {
	return func(ctx context.Context, tid, model string, v admin.RelayVersions) (modelprotocol.Policy, error) {
		deny := errors.New("ai.budget_unavailable")
		if tid != tenant || model != syntheticModel || v != (admin.RelayVersions{ModelRevision: 1, ProviderRevision: 1, CredentialRevision: 1, ProviderID: syntheticProvider, CredentialID: syntheticCredential, CipherVersion: 1}) {
			return modelprotocol.Policy{}, deny
		}
		// The Policy API carries revisions but not price/target. Re-read exact
		// protected synthetic configuration; never bless arbitrary pinned prices.
		err := db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
			var mb, pb []byte
			if e := tx.QueryRow(ctx, `SELECT m.document,p.document FROM ai.model m JOIN ai.provider p ON p.tenant_id=m.tenant_id AND p.id=m.provider_id WHERE m.tenant_id=$1 AND m.id=$2 AND m.revision=1 AND p.revision=1`, tenant, syntheticModel).Scan(&mb, &pb); e != nil {
				return deny
			}
			var m api.Model
			var p api.Provider
			if json.Unmarshal(mb, &m) != nil || json.Unmarshal(pb, &p) != nil || m.Id != syntheticModel || m.ProviderId != syntheticProvider || m.UpstreamModel != syntheticUpstream || !m.Enabled || m.Revision != 1 || m.ContextWindow != 64 || m.MaxOutputTokens != 2 || !reflect.DeepEqual(m.Price, syntheticPrice()) || p.Id != syntheticProvider || p.Revision != 1 || !p.Enabled || p.Kind != "openai_compatible" || p.BaseUrl != syntheticURL || p.GetActiveCredentialId() != syntheticCredential {
				return deny
			}
			return nil
		})
		if err != nil {
			return modelprotocol.Policy{}, deny
		}
		return syntheticProtocol(), nil
	}
}

// syntheticTransport contains no client, resolver or upstream execution. The
// finite in-memory generation completes before Stopped is closed, not on Close.
func syntheticTransport(ctx context.Context, s relay.Submission, _ []byte) (relay.Exchange, error) {
	if ctx.Err() != nil || s.Target.BaseURL != syntheticURL || s.Target.UpstreamModel != syntheticUpstream || s.Request.Model != syntheticUpstream || s.PublicModel != syntheticModel || s.Request.MaxTokens != 2 {
		return relay.Exchange{}, errors.New("ai.budget_unavailable")
	}
	var b string
	if s.Request.GetStream() {
		b = fmt.Sprintf("data: {\"id\":\"synthetic\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"synthetic\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":32,\"completion_tokens\":2,\"total_tokens\":34}}\n\ndata: [DONE]\n\n", syntheticModel, syntheticModel)
	} else {
		b = fmt.Sprintf(`{"id":"synthetic","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":32,"completion_tokens":2,"total_tokens":34}}`, syntheticModel)
	}
	proof := make(chan struct{})
	body := io.NopCloser(strings.NewReader(b))
	close(proof)
	return relay.Exchange{Body: body, Stopped: proof}, nil
}
