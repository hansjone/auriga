package ingress

import (
	"encoding/json"
	"net/http"
)

// Problem is RFC 7807 problem+json with an Auriga error code.
type Problem struct {
	Type   string `json:"type,omitempty"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code"`
}

// Stable error codes (to be registered in contracts).
const (
	CodeUnauthorized           = "agent.ingress.unauthorized"
	CodeTicketInvalid          = "agent.ingress.ticket_invalid"
	CodeTicketExpired          = "agent.ingress.ticket_expired"
	CodeTicketInstanceMismatch = "agent.ingress.ticket_instance_mismatch"
	CodeTicketPathDenied       = "agent.ingress.ticket_path_denied"
	CodeTicketOpDenied         = "agent.ingress.ticket_op_denied"
	CodePathEscape             = "agent.path_escape"
	CodePathInvalid            = "agent.path_invalid"
	CodeInstanceNotFound       = "agent.instance_not_found"
	CodeMethodNotAllowed       = "agent.ingress.method_not_allowed"
	CodeInternal               = "agent.ingress.internal"
)

func writeProblem(w http.ResponseWriter, status int, code, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:   "about:blank",
		Title:  title,
		Status: status,
		Detail: detail,
		Code:   code,
	})
}
