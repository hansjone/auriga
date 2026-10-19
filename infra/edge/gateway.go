package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Gateway is the edge HTTP handler.
type Gateway struct {
	Cfg      Config
	Log      *slog.Logger
	Trusted  *TrustedProxies
	Sessions SessionStore
	Routes   RouteStore
	Owners   OwnerChecker
	Tickets  TicketStore
	Signer   IdentitySigner
	Limiter  *TokenBucketLimiter
	Now      func() time.Time

	iamURL       *url.URL
	orgURL       *url.URL
	workspaceURL *url.URL

	portalStatic http.Handler
	adminStatic  http.Handler
	panelStatic  http.Handler
}

func NewGateway(cfg Config, sessions SessionStore, routes RouteStore, tickets TicketStore, signer IdentitySigner, log *slog.Logger) (*Gateway, error) {
	trusted, err := NewTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, err
	}
	iamURL, err := parseUpstream(cfg.Upstreams["iam"])
	if err != nil {
		return nil, err
	}
	orgURL, err := parseUpstream(cfg.Upstreams["org"])
	if err != nil {
		return nil, err
	}
	wsURL, err := parseUpstream(cfg.Upstreams["workspace"])
	if err != nil {
		return nil, err
	}
	g := &Gateway{
		Cfg:          cfg,
		Log:          log,
		Trusted:      trusted,
		Sessions:     sessions,
		Routes:       routes,
		Owners:       RouteOwnerChecker{Routes: routes},
		Tickets:      tickets,
		Signer:       signer,
		Limiter:      NewTokenBucketLimiter(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Burst, cfg.RateLimit.PathPrefix),
		Now:          time.Now,
		iamURL:       iamURL,
		orgURL:       orgURL,
		workspaceURL: wsURL,
		portalStatic: newSPAFileServer(cfg.Static.PortalRoot),
		adminStatic:  newSPAFileServer(cfg.Static.AdminRoot),
		panelStatic:  newSPAFileServer(cfg.Static.PanelRoot),
	}
	return g, nil
}

func (g *Gateway) Handler() http.Handler {
	var h http.Handler = http.HandlerFunc(g.serveHTTP)
	h = g.Limiter.Middleware(h, func(r *http.Request) string {
		return ResolveClientIP(r, g.Trusted)
	})
	return h
}

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	rid, r := ensureRequestID(r)
	clientIP := ResolveClientIP(r, g.Trusted)
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyClientIP, clientIP))
	host := requestHost(r)
	path := r.URL.Path

	// Instance host: <instanceId>.<parent>
	if instID, ok := g.instanceIDFromHost(host); ok {
		g.serveInstance(w, r, instID, "", clientIP, rid)
		return
	}

	audience, knownHost := normalizeAudience(host, g.Cfg.PortalHost, g.Cfg.AdminHost)

	// Path-based instance: /i/<id>/...
	if strings.HasPrefix(path, "/i/") {
		rest := strings.TrimPrefix(path, "/i/")
		parts := strings.SplitN(rest, "/", 2)
		instID := parts[0]
		suffix := "/"
		if len(parts) == 2 {
			suffix = "/" + parts[1]
		}
		if instID == "" {
			writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "", rid))
			return
		}
		g.serveInstance(w, r, instID, suffix, clientIP, rid)
		return
	}

	// /open/<instanceId>
	if strings.HasPrefix(path, "/open/") {
		if !knownHost || audience != "portal" {
			writeProblem(w, problem(http.StatusForbidden, "edge.forbidden", "Forbidden", "open only on portal host", rid))
			return
		}
		g.handleOpen(w, r, strings.TrimPrefix(path, "/open/"), clientIP, rid)
		return
	}

	if !knownHost {
		writeProblem(w, problem(http.StatusNotFound, "edge.unknown_host", "Not Found", "unknown host", rid))
		return
	}

	// Admin API isolation
	if isAdminAPIPath(path) && audience != "admin" {
		writeProblem(w, problem(http.StatusForbidden, "edge.admin_host_required", "Forbidden", "admin API only on admin host", rid))
		return
	}

	// Platform API / oauth routes
	if upstream := g.matchUpstream(path); upstream != nil {
		g.proxyAPI(w, r, upstream, audience, clientIP, rid)
		return
	}

	// Panel static
	if strings.HasPrefix(path, "/panel/") || path == "/panel" {
		if g.Cfg.Static.PanelRoot == "" {
			writeProblem(w, problem(http.StatusNotFound, "edge.panel_unconfigured", "Not Found", "panel static root not configured; see docs/dev/edge.md", rid))
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(path, "/panel")
		if r2.URL.Path == "" {
			r2.URL.Path = "/"
		}
		g.panelStatic.ServeHTTP(w, r2)
		return
	}

	// SPA static by audience
	if audience == "admin" {
		g.adminStatic.ServeHTTP(w, r)
		return
	}
	g.portalStatic.ServeHTTP(w, r)
}

