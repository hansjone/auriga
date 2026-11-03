// Package bifrostbackend is Auriga's small HTTP adapter around the maintained Bifrost core.
package bifrostbackend

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	core "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/keyselectors"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"
)

type Route struct {
	ID     string  `json:"providerId"`
	Kind   string  `json:"-"`
	URL    string  `json:"-"`
	Secret string  `json:"-"`
	Model  string  `json:"model"`
	Weight float64 `json:"weight"`
}

type Result struct {
	InputTokens, OutputTokens int64
	Confirmed                 bool
	ObservedBytes             int64
	NoCharge                  bool
}
type account struct {
	routes  []Route
	private bool
}

func (a account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, 0, len(a.routes))
	seen := make(map[string]bool)
	for _, r := range a.routes {
		if !seen[r.ID] {
			out = append(out, schemas.ModelProvider(r.ID))
			seen[r.ID] = true
		}
	}
	return out, nil
}
func (a account) GetConfigForProvider(id schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	for _, r := range a.routes {
		if string(id) == r.ID {
			return &schemas.ProviderConfig{
				NetworkConfig:            schemas.NetworkConfig{BaseURL: r.URL, DefaultRequestTimeoutInSeconds: 120, MaxRetries: 0, StreamIdleTimeoutInSeconds: 30, MaxConnsPerHost: 2, AllowPrivateNetwork: a.private},
				ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{Concurrency: 1, BufferSize: 2},
				CustomProviderConfig:     &schemas.CustomProviderConfig{BaseProviderType: schemas.ModelProvider(r.Kind)},
			}, nil
		}
	}
	return nil, errors.New("unknown configured provider")
}
func (a account) GetKeysForProvider(_ context.Context, id schemas.ModelProvider) ([]schemas.Key, error) {
	for _, r := range a.routes {
		if string(id) == r.ID {
			return []schemas.Key{{ID: r.ID, Name: r.ID, Value: *schemas.NewSecretVar(r.Secret), Weight: 1, Models: schemas.WhiteList{"*"}}}, nil
		}
	}
	return nil, errors.New("unknown configured provider")
}

// Run uses Bifrost's weighted selector, provider workers, retry/fallback and SSE
// parsers. Every invocation owns an immutable account snapshot and drains its
// workers before returning, including when the downstream writer disconnects.
func Run(ctx context.Context, routes []Route, raw []byte, allowPrivate bool, emit func([]byte) error) (Result, error) {
	var result Result
	if len(routes) == 0 || emit == nil {
		return Result{NoCharge: true}, errors.New("no enabled provider")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	bc := schemas.NewBifrostContext(requestCtx, time.Now().Add(150*time.Second))
	defer bc.Cancel()
	weighted := make([]schemas.Key, len(routes))
	for i, r := range routes {
		// A pool selects a route, not a provider: one provider can serve several
		// models with distinct weights. Keep the provider ID for Bifrost config.
		weighted[i] = schemas.Key{ID: strconv.Itoa(i), Weight: r.Weight}
	}
	selected, err := keyselectors.WeightedRandom(bc, weighted, "", "pool")
	if err != nil {
		return result, err
	}
	ordered := make([]Route, 0, len(routes))
	for i, r := range routes {
		if strconv.Itoa(i) == selected.ID {
			ordered = append(ordered, r)
		}
	}
	for i, r := range routes {
		if strconv.Itoa(i) != selected.ID {
			ordered = append(ordered, r)
		}
	}
	client, err := core.Init(requestCtx, schemas.BifrostConfig{Account: account{ordered, allowPrivate}, InitialPoolSize: 2, DropExcessRequests: true, Logger: core.NewDefaultLogger(schemas.LogLevelError)})
	if err != nil {
		return Result{NoCharge: true}, errors.New("provider initialization failed")
	}
	defer client.Shutdown()
	var input openai.OpenAIChatRequest
	if json.Unmarshal(raw, &input) != nil {
		return Result{NoCharge: true}, errors.New("invalid chat request")
	}
	publicModel := input.Model
	input.Model = ordered[0].Model
	req := input.ToBifrostChatRequest(bc)
	req.Provider = schemas.ModelProvider(ordered[0].ID)
	req.Model = ordered[0].Model
	for _, r := range ordered[1:] {
		req.Fallbacks = append(req.Fallbacks, schemas.Fallback{Provider: schemas.ModelProvider(r.ID), Model: r.Model})
	}
	capture := func(resp *schemas.BifrostChatResponse) {
		if resp != nil && resp.Usage != nil {
			u := resp.Usage
			result.InputTokens, result.OutputTokens = int64(u.PromptTokens), int64(u.CompletionTokens)
			result.Confirmed = u.PromptTokens >= 0 && u.CompletionTokens >= 0 && u.TotalTokens > 0 && u.TotalTokens == u.PromptTokens+u.CompletionTokens
		}
	}
	encode := func(resp *schemas.BifrostChatResponse) error {
		b, e := json.Marshal(resp)
		if e != nil {
			return e
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(b, &fields) != nil {
			return errors.New("invalid provider response")
		}
		delete(fields, "extra_fields")
		// The route's real model belongs to the provider hop. Both ordinary and
		// streaming replies retain the model ID the caller is authorized to use.
		fields["model"], _ = json.Marshal(publicModel)
		b, e = json.Marshal(fields)
		if e != nil {
			return e
		}
		result.ObservedBytes += outputBytes(b)
		return emit(b)
	}
	if input.Stream == nil || !*input.Stream {
		response, failure := client.ChatCompletionRequest(bc, req)
		if failure != nil {
			err := loggedProviderFailure(ctx, failure)
			result.NoCharge = err.Definitive
			return result, err
		}
		capture(response)
		return result, encode(response)
	}
	stream, failure := client.ChatCompletionStreamRequest(bc, req)
	if failure != nil {
		err := loggedProviderFailure(ctx, failure)
		result.NoCharge = err.Definitive
		return result, err
	}
	var streamErr error
	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			streamErr = loggedProviderFailure(ctx, chunk.BifrostError)
			cancel()
			continue
		}
		if chunk.BifrostChatResponse == nil {
			continue
		}
		capture(chunk.BifrostChatResponse)
		if streamErr == nil {
			if e := encode(chunk.BifrostChatResponse); e != nil {
				streamErr = e
				cancel()
			}
		}
	}
	if requestCtx.Err() != nil && streamErr == nil {
		streamErr = requestCtx.Err()
	}
	if streamErr != nil && result.ObservedBytes == 0 {
		var f *ProviderFailure
		if errors.As(streamErr, &f) {
			result.NoCharge = f.Definitive
		}
	}
	return result, streamErr
}

