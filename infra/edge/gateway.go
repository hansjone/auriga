package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const maxAPIBodyBytes = 1 << 20       // 1 MiB — only /api/**
const maxInstanceBodyBytes = 64 << 20 // 64 MiB, aligned with agent uploads.

// Gateway is the edge HTTP handler.
type Gateway struct {
	Cfg           Config
	Log           *slog.Logger
	Trusted       *TrustedProxies
	Sessions      SessionStore
	Routes        RouteStore
	Owners        OwnerChecker
	Tickets       TicketStore
	Signer        IdentitySigner
	Limiter       *TokenBucketLimiter
	Conns         *SessionConnHub
	Now           func() time.Time
	Ingress       *NodeIngress
	AdminCSRF     AdminCSRFVerifier
	SessionRPC    BrowserSessionRPC
	Activity      *sessionActivity
	IAMTransport  http.RoundTripper
	APITransports map[*url.URL]http.RoundTripper

	iamURL          *url.URL
	orgURL          *url.URL
	workspaceURL    *url.URL
	teamURL         *url.URL
	collabURL       *url.URL
	resourceURL     *url.URL
	deviceURL       *url.URL
	litellmURL      *url.URL
	litellmHandoffs liteLLMHandoffs

	networkSubmitURL *url.URL

	portalStatic http.Handler
	adminStatic  http.Handler
	panelStatic  http.Handler

	networkWSS *NetworkWSS
}

func NewGateway(cfg Config, sessions SessionStore, routes RouteStore, tickets TicketStore, signer IdentitySigner, log *slog.Logger) (*Gateway, error) {
	if err := cfg.validateLiteLLMUI(); err != nil {
		return nil, err
	}
	litellmURL, err := cfg.parseLiteLLMOrigin()
	if err != nil {
		return nil, err
	}
	resourceURL, err := cfg.parseResourceUpstream()
	if err != nil {
		return nil, err
	}
	deviceURL, err := cfg.parseDeviceUpstream()
	if err != nil {
		return nil, err
	}
	networkSubmitURL, err := cfg.parseNetworkSubmitUpstream()
	if err != nil {
		return nil, err
	}
	collabURL, err := cfg.parseCollabUpstream()
	if err != nil {
		return nil, err
	}
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
	var teamURL *url.URL
	if raw := cfg.Upstreams["ai-gateway"]; raw != "" {
		teamURL, err = parseTeamUpstream(raw)
		if err != nil {
			return nil, err
		}
	}
	hub := NewSessionConnHub()
	ingress, err := loadNodeIngress(cfg.NodeTLS)
	if err != nil {
		return nil, err
	}
	if ingress != nil {
		ingress.allowLocal = cfg.AllowLocalIngress
	}
	activity := newSessionActivity()
	wireSessionRevoke(sessions, func(sid string) {
		activity.revoke(sid)
		hub.CancelSession(sid)
	})
	networkWSS, err := newNetworkWSS(cfg.NetworkWSS, cfg.PortalHost)
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
		Conns:        hub,
		Activity:     activity,
		Now:          time.Now,
		Ingress:      ingress,
		iamURL:       iamURL,
		orgURL:       orgURL,
		workspaceURL: wsURL,
		teamURL:      teamURL,
		collabURL:    collabURL,
		resourceURL:  resourceURL,
		deviceURL:    deviceURL,
		litellmURL:   litellmURL,
		portalStatic: newSPAFileServer(cfg.Static.PortalRoot),
		adminStatic:  newSPAFileServer(cfg.Static.AdminRoot),
		panelStatic:  newSPAFileServer(cfg.Static.PanelRoot),
		networkWSS:   networkWSS,

		networkSubmitURL: networkSubmitURL,
	}
	if cfg.Access.Valid() && (cfg.IAMHop.CAFile != "" || cfg.IAMHop.AllowLoopbackHTTP) {
		g.IAMTransport, err = newIAMHopTransport(cfg.IAMHop, iamURL)
		if err != nil {
			return nil, err
		}
	}
	if cfg.Access.Valid() {
		g.APITransports = make(map[*url.URL]http.RoundTripper)
		if deviceURL != nil && cfg.DeviceHop != (IAMHopConfig{}) {
			transport, err := newIAMHopTransport(cfg.DeviceHop, deviceURL)
			if err != nil {
				return nil, err
			}
			transport.ResponseHeaderTimeout = 60 * time.Second
			g.APITransports[deviceURL] = transport
		}
		if networkSubmitURL != nil {
			transport, err := newIAMHopTransport(cfg.NetworkSubmitHop, networkSubmitURL)
			if err != nil {
				return nil, err
			}
			// Retain every device proxy timeout/limit, but use this hop's
			// explicit trust material and never an environment proxy.
			deviceTransport, ok := apiProxyTransport.(*http.Transport)
			if !ok {
				return nil, errors.New("device proxy transport unavailable")
			}
			trusted := deviceTransport.Clone()
			trusted.Proxy = nil
			trusted.TLSClientConfig = transport.TLSClientConfig
			g.APITransports[networkSubmitURL] = trusted
		}
		if resourceURL != nil {
			transport, err := newIAMHopTransport(cfg.IAMHop, resourceURL)
			if err != nil {
				return nil, err
			}
			g.APITransports[resourceURL] = transport
		}
		if collabURL != nil {
			transport, err := newIAMHopTransport(cfg.CollabHop, collabURL)
			if err != nil {
				return nil, err
			}
			g.APITransports[collabURL] = transport
		}
		for _, target := range []*url.URL{orgURL, wsURL} {
			if g.IAMTransport != nil && sameInternalHTTPSOrigin(iamURL, target) {
				g.APITransports[target] = g.IAMTransport
			} else if cfg.IAMHop.AllowLoopbackHTTP && target.Scheme == "http" {
				transport, err := newIAMHopTransport(cfg.IAMHop, target)
				if err != nil {
					return nil, err
				}
				g.APITransports[target] = transport
			}
		}
		g.portalStatic = newAccessSPAFileServer(cfg.Static.PortalRoot, cfg.Access.Mode())
		g.adminStatic = newAccessSPAFileServer(cfg.Static.AdminRoot, cfg.Access.Mode())
	}
	return g, nil
}

