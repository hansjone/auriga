package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hansjone/auriga/infra/gen/deployment"
)

type LiteLLMUIConfig struct {
	Origin          deployment.Origin   `json:"origin"`
	Listener        deployment.Listener `json:"listener"`
	Login           *LiteLLMUILogin     `json:"login,omitempty"`
	ExternalOrigins map[string]string   `json:"externalOrigins,omitempty"`
}

func (c Config) validateLiteLLMUI() error {
	invalid := fmt.Errorf("LiteLLM UI requires a separate browser origin and listener")
	if c.LiteLLMOrigin == "" {
		if c.LiteLLMUI != nil {
			return invalid
		}
		return nil
	}
	if !c.Access.Valid() || c.LiteLLMUI == nil {
		return invalid
	}
	u := c.LiteLLMUI
	host, port, err := parseAuthority(originAuthority(u.Origin), u.Origin.Scheme)
	if err != nil || host != u.Origin.Host || port != int(u.Origin.Port) || u.Origin.Scheme != c.Access.AdminOrigin().Scheme {
		return invalid
	}
	url := c.liteLLMUIURL()
	if url == c.Access.AdminURL() || url == c.Access.PortalURL() || url == c.Access.Issuer() || u.Origin.Host == c.Access.InstanceControl().DnsAlias {
		return invalid
	}
	if c.Access.Mode() == "ip" && u.Origin.Host != c.Access.AdminOrigin().Host {
		return invalid
	}
	if parent := c.Access.InstanceParentDomain(); parent != "" && (host == parent || strings.HasSuffix(host, "."+parent)) {
		return invalid
	}
	ip := net.ParseIP(u.Listener.Address)
	if ip == nil || ip.To4() == nil || u.Listener.Port < 1 || u.Listener.Port > 65535 || u.Listener.Tls != c.Access.Listeners().Admin.Tls {
		return invalid
	}
	listeners := c.Access.Listeners()
	for _, other := range []deployment.Listener{listeners.Portal, listeners.Admin, listeners.InstanceControl} {
		if other.Port == u.Listener.Port {
			return invalid
		}
	}
	if c.Access.IngressKind() == "direct" && (u.Listener.Port != u.Origin.Port || u.Listener.Tls != (u.Origin.Scheme == "https")) {
		return invalid
	}
	if u.Login != nil && (u.Login.PasswordFile == "" || u.Login.KeyInfoKeyFile == "") {
		return fmt.Errorf("LiteLLM UI passwordFile and keyInfoKeyFile are required")
	}
	return c.validateLiteLLMExternalOrigins()
}

func (c Config) liteLLMUIURL() string {
	if c.LiteLLMUI == nil {
		return ""
	}
	return c.LiteLLMUI.Origin.Scheme + "://" + originAuthority(c.LiteLLMUI.Origin)
}

type liteLLMHandoff struct {
	session string
	expires time.Time
	target  string
}

type liteLLMHandoffs struct {
	mu     sync.Mutex
	grants map[[32]byte]liteLLMHandoff
}

func (g *Gateway) checkLiteLLMAdministrator(r *http.Request, raw string) (int, string) {
	if raw == "" {
		return 401, "common.unauthenticated"
	}
	if g.SessionRPC == nil {
		return 503, "common.unavailable"
	}
	sess, err := g.browserSession(r, raw, "admin", r.Method == http.MethodGet)
	if err != nil {
		if errors.Is(err, errIAMUnauthenticated) || errors.Is(err, ErrSessionNotFound) {
			return 401, "common.unauthenticated"
		}
		return 503, "common.unavailable"
	}
	response, err := g.SessionRPC.IntrospectSession(r.Context(), raw, "admin", g.Cfg.Access.TenantID())
	if err != nil {
		return 503, "common.unavailable"
	}
	fresh, err := introspectedSession(response, "admin", g.Cfg.Access.TenantID(), g.Now().UTC())
	if err != nil || !sameSessionBinding(sess, fresh) {
		if errors.Is(err, errIAMUnauthenticated) {
			return 401, "common.unauthenticated"
		}
		return 503, "common.unavailable"
	}
	if !liteLLMAdministrator(response.AccessToken, fresh) {
		return 403, "common.forbidden"
	}
	return 0, ""
}

func (g *Gateway) issueLiteLLMHandoff(raw string, targets ...string) (string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(entropy[:])
	state := &g.litellmHandoffs
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.grants == nil {
		state.grants = make(map[[32]byte]liteLLMHandoff)
	}
	now := g.Now().UTC()
	for key, grant := range state.grants {
		if !now.Before(grant.expires) {
			delete(state.grants, key)
		}
	}
	if len(state.grants) >= 1024 {
		return "", fmt.Errorf("LiteLLM handoff capacity exhausted")
	}
	target := g.Cfg.liteLLMUIURL()
	if len(targets) == 1 {
		target = targets[0]
	}
	state.grants[sha256.Sum256([]byte(code))] = liteLLMHandoff{raw, now.Add(time.Minute), target}
	return code, nil
}

