package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	if strings.HasPrefix(r.URL.Path, "/ai/v1/") && !dataPath && audience != "admin" {
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
	} else {
		if audience != "admin" || entry.role != "admin" {
			fail(403, "common.forbidden")
			return
		}
		raw, valid := singleCookie(r, cookieName(entry.scheme == "https", sessionCookieBase("admin")))
		if !valid {
			fail(401, "common.unauthenticated")
			return
		}
		if g.SessionRPC == nil {
			fail(503, "common.unavailable")
			return
		}
		sess, err := g.browserSession(r, raw, "admin", r.Method == http.MethodGet)
		if err != nil {
			if errors.Is(err, errIAMUnauthenticated) || errors.Is(err, ErrSessionNotFound) {
				fail(401, "common.unauthenticated")
			} else {
				fail(503, "common.unavailable")
			}
			return
		}
		// Every UI/API request checks the authoritative session, including revocation.
		response, err := g.SessionRPC.IntrospectSession(r.Context(), raw, "admin", g.Cfg.Access.TenantID())
		if err != nil {
			fail(503, "common.unavailable")
			return
		}
		fresh, err := introspectedSession(response, "admin", g.Cfg.Access.TenantID(), g.Now().UTC())
		if err != nil || !sameSessionBinding(sess, fresh) {
			if errors.Is(err, errIAMUnauthenticated) {
				fail(401, "common.unauthenticated")
			} else {
				fail(503, "common.unavailable")
			}
			return
		}
		if !liteLLMAdministrator(response.AccessToken, fresh) {
			fail(403, "common.forbidden")
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
			pr.Out.Header.Del("Cookie")
		} else {
			pr.Out.Header.Set("Cookie", stripAurigaCookies(pr.In.Header.Get("Cookie"), true))
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
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
				if location.Scheme+"://"+location.Host == g.Cfg.Access.AdminURL() {
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