func (g *Gateway) Handler() http.Handler {
	var h http.Handler = http.HandlerFunc(g.serveHTTP)
	h = g.Limiter.Middleware(h, func(r *http.Request) string {
		return ResolveClientIP(r, g.Trusted)
	})
	if g.Cfg.Access.Valid() {
		h = g.accessHandler(h)
	}
	return h
}

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	rid, r := ensureRequestID(r)
	clientIP := ResolveClientIP(r, g.Trusted)
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyClientIP, clientIP))
	if entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry); ok && entry.role == "relayAdmin" {
		g.proxyLiteLLM(w, r, entry.role, rid)
		return
	}
	host := requestHost(r)

	// Instance host first: only reject traversal; do not apply portal/admin path strictness (N1).
	if instID, ok := g.instanceIDFromHost(host); ok {
		if !instancePathOK(r) {
			writeProblem(w, problem(http.StatusBadRequest, "edge.bad_path", "Bad Request", "path traversal rejected", rid))
			return
		}
		g.serveInstance(w, r, instID, "", clientIP, rid)
		return
	}

	path, ok := sanitizeRequestPath(r)
	if g.Cfg.InstanceParentDomain == "" && strings.Contains(strings.ToLower(r.URL.EscapedPath()), "%25") {
		ok = false
	}
	if !ok {
		writeProblem(w, problem(http.StatusBadRequest, "edge.bad_path", "Bad Request", "unsafe or non-normalized path", rid))
		return
	}
	if path != r.URL.Path {
		r2 := r.Clone(r.Context())
		r2.URL.Path = path
		r2.URL.RawPath = ""
		r = r2
	}

	audience, knownHost := normalizeAudience(host, g.Cfg.PortalHost, g.Cfg.AdminHost)
	if entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry); ok {
		audience, knownHost = entry.role, true
	}

	// Path-based instance: /i/<id>/... — only on portal host when no parent domain is configured.
	if strings.HasPrefix(path, "/i/") {
		if g.Cfg.InstanceParentDomain != "" || !knownHost || audience != "portal" {
			writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "path-based instance routing disabled", rid))
			return
		}
		rest := strings.TrimPrefix(path, "/i/")
		parts := strings.SplitN(rest, "/", 2)
		instID := parts[0]
		suffix := "/"
		if len(parts) == 2 {
			suffix = "/" + parts[1]
		}
		if !canonicalInstanceID(instID) {
			writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "", rid))
			return
		}
		mount := instanceMount(instID)
		escaped := r.URL.EscapedPath()
		if !strictInstancePath(path, r.URL.RawPath) || escaped != strings.TrimSuffix(mount, "/") && !strings.HasPrefix(escaped, mount) {
			writeProblem(w, problem(http.StatusBadRequest, "edge.bad_path", "Bad Request", "path traversal rejected", rid))
			return
		}
		if len(parts) == 1 {
			location := mount
			if r.URL.RawQuery != "" {
				location += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, location, http.StatusPermanentRedirect)
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

	// Network data plane: NATS-over-WSS upgrade (auth is NATS CONNECT / callout, not browser session).
	if g.networkWSS != nil && g.networkWSS.Match(path) {
		if audience != "portal" {
			writeProblem(w, problem(http.StatusForbidden, "edge.forbidden", "Forbidden", "network wss only on portal host", rid))
			return
		}
		g.networkWSS.ServeHTTP(w, r)
		return
	}

	// Admin API isolation (use sanitized path)
	if isAdminAPIPath(path) && audience != "admin" {
		if g.Cfg.Access.Valid() {
			writeProblem(w, problem(http.StatusForbidden, "common.forbidden", "Forbidden", "admin entry required", rid))
			return
		}
		writeProblem(w, problem(http.StatusForbidden, "edge.admin_host_required", "Forbidden", "admin API only on admin host", rid))
		return
	}

	// Both credential types are independently verified by their owning runtime.
	// A virtual-key prefix selects LiteLLM; it never grants platform identity.
	if g.litellmURL != nil && (path == "/ai" || strings.HasPrefix(path, "/ai/")) {
		data := path == "/ai/v1/models" || path == "/ai/v1/chat/completions"
		team, _ := teamMethod(path, r.Method)
		deviceToken := audience == "portal" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-")
		if !team && (!data || !deviceToken) {
			g.proxyLiteLLM(w, r, audience, rid)
			return
		}
	}
	// Device AI has an exact route table and an independent runtime from Team.
	if isDevicePath(path) && (path != "/api/ai/v1/me/usage" || audience == "portal") {
		g.proxyDevice(w, r, audience, clientIP, rid)
		return
	}
	if path == "/api/resource" || strings.HasPrefix(path, "/api/resource/") {
		g.proxyResource(w, r, audience, clientIP, rid)
		return
	}

	// Team uses only device Bearer credentials, never browser-session exchange.
	if path == "/ai" || strings.HasPrefix(path, "/ai/") {
		g.proxyTeam(w, r, audience, clientIP, rid)
		return
	}

	// Platform API / oauth routes
	if path == "/api/collab" || strings.HasPrefix(path, "/api/collab/") {
		g.proxyNetworkKeys(w, r, audience, clientIP, rid)
		return
	}
	if upstream := g.matchUpstream(path); upstream != nil {
		g.proxyAPI(w, r, upstream, audience, clientIP, rid)
		return
	}
	// Unconfigured and non-management AI APIs must never fall through to a SPA.
	if path == "/api/ai" || strings.HasPrefix(path, "/api/ai/") {
		writeProblem(w, problem(http.StatusNotFound, "common.not_found", "Not Found", "AI API not configured or allowed", rid))
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
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
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
		if strings.HasSuffix(p, "/") {
			trimmed := strings.TrimSuffix(p, "/")
			if path == trimmed || strings.HasPrefix(path, p) {
				return true
			}
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func (g *Gateway) proxyAPI(w http.ResponseWriter, r *http.Request, target *url.URL, audience, clientIP, rid string) {
	if g.Cfg.Access.Valid() {
		g.proxyAccessAPI(w, r, target, audience, clientIP, rid)
		return
	}
	if err := g.checkUpgradeOriginAPI(r, audience); err != nil {
		writeProblem(w, problem(http.StatusForbidden, "edge.bad_origin", "Forbidden", err.Error(), rid))
		return
	}

	tp := ensureTraceparent(r)
	bearer := ""
	authHeader := r.Header.Get("Authorization")
	hasClientBearer := strings.HasPrefix(strings.ToLower(authHeader), "bearer ")
	public := g.isPublicPath(r.URL.Path)

	needSession := !public && !hasClientBearer
	sess, err := g.loadSession(r, audience, r.Method == http.MethodGet)
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
	if sess != nil && isWriteMethod(r.Method) {
		if public {
			bearer = ""
		} else {
			if err := g.checkCSRF(r, sess); err != nil {
				writeProblem(w, problem(http.StatusForbidden, "edge.csrf_failed", "Forbidden", err.Error(), rid))
				return
			}
			bearer = sess.AccessToken
		}
	} else if sess != nil {
		bearer = sess.AccessToken
	} else if hasClientBearer {
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") && len(authHeader) > 7 {
			bearer = strings.TrimSpace(authHeader[7:])
		}
	}

	if strings.HasPrefix(r.URL.Path, "/api/") && r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
	}

	if sess != nil && isUpgradeRequest(r) {
		deadline := sess.ExpiresAt
		if !sess.IdleExpiresAt.IsZero() && (deadline.IsZero() || sess.IdleExpiresAt.Before(deadline)) {
			deadline = sess.IdleExpiresAt
		}
		ctx, stop := g.Conns.Attach(r.Context(), sess.SessionID, deadline)
		defer stop()
		r = r.WithContext(ctx)
	}

	proxy := newReverseProxy(target, apiProxyTransport, true)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		rewriteOutboundAPI(pr.Out, clientIP, bearer, rid, tp)
	}
	proxy.ServeHTTP(w, r)
}

