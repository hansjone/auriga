package bifrostbackend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
	"github.com/maximhq/bifrost/core/network"
)

const catalogLimit = 1000
const catalogBytes = 2 << 20

type catalogFailure string

func (e catalogFailure) Error() string { return string(e) }

// ProviderModels reads only this tenant's configured provider and credential.
// Neither a caller URL nor caller credentials can select the outbound target.
func (b *Backend) ProviderModels(ctx context.Context, tenant, id string) (api.RelayProviderModelPage, error) {
	if !validUUID(tenant) || !validUUID(id) {
		return api.RelayProviderModelPage{}, invalid
	}
	var route Route
	err := b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var raw, sealed []byte
		if e := tx.QueryRow(ctx, `SELECT document,secret FROM ai.relay_provider WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&raw, &sealed); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return missing
			}
			return e
		}
		var config api.RelayProviderInput
		if json.Unmarshal(raw, &config) != nil || !providerValid(config, b.allowPrivate) {
			return denied
		}
		if !config.Enabled {
			return catalogFailure("provider_disabled")
		}
		if len(sealed) == 0 {
			return catalogFailure("credential_missing")
		}
		secret, e := b.decrypt(tenant, id, sealed)
		if e != nil {
			return e
		}
		route = Route{ID: id, Kind: providerKind(config.Kind), URL: config.BaseUrl, Secret: secret}
		return nil
	})
	if err != nil {
		return api.RelayProviderModelPage{}, err
	}
	return fetchProviderModels(ctx, route, b.allowPrivate)
}

func fetchProviderModels(ctx context.Context, route Route, allowPrivate bool) (api.RelayProviderModelPage, error) {
	out := api.RelayProviderModelPage{Items: []api.RelayProviderModel{}}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dial := network.SSRFSafeDialContext(3 * time.Second)
	if allowPrivate {
		dial = (&net.Dialer{Timeout: 3 * time.Second}).DialContext
	}
	transport := &http.Transport{Proxy: nil, DialContext: dial, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	seen := map[string]int{}
	pageToken := ""
	for page := 0; page < 5; page++ {
		endpoint, headers, err := catalogEndpoint(route, pageToken)
		if err != nil {
			return out, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return out, catalogFailure("upstream_unavailable")
		}
		req.Header = headers
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return out, catalogFailure("upstream_timeout")
			}
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				return out, catalogFailure("upstream_timeout")
			}
			return out, catalogFailure("upstream_unavailable")
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, catalogBytes+1))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			switch resp.StatusCode {
			case 401, 403:
				return out, catalogFailure("upstream_unauthorized")
			case 404, 405, 501:
				return out, catalogFailure("unsupported")
			case 429:
				return out, catalogFailure("upstream_rate_limited")
			default:
				return out, catalogFailure("upstream_unavailable")
			}
		}
		if readErr != nil || len(raw) > catalogBytes {
			return out, catalogFailure("upstream_invalid_response")
		}
		models, next, err := decodeCatalog(route.Kind, raw)
		if err != nil {
			return out, err
		}
		for _, model := range models {
			if !validCatalogID(model.Id) {
				return out, catalogFailure("upstream_invalid_response")
			}
			if index, exists := seen[model.Id]; exists {
				previous := &out.Items[index]
				previous.ContextWindow = minimumKnownByBoth(previous.ContextWindow, model.ContextWindow)
				previous.MaxInputTokens = minimumKnownByBoth(previous.MaxInputTokens, model.MaxInputTokens)
				previous.MaxOutputTokens = minimumKnownByBoth(previous.MaxOutputTokens, model.MaxOutputTokens)
			} else {
				if len(out.Items) == catalogLimit {
					return out, catalogFailure("catalog_too_large")
				}
				seen[model.Id] = len(out.Items)
				out.Items = append(out.Items, model)
			}
		}
		if next == "" {
			slices.SortFunc(out.Items, func(a, b api.RelayProviderModel) int { return strings.Compare(a.Id, b.Id) })
			return out, nil
		}
		if next == pageToken || len(next) > 1024 {
			return out, catalogFailure("upstream_invalid_response")
		}
		pageToken = next
	}
	return out, catalogFailure("catalog_too_large")
}

func catalogEndpoint(route Route, token string) (string, http.Header, error) {
	headers := http.Header{"Accept": []string{"application/json"}}
	u, err := url.Parse(route.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return "", nil, invalid
	}
	q := url.Values{}
	switch route.Kind {
	case "openai", "minimax", "deepseek":
		u.Path = "/v1/models"
		headers.Set("Authorization", "Bearer "+route.Secret)
	case "anthropic":
		u.Path = "/v1/models"
		q.Set("limit", "1000")
		if token != "" {
			q.Set("after_id", token)
		}
		headers.Set("x-api-key", route.Secret)
		headers.Set("anthropic-version", "2023-06-01")
	case "gemini":
		u.Path = "/v1beta/models"
		q.Set("pageSize", "1000")
		if token != "" {
			q.Set("pageToken", token)
		}
		headers.Set("x-goog-api-key", route.Secret)
	default:
		return "", nil, catalogFailure("unsupported")
	}
	u.RawQuery = q.Encode()
	return u.String(), headers, nil
}

func decodeCatalog(kind string, raw []byte) ([]api.RelayProviderModel, string, error) {
	var body struct {
		Data []struct {
			ID                  string          `json:"id"`
			ContextWindow       json.RawMessage `json:"context_window"`
			ContextLength       json.RawMessage `json:"context_length"`
			MaxInputTokens      json.RawMessage `json:"max_input_tokens"`
			MaxTokens           json.RawMessage `json:"max_tokens"`
			MaxOutputTokens     json.RawMessage `json:"max_output_tokens"`
			MaxCompletionTokens json.RawMessage `json:"max_completion_tokens"`
			TopProvider         json.RawMessage `json:"top_provider"`
		} `json:"data"`
		Models []struct {
			Name             string          `json:"name"`
			Methods          []string        `json:"supportedGenerationMethods"`
			InputTokenLimit  json.RawMessage `json:"inputTokenLimit"`
			OutputTokenLimit json.RawMessage `json:"outputTokenLimit"`
		} `json:"models"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
		Next    string `json:"nextPageToken"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil, "", catalogFailure("upstream_invalid_response")
	}
	models := []api.RelayProviderModel{}
	next := ""
	if kind == "gemini" {
		if body.Models == nil {
			return nil, "", catalogFailure("upstream_invalid_response")
		}
		for _, m := range body.Models {
			if slices.Contains(m.Methods, "generateContent") {
				models = append(models, api.RelayProviderModel{Id: strings.TrimPrefix(m.Name, "models/"), MaxInputTokens: catalogTokenLimit(m.InputTokenLimit), MaxOutputTokens: catalogTokenLimit(m.OutputTokenLimit)})
			}
		}
		next = body.Next
	} else {
		if body.Data == nil {
			return nil, "", catalogFailure("upstream_invalid_response")
		}
		for _, m := range body.Data {
			model := api.RelayProviderModel{Id: m.ID}
			if kind == "anthropic" {
				model.MaxInputTokens = catalogTokenLimit(m.MaxInputTokens)
				model.MaxOutputTokens = catalogTokenLimit(m.MaxTokens)
			} else {
				var top struct {
					ContextLength       json.RawMessage `json:"context_length"`
					MaxCompletionTokens json.RawMessage `json:"max_completion_tokens"`
				}
				// A malformed optional extension must not discard the model ID.
				_ = json.Unmarshal(m.TopProvider, &top)
				model.ContextWindow = minimumKnown(catalogTokenLimit(m.ContextWindow), catalogTokenLimit(m.ContextLength), catalogTokenLimit(top.ContextLength))
				model.MaxOutputTokens = minimumKnown(catalogTokenLimit(m.MaxOutputTokens), catalogTokenLimit(m.MaxCompletionTokens), catalogTokenLimit(top.MaxCompletionTokens))
			}
			models = append(models, model)
		}
		if kind == "anthropic" && body.HasMore {
			next = body.LastID
			if next == "" {
				return nil, "", catalogFailure("upstream_invalid_response")
			}
		}
	}
	return models, next, nil
}

// Only integer literals that can be represented exactly by all API consumers are
// projected. A missing, malformed or out-of-range optional limit stays unknown.
func catalogTokenLimit(raw json.RawMessage) *int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || n < 1 || n > maxInteger {
		return nil
	}
	return &n
}

func minimumKnown(values ...*int64) *int64 {
	var minimum *int64
	for _, value := range values {
		if value != nil && (minimum == nil || *value < *minimum) {
			minimum = value
		}
	}
	return minimum
}

// Duplicate provider records may describe different routes. Never advertise a
// limit as known unless every record supplying that model ID knows that limit.
func minimumKnownByBoth(a, b *int64) *int64 {
	if a == nil || b == nil {
		return nil
	}
	return minimumKnown(a, b)
}

func validCatalogID(id string) bool {
	return id != "" && len(id) <= 256 && strings.TrimSpace(id) == id && !strings.ContainsFunc(id, unicode.IsControl)
}

func rejectCatalog(w http.ResponseWriter, err error) {
	var failure catalogFailure
	if !errors.As(err, &failure) {
		reject(w, err)
		return
	}
	status, code := http.StatusServiceUnavailable, "common.unavailable"
	if failure == "credential_missing" || failure == "provider_disabled" {
		status, code = http.StatusConflict, "common.conflict"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "title": http.StatusText(status), "params": map[string]string{"reason": string(failure)}})
}
