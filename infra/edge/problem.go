package main

import (
	"encoding/json"
	"net/http"
)

// Problem is RFC 9457 application/problem+json aligned with contracts/openapi/common.yaml.
type Problem struct {
	Type     string         `json:"type,omitempty"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Code     string         `json:"code"`
	Params   map[string]any `json:"params,omitempty"`
	TraceID  string         `json:"traceId,omitempty"`
	Instance string         `json:"instance,omitempty"`
}

func writeProblem(w http.ResponseWriter, p Problem) {
	if p.Type == "" {
		p.Type = "about:blank"
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

func problem(status int, code, title, detail, traceID string) Problem {
	return Problem{
		Title:   title,
		Status:  status,
		Detail:  detail,
		Code:    code,
		TraceID: traceID,
	}
}
