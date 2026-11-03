package bifrostbackend

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
)

// inputTokenEstimate accounts only for inline text and client-executed function
// tools. Media, opaque state and provider-executed tools can add billable input
// after dispatch; neither JSON bytes nor capability hints bound that input.
// Metering-only requests retain upstream semantics. The strict allowlist is
// intentionally separate from HTTP's wire allowlist: a newly supported field
// must acquire an accounting rule before a budgeted key can use it.
func inputTokenEstimate(raw []byte, meteringOnly bool) (int64, error) {
	estimate := int64(len(raw))*2 + 64
	if meteringOnly {
		return estimate, nil
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(raw, &request) != nil || request == nil {
		return 0, invalid
	}
	for name, value := range request {
		valid := false
		switch name {
		case "messages":
			valid = boundedMessages(value)
		case "tools":
			valid = boundedFunctionTools(value)
		case "tool_choice":
			valid = boundedToolChoice(value)
		case "response_format":
			valid = boundedResponseFormat(value)
		case "model", "stream", "stream_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "seed", "stop", "n", "presence_penalty", "frequency_penalty", "logit_bias", "logprobs", "top_logprobs", "parallel_tool_calls", "reasoning_effort", "service_tier", "user", "metadata":
			// Run decodes these into scalar/inline generation parameters, not
			// provider state, retrieval or tool declarations. HTTP replaces user,
			// removes metadata and fixes streaming usage before this estimate.
			valid = true
		}
		if !valid {
			return 0, inputAccountingError("/" + name)
		}
	}
	if request["messages"] == nil {
		return 0, invalid
	}
	return estimate, nil
}

type inputAccountingError string

func (e inputAccountingError) Error() string { return string(invalid) }
func (e inputAccountingError) Unwrap() error { return invalid }

func boundedMessages(raw json.RawMessage) bool {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return false
	}
	for _, message := range messages {
		if !inputFields(message, "role", "name", "content", "tool_call_id", "is_error", "refusal", "reasoning_content", "reasoning", "reasoning_details", "tool_calls") {
			return false
		}
		var role string
		if json.Unmarshal(message["role"], &role) != nil || !slices.Contains([]string{"user", "assistant", "system", "developer", "tool"}, role) {
			return false
		}
		if !boundedReasoning(message["reasoning_details"]) || !boundedFunctionCalls(message["tool_calls"]) {
			return false
		}
		content := bytes.TrimSpace(message["content"])
		if len(content) == 0 || bytes.Equal(content, []byte("null")) {
			continue
		}
		var text string
		if content[0] == '"' && json.Unmarshal(content, &text) == nil {
			continue
		}
		var blocks []map[string]json.RawMessage
		if content[0] != '[' || json.Unmarshal(content, &blocks) != nil {
			return false
		}
		for _, block := range blocks {
			var kind string
			if json.Unmarshal(block["type"], &kind) != nil || kind != "text" && kind != "refusal" {
				return false
			}
			value := "text"
			if kind == "refusal" {
				value = "refusal"
			}
			if json.Unmarshal(block[value], &text) != nil {
				return false
			}
			if !boundedCacheControl(block["cache_control"]) {
				return false
			}
			for key := range block {
				if key != "type" && key != value && key != "cache_control" {
					return false
				}
			}
		}
	}
	return true
}

func boundedReasoning(raw json.RawMessage) bool {
	var details []struct {
		Type    string  `json:"type"`
		ID      *string `json:"id,omitempty"`
		Index   *int    `json:"index,omitempty"`
		Text    *string `json:"text,omitempty"`
		Summary *string `json:"summary,omitempty"`
	}
	if !inputJSON(raw, &details) {
		return false
	}
	for _, detail := range details {
		if detail.Type != "reasoning.text" && detail.Type != "reasoning.summary" {
			return false
		}
	}
	return true
}

func boundedFunctionCalls(raw json.RawMessage) bool {
	var calls []struct {
		Type         string          `json:"type"`
		ID           *string         `json:"id,omitempty"`
		Index        *int            `json:"index,omitempty"`
		Function     json.RawMessage `json:"function"`
		CacheControl json.RawMessage `json:"cache_control,omitempty"`
	}
	if !inputJSON(raw, &calls) {
		return false
	}
	for _, call := range calls {
		var function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if call.Type != "function" || !inputJSON(call.Function, &function) || function.Name == "" || !boundedCacheControl(call.CacheControl) {
			return false
		}
		callID := function.Name
		if call.ID != nil && strings.TrimSpace(*call.ID) != "" {
			callID = *call.ID
		}
		// Gemini's converter extracts opaque thought state from this otherwise
		// textual ID (or the function name when ID is absent).
		if providerUtils.StripThoughtSignature(callID) != callID {
			return false
		}
	}
	return true
}

