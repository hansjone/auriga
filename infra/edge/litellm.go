package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"
)

// token comes exclusively from the authenticated IAM IntrospectSession RPC,
// never from request headers or a client-decoded JWT. IAM resolves org
// permissions when issuing it; an admin-audience login alone grants no rights.
func liteLLMAdministrator(token string, session *Session) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 16384 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Context     string   `json:"ctx"`
		Tenant      string   `json:"tid"`
		Subject     string   `json:"sub"`
		Permissions []string `json:"perms"`
	}
	return json.Unmarshal(raw, &claims) == nil && claims.Context == "admin" && claims.Tenant == session.TenantID && claims.Subject == session.AccountID && slices.Contains(claims.Permissions, "ai.providers.write")
}

func (c Config) parseLiteLLMOrigin() (*url.URL, error) {
	if c.LiteLLMOrigin == "" {
		return nil, nil
	}
	u, err := url.Parse(c.LiteLLMOrigin)
	if err != nil || !c.Access.Valid() || u.Host == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(c.LiteLLMOrigin, "#") {
		return nil, fmt.Errorf("invalid LiteLLM origin")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || u.Hostname() != "litellm" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("LiteLLM origin requires HTTPS or private deployment service")
		}
	}
	return u, nil
}

var liteLLMTransport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 60 * time.Second, IdleConnTimeout: 60 * time.Second, MaxIdleConnsPerHost: 32}

func liteLLMCookieAllowed(name string) bool {
	return name == "token" || name == "litellm_cp_return_to" || name == "litellm_oauth_state"
}

func liteLLMCookies(header string) string {
	r := &http.Request{Header: http.Header{"Cookie": {header}}}
	var kept []string
	for _, cookie := range r.Cookies() {
		if liteLLMCookieAllowed(cookie.Name) {
			kept = append(kept, cookie.String())
		}
	}
	return strings.Join(kept, "; ")
}

