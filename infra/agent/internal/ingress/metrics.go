package ingress

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// Lightweight Prometheus text exposition without external deps.
type metrics struct {
	requests atomic.Int64
}

func newMetrics() *metrics { return &metrics{} }

func (m *metrics) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprintf(w, "# HELP auriga_agent_ingress_requests_total Total ingress requests.\n")
		_, _ = fmt.Fprintf(w, "# TYPE auriga_agent_ingress_requests_total counter\n")
		_, _ = fmt.Fprintf(w, "auriga_agent_ingress_requests_total %d\n", m.requests.Load())
	})
}
