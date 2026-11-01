// Package bifrostbackend is Auriga's small HTTP adapter around the maintained Bifrost core.
package bifrostbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
}
type account struct {
	routes  []Route
	private bool
}

func (a account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, len(a.routes))
	for i, r := range a.routes {
		out[i] = schemas.ModelProvider(r.ID)
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
		return result, errors.New("no enabled provider")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	bc := schemas.NewBifrostContext(requestCtx, time.Now().Add(150*time.Second))
	defer bc.Cancel()
	weighted := make([]schemas.Key, len(routes))
	for i, r := range routes {
		weighted[i] = schemas.Key{ID: r.ID, Weight: r.Weight}
	}
	selected, err := keyselectors.WeightedRandom(bc, weighted, "", "pool")
	if err != nil {
		return result, err
	}
	ordered := make([]Route, 0, len(routes))
	for _, r := range routes {
		if r.ID == selected.ID {
			ordered = append(ordered, r)
		}
	}
	for _, r := range routes {
		if r.ID != selected.ID {
			ordered = append(ordered, r)
		}
	}
	client, err := core.Init(requestCtx, schemas.BifrostConfig{Account: account{ordered, allowPrivate}, InitialPoolSize: 2, DropExcessRequests: true, Logger: core.NewDefaultLogger(schemas.LogLevelError)})
	if err != nil {
		return result, errors.New("provider initialization failed")
	}
	defer client.Shutdown()
	var input openai.OpenAIChatRequest
	if json.Unmarshal(raw, &input) != nil {
		return result, errors.New("invalid chat request")
	}
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
			result = Result{int64(u.PromptTokens), int64(u.CompletionTokens), u.PromptTokens >= 0 && u.CompletionTokens >= 0 && u.TotalTokens == u.PromptTokens+u.CompletionTokens}
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
		b, e = json.Marshal(fields)
		if e != nil {
			return e
		}
		return emit(b)
	}
	if input.Stream == nil || !*input.Stream {
		response, failure := client.ChatCompletionRequest(bc, req)
		if failure != nil {
			return result, providerFailure(failure)
		}
		capture(response)
		return result, encode(response)
	}
	stream, failure := client.ChatCompletionStreamRequest(bc, req)
	if failure != nil {
		return result, providerFailure(failure)
	}
	var streamErr error
	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			streamErr = providerFailure(chunk.BifrostError)
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
	return result, streamErr
}

func providerFailure(f *schemas.BifrostError) error {
	// Upstream error text can include credentials or request content.
	if f == nil {
		return io.ErrUnexpectedEOF
	}
	code := 0
	if f.StatusCode != nil {
		code = *f.StatusCode
	}
	return fmt.Errorf("provider request failed (HTTP %d)", code)
}

func providerKind(kind string) string {
	if kind == "openai-compatible" {
		return "openai"
	}
	return strings.ToLower(kind)
}