func (g *Gateway) matchUpstream(path string) *url.URL {
	switch {
	case strings.HasPrefix(path, "/api/iam/v1/"),
		strings.HasPrefix(path, "/oauth2/"),
		strings.HasPrefix(path, "/.well-known/"),
		path == "/userinfo" || strings.HasPrefix(path, "/userinfo/"):
		return g.iamURL
	case strings.HasPrefix(path, "/api/org/v1/"):
		return g.orgURL
	case strings.HasPrefix(path, "/api/workspace/v1/"):
		return g.workspaceURL
	default:
		return nil
	}
}

func isAdminAPIPath(path string) bool {
	// /api/<svc>/v1/admin/**
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	// api, svc, v1, admin, ...
	if len(parts) < 4 {
		return false
	}
	return parts[0] == "api" && parts[2] == "v1" && parts[3] == "admin"
}

func (g *Gateway) isPublicPath(path string) bool {
	for _, p := range g.Cfg.PublicPathPrefixes {
		if p == "" {
			continue
		}
		if p == path || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (g *Gateway) proxyAPI(w http.ResponseWriter, r *http.Request, target *url.URL, audience, clientIP, rid string) {
	tp := ensureTraceparent(r)
	bearer := ""
	authHeader := r.Header.Get("Authorization")
	hasClientBearer := strings.HasPrefix(strings.ToLower(authHeader), "bearer ")

	needSession := !g.isPublicPath(r.URL.Path) && !hasClientBearer
	sess, err := g.loadSession(r, audience)
	if err != nil && needSession {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "session required", rid))
		return
	}
	if sess != nil {
		if err := sess.Valid(g.Now().UTC(), audience); err != nil {
			if needSession {
				writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", err.Error(), rid))
				return
			}
			sess = nil
		}
	}
	if sess != nil && isWriteMethod(r.Method) && !g.isPublicPath(r.URL.Path) {
		if err := g.checkCSRF(r, sess); err != nil {
			writeProblem(w, problem(http.StatusForbidden, "edge.csrf_failed", "Forbidden", err.Error(), rid))
			return
		}
		bearer = sess.AccessToken
	} else if sess != nil {
		bearer = sess.AccessToken
	} else if hasClientBearer {
		bearer = strings.TrimSpace(authHeader[len("Bearer "):])
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") && len(authHeader) > 7 {
			bearer = strings.TrimSpace(authHeader[7:])
		}
	}

	proxy := newReverseProxy(target)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		rewriteOutboundAPI(pr.Out, clientIP, bearer, rid, tp)
	}
	proxy.ServeHTTP(w, r)
}