// sessionKey is the session-store key for the request's session cookie.
func (g *Gateway) sessionKey(r *http.Request, audience string) string {
	raw := getCookie(r, cookieName(g.requestSecureCookies(r), sessionCookieBase(audience)))
	if raw == "" {
		return ""
	}
	return HashCookie(raw)
}

// boundSession re-reads the portal session behind a ticket or instance
// binding. Stores keyed by cookie hash (NATS) need the key; the session id
// must still match so a reused key cannot swap in another session.
func (g *Gateway) boundSession(ctx context.Context, key, sessionID string) (*Session, error) {
	if key == "" {
		return getSessionByID(g.Sessions, ctx, sessionID)
	}
	s, err := g.Sessions.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if s.SessionID != sessionID {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

func (g *Gateway) loadSession(r *http.Request, audience string, recoverRead bool) (*Session, error) {
	name := cookieName(g.requestSecureCookies(r), sessionCookieBase(audience))
	raw := getCookie(r, name)
	if raw == "" {
		return nil, ErrSessionNotFound
	}
	return g.browserSession(r, raw, audience, recoverRead)
}

func (g *Gateway) checkCSRF(r *http.Request, sess *Session) error {
	csrfName := cookieName(g.requestSecureCookies(r), csrfCookieBase())
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
	if g.Cfg.InstanceParentDomain == "" && !canonicalInstanceID(instanceID) {
		writeProblem(w, problem(http.StatusNotFound, "edge.not_found", "Not Found", "", rid))
		return
	}
	sessKey := g.sessionKey(r, "portal")
	// Ownership and route tenant are not known yet. A missing projection must
	// be recovered by a normal authenticated API GET, without an early Touch.
	sess, err := g.loadSession(r, "portal", false)
	if errors.Is(err, errIAMUnavailable) {
		writeProblem(w, problem(http.StatusServiceUnavailable, "common.unavailable", "Service Unavailable", "session unavailable", rid))
		return
	}
	if err != nil || sess.Valid(g.Now().UTC(), "portal") != nil {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "portal session required", rid))
		return
	}
	ok, err := g.Owners.Owns(r.Context(), sess.AccountID, instanceID)
	if errors.Is(err, ErrRouteUnavailable) {
		g.routeUnavailable(w, rid)
		return
	}
	if err != nil || !ok {
		writeProblem(w, problem(http.StatusForbidden, "edge.not_owner", "Forbidden", "not instance owner", rid))
		return
	}
	route, err := g.Routes.Get(r.Context(), instanceID)
	if errors.Is(err, ErrRouteUnavailable) {
		g.routeUnavailable(w, rid)
		return
	}
	if err != nil || route.InstanceID != instanceID || route.TenantID != sess.TenantID {
		writeProblem(w, problem(http.StatusForbidden, "edge.not_owner", "Forbidden", "not instance owner", rid))
		return
	}
	if sess, err = g.touchBrowserSession(r.Context(), getCookie(r, cookieName(g.requestSecureCookies(r), sessionCookieBase("portal"))), sess); err != nil {
		status := http.StatusServiceUnavailable
		code := "common.unavailable"
		if errors.Is(err, errIAMUnauthenticated) {
			status = http.StatusUnauthorized
			code = "common.unauthenticated"
		}
		writeProblem(w, problem(status, code, http.StatusText(status), "session unavailable", rid))
		return
	}
	nonce, err := randomID(24)
	if err != nil {
		writeProblem(w, problem(http.StatusInternalServerError, "edge.ticket_error", "Internal Server Error", "cannot issue nonce", rid))
		return
	}
	sum := sha256.Sum256([]byte(nonce))
	nonceHash := hex.EncodeToString(sum[:])
	target := g.instanceTarget(r, instanceID)
	if target.Origin == "" || target.Mount == "" {
		writeProblem(w, problem(http.StatusBadRequest, "edge.bad_origin", "Bad Request", "invalid instance target", rid))
		return
	}
	ticket, err := g.Tickets.Issue(sess.SessionID, sessKey, sess.AccountID, sess.TenantID, instanceID, nonceHash, 60*time.Second, sess.ExpiresAt, target)
	if err != nil {
		writeProblem(w, problem(http.StatusInternalServerError, "edge.ticket_error", "Internal Server Error", "cannot issue ticket", rid))
		return
	}
	domain := openNonceCookieDomain(g.Cfg.InstanceParentDomain)
	setOpenNonceCookie(w, g.requestSecureCookies(r), domain, instanceID, nonce, 60, target.Mount)
	loc := g.ticketRedirectURL(r, instanceID, ticket.Token)
	http.Redirect(w, r, loc, http.StatusFound)
}

