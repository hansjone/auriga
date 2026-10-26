// Package relayhttp installs the device-only D7 HTTP surface on a trusted runtime.
package relayhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaysupervisor"
)

const modelsPath = "/ai/v1/models"
const chatPath = "/ai/v1/chat/completions"
const usagePath = "/api/ai/v1/me/usage"

// Pending is a trusted recovery handoff, never an HTTP response. An empty Ref
// means uncertainty is still unlocated. A nonempty Ref does not prove that an
// admission committed or a row exists; recovery must re-read durable RLS state.
// StopProof is live process evidence only; it must not be inferred after restart.
type Pending = relaysupervisor.Pending

type Config struct {
	Core      *relay.Core
	Gate      *modelgate.Gate
	Authority *modelgate.Authority
	Ledger    *budget.Ledger
	// ReserveRecovery must reserve fixed capacity before Execute. The returned
	// finish synchronously accepts ownership even after client cancellation.
	ReserveRecovery                         func() (finish func(*Pending), ok bool)
	ReadTimeout, WriteTimeout, QueryTimeout time.Duration
	MaxResponseBytes                        int64
	MaxInFlight                             int
}

type Handler struct {
	c        Config
	inFlight chan struct{}
	closed   atomic.Bool
}

func (h *Handler) CloseAdmission() { h.closed.Store(true) }

// New requires already constructed, mutually consistent trusted components.
// Mount directly, without ServeMux path cleaning, redirects, retries or CORS.
func New(c Config) (*Handler, error) {
	if c.Core == nil || c.Gate == nil || c.Authority == nil || c.Ledger == nil || c.ReserveRecovery == nil ||
		c.ReadTimeout <= 0 || c.WriteTimeout <= 0 || c.QueryTimeout <= 0 || c.MaxResponseBytes < modelprotocol.EventLimit || c.MaxInFlight < 1 || c.MaxInFlight > 1024 {
		return nil, errors.New("common.validation_failed")
	}
	return &Handler{c: c, inFlight: make(chan struct{}, c.MaxInFlight)}, nil
}

func rawRoute(r *http.Request) (string, bool) {
	path, _, _ := strings.Cut(r.RequestURI, "?")
	if r.URL == nil || r.URL.IsAbs() || r.URL.Host != "" || r.URL.Fragment != "" || r.URL.Opaque != "" ||
		strings.ContainsAny(path, "%\\#") || path != r.URL.Path || r.URL.RawPath != "" {
		return "", false
	}
	return path, (r.Method == "GET" && (path == modelsPath || path == usagePath)) || (r.Method == "POST" && path == chatPath)
}

func query(r *http.Request, path string) (int, string, error) {
	limit := 50
	if len(r.URL.RawQuery) > 4096 {
		return 0, "", errors.New("common.validation_failed")
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, "", errors.New("common.validation_failed")
	}
	for k, v := range q {
		if path != usagePath || len(v) != 1 || (k != "limit" && k != "cursor") {
			return 0, "", errors.New("common.validation_failed")
		}
		if k == "limit" {
			if v[0] == "" || strings.Trim(v[0], "0123456789") != "" {
				return 0, "", errors.New("common.validation_failed")
			}
			limit, err = strconv.Atoi(v[0])
			if err != nil || limit < 1 || limit > 200 {
				return 0, "", errors.New("common.validation_failed")
			}
		}
		if k == "cursor" && (len(v[0]) > 512 || !utf8.ValidString(v[0])) {
			return 0, "", errors.New("common.validation_failed")
		}
	}
	return limit, q.Get("cursor"), nil
}

// Inspect all keys, including noncanonical keys from in-process wrappers.
func values(h http.Header, name string) []string {
	var out []string
	for k, v := range h {
		if strings.EqualFold(k, name) {
			out = append(out, v...)
		}
	}
	return out
}

func bearer(r *http.Request) (string, error) {
	for k := range r.Header {
		lower := strings.ToLower(k)
		if lower == "origin" || lower == "cookie" || lower == "cookie2" || lower == "referer" || lower == "sec-fetch-site" || lower == "sec-fetch-dest" || lower == "sec-fetch-user" || strings.HasPrefix(lower, "access-control-") || strings.HasPrefix(lower, "auriga-forwarded-") {
			return "", errors.New("common.forbidden")
		}
	}
	v := values(r.Header, "Authorization")
	if len(v) != 1 || !strings.HasPrefix(v[0], "Bearer ") {
		return "", errors.New("common.unauthenticated")
	}
	raw := strings.TrimPrefix(v[0], "Bearer ")
	if len(raw) == 0 || len(raw) > 16384 || strings.ContainsAny(raw, " ,\t\r\n") {
		return "", errors.New("common.unauthenticated")
	}
	return raw, nil
}

