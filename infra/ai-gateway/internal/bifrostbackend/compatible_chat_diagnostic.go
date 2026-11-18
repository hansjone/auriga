package bifrostbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"github.com/maximhq/bifrost/core/schemas"
)

type compatibleChatDiagnosticKey struct{}

type compatibleChatDiagnostic struct {
	traceID string
	stream  bool
	fields  []slog.Attr
}

// Record only fixed field names and their JSON types after the relay has
// normalized the request, before Bifrost can rename or add wire fields.
func withCompatibleChatDiagnostic(ctx context.Context, keySource string, routes []Route, stream bool, fields map[string]json.RawMessage) context.Context {
	if keySource != "external" || len(routes) != 1 || routes[0].Kind != "openai-compatible" {
		return ctx
	}
	shape := compatibleChatDiagnostic{traceID: observability.TraceID(ctx), stream: stream}
	for _, field := range []string{
		"max_tokens", "max_completion_tokens", "stream_options", "temperature", "top_p",
		"reasoning_effort", "service_tier", "tools", "tool_choice", "response_format",
	} {
		if value := bytes.TrimSpace(fields[field]); len(value) != 0 {
			shape.fields = append(shape.fields, slog.String(field, relayJSONType(value[0])))
		}
	}
	return context.WithValue(ctx, compatibleChatDiagnosticKey{}, shape)
}

func relayJSONType(first byte) string {
	switch first {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		// A request field has already been decoded and contains valid JSON.
		return "number"
	}
}

func compatibleProviderErrorType(f *schemas.BifrostError) string {
	if f == nil || f.Error == nil || f.Error.Type == nil || *f.Error.Type == "" {
		return "missing"
	}
	switch *f.Error.Type {
	case "invalid_request_error", "invalid_request", "invalid_argument", "bad_request",
		"unsupported_parameter", "unsupported_value", "model_not_found":
		return *f.Error.Type
	default:
		return "other"
	}
}