type ProviderFailure struct {
	Status     int
	Code       string
	Definitive bool
	Reason     string
	Parameter  string
}

func (e *ProviderFailure) Error() string { return e.Code }
func loggedProviderFailure(ctx context.Context, failure *schemas.BifrostError) *ProviderFailure {
	err := providerFailure(failure)
	slog.WarnContext(ctx, "relay.provider_failure", "status", err.Status, "code", err.Code, "reason", err.Reason, "parameter", err.Parameter)
	return err
}
func providerFailure(f *schemas.BifrostError) *ProviderFailure {
	out := &ProviderFailure{Status: 502, Code: "provider_unavailable", Reason: "other", Parameter: "other"}
	if f == nil {
		return out
	}
	if f.StatusCode != nil {
		code := *f.StatusCode
		if code >= 400 && code < 500 {
			out.Status = code
			out.Code = "provider_rejected_request"
		}
		if code >= 400 && code < 600 && !f.IsBifrostError {
			out.Definitive = true
		}
		if code == 408 || code == 504 {
			out.Status = 504
			out.Code = "provider_timeout"
		}
	}
	if f.Error != nil {
		if p, ok := f.Error.Param.(string); ok {
			switch p {
			case "model", "messages", "max_tokens", "max_completion_tokens", "temperature", "top_p", "reasoning_effort", "tools", "tool_choice", "response_format", "stream", "stream_options", "service_tier":
				out.Parameter = p
			}
		}
		var dial *net.OpError
		if errors.As(f.Error.Error, &dial) && dial.Op == "dial" {
			out.Definitive = true
		}
		if errors.Is(f.Error.Error, context.DeadlineExceeded) {
			out.Status = 504
			out.Code = "provider_timeout"
			out.Definitive = false
		}
		if f.Error.Code != nil {
			switch *f.Error.Code {
			case "context_length_exceeded", "invalid_request_error", "rate_limit_exceeded", "unsupported_parameter", "unsupported_value", "model_not_found", "invalid_api_key", "insufficient_quota", "permission_denied", "api_unsupported":
				out.Reason = *f.Error.Code
			}
			switch *f.Error.Code {
			case "context_length_exceeded", "invalid_request_error", "rate_limit_exceeded":
				out.Code = *f.Error.Code
			}
		}
	}
	return out
}
func providerKind(kind string) string {
	if kind == "openai-compatible" {
		return "openai"
	}
	return strings.ToLower(kind)
}

func outputBytes(raw []byte) int64 {
	var fields struct {
		Choices []struct {
			Delta   map[string]json.RawMessage
			Message map[string]json.RawMessage
		}
	}
	if json.Unmarshal(raw, &fields) != nil {
		return 0
	}
	var total int64
	var count func(any)
	count = func(value any) {
		switch v := value.(type) {
		case string:
			total += int64(len(v))
		case []any:
			for _, x := range v {
				count(x)
			}
		case map[string]any:
			for _, x := range v {
				count(x)
			}
		}
	}
	for _, c := range fields.Choices {
		m := c.Delta
		if m == nil {
			m = c.Message
		}
		for _, name := range []string{"content", "tool_calls", "function_call", "reasoning_content", "reasoning"} {
			if value, ok := m[name]; ok {
				var decoded any
				if json.Unmarshal(value, &decoded) == nil {
					count(decoded)
				}
			}
		}
	}
	return total
}
