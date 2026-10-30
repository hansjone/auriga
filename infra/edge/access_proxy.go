package main

import (
	"errors"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
)

const adminCSRFBootstrapPath = "/api/iam/v1/admin/session/csrf-tokens"

var loginTransactionPath = regexp.MustCompile(`^/api/iam/v1/login/transactions/[0-9a-fA-F-]+(?:/advance|/events)?$`)
var loginCallbackPath = regexp.MustCompile(`^/api/iam/v1/login/callback/[0-9a-fA-F-]+$`)
var invitationPreviewPath = regexp.MustCompile(`^/api/org/v1/invitations/preview/[^/]+$`)

func loginOperation(r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/iam/v1/login/providers" {
		return r.Method == "GET"
	}
	if p == "/api/iam/v1/login/transactions" {
		return r.Method == "POST"
	}
	if loginCallbackPath.MatchString(p) {
		return r.Method == "GET" || r.Method == "POST"
	}
	if !loginTransactionPath.MatchString(p) {
		return false
	}
	if strings.HasSuffix(p, "/advance") {
		return r.Method == "POST"
	}
	if strings.HasSuffix(p, "/events") {
		return r.Method == "GET"
	}
	return r.Method == "GET" || r.Method == "DELETE"
}

func protocolOperation(r *http.Request) bool {
	switch r.URL.Path {
	case "/.well-known/openid-configuration", "/oauth2/jwks", "/userinfo":
		return r.Method == "GET"
	case "/oauth2/token", "/oauth2/device_authorization", "/oauth2/revoke":
		return r.Method == "POST"
	case "/oauth2/authorize":
		return r.Method == "GET" || r.Method == "POST"
	}
	return false
}

func singleCookie(r *http.Request, name string) (string, bool) {
	value, count := "", 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value = c.Value
			count++
		}
	}
	return value, count == 1 && value != ""
}