func (g *Gateway) ticketRedirectURL(r *http.Request, instanceID, token string) string {
	target := g.instanceTarget(r, instanceID)
	return target.Origin + target.Mount + "_auriga/ticket?t=" + url.QueryEscape(token)
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
	instPath := r.URL.Path
	if pathSuffix != "" {
		instPath = pathSuffix
		if instPath == "" {
			instPath = "/"
		}
	}

	if instPath == "/_auriga/ticket" {
		g.redeemTicket(w, r, instanceID, rid)
		return
	}

	if err := g.checkUpgradeOriginInstance(r, instanceID); err != nil {
		writeProblem(w, problem(http.StatusForbidden, "edge.bad_origin", "Forbidden", err.Error(), rid))
		return
	}

	binding, err := g.lookupInstBinding(r)
	if err != nil || binding.InstanceID != instanceID {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "instance cookie required", rid))
		return
	}
	target := g.instanceTarget(r, instanceID)
	if (pathSuffix != "" || binding.Target.Origin != "") && binding.Target != target {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "instance target mismatch", rid))
		return
	}
	sess, err := g.boundSession(r.Context(), binding.SessionKey, binding.SessionID)
	if err != nil || sess.Valid(g.Now().UTC(), "portal") != nil {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "portal session invalid", rid))
		return
	}
	route, err := g.Routes.Get(r.Context(), instanceID)
	if errors.Is(err, ErrRouteUnavailable) {
		g.routeUnavailable(w, rid)
		return
	}
	if err != nil {
		writeProblem(w, problem(http.StatusForbidden, "edge.not_owner", "Forbidden", "not instance owner", rid))
		return
	}
	if route.InstanceID != instanceID || sess.AccountID != route.OwnerID || sess.AccountID != binding.AccountID || sess.TenantID != route.TenantID || sess.TenantID != binding.TenantID {
		writeProblem(w, problem(http.StatusForbidden, "edge.not_owner", "Forbidden", "not instance owner", rid))
		return
	}

	if route.State != "running" {
		writeProblem(w, problem(http.StatusServiceUnavailable, "edge.instance_not_running", "Service Unavailable", "instance state="+route.State, rid))
		return
	}

	identityPath := r.URL.EscapedPath()
	if pathSuffix != "" {
		identityPath = "/" + strings.TrimPrefix(identityPath, target.Mount)
	}
	claims := IdentityClaims{
		Iss: "auriga-edge",
		Aud: instanceID,
		Sub: sess.AccountID,
		Tid: sess.TenantID,
		Sid: sess.SessionID,
		Htm: r.Method,
		Htu: identityPath,
		Pv:  1,
	}
	token, err := g.Signer.Sign(claims)
	if err != nil {
		writeProblem(w, problem(http.StatusInternalServerError, "edge.identity_sign_failed", "Internal Server Error", "cannot sign identity", rid))
		return
	}

	ingress, transport, err := g.Ingress.target(route)
	if err != nil {
		writeProblem(w, problem(http.StatusBadGateway, "edge.bad_ingress", "Bad Gateway", "invalid ingress", rid))
		return
	}

	deadline := sess.ExpiresAt
	if !sess.IdleExpiresAt.IsZero() && (deadline.IsZero() || sess.IdleExpiresAt.Before(deadline)) {
		deadline = sess.IdleExpiresAt
	}
	ctx, stop := g.Conns.Attach(r.Context(), sess.SessionID, deadline)
	defer stop()
	r = r.WithContext(ctx)
	// A revoke between the first lookup and Attach must not leave an untracked upgrade.
	freshSession, err := g.boundSession(ctx, binding.SessionKey, binding.SessionID)
	if err != nil || freshSession.Valid(g.Now().UTC(), "portal") != nil {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.unauthorized", "Unauthorized", "portal session invalid", rid))
		return
	}
	if r.ContentLength > maxInstanceBodyBytes {
		writeProblem(w, problem(http.StatusRequestEntityTooLarge, "common.validation_failed", "Payload Too Large", "instance request body too large", rid))
		return
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxInstanceBodyBytes)
	}

	proxy := newReverseProxy(ingress, transport, false)
	baseError := proxy.ErrorHandler
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeProblem(w, problem(http.StatusRequestEntityTooLarge, "common.validation_failed", "Payload Too Large", "instance request body too large", rid))
			return
		}
		baseError(w, r, err)
	}
	tp := ensureTraceparent(r)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		pr.Out.URL.Path = "/i/" + instanceID + instPath
		pr.Out.URL.RawPath = ""
		if pathSuffix == "" && r.URL.RawPath != "" {
			pr.Out.URL.RawPath = "/i/" + instanceID + r.URL.RawPath
		} else if pathSuffix != "" && r.URL.RawPath != "" {
			pr.Out.URL.RawPath = "/i/" + instanceID + "/" + strings.TrimPrefix(r.URL.RawPath, target.Mount)
		}
		rewriteOutboundInstance(pr.Out, clientIP, token, rid, tp)
	}
	proxy.ModifyResponse = stripInstanceSetCookie
	if pathSuffix != "" {
		proxy.ModifyResponse = func(resp *http.Response) error {
			return mapInstanceResponse(resp, target, identityPath, r.URL.RawQuery, g.requestSecureCookies(r))
		}
	}
	proxy.ServeHTTP(w, r)
}

