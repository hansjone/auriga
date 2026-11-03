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
	seen := map[string]bool{}
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
		ids, next, err := decodeCatalog(route.Kind, raw)
		if err != nil {
			return out, err
		}
		for _, id := range ids {
			if !validCatalogID(id) {
				return out, catalogFailure("upstream_invalid_response")
			}
			if !seen[id] {
				if len(out.Items) == catalogLimit {
					return out, catalogFailure("catalog_too_large")
				}
				seen[id] = true
				out.Items = append(out.Items, api.RelayProviderModel{Id: id})
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

func decodeCatalog(kind string, raw []byte) ([]string, string, error) {
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
		Next    string `json:"nextPageToken"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil, "", catalogFailure("upstream_invalid_response")
	}
	ids := []string{}
	next := ""
	if kind == "gemini" {
		if body.Models == nil {
			return nil, "", catalogFailure("upstream_invalid_response")
		}
		for _, m := range body.Models {
			if slices.Contains(m.Methods, "generateContent") {
				ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
			}
		}
		next = body.Next
	} else {
		if body.Data == nil {
			return nil, "", catalogFailure("upstream_invalid_response")
		}
		for _, m := range body.Data {
			ids = append(ids, m.ID)
		}
		if kind == "anthropic" && body.HasMore {
			next = body.LastID
			if next == "" {
				return nil, "", catalogFailure("upstream_invalid_response")
			}
		}
	}
	return ids, next, nil
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