func postHeaders(r *http.Request) (string, error) {
	v := values(r.Header, "Content-Type")
	if len(v) != 1 {
		return "", errors.New("common.validation_failed")
	}
	typ, params, err := mime.ParseMediaType(v[0])
	if err != nil || typ != "application/json" || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) || len(values(r.Header, "Content-Encoding")) != 0 {
		return "", errors.New("common.validation_failed")
	}
	v = values(r.Header, "Idempotency-Key")
	if len(v) > 1 || (len(v) == 1 && (!utf8.ValidString(v[0]) || utf8.RuneCountInString(v[0]) > 128 || strings.ContainsAny(v[0], ",\r\n"))) {
		return "", errors.New("common.validation_failed")
	}
	if len(v) == 1 {
		return v[0], nil
	}
	return "", nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := rand.Text()
	w.Header().Set("X-Request-Id", id)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s := response{w: w, rc: http.NewResponseController(w), timeout: h.c.WriteTimeout, max: h.c.MaxResponseBytes, language: r.Header.Get("Accept-Language")}
	// net/http needs these real deadlines, including rejected request-body drain.
	readErr := s.rc.SetReadDeadline(time.Now().Add(h.c.ReadTimeout))
	writeErr := s.rc.SetWriteDeadline(time.Now().Add(h.c.WriteTimeout))
	if readErr != nil || writeErr != nil {
		// An embedding middleware that hides deadline support cannot safely
		// serve this surface. Abort rather than start an unbounded error write.
		panic(http.ErrAbortHandler)
	}
	path, ok := rawRoute(r)
	problem := path == usagePath || strings.HasPrefix(r.RequestURI, "/api/")
	s.problem = problem
	fail := func(err error) {
		w.Header().Set("Connection", "close")
		_ = s.rc.SetReadDeadline(time.Now())
		s.failure(r.Context(), id, problem, err)
	}
	if h.closed.Load() {
		fail(errors.New("ai.budget_unavailable"))
		return
	}
	select {
	case h.inFlight <- struct{}{}:
		defer func() { <-h.inFlight }()
	default:
		fail(errors.New("common.rate_limited"))
		return
	}
	if !ok {
		fail(errors.New("common.not_found"))
		return
	}
	limit, cursor, err := query(r, path)
	if err != nil {
		fail(err)
		return
	}
	raw, err := bearer(r)
	if err != nil {
		fail(err)
		return
	}
	if path == chatPath {
		key, e := postHeaders(r)
		if e != nil {
			fail(e)
			return
		}
		if r.ContentLength > modelprotocol.RequestLimit {
			fail(errors.New("common.validation_failed"))
			return
		}
		stop := interrupt(r.Context(), func() { _ = s.rc.SetReadDeadline(time.Now()) })
		body, e := io.ReadAll(io.LimitReader(r.Body, modelprotocol.RequestLimit+1))
		stop()
		if e != nil || len(body) > modelprotocol.RequestLimit || r.Context().Err() != nil {
			fail(errors.New("common.validation_failed"))
			return
		}
		_ = s.rc.SetReadDeadline(time.Time{})
		finish, accepted := h.c.ReserveRecovery()
		if !accepted || finish == nil {
			fail(errors.New("ai.budget_unavailable"))
			return
		}
		out, e := h.execute(r.Context(), raw, body, key, s.sink, finish)
		if out.Ref.RequestID != "" {
			id = out.Ref.RequestID
			if !s.committed {
				w.Header().Set("X-Request-Id", id)
			}
		}
		if e != nil {
			s.failure(r.Context(), id, false, e)
			return
		}
		if s.committed {
			return
		}
		if out.Result.Completion == nil {
			s.failure(r.Context(), id, false, errors.New("common.internal"))
			return
		}
		s.json(r.Context(), 200, "application/json", out.Result.Completion)
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		fail(errors.New("common.validation_failed"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.c.QueryTimeout)
	defer cancel()
	if path == modelsPath {
		page, e := h.c.Gate.Catalog(ctx, raw)
		if e != nil {
			fail(e)
			return
		}
		s.json(ctx, 200, "application/json", page)
		return
	}
	identity, e := h.c.Authority.Check(ctx, raw)
	if e != nil {
		fail(e)
		return
	}
	ctx, until := context.WithDeadline(ctx, identity.ExpiresAt())
	defer until()
	page, e := h.c.Ledger.ListUsage(ctx, identity.Tenant(), identity.Account(), limit, cursor)
	if e != nil {
		fail(e)
		return
	}
	if ctx.Err() != nil || !identity.ExpiresAt().After(time.Now()) {
		fail(errors.New("common.unauthenticated"))
		return
	}
	s.json(ctx, 200, "application/json", page)
}

func (h *Handler) execute(ctx context.Context, raw string, body []byte, key string, sink modelprotocol.Sink, finish func(*Pending)) (out relay.Output, err error) {
	returned := false
	defer func() {
		if !returned {
			// A panic may follow dispatch; its missing Ref proves nothing. Keep
			// the reserved cell faulted and let the original panic propagate.
			finish(&Pending{Faulted: true})
		}
	}()
	out, err = h.c.Core.Execute(ctx, raw, bytes.NewReader(body), key, sink)
	returned = true
	if out.Pending && out.PendingRef != (budget.Ref{}) {
		finish(&Pending{Ref: out.PendingRef, StopProof: out.StopProof})
	} else {
		// Admit assigns result only after INSERT succeeds, before COMMIT. On
		// error that result always supplies PendingRef; an empty Ref on normal
		// return therefore means this admission never committed a durable row.
		// Nonempty Ref (including not-found/COMMIT ambiguity) is never released here.
		finish(nil)
	}
	return out, err
}