func (g *Gateway) redeemTicket(w http.ResponseWriter, r *http.Request, instanceID, rid string) {
	query, queryErr := url.ParseQuery(r.URL.RawQuery)
	t := query.Get("t")
	if r.Method != http.MethodGet || queryErr != nil || len(query) != 1 || len(query["t"]) != 1 || t == "" {
		writeProblem(w, problem(http.StatusBadRequest, "edge.bad_ticket", "Bad Request", "missing ticket", rid))
		return
	}
	target := g.instanceTarget(r, instanceID)
	secure := g.requestSecureCookies(r)
	domain := openNonceCookieDomain(g.Cfg.InstanceParentDomain)
	nonceName := openNonceCookieName(secure, domain, instanceID, target.Mount)
	nonce, nonceErr := uniqueInstanceCookie(r, nonceName)
	if nonceErr != nil {
		writeProblem(w, problem(http.StatusForbidden, "edge.bad_ticket_nav", "Forbidden", "open nonce cookie required", rid))
		return
	}
	ticket, err := g.Tickets.Redeem(t)
	if err != nil {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.bad_ticket", "Unauthorized", err.Error(), rid))
		return
	}
	sum := sha256.Sum256([]byte(nonce))
	got := hex.EncodeToString(sum[:])
	if ticket.NonceHash == "" || !strings.EqualFold(got, ticket.NonceHash) {
		clearOpenNonceCookie(w, secure, domain, instanceID, target.Mount)
		writeProblem(w, problem(http.StatusForbidden, "edge.bad_ticket_nav", "Forbidden", "open nonce mismatch", rid))
		return
	}
	clearOpenNonceCookie(w, secure, domain, instanceID, target.Mount)
	if ticket.InstanceID != instanceID || ticket.Target != target {
		writeProblem(w, problem(http.StatusForbidden, "edge.bad_ticket", "Forbidden", "ticket instance mismatch", rid))
		return
	}
	sess, sessionErr := g.boundSession(r.Context(), ticket.SessionKey, ticket.SessionID)
	if sessionErr != nil || sess.Valid(g.Now().UTC(), "portal") != nil || sess.AccountID != ticket.AccountID || sess.TenantID != ticket.TenantID {
		writeProblem(w, problem(http.StatusUnauthorized, "edge.bad_ticket", "Unauthorized", "portal session invalid", rid))
		return
	}
	expiresAt := sess.ExpiresAt
	if !ticket.SessionExpiresAt.IsZero() && ticket.SessionExpiresAt.Before(expiresAt) {
		expiresAt = ticket.SessionExpiresAt
	}
	binding, err := g.Tickets.BindInstance(ticket.SessionID, ticket.SessionKey, ticket.AccountID, ticket.TenantID, ticket.InstanceID, expiresAt, target)
	if err != nil {
		writeProblem(w, problem(http.StatusInternalServerError, "edge.ticket_error", "Internal Server Error", "", rid))
		return
	}
	setInstanceCookie(w, secure, binding.CookieRaw, 0, target.Mount)
	http.Redirect(w, r, target.Mount, http.StatusFound)
}

