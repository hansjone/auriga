package bifrostbackend

import (
	"encoding/json"
	"net/http"
	"slices"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// UnknownContextWindow is the context capacity published for a pool whose real
// window is unknown (many providers, including OpenAI, do not list it).
const UnknownContextWindow = 1000000

func publicModel(model api.RelayPool) map[string]any {
	out := map[string]any{"id": model.Id, "object": "model", "created": 0, "owned_by": "auriga"}
	resolved, err := resolvePoolCapabilities(model)
	if err != nil {
		return out
	}
	model = resolved
	if model.ContextWindow >= 2 && model.ContextWindow <= maxInteger {
		out["context_window"] = model.ContextWindow
	} else if model.MaxOutputTokens < UnknownContextWindow {
		// Unknown context is published as a high placeholder, never a low guess:
		// the agent compacts on pressure near it and, below it, on the
		// context_length_exceeded error the relay normalises from any provider.
		out["context_window"] = int64(UnknownContextWindow)
	}
	if model.MaxOutputTokens > 0 && model.MaxOutputTokens <= maxInteger {
		out["max_output_tokens"] = model.MaxOutputTokens
	}
	return out
}

// Capabilities are directory metadata only. Legacy documents have no policy and
// retain their explicit values. A snapshot is usable only for its exact source
// route; switching/removing that route cannot borrow another model's metadata.
func resolvePoolCapabilities(pool api.RelayPool) (api.RelayPool, error) {
	if pool.Capabilities == nil {
		return pool, nil
	}
	policy := *pool.Capabilities
	pool.Capabilities = &policy
	if !slices.Contains([]string{"inherit", "manual"}, policy.ContextWindowMode) || !slices.Contains([]string{"inherit", "manual"}, policy.MaxOutputTokensMode) || !validPoolCapabilities(policy.GetRecommendedContextWindow(), 0) || policy.GetRecommendedMaxOutputTokens() < 0 || policy.GetRecommendedMaxOutputTokens() > maxInteger {
		return pool, invalid
	}
	found := slices.ContainsFunc(pool.Routes, func(route api.RelayRoute) bool {
		return route.ProviderId == policy.SourceProviderId && route.Model == policy.SourceModel
	})
	if !found {
		index := slices.IndexFunc(pool.Routes, func(route api.RelayRoute) bool { return route.Weight > 0 })
		if index < 0 {
			return pool, invalid
		}
		policy.SourceProviderId, policy.SourceModel = pool.Routes[index].ProviderId, pool.Routes[index].Model
		policy.Upstream = nil
	}
	if !validUUID(policy.SourceProviderId) || !validCatalogID(policy.SourceModel) {
		return pool, invalid
	}
	context, output := policy.GetRecommendedContextWindow(), policy.GetRecommendedMaxOutputTokens()
	if snapshot := policy.Upstream; snapshot != nil {
		if snapshot.ProviderId != policy.SourceProviderId || snapshot.Model.Id != policy.SourceModel {
			policy.Upstream = nil
		} else {
			model := snapshot.Model
			if model.ContextWindow != nil && (*model.ContextWindow < 2 || *model.ContextWindow > maxInteger) || model.MaxInputTokens != nil && (*model.MaxInputTokens < 1 || *model.MaxInputTokens > maxInteger) || model.MaxOutputTokens != nil && (*model.MaxOutputTokens < 1 || *model.MaxOutputTokens > maxInteger) {
				return pool, invalid
			}
			if model.ContextWindow != nil {
				context = *model.ContextWindow
			}
			if model.MaxOutputTokens != nil {
				output = *model.MaxOutputTokens
			}
		}
	}
	if policy.ContextWindowMode == "inherit" {
		pool.ContextWindow = context
	}
	if policy.MaxOutputTokensMode == "inherit" {
		pool.MaxOutputTokens = output
	}
	if !validPoolCapabilities(pool.ContextWindow, pool.MaxOutputTokens) {
		return pool, invalid
	}
	return pool, nil
}

func validPoolCapabilities(context, output int64) bool {
	return (context == 0 || context >= 2) && context <= maxInteger && output >= 0 && output <= maxInteger && (context == 0 || output < context)
}

func outputTokenLimit(fields map[string]json.RawMessage, _ int64) (int64, string, error) {
	limit := int64(0)
	set := false
	for _, name := range []string{"max_tokens", "max_completion_tokens"} {
		if value := fields[name]; value != nil {
			var n int64
			if json.Unmarshal(value, &n) != nil || n < 1 || n > maxInteger || set && limit != n {
				return 0, "/" + name, invalid
			}
			limit, set = n, true
		}
	}
	return limit, "", nil
}

func rejectOutputTokenLimit(w http.ResponseWriter, field string, _ int64) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": http.StatusBadRequest, "code": string(invalid), "title": "Bad Request",
		"detail": "Supplied output token limits must agree and be positive safe integers; budgeted keys require an explicit output limit",
		"params": map[string]any{"field": field, "minimum": 1, "maximum": maxInteger},
	})
}