func (g *Gateway) loadSession(r *http.Request, audience string) (*Session, error) {
	name := cookieName(g.Cfg.SecureCookies, sessionCookieBase(audience))
	raw := getCookie(r, name)
	if raw == "" {
		// also try non-secure name when secure expected? no — config decides.
		return nil, ErrSessionNotFound
	}
	return g.Sessions.Get(r.Context(), HashCookie(raw))
}

func (g *Gateway) checkCSRF(r *http.Request, sess *Session) error {
	csrfName := cookieName(g.Cfg.SecureCookies, csrfCookieBase())
	cookieVal := getCookie(r, csrfName)
	headerVal := strings.TrimSpace(r.Header.Get("X-Auriga-CSRF"))
	if cookieVal == "" || headerVal == "" || cookieVal != headerVal {
		return errCSRF
	}
	sum := sha256.Sum256([]byte(headerVal))
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, sess.CSRFHash) {
		return errCSRF
	}
	return nil
}

var errCSRF = errString("csrf validation failed")

type errString string

func (e errString) Error() string { return string(e) }

func (g *Gateway) handleOpen(w http.ResponseWriter, r *http.Request, rest, clientIP, rid string) {
	_ = clientIP
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeProblem(w, problem(http.StatusMethodNotAllowed, "edge.method_not_allowed", "Method Not Allowed", "", rid))
		return
	}
	instanceID := strings.Trim(rest, "/")
	if instanceID == "" || strings.Contains(instanceID, "/") {
		writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "", rid))
		return
	}
	sess, err := g.loadSession(r, "portal")
	if err != nil || sess.Valid(g.Now().UTC(), "portal") != nil {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "portal session required", rid))
		return
	}
	ok, err := g.Owners.Owns(r.Context(), sess.AccountID, instanceID)
	if err != nil || !ok {
		writeProblem(w, problem(http.StatusForbidden, "edge.not_owner", "Forbidden", "not instance owner", rid))
		return
	}
	ticket, err := g.Tickets.Issue(sess.SessionID, sess.AccountID, sess.TenantID, instanceID, 60*time.Second)
	if err != nil {
		writeProblem(w, problem(http.StatusInternalServerError, "edge.ticket_error", "Internal Server Error", "cannot issue ticket", rid))
		return
	}
	loc := g.ticketRedirectURL(r, instanceID, ticket.Token)
	http.Redirect(w, r, loc, http.StatusFound)
}

func (g *Gateway) ticketRedirectURL(r *http.Request, instanceID, token string) string {
	scheme := "http"
	if g.Cfg.SecureCookies || r.TLS != nil {
		scheme = "https"
	}
	if g.Cfg.InstanceParentDomain != "" {
		host := instanceID + "." + g.Cfg.InstanceParentDomain
		return scheme + "://" + host + "/_auriga/ticket?t=" + url.QueryEscape(token)
	}
	// path-based fallback
	return scheme + "://" + requestHost(r) + "/i/" + instanceID + "/_auriga/ticket?t=" + url.QueryEscape(token)
}

func (g *Gateway) instanceIDFromHost(host string) (string, bool) {
	parent := strings.ToLower(strings.TrimSpace(g.Cfg.InstanceParentDomain))
	if parent == "" {
		return "", false
	}
	host = strings.ToLower(host)
	suffix := "." + parent
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(host, suffix)
	if id == "" || strings.Contains(id, ".") {
		return "", false
	}
	return id, true
}

