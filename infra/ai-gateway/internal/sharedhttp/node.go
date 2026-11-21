package sharedhttp

import (
	"context"
	"net/http"
	"strings"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
)

type nodeContextKey struct{}

// NodeID is established only by the verified mTLS wrapper, never HTTP headers.
func NodeID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(nodeContextKey{}).(string)
	return id, ok && id != ""
}

// NewNodeHandler shares the model transport's exact Node certificate policy.
func NewNodeHandler(ids []string, next http.Handler) (http.Handler, error) {
	nodes, err := nodeSet(ids)
	if err != nil || next == nil {
		return nil, sharedauth.ErrInvalid
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !nodePeer(r.TLS, nodes) {
			writeError(w, http.StatusForbidden, "common.forbidden")
			return
		}
		for _, uri := range r.TLS.PeerCertificates[0].URIs {
			if strings.HasPrefix(uri.String(), "auriga://node/") {
				id := strings.TrimPrefix(uri.String(), "auriga://node/")
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nodeContextKey{}, id)))
				return
			}
		}
		writeError(w, http.StatusForbidden, "common.forbidden")
	}), nil
}
