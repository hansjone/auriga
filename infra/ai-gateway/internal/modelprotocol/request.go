// Package modelprotocol implements the bounded D2 fake-transport protocol only.
package modelprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// Policy is service configuration bound to one model, never client metadata.
// MeasureInput covers the entire effective input including tools/templates and
// returns exact tokens or a proven safe upper bound. Missing capabilities deny.
type Policy struct {
	Model                          string
	ContextWindow, MaxOutputTokens int64
	Tools, Temperature, TopP, Stop bool
	MeasureInput                   func(context.Context, api.ChatCompletionRequest) (int64, error)
	VerifyUsage                    func(context.Context, api.TokenUsage) bool
}

// Request keeps an isolated validated snapshot; Value returns a fresh copy.
type Request struct {
	raw        []byte
	policy     Policy
	inputBound int64
}

func (r *Request) InputBound() int64 { return r.inputBound }
func (r *Request) Value() api.ChatCompletionRequest {
	v, _ := requestValue(r.raw) // DecodeRequest already validated this private snapshot.
	return v
}

// Decode open parameters separately: generated map decoding rounds large integers
// and rejects otherwise valid schema numbers outside the float64 range.
func requestValue(raw []byte) (api.ChatCompletionRequest, error) {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	var tools []map[string]json.RawMessage
	_ = json.Unmarshal(m["tools"], &tools)
	parameters := make([]map[string]any, len(tools))
	for i, t := range tools {
		var fn map[string]json.RawMessage
		_ = json.Unmarshal(t["function"], &fn)
		d := json.NewDecoder(bytes.NewReader(fn["parameters"]))
		d.UseNumber()
		_ = d.Decode(&parameters[i])
		fn["parameters"] = json.RawMessage(`{}`)
		t["function"], _ = json.Marshal(fn)
	}
	if len(tools) > 0 {
		m["tools"], _ = json.Marshal(tools)
	}
	b, _ := json.Marshal(m)
	var v api.ChatCompletionRequest
	err := json.Unmarshal(b, &v)
	if err == nil {
		for i, p := range parameters {
			v.Tools[i].Function.Parameters = p
		}
	}
	return v, err
}

// ValidateRequest is the equivalent entry for callers holding generated types.
func ValidateRequest(ctx context.Context, v api.ChatCompletionRequest, p Policy) (*Request, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, failure("common.validation_failed")
	}
	return DecodeRequest(ctx, bytes.NewReader(b), p)
}

func DecodeRequest(ctx context.Context, source io.Reader, p Policy) (*Request, error) {
	b, err := boundedRead(source, RequestLimit)
	if err != nil || !parse(b, requestRule, nil) || ctx.Err() != nil {
		return nil, failure("common.validation_failed")
	}
	v, err := requestValue(b)
	if err != nil {
		return nil, failure("common.validation_failed")
	}
	if p.Model != v.Model || p.ContextWindow < 1 || p.ContextWindow > safeInteger || p.MaxOutputTokens < 1 || p.MaxOutputTokens > safeInteger || p.MeasureInput == nil || v.MaxTokens > p.MaxOutputTokens || v.MaxTokens > p.ContextWindow {
		return nil, failure("common.validation_failed")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	for key, allowed := range map[string]bool{"tools": p.Tools, "tool_choice": p.Tools, "temperature": p.Temperature, "top_p": p.TopP, "stop": p.Stop} {
		if _, ok := fields[key]; ok && !allowed {
			return nil, failure("common.validation_failed")
		}
	}
	if !history(v, p.Tools) {
		return nil, failure("common.validation_failed")
	}
	if v.ToolChoice != nil && (*v.ToolChoice == "required" || *v.ToolChoice == "auto") && len(v.Tools) == 0 {
		return nil, failure("common.validation_failed")
	}
	names := map[string]bool{}
	for _, tool := range v.Tools {
		if names[tool.Function.Name] {
			return nil, failure("common.validation_failed")
		}
		names[tool.Function.Name] = true
	}
	r := &Request{raw: b, policy: p}
	n, err := p.MeasureInput(ctx, r.Value())
	if err != nil || ctx.Err() != nil || n < 0 || n > p.ContextWindow-v.MaxTokens {
		return nil, failure("common.validation_failed")
	}
	r.inputBound = n
	return r, nil
}

func history(v api.ChatCompletionRequest, tools bool) bool {
	pending, seen := map[string]bool{}, map[string]bool{}
	for _, m := range v.Messages {
		if len(pending) > 0 && m.Role != "tool" {
			return false
		}
		if m.Role == "tool" {
			if m.ToolCallId == nil || !pending[*m.ToolCallId] || m.Content.Get() == nil || len(m.ToolCalls) > 0 {
				return false
			}
			delete(pending, *m.ToolCallId)
		} else if m.ToolCallId != nil || (len(m.ToolCalls) > 0 && (m.Role != "assistant" || !tools)) || (m.Content.Get() == nil && (m.Role != "assistant" || len(m.ToolCalls) == 0)) {
			return false
		}
		for _, c := range m.ToolCalls {
			if seen[c.Id] {
				return false
			}
			seen[c.Id] = true
			pending[c.Id] = true
		}
	}
	return len(pending) == 0
}