func (g *Gateway) consumeLiteLLMHandoff(code string, targets ...string) string {
	if len(code) != 43 {
		return ""
	}
	state := &g.litellmHandoffs
	state.mu.Lock()
	defer state.mu.Unlock()
	key := sha256.Sum256([]byte(code))
	grant, exists := state.grants[key]
	delete(state.grants, key)
	target := g.Cfg.liteLLMUIURL()
	if len(targets) == 1 {
		target = targets[0]
	}
	if !exists || !g.Now().UTC().Before(grant.expires) || grant.target != target {
		return ""
	}
	return grant.session
}

var liteLLMHandoffPage = template.Must(template.New("litellm-handoff").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>LiteLLM</title><body><form method="post" action="{{.Action}}"><input type="hidden" name="code" value="{{.Code}}"><button type="submit">打开 LiteLLM</button></form><script nonce="{{.Nonce}}">document.forms[0].submit()</script></body></html>`))

func (g *Gateway) startLiteLLMHandoff(w http.ResponseWriter, r *http.Request, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "LiteLLM handoff rejected", rid))
	}
	raw, valid := singleCookie(r, cookieName(g.Cfg.Access.AdminOrigin().Scheme == "https", sessionCookieBase("admin")))
	if !valid {
		fail(401, "common.unauthenticated")
		return
	}
	if status, code := g.checkLiteLLMAdministrator(r, raw); status != 0 {
		fail(status, code)
		return
	}
	target := g.liteLLMHandoffTarget(r)
	if target == "" {
		fail(403, "common.forbidden")
		return
	}
	code, err := g.issueLiteLLMHandoff(raw, target)
	if err != nil {
		fail(503, "common.unavailable")
		return
	}
	hash := sha256.Sum256([]byte(code))
	nonce := base64.RawURLEncoding.EncodeToString(hash[:16])
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// POST navigation needs a non-opaque Origin for the strict handoff check.
	// Send only the admin origin as Referer; the code remains in the form body.
	w.Header().Set("Referrer-Policy", "strict-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; form-action "+target+"; frame-ancestors 'none'; base-uri 'none'")
	if err := liteLLMHandoffPage.Execute(w, struct{ Action, Code, Nonce string }{target + "/ai/session", code, nonce}); err != nil && g.Log != nil {
		g.Log.Warn("LiteLLM handoff response interrupted")
	}
}

func (g *Gateway) liteLLMHandoffOrigin(r *http.Request) bool {
	target := g.liteLLMPresentationOrigin(r)
	if target == "" {
		return false
	}
	if target != g.Cfg.liteLLMUIURL() {
		if len(r.Header.Values("Origin")) != 1 {
			return false
		}
		return g.Cfg.LiteLLMUI.ExternalOrigins[r.Header.Get("Origin")] == target
	}
	o := g.Cfg.Access.AdminOrigin()
	return strictOrigin(r, accessEntry{role: "admin", scheme: o.Scheme, host: o.Host, port: int(o.Port)})
}

func (g *Gateway) finishLiteLLMHandoff(w http.ResponseWriter, r *http.Request, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "LiteLLM handoff rejected", rid))
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.URL.ForceQuery || !g.liteLLMHandoffOrigin(r) || err != nil || mediaType != "application/x-www-form-urlencoded" {
		fail(403, "common.forbidden")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || len(r.PostForm) != 1 || len(r.PostForm["code"]) != 1 {
		fail(400, "common.validation_failed")
		return
	}
	raw := g.consumeLiteLLMHandoff(r.PostForm.Get("code"), g.liteLLMPresentationOrigin(r))
	if status, code := g.checkLiteLLMAdministrator(r, raw); status != 0 {
		fail(status, code)
		return
	}
	token, err := g.loginLiteLLMUI(r)
	if err != nil {
		fail(503, "common.unavailable")
		return
	}
	// Recheck after the exchange: a concurrent logout must not establish a new gate.
	if status, code := g.checkLiteLLMAdministrator(r, raw); status != 0 {
		fail(status, code)
		return
	}
	secure := strings.HasPrefix(g.liteLLMPresentationOrigin(r), "https://")
	http.SetCookie(w, &http.Cookie{Name: cookieName(secure, "auriga_litellm_session"), Value: raw, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	if token != "" {
		http.SetCookie(w, &http.Cookie{Name: "token", Value: token, Path: "/ai", Secure: secure, SameSite: http.SameSiteStrictMode})
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/ai/ui/", http.StatusSeeOther)
}
