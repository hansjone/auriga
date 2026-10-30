// Package litellm forwards authenticated member traffic to the installation's
// LiteLLM proxy. LiteLLM alone owns models, virtual keys and spend accounting.
package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

type Principal struct {
	Tenant, Account, Device string
	Expires                 time.Time
}
type Authenticate func(context.Context, string) (Principal, error)
type Handler struct {
	target       *url.URL
	keyFile      string
	authenticate Authenticate
	transport    *http.Transport
}

func New(origin, keyFile string, authenticate Authenticate) (*Handler, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(origin, "#") || authenticate == nil || keyFile == "" {
		return nil, errors.New("invalid LiteLLM configuration")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || u.Hostname() != "litellm" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("LiteLLM requires HTTPS or private deployment service")
		}
	}
	return &Handler{target: u, keyFile: keyFile, authenticate: authenticate, transport: &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 60 * time.Second, IdleConnTimeout: 60 * time.Second, MaxIdleConnsPerHost: 32,
	}}, nil
}
func (h *Handler) Close() { h.transport.CloseIdleConnections() }

func reject(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "code": code, "title": http.StatusText(status)})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := ""
	switch r.URL.Path {
	case "/ai/v1/models":
		method = http.MethodGet
	case "/ai/v1/chat/completions":
		method = http.MethodPost
	default:
		reject(w, 404, "common.not_found")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		reject(w, 405, "common.validation_failed")
		return
	}
	if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		reject(w, 400, "common.validation_failed")
		return
	}
	for name := range r.Header {
		n := strings.ToLower(name)
		if n == "cookie" || n == "origin" || n == "referer" || strings.HasPrefix(n, "auriga-forwarded-") {
			reject(w, 403, "common.forbidden")
			return
		}
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") {
		reject(w, 401, "common.unauthenticated")
		return
	}
	raw := strings.TrimPrefix(auth[0], "Bearer ")
	if raw == "" || len(raw) > 16384 || strings.ContainsAny(raw, " ,\t\r\n") {
		reject(w, 401, "common.unauthenticated")
		return
	}
	checkCtx, stop := context.WithTimeout(r.Context(), 10*time.Second)
	actor, err := h.authenticate(checkCtx, raw)
	stop()
	if err != nil {
		status, code := 503, "common.unavailable"
		if err.Error() == "common.unauthenticated" {
			status, code = 401, "common.unauthenticated"
		}
		if err.Error() == "common.forbidden" {
			status, code = 403, "common.forbidden"
		}
		reject(w, status, code)
		return
	}
	if actor.Tenant == "" || actor.Account == "" || actor.Device == "" || !time.Now().Before(actor.Expires) {
		reject(w, 401, "common.unauthenticated")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), actor.Expires)
	defer cancel()
	r = r.Clone(ctx)
	if method == http.MethodPost {
		var fields map[string]json.RawMessage
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if decoder.Decode(&fields) != nil || fields == nil || decoder.Decode(new(any)) != io.EOF {
			reject(w, 400, "common.validation_failed")
			return
		}
		// Accept OpenAI chat parameters only; provider URLs, credentials, hooks
		// and LiteLLM-specific routing controls belong to the administrator.
		allowed := map[string]bool{}
		for _, name := range []string{"model", "messages", "stream", "stream_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "tools", "tool_choice", "response_format", "seed", "stop", "n", "presence_penalty", "frequency_penalty", "logit_bias", "logprobs", "top_logprobs", "parallel_tool_calls", "reasoning_effort", "service_tier", "user", "metadata"} {
			allowed[name] = true
		}
		for name := range fields {
			if !allowed[name] {
				reject(w, 400, "common.validation_failed")
				return
			}
		}
		fields["user"], _ = json.Marshal(actor.Tenant + ":" + actor.Account)
		fields["metadata"], _ = json.Marshal(map[string]string{"tenant_id": actor.Tenant, "account_id": actor.Account, "device_id": actor.Device})
		body, e := json.Marshal(fields)
		if e != nil {
			reject(w, 400, "common.validation_failed")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.GetBody = nil
	} else {
		r.Body = http.NoBody
		r.ContentLength = 0
	}
	secret, err := os.ReadFile(h.keyFile)
	key := strings.TrimSpace(string(secret))
	clear(secret)
	if err != nil || !strings.HasPrefix(key, "sk-") || len(key) > 16384 || strings.ContainsAny(key, " \r\n\t") {
		reject(w, 503, "common.unavailable")
		return
	}
	proxy := &httputil.ReverseProxy{Transport: h.transport, FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(h.target)
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/ai")
			pr.Out.URL.RawPath = ""
			pr.Out.Host = h.target.Host
			// Forward only protocol headers and trace context. No caller-selected key,
			// LiteLLM administration headers or platform credentials cross this hop.
			pr.Out.Header = make(http.Header)
			for _, name := range []string{"Accept", "X-Request-Id", "traceparent", "tracestate"} {
				if value := pr.In.Header.Get(name); value != "" {
					pr.Out.Header.Set(name, value)
				}
			}
			pr.Out.Header.Set("Content-Type", "application/json")
			pr.Out.Header.Set("Authorization", "Bearer "+key)
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return errors.New("LiteLLM redirect rejected")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { reject(w, 502, "common.unavailable") },
	}
	proxy.ServeHTTP(w, r)
}