func (g *Gateway) lookupInstBinding(r *http.Request) (*InstBinding, error) {
	mount := "/"
	if g.Cfg.InstanceParentDomain == "" {
		mount = "/i/"
	}
	name := instanceCookieName(g.requestSecureCookies(r), mount)
	raw, err := uniqueInstanceCookie(r, name)
	if err != nil {
		return nil, ErrSessionNotFound
	}
	return g.Tickets.LookupInstance(raw)
}

func (g *Gateway) checkUpgradeOriginAPI(r *http.Request, audience string) error {
	if !isUpgradeRequest(r) {
		return nil
	}
	oh, err := parseOriginHost(r)
	if err != nil {
		return err
	}
	switch audience {
	case "portal":
		if oh == strings.ToLower(g.Cfg.PortalHost) {
			return nil
		}
	case "admin":
		if oh == strings.ToLower(g.Cfg.AdminHost) {
			return nil
		}
	}
	return errString("upgrade Origin not allowed")
}

func (g *Gateway) checkUpgradeOriginInstance(r *http.Request, instanceID string) error {
	if !isUpgradeRequest(r) {
		return nil
	}
	if entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry); ok {
		if strictOrigin(r, entry) {
			return nil
		}
		return errString("upgrade Origin not allowed")
	}
	oh, err := parseOriginHost(r)
	if err != nil {
		return err
	}
	reqHost := requestHost(r)
	if oh != reqHost {
		return errString("upgrade Origin not allowed")
	}
	// Path-based: request host is portal; origin must be portal and path already scoped to this instance.
	if g.Cfg.InstanceParentDomain == "" {
		if oh == strings.ToLower(g.Cfg.PortalHost) {
			return nil
		}
		return errString("upgrade Origin not allowed")
	}
	id, ok := g.instanceIDFromHost(oh)
	if !ok || id != instanceID {
		return errString("upgrade Origin not allowed")
	}
	return nil
}