func (g *Gateway) proxyAccessAPI(w http.ResponseWriter, r *http.Request, target *url.URL, audience, clientIP, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "request rejected", rid))
	}
	unavailable := func(reason string) {
		if g.Log != nil {
			g.Log.Warn("Access API request rejected", "reason", reason, "requestId", rid)
		}
		fail(503, "common.unavailable")
	}
	e, ok := r.Context().Value(accessEntryKey{}).(accessEntry)
	if !ok {
		fail(403, "common.forbidden")
		return
	}
	iam := target == g.iamURL
	if r.URL.Path == "/oauth2/revoke" && (r.Method != http.MethodPost || audience != "portal") {
		fail(403, "common.forbidden")
		return
	}
	bootstrap := r.Method == "POST" && r.URL.Path == adminCSRFBootstrapPath
	ipAdmin := g.Cfg.Access.Mode() == "ip" && audience == "admin"
	if r.URL.Path == adminCSRFBootstrapPath && (!bootstrap || !ipAdmin) {
		fail(403, "common.forbidden")
		return
	}
	if ipAdmin && r.URL.Path == "/oauth2/authorize" {
		fail(403, "common.forbidden")
		return
	}
	preview := target == g.orgURL && r.Method == http.MethodGet && invitationPreviewPath.MatchString(r.URL.Path)
	// A second decoder upstream must not turn an anonymous code into separators or dot segments.
	if preview && strings.Contains(strings.ToLower(r.URL.EscapedPath()), "%25") {
		fail(http.StatusBadRequest, "edge.bad_path")
		return
	}
	public := preview || iam && (loginOperation(r) || protocolOperation(r))
	raw, cookieOK := singleCookie(r, cookieName(e.scheme == "https", sessionCookieBase(audience)))
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == cookieName(e.scheme == "https", sessionCookieBase(audience)) {
			count++
		}
	}
	if count > 1 {
		fail(400, "common.validation_failed")
		return
	}
	var sess *Session
	// Invitation previews are anonymous even when the browser has a session.
	if cookieOK && !preview {
		loaded, err := g.browserSession(r, raw, audience, !public && r.Method == http.MethodGet)
		if err != nil && !errors.Is(err, ErrSessionNotFound) && !errors.Is(err, errIAMUnauthenticated) && !public {
			unavailable("browser_session_unavailable")
			return
		}
		if err == nil && loaded.Valid(g.Now().UTC(), audience) == nil && loaded.TenantID == g.Cfg.Access.TenantID() {
			sess = loaded
		}
	}
	auth := r.Header.Get("Authorization")
	clientBearer := strings.HasPrefix(auth, "Bearer ") && len(auth) > len("Bearer ")
	if bootstrap && sess == nil {
		fail(401, "common.unauthenticated")
		return
	}
	if !public && sess == nil && (!clientBearer || cookieOK || ipAdmin && isWriteMethod(r.Method)) {
		fail(401, "common.unauthenticated")
		return
	}
	if bootstrap {
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || len(r.Header.Values("Content-Type")) != 1 || contentType != "application/json" || len(r.Header.Values("X-Auriga-CSRF-Bootstrap")) != 1 || r.Header.Get("X-Auriga-CSRF-Bootstrap") != "admin-session-v1" || !strictOrigin(r, e) {
			fail(403, "common.forbidden")
			return
		}
	} else if sess != nil && isWriteMethod(r.Method) && !public {
		if ipAdmin {
			if g.AdminCSRF == nil {
				unavailable("admin_csrf_verifier_unavailable")
				return
			}
			if len(r.Header.Values("X-Auriga-CSRF")) != 1 {
				fail(403, "common.forbidden")
				return
			}
			err := g.AdminCSRF.Verify(r.Context(), raw, r.Header.Get("X-Auriga-CSRF"), sess, g.Now().UTC())
			if err != nil {
				switch {
				case errors.Is(err, errIAMUnauthenticated):
					fail(401, "common.unauthenticated")
				case errors.Is(err, errCSRF):
					fail(403, "common.forbidden")
				default:
					unavailable("admin_csrf_rpc_unavailable")
				}
				return
			}
		} else {
			count := 0
			for _, cookie := range r.Cookies() {
				if cookie.Name == cookieName(e.scheme == "https", csrfCookieBase()) {
					count++
				}
			}
			if count > 1 {
				fail(400, "common.validation_failed")
				return
			}
			if len(r.Header.Values("X-Auriga-CSRF")) != 1 || g.checkCSRF(r, sess) != nil {
				fail(403, "common.forbidden")
				return
			}
		}
	}
	if iam && g.IAMTransport == nil {
		unavailable("iam_transport_unavailable")
		return
	}
	selectedCookies, err := g.iamCookies(r, e, bootstrap)
	if err != nil {
		fail(400, "common.validation_failed")
		return
	}
	bearer := ""
	if sess != nil {
		bearer = sess.AccessToken
	} else if clientBearer {
		bearer = strings.TrimPrefix(auth, "Bearer ")
	}
	loginWrite := loginOperation(r) && isWriteMethod(r.Method) && !loginCallbackPath.MatchString(r.URL.Path)
	if public && !loginWrite {
		bearer = ""
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodyBytes)
	}
	transport := g.APITransports[target]
	if iam {
		transport = g.IAMTransport
	}
	if transport == nil {
		unavailable("api_transport_unavailable")
		return
	}
	if sess != nil && !public {
		_, err := g.touchBrowserSession(r.Context(), raw, sess)
		if err != nil {
			if errors.Is(err, errIAMUnauthenticated) {
				fail(401, "common.unauthenticated")
			} else {
				unavailable("browser_session_touch_unavailable")
			}
			return
		}
		if !g.Now().UTC().Before(sess.AccessTokenExpiresAt) {
			unavailable("browser_access_token_expired")
			return
		}
	}
	proxy := newReverseProxy(target, transport, true)
	if target == g.collabURL {
		proxy.ModifyResponse = func(resp *http.Response) error {
			if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.StatusCode != http.StatusNotModified {
				return errString("collab redirect rejected")
			}
			return nil
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, request *http.Request, err error) {
		if g.Log != nil {
			g.Log.Warn("Access API request rejected", "reason", "upstream_transport_unavailable", "requestId", requestID(request))
		}
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "control plane unavailable", requestID(request)))
	}
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		stripUntrustedEntryHeaders(pr.Out.Header)
		if target == g.collabURL {
			for name := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "auriga-forwarded-") {
					pr.Out.Header.Del(name)
				}
			}
		}
		rewriteOutboundAPI(pr.Out, clientIP, bearer, rid, ensureTraceparent(r))
		pr.Out.Header.Del("Cookie")
		if iam {
			pr.Out.Host = r.Host
			if selectedCookies != "" {
				pr.Out.Header.Set("Cookie", selectedCookies)
			}
			origin := g.Cfg.Access.PortalURL()
			if audience == "admin" {
				origin = g.Cfg.Access.AdminURL()
			}
			pr.Out.Header.Set("X-Auriga-Entry-Origin", origin)
			pr.Out.Header.Set("X-Auriga-Entry-Role", audience)
			// OAuth client credentials and userinfo have their own exact protocol authentication.
			if r.URL.Path == "/oauth2/token" || r.URL.Path == "/oauth2/device_authorization" || r.URL.Path == "/oauth2/revoke" || r.URL.Path == "/userinfo" {
				if auth != "" {
					pr.Out.Header.Set("Authorization", auth)
				}
			}
		}
	}
	proxy.ServeHTTP(w, r)
}

func (g *Gateway) iamCookies(r *http.Request, e accessEntry, bootstrap bool) (string, error) {
	var names []string
	if bootstrap {
		names = append(names, sessionCookieBase(e.role))
	}
	if loginOperation(r) && r.URL.Path != "/api/iam/v1/login/providers" {
		names = append(names, "auriga_"+e.role+"_login_tx")
	}
	needsSession := r.Method == "POST" && r.URL.Path == "/api/iam/v1/logout" || loginOperation(r) && isWriteMethod(r.Method) && !loginCallbackPath.MatchString(r.URL.Path)
	if needsSession {
		names = append(names, sessionCookieBase(e.role))
		if r.Method == "POST" && r.URL.Path == "/api/iam/v1/logout" {
			names = append(names, "auriga_"+e.role+"_login_tx")
		}
		if g.Cfg.Access.Mode() != "ip" || e.role != "admin" {
			names = append(names, csrfCookieBase())
		}
	}
	if r.URL.Path == "/oauth2/authorize" && (r.Method == "GET" || r.Method == "POST") {
		names = append(names, sessionCookieBase(e.role))
		if g.Cfg.Access.Mode() != "ip" || e.role != "admin" {
			names = append(names, csrfCookieBase())
		}
	}
	var selected []string
	for _, base := range names {
		name := cookieName(e.scheme == "https", base)
		count := 0
		for _, c := range r.Cookies() {
			if c.Name == name {
				count++
				selected = append(selected, c.String())
			}
		}
		if count > 1 {
			return "", errString("ambiguous cookie")
		}
	}
	return strings.Join(selected, "; "), nil
}