func boundedFunctionTools(raw json.RawMessage) bool {
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description *string         `json:"description,omitempty"`
			Parameters  json.RawMessage `json:"parameters,omitempty"`
			Strict      *bool           `json:"strict,omitempty"`
		} `json:"function"`
		CacheControl        json.RawMessage `json:"cache_control,omitempty"`
		DeferLoading        *bool           `json:"defer_loading,omitempty"`
		AllowedCallers      []string        `json:"allowed_callers,omitempty"`
		EagerInputStreaming *bool           `json:"eager_input_streaming,omitempty"`
		InputExamples       []struct {
			Input       json.RawMessage `json:"input"`
			Description *string         `json:"description,omitempty"`
		} `json:"input_examples,omitempty"`
	}
	if !inputJSON(raw, &tools) {
		return false
	}
	for _, tool := range tools {
		if tool.Type != "function" || tool.Function.Name == "" || !boundedCacheControl(tool.CacheControl) || !inlineSchema(tool.Function.Parameters) {
			return false
		}
		for _, caller := range tool.AllowedCallers {
			if caller != "direct" {
				return false
			}
		}
	}
	return true
}

func boundedToolChoice(raw json.RawMessage) bool {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return true
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		return slices.Contains([]string{"auto", "none", "required", "any"}, mode)
	}
	var choice struct {
		Type     string `json:"type"`
		Function *struct {
			Name string `json:"name"`
		} `json:"function,omitempty"`
		AllowedTools *struct {
			Mode  string          `json:"mode"`
			Tools json.RawMessage `json:"tools"`
		} `json:"allowed_tools,omitempty"`
	}
	if !inputJSON(raw, &choice) {
		return false
	}
	switch choice.Type {
	case "function":
		return choice.Function != nil && choice.Function.Name != "" && choice.AllowedTools == nil
	case "allowed_tools":
		return choice.Function == nil && choice.AllowedTools != nil && slices.Contains([]string{"auto", "required"}, choice.AllowedTools.Mode) && boundedFunctionTools(choice.AllowedTools.Tools)
	default:
		return false
	}
}

func boundedResponseFormat(raw json.RawMessage) bool {
	var format struct {
		Type       string `json:"type"`
		JSONSchema *struct {
			Name        string          `json:"name"`
			Description *string         `json:"description,omitempty"`
			Schema      json.RawMessage `json:"schema"`
			Strict      *bool           `json:"strict,omitempty"`
		} `json:"json_schema,omitempty"`
	}
	if !inputJSON(raw, &format) {
		return false
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return true
	}
	switch format.Type {
	case "text", "json_object":
		return format.JSONSchema == nil
	case "json_schema":
		return format.JSONSchema != nil && format.JSONSchema.Name != "" && len(format.JSONSchema.Schema) != 0 && inlineSchema(format.JSONSchema.Schema)
	default:
		return false
	}
}

func boundedCacheControl(raw json.RawMessage) bool {
	var control struct {
		Type  string  `json:"type"`
		TTL   *string `json:"ttl,omitempty"`
		Scope *string `json:"scope,omitempty"`
	}
	return inputJSON(raw, &control)
}

// Schemas are inline data, not a port for retrieving external documents. Keep
// local references; fail closed on URI references, including future dialects.
func inlineSchema(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	// The upstream function-schema decoder also accepts a JSON-encoded string.
	// Do not let that alternate representation hide a second layer of semantics.
	switch value.(type) {
	case map[string]any, bool, nil:
	default:
		return false
	}
	return localSchemaReferences(value)
}

func localSchemaReferences(value any) bool {
	switch node := value.(type) {
	case map[string]any:
		for name, child := range node {
			if name == "$ref" || name == "$dynamicRef" || name == "$recursiveRef" {
				if reference, ok := child.(string); !ok || !strings.HasPrefix(reference, "#") {
					return false
				}
			}
			switch name {
			case "$defs", "definitions", "properties", "patternProperties", "dependentSchemas", "dependencies":
				if schemas, ok := child.(map[string]any); ok {
					for _, schema := range schemas {
						if !localSchemaReferences(schema) {
							return false
						}
					}
				}
			case "items", "prefixItems", "allOf", "anyOf", "oneOf", "not", "if", "then", "else", "contains", "propertyNames", "additionalProperties", "additionalItems", "unevaluatedProperties", "unevaluatedItems", "contentSchema":
				if !localSchemaReferences(child) {
					return false
				}
			}
		}
	case []any:
		for _, child := range node {
			if !localSchemaReferences(child) {
				return false
			}
		}
	}
	return true
}

func inputJSON(raw json.RawMessage, value any) bool {
	if len(raw) == 0 {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value) == nil
}

func inputFields(fields map[string]json.RawMessage, allowed ...string) bool {
	if fields == nil {
		return false
	}
	for name := range fields {
		if !slices.Contains(allowed, name) {
			return false
		}
	}
	return true
}

func rejectInputAccounting(w http.ResponseWriter, err error) {
	field := inputAccountingError("/messages")
	_ = errors.As(err, &field)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": http.StatusBadRequest, "code": string(invalid), "title": "Bad Request",
		"detail": "Budgeted keys require inline text and local function tools with a trusted input-token bound",
		"params": map[string]string{"field": string(field)},
	})
}
