// Package sharedhttp exposes the trusted Node-to-N shared model transport.
package sharedhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaylimits"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
)

const InvokePath = "/internal/shared/model/invoke"

type Invoker interface {
	Invoke(context.Context, sharedmodel.SharedRequest, func([]byte) error) error
}

type handler struct {
	tenant   string
	nodes    map[string]bool
	provider Invoker
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && value == id.String()
}

func nodeSet(ids []string) (map[string]bool, error) {
	if len(ids) == 0 || len(ids) > 10000 {
		return nil, sharedauth.ErrInvalid
	}
	nodes := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !canonicalUUID(id) || id == uuid.Nil.String() || nodes[id] {
			return nil, sharedauth.ErrInvalid
		}
		nodes[id] = true
	}
	return nodes, nil
}

func nodePeer(state *tls.ConnectionState, nodes map[string]bool) bool {
	if state == nil || len(state.PeerCertificates) == 0 || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
		return false
	}
	cert := state.PeerCertificates[0]
	now := time.Now()
	if !state.VerifiedChains[0][0].Equal(cert) || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return false
	}
	id := ""
	for _, uri := range cert.URIs {
		if strings.HasPrefix(uri.String(), "auriga://node/") {
			candidate := strings.TrimPrefix(uri.String(), "auriga://node/")
			if id != "" || !canonicalUUID(candidate) {
				return false
			}
			id = candidate
		}
	}
	return nodes[id]
}

// NewHandler fixes tenant from deployment configuration, never from a request
// header or unverified capability. The TLS peer is rechecked on each request.
func NewHandler(tenant string, nodes []string, provider Invoker) (http.Handler, error) {
	allowed, err := nodeSet(nodes)
	if err != nil || !canonicalUUID(tenant) || tenant == uuid.Nil.String() || provider == nil {
		return nil, sharedauth.ErrInvalid
	}
	return &handler{tenant: tenant, nodes: allowed, provider: provider}, nil
}

func singleHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] {
		return "", false
	}
	return values[0], true
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r, finishTrace := observability.Begin(w, r)
	defer finishTrace()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !nodePeer(r.TLS, h.nodes) {
		writeError(w, http.StatusForbidden, "common.forbidden")
		return
	}
	if r.URL.Path != InvokePath {
		writeError(w, http.StatusNotFound, "common.not_found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "common.validation_failed")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || err != nil || media != "application/json" || r.Header.Get("Content-Encoding") != "" {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	capability, capOK := singleHeader(r, "Auriga-Shared-Capability")
	session, sessionOK := singleHeader(r, "Auriga-Shared-Session-Id")
	task, taskOK := singleHeader(r, "Auriga-Shared-Task-Id")
	call, callOK := singleHeader(r, "Idempotency-Key")
	if !capOK || len(capability) > 16384 || !sessionOK || !taskOK || !callOK || !canonicalUUID(session) || !canonicalUUID(task) || !canonicalUUID(call) {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, sharedmodel.MaxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, "common.validation_failed")
		return
	}
	var fields map[string]json.RawMessage
	var streaming bool
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	if raw, ok := fields["stream"]; ok && (bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &streaming) != nil) {
		writeError(w, http.StatusBadRequest, "common.validation_failed")
		return
	}
	var response []byte
	started := false
	emit := func(chunk []byte) error {
		var compact bytes.Buffer
		if int64(len(chunk)) > relaylimits.ChatBodyBytes || json.Compact(&compact, chunk) != nil {
			return sharedauth.ErrUnavailable
		}
		if !streaming {
			if response != nil {
				return sharedauth.ErrUnavailable
			}
			response = bytes.Clone(compact.Bytes())
			return nil
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Accel-Buffering", "no")
			started = true
		}
		if _, err := w.Write(append(append([]byte("data: "), compact.Bytes()...), '\n', '\n')); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	err = h.provider.Invoke(r.Context(), sharedmodel.SharedRequest{TenantID: h.tenant, SessionID: session, TaskID: task, CallID: call, Capability: capability, Body: body}, emit)
	if err != nil {
		status, code := invocationError(err)
		if started {
			_, _ = w.Write(append(append([]byte("data: "), errorJSON(code)...), '\n', '\n'))
			_ = http.NewResponseController(w).Flush()
		} else {
			writeError(w, status, code)
		}
		return
	}
	if streaming {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		_ = http.NewResponseController(w).Flush()
		return
	}
	if response == nil {
		writeError(w, http.StatusServiceUnavailable, "common.unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(response)
}

func invocationError(err error) (int, string) {
	for _, denied := range []error{sharedauth.ErrBinding, sharedauth.ErrRevoked, sharedauth.ErrExpired, sharedauth.ErrSignature} {
		if errors.Is(err, denied) {
			return http.StatusForbidden, denied.Error()
		}
	}
	if errors.Is(err, sharedauth.ErrConflict) {
		return http.StatusConflict, sharedauth.ErrConflict.Error()
	}
	if errors.Is(err, sharedauth.ErrInvalid) {
		return http.StatusBadRequest, sharedauth.ErrInvalid.Error()
	}
	return http.StatusServiceUnavailable, sharedauth.ErrUnavailable.Error()
}

func errorJSON(code string) []byte {
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": code}})
	return raw
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(errorJSON(code))
}
