package main

import (
	"net/http"
	"regexp"
)

// randomID(12) is 16 unpadded base64url characters, independent of credentials.
var openFlowPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`)

func validOpenFlow(flow string) bool { return openFlowPattern.MatchString(flow) }

func (g *Gateway) logOpenNonce(r *http.Request, rid, outcome, name string) {
	if g.Log == nil {
		return
	}
	count, empty := 0, false
	for _, c := range r.Cookies() {
		if name != "" && c.Name == name {
			count++
			empty = c.Value == ""
		}
	}
	state := "missing"
	switch {
	case count > 1:
		state = "duplicate"
	case count == 1 && empty:
		state = "empty"
	case count == 1:
		state = "single"
	}
	g.Log.InfoContext(r.Context(), "Open nonce exchange", "request_id", rid,
		"nonce_state", state, "nonce_count", count, "nonce_name", name, "outcome", outcome)
}
