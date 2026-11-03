package bifrostbackend

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// inputTokenEstimate is a text wire estimate, not a tokenizer for media. A URL,
// encoded file, or pool capability hint cannot establish a billable media
// ceiling. Budgeted requests fail before admission until a trusted media
// accounting port exists. Metering-only requests retain upstream semantics.
func inputTokenEstimate(raw []byte, meteringOnly bool) (int64, error) {
	estimate := int64(len(raw))*2 + 64
	if meteringOnly {
		return estimate, nil
	}
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(raw, &request) != nil || len(request.Messages) == 0 {
		return 0, invalid
	}
	for _, message := range request.Messages {
		content := bytes.TrimSpace(message.Content)
		if len(content) == 0 || bytes.Equal(content, []byte("null")) {
			continue
		}
		var text string
		if content[0] == '"' && json.Unmarshal(content, &text) == nil {
			continue
		}
		var blocks []map[string]json.RawMessage
		if content[0] != '[' || json.Unmarshal(content, &blocks) != nil {
			return 0, invalid
		}
		for _, block := range blocks {
			var kind string
			if json.Unmarshal(block["type"], &kind) != nil || kind != "text" && kind != "refusal" {
				return 0, invalid
			}
			value := "text"
			if kind == "refusal" {
				value = "refusal"
			}
			if json.Unmarshal(block[value], &text) != nil {
				return 0, invalid
			}
			for key := range block {
				if key != "type" && key != value && key != "cache_control" {
					return 0, invalid
				}
			}
		}
	}
	return estimate, nil
}

func rejectInputAccounting(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": http.StatusBadRequest, "code": string(invalid), "title": "Bad Request",
		"detail": "Budgeted keys require text-only message content until trusted media token accounting is available",
		"params": map[string]string{"field": "/messages"},
	})
}