func parseOriginHost(r *http.Request) (string, error) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return "", errString("upgrade requires Origin")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return "", errString("upgrade invalid Origin")
	}
	return strings.ToLower(u.Hostname()), nil
}

func isUpgradeRequest(r *http.Request) bool {
	// N4: require explicit websocket upgrade, not merely Connection: upgrade (e.g. h2c).
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func (g *Gateway) requestSecureCookies(r *http.Request) bool {
	if entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry); ok {
		return entry.scheme == "https"
	}
	return g.Cfg.SecureCookies
}

func stripInstanceSetCookie(resp *http.Response) error {
	vals := resp.Header.Values("Set-Cookie")
	if len(vals) == 0 {
		return nil
	}
	resp.Header.Del("Set-Cookie")
	for _, c := range vals {
		name := c
		if i := strings.IndexByte(c, '='); i >= 0 {
			name = c[:i]
		}
		lname := strings.ToLower(strings.TrimSpace(name))
		if strings.Contains(lname, "auriga_") || strings.HasPrefix(lname, "__host-") || strings.HasPrefix(lname, "__secure-") {
			continue
		}
		resp.Header.Add("Set-Cookie", c)
	}
	return nil
}

func (g *Gateway) routeUnavailable(w http.ResponseWriter, rid string) {
	if g.Log != nil {
		g.Log.Warn("instance route service unavailable", "request_id", rid)
	}
	writeProblem(w, problem(http.StatusServiceUnavailable, "common.unavailable", "Service Unavailable", "route service unavailable", rid))
}