func (g *Gateway) serveInstance(w http.ResponseWriter, r *http.Request, instanceID, pathSuffix, clientIP, rid string) {
	// Determine path as seen by instance (htu).
	instPath := r.URL.Path
	if pathSuffix != "" {
		// path-based /i/<id>/...
		instPath = pathSuffix
		if instPath == "" {
			instPath = "/"
		}
	}

	// Ticket redeem
	if strings.HasPrefix(instPath, "/_auriga/ticket") {
		g.redeemTicket(w, r, instanceID, rid)
		return
	}

	route, err := g.Routes.Get(r.Context(), instanceID)
	if err != nil {
		writeProblem(w, problem(http.StatusNotFound, "edge.instance_not_found", "Not Found", "unknown instance", rid))
		return
	}

	binding, err := g.lookupInstBinding(r)
	if err != nil || binding.InstanceID != instanceID {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "instance cookie required", rid))
		return
	}
	// Portal session must still be valid
	// We don't have cookie raw for portal on instance origin — binding carries session id;
	// look up by scanning is not available. Skeleton: store sessionId in binding and require
	// a SessionStore that can GetBySessionID, OR re-validate via introspect.
	// For B7 skeleton: keep a side map in MemorySessionStore keyed by sessionId for lookup.
	sess, err := getSessionByID(g.Sessions, r.Context(), binding.SessionID)
	if err != nil || sess.Valid(g.Now().UTC(), "portal") != nil {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "portal session invalid", rid))
		return
	}
	if sess.AccountID != route.OwnerID || sess.AccountID != binding.AccountID {
		writeProblem(w, problem(http.StatusForbidden, "edge.not_owner", "Forbidden", "not instance owner", rid))
		return
	}

	if route.State != "running" {
		writeProblem(w, problem(http.StatusServiceUnavailable, "edge.instance_not_running", "Service Unavailable", "instance state="+route.State, rid))
		return
	}

	claims := IdentityClaims{
		Iss: "auriga-edge",
		Aud: instanceID,
		Sub: sess.AccountID,
		Tid: sess.TenantID,
		Sid: sess.SessionID,
		Htm: r.Method,
		Htu: instPath,
		Pv:  1,
	}
	token, err := g.Signer.Sign(claims)
	if err != nil {
		writeProblem(w, problem(http.StatusInternalServerError, "edge.identity_sign_failed", "Internal Server Error", "cannot sign identity", rid))
		return
	}

	ingress, err := url.Parse(route.Ingress)
	if err != nil || ingress.Scheme == "" {
		writeProblem(w, problem(http.StatusBadGateway, "edge.bad_ingress", "Bad Gateway", "invalid ingress", rid))
		return
	}
	proxy := newReverseProxy(ingress)
	tp := ensureTraceparent(r)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		// path-based: strip /i/<id> prefix already reflected in instPath
		if pathSuffix != "" {
			pr.Out.URL.Path = instPath
			pr.Out.URL.RawPath = ""
		}
		rewriteOutboundInstance(pr.Out, clientIP, token, rid, tp)
	}
	proxy.ServeHTTP(w, r)
}

func (g *Gateway) redeemTicket(w http.ResponseWriter, r *http.Request, instanceID, rid string) {
	t := r.URL.Query().Get("t")
	if t == "" {
		writeProblem(w, problem(http.StatusBadRequest, "edge.bad_ticket", "Bad Request", "missing ticket", rid))
		return
	}
	ticket, err := g.Tickets.Redeem(t)
	if err != nil {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.bad_ticket", "Unauthorized", err.Error(), rid))
		return
	}
	if ticket.InstanceID != instanceID {
		writeProblem(w, problem(http.StatusForbidden, "edge.bad_ticket", "Forbidden", "ticket instance mismatch", rid))
		return
	}
	binding, err := g.Tickets.BindInstance(ticket.SessionID, ticket.AccountID, ticket.TenantID, ticket.InstanceID)
	if err != nil {
		writeProblem(w, problem(http.StatusInternalServerError, "edge.ticket_error", "Internal Server Error", "", rid))
		return
	}
	setInstanceCookie(w, g.Cfg.SecureCookies, binding.CookieRaw, 0)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (g *Gateway) lookupInstBinding(r *http.Request) (*InstBinding, error) {
	name := cookieName(g.Cfg.SecureCookies, instCookieBase())
	raw := getCookie(r, name)
	if raw == "" {
		return nil, ErrSessionNotFound
	}
	return g.Tickets.LookupInstance(raw)
}
