package modelprotocol

import api "github.com/hansjone/auriga/infra/gen/aiapi"

// Error contains fixed catalog messages only; never wraps untrusted I/O/errors.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func failure(code string) *Error {
	m := map[string]string{
		"common.validation_failed": "Validation failed",
		"ai.upstream_rejected":     "The model provider rejected the request",
		"ai.upstream_timeout":      "The model provider timed out",
		"ai.upstream_unavailable":  "The model provider is unavailable",
		"ai.stream_interrupted":    "The model response stream was interrupted",
	}
	return &Error{Code: code, Message: m[code]}
}

// Envelope uses a local trusted request ID; upstream IDs/messages never escape.
func (e *Error) Envelope(requestID string) api.AiError {
	kind := "upstream_error"
	if e.Code == "common.validation_failed" {
		kind = "invalid_request_error"
	}
	return api.AiError{RequestId: requestID, Error: api.AiErrorError{Code: e.Code, Message: e.Message, Type: kind}}
}