func (g *Gateway) proxyLiteLLM(w http.ResponseWriter, r *http.Request, audience, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "LiteLLM request rejected", rid))
	}
	entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry)
	if !ok || !g.Cfg.Access.Valid() {
		fail(403, "common.forbidden")
		return
	}
	if r.URL.RawPath != "" || r.URL.EscapedPath() != r.URL.Path {
		fail(400, "common.validation_failed")
		return
	}
	dataPath := r.URL.Path == "/ai/v1/models" || r.URL.Path == "/ai/v1/chat/completions"
	data := dataPath && audience == "portal"
	if strings.HasPrefix(r.URL.Path, "/ai/v1/") && !dataPath && audience != "relayAdmin" {
		fail(404, "common.not_found")
		return
	}
	if data {
		if audience != "portal" || entry.role != "portal" {
			fail(403, "common.forbidden")
			return
		}
		method := http.MethodPost
		if r.URL.Path == "/ai/v1/models" {
			method = http.MethodGet
		}
		if r.Method != method {
			w.Header().Set("Allow", method)
			fail(405, "common.validation_failed")
			return
		}
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer sk-") || len(auth[0]) <= 10 || len(auth[0]) > 16384 || strings.ContainsAny(strings.TrimPrefix(auth[0], "Bearer "), " ,\r\n\t") {
			fail(401, "common.unauthenticated")
			return
		}
		if len(r.Header.Values("Origin")) != 0 {
			fail(403, "common.forbidden")
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			fail(400, "common.validation_failed")
			return
		}
		if method == http.MethodPost {
			var fields map[string]json.RawMessage
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAPIBodyBytes))
			if decoder.Decode(&fields) != nil || fields == nil || decoder.Decode(new(any)) != io.EOF {
				fail(400, "common.validation_failed")
				return
			}
			allowed := map[string]bool{}
			for _, name := range []string{"model", "messages", "stream", "stream_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "tools", "tool_choice", "response_format", "seed", "stop", "n", "presence_penalty", "frequency_penalty", "logit_bias", "logprobs", "top_logprobs", "parallel_tool_calls", "reasoning_effort", "service_tier", "user", "metadata"} {
				allowed[name] = true
			}
			for name := range fields {
				if !allowed[name] {
					fail(400, "common.validation_failed")
					return
				}
			}
			// External callers must never charge a tenant:account end-user budget.
			fingerprint := sha256.Sum256([]byte(strings.TrimPrefix(auth[0], "Bearer ")))
			fields["user"], _ = json.Marshal(fmt.Sprintf("external:%x", fingerprint))
			fields["metadata"] = json.RawMessage(`{"source":"external"}`)
			body, err := json.Marshal(fields)
			if err != nil {
				fail(400, "common.validation_failed")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength, r.GetBody = int64(len(body)), nil
		} else {
			r.Body, r.ContentLength = http.NoBody, 0
		}
	} else {
		if audience == "admin" && entry.role == "admin" {
			if r.Method == http.MethodGet && r.URL.RawQuery == "" && !r.URL.ForceQuery && (r.URL.Path == "/ai/ui" || r.URL.Path == "/ai/ui/") {
				g.startLiteLLMHandoff(w, r, rid)
			} else {
				fail(403, "common.forbidden")
			}
			return
		}
		if audience != "relayAdmin" || entry.role != "relayAdmin" || g.Cfg.LiteLLMUI == nil || !strings.HasPrefix(r.URL.Path, "/ai/") {
			fail(403, "common.forbidden")
			return
		}
		if r.URL.Path == "/ai/session" {
			g.finishLiteLLMHandoff(w, r, rid)
			return
		}
		if r.Method == http.MethodOptions {
			fail(405, "common.forbidden")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && r.Header.Get("Sec-Fetch-Mode") != "navigate" {
			fail(403, "common.forbidden")
			return
		}
		raw, valid := singleCookie(r, cookieName(entry.scheme == "https", "auriga_litellm_session"))
		if !valid {
			fail(401, "common.unauthenticated")
			return
		}
		if status, code := g.checkLiteLLMAdministrator(r, raw); status != 0 {
			fail(status, code)
			return
		}
		// LiteLLM UI owns its own login token. Requiring exact Origin for writes
		// protects the additional platform-session gate without injecting a master key.
		if isWriteMethod(r.Method) && !strictOrigin(r, entry) {
			fail(403, "common.forbidden")
			return
		}
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
	}
	proxy := newReverseProxy(g.litellmURL, liteLLMTransport, false)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		// The deployment sets SERVER_ROOT_PATH=/ai. Preserve it in the ASGI
		// request path so mounted UI/static routes resolve their nested root_path.
		pr.Out.URL.Path = pr.In.URL.Path
		pr.Out.URL.RawPath = ""
		stripUntrustedEntryHeaders(pr.Out.Header)
		for name := range pr.Out.Header {
			if strings.HasPrefix(strings.ToLower(name), "auriga-") || strings.HasPrefix(strings.ToLower(name), "x-auriga-") {
				pr.Out.Header.Del(name)
			}
		}
		pr.Out.Header.Set("X-Request-Id", rid)
		pr.Out.Header.Set("traceparent", ensureTraceparent(r))
		if data {
			pr.Out.Header = make(http.Header)
			for _, name := range []string{"Accept", "Authorization"} {
				if value := pr.In.Header.Get(name); value != "" {
					pr.Out.Header.Set(name, value)
				}
			}
			pr.Out.Header.Set("Content-Type", "application/json")
			pr.Out.Header.Set("X-Request-Id", rid)
			pr.Out.Header.Set("traceparent", ensureTraceparent(r))
		} else {
			pr.Out.Header.Set("Cookie", liteLLMCookies(pr.In.Header.Get("Cookie")))
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		for name := range resp.Header {
			if strings.HasPrefix(strings.ToLower(name), "access-control-") {
				resp.Header.Del(name)
			}
		}
		resp.Header.Set("Cache-Control", "no-store")
		resp.Header.Set("X-Content-Type-Options", "nosniff")
		if !data {
			cookies := resp.Cookies()
			resp.Header.Del("Set-Cookie")
			for _, cookie := range cookies {
				if liteLLMCookieAllowed(cookie.Name) {
					cookie.Domain, cookie.Path = "", "/ai"
					cookie.Secure = entry.scheme == "https"
					cookie.SameSite = http.SameSiteStrictMode
					resp.Header.Add("Set-Cookie", cookie.String())
				}
			}
		}
		if data {
			resp.Header.Del("Set-Cookie")
			if resp.StatusCode >= 300 && resp.StatusCode < 400 {
				return errors.New("LiteLLM data redirect rejected")
			}
		}
		if !data && resp.Header.Get("Location") != "" {
			location, err := url.Parse(resp.Header.Get("Location"))
			if err != nil || location.User != nil {
				return errors.New("invalid LiteLLM redirect")
			}
			if location.IsAbs() {
				if location.Scheme+"://"+location.Host == g.Cfg.liteLLMUIURL() {
					return nil
				}
				if location.Scheme != g.litellmURL.Scheme || location.Host != g.litellmURL.Host {
					return errors.New("external LiteLLM redirect rejected")
				}
				location.Scheme, location.Host = "", ""
			}
			if location.Host != "" {
				return errors.New("external LiteLLM redirect rejected")
			}
			if strings.HasPrefix(location.Path, "/") && location.Path != "/ai" && !strings.HasPrefix(location.Path, "/ai/") {
				location.Path = "/ai" + location.Path
			}
			resp.Header.Set("Location", location.String())
		}
		return nil
	}
	proxy.ServeHTTP(w, r)
}
