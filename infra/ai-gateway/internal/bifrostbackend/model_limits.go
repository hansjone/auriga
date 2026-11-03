package bifrostbackend

import (
	"encoding/json"
	"net/http"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func publicModel(model api.RelayPool) map[string]any {
	return map[string]any{"id": model.Id, "object": "model", "created": 0, "owned_by": "auriga", "context_window": model.ContextWindow, "max_output_tokens": model.MaxOutputTokens}
}

func outputTokenLimit(fields map[string]json.RawMessage, maximum int64) (int64, string, error) {
	limit := maximum
	set := false
	for _, name := range []string{"max_tokens", "max_completion_tokens"} {
		if value := fields[name]; value != nil {
			var n int64
			if json.Unmarshal(value, &n) != nil || n < 1 || n > maximum || set && limit != n {
				return 0, "/" + name, invalid
			}
			limit, set = n, true
		}
	}
	return limit, "", nil
}

func rejectOutputTokenLimit(w http.ResponseWriter, field string, maximum int64) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": http.StatusBadRequest, "code": string(invalid), "title": "Bad Request",
		"detail": "Output token limits must agree and be positive integers no larger than the model maximum",
		"params": map[string]any{"field": field, "minimum": 1, "maximum": maximum},
	})
}
