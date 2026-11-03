package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

const relayAdminPrefix = "/api/ai/v1/admin/relay"

var relayManagementPath = regexp.MustCompile(`^/(providers(?:/[a-zA-Z0-9_-]+(?:/(?:keys|models))?)?|pools|virtual-keys(?:/[a-zA-Z0-9_-]+)?|logs)$`)

// The JWT is returned by authenticated IAM introspection on this request.
// It is never taken from a caller Bearer header or a cached allow decision.
func relayAdministrator(token string, session *Session, permission string) bool {
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
	return json.Unmarshal(raw, &claims) == nil && claims.Context == "admin" && claims.Tenant == session.TenantID && claims.Subject == session.AccountID && slices.Contains(claims.Permissions, permission)
}

func (g *Gateway) proxyRelayAdmin(w http.ResponseWriter, r *http.Request, audience, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "relay administration rejected", rid))
	}
	entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry)
	if !ok || audience != "admin" || entry.role != "admin" || !g.Cfg.Access.Valid() {
		fail(403, "common.forbidden")
		return
	}
	if g.relayURL == nil || g.SessionRPC == nil {
		fail(503, "common.unavailable")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, relayAdminPrefix)
	if r.URL.RawPath != "" || !relayManagementPath.MatchString(suffix) {
		fail(404, "common.not_found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete {
		fail(405, "common.validation_failed")
		return
	}
	if strings.HasSuffix(suffix, "/models") && r.Method != http.MethodGet {
		fail(405, "common.validation_failed")
		return
	}
	if strings.HasPrefix(suffix, "/logs") && r.Method != http.MethodGet {
		fail(405, "common.validation_failed")
		return
	}
	if isWriteMethod(r.Method) && !strictOrigin(r, entry) {
		fail(403, "common.forbidden")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		fail(403, "common.forbidden")
		return
	}
	if len(r.Header.Values("Origin")) > 0 && !strictOrigin(r, entry) {
		fail(403, "common.forbidden")
		return
	}
	if r.Header.Get("Authorization") != "" {
		fail(403, "common.forbidden")
		return
	}
	raw, valid := singleCookie(r, cookieName(entry.scheme == "https", sessionCookieBase("admin")))
	if !valid {
		fail(401, "common.unauthenticated")
		return
	}
	original, err := g.browserSession(r, raw, "admin", r.Method == http.MethodGet)
	if err != nil {
		if errors.Is(err, errIAMUnauthenticated) || errors.Is(err, ErrSessionNotFound) {
			fail(401, "common.unauthenticated")
		} else {
			fail(503, "common.unavailable")
		}
		return
	}
	response, err := g.SessionRPC.IntrospectSession(r.Context(), raw, "admin", g.Cfg.Access.TenantID())
	if err != nil {
		fail(503, "common.unavailable")
		return
	}
	fresh, err := introspectedSession(response, "admin", g.Cfg.Access.TenantID(), g.Now().UTC())
	if err != nil || !sameSessionBinding(original, fresh) {
		if errors.Is(err, errIAMUnauthenticated) {
			fail(401, "common.unauthenticated")
		} else {
			fail(503, "common.unavailable")
		}
		return
	}
	// Introspection carries current authority, but deliberately has no CSRF
	// material. Keep only the matching browserSession projection's binding.
	fresh.CSRFHash = original.CSRFHash
	permission := "ai.providers.write"
	if r.Method == http.MethodGet {
		permission = "ai.providers.read"
	}
	if suffix == "/pools" {
		if r.Method == http.MethodGet {
			permission = "ai.providers.read"
		} else {
			permission = "ai.providers.write"
		}
	}
	if strings.HasSuffix(suffix, "/keys") {
		permission = "ai.credentials.rotate"
	}
	if strings.HasPrefix(suffix, "/logs") {
		permission = "ai.usage.admin"
	}
	if !relayAdministrator(response.AccessToken, fresh, permission) {
		fail(403, "common.forbidden")
		return
	}
	if isWriteMethod(r.Method) || permission == "ai.usage.admin" {
		age := g.Now().UTC().Sub(fresh.StepUpAt)
		if fresh.StepUpAt.IsZero() || age < 0 || age > 10*time.Minute {
			fail(403, "iam.step_up_required")
			return
		}
	}
	if isWriteMethod(r.Method) {
		if len(r.Header.Values("X-Auriga-CSRF")) != 1 {
			fail(403, "common.forbidden")
			return
		}
		if g.Cfg.Access.Mode() == "ip" {
			if g.AdminCSRF == nil {
				fail(503, "common.unavailable")
				return
			}
			if err = g.AdminCSRF.Verify(r.Context(), raw, r.Header.Get("X-Auriga-CSRF"), fresh, g.Now().UTC()); err != nil {
				if errors.Is(err, errCSRF) {
					fail(403, "common.forbidden")
				} else if errors.Is(err, errIAMUnauthenticated) {
					fail(401, "common.unauthenticated")
				} else {
					fail(503, "common.unavailable")
				}
				return
			}
		} else {
			_, valid = singleCookie(r, cookieName(entry.scheme == "https", csrfCookieBase()))
			if !valid || g.checkCSRF(r, fresh) != nil {
				fail(403, "common.forbidden")
				return
			}
		}
	}
	password, err := os.ReadFile(g.Cfg.Relay.AdminPasswordFile)
	if err != nil {
		fail(503, "common.unavailable")
		return
	}
	secret := strings.TrimSpace(string(password))
	clear(password)
	if secret == "" || len(secret) > 1024 || strings.ContainsAny(secret, "\r\n") {
		fail(503, "common.unavailable")
		return
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
	}
	proxy := newReverseProxy(g.relayURL, relayTransport, false)
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(g.relayURL)
		pr.Out.URL.Path = "/api" + suffix
		pr.Out.URL.RawPath = ""
		pr.Out.Header = make(http.Header)
		pr.Out.SetBasicAuth("auriga", secret)
		pr.Out.Header.Set("Content-Type", "application/json")
		pr.Out.Header.Set("Accept", "application/json")
		pr.Out.Header.Set("X-Auriga-Tenant", fresh.TenantID)
		pr.Out.Header.Set("Idempotency-Key", r.Header.Get("Idempotency-Key"))
		pr.Out.Header.Set("X-Request-Id", rid)
		pr.Out.Header.Set("traceparent", ensureTraceparent(r))
	}
	proxy.ModifyResponse = relayResponse
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { fail(503, "common.unavailable") }
	proxy.ServeHTTP(w, r)
}
