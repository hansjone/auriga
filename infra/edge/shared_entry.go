package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"
)

// SharedBrowserBinding is server-only state; never trust identity fields from a browser.
type SharedBrowserBinding struct {
	Binding           map[string]any
	PlatformSessionID string
	Target            InstanceTarget
	ExpiresAt         time.Time
}

// SharedEntryStore must persist ConsumeAndBind atomically across edge replicas/restarts.
// Return common.conflict for a previously consumed ticket. Never use a local replay cache here.
type SharedEntryStore interface {
	ConsumeAndBind(context.Context, string, time.Time, string, SharedBrowserBinding) error
	Lookup(context.Context, string) (SharedBrowserBinding, error)
}

// SharedEntryAuthority adapters must use W Resolve + current IAM authorization, not projections.
type SharedEntryAuthority interface {
	IsShared(context.Context, string) (bool, error)
	Check(context.Context, map[string]any, string) error
	// Watch must cancel on revoke, expiry or loss of authority; subscribe BEFORE the final Check.
	Watch(context.Context, map[string]any, string, context.CancelFunc) (func(), error)
}
type SharedEntry struct {
	Authority   SharedEntryAuthority
	Store       SharedEntryStore
	TicketKeys  map[string]ed25519.PublicKey
	IdentityKey ed25519.PrivateKey
	IdentityKid string
}

func sharedJTI() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return ""
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	const h = "0123456789abcdef"
	s := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			s = append(s, '-')
		}
		s = append(s, h[v>>4], h[v&15])
	}
	return string(s)
}
func sharedError(w http.ResponseWriter, r *http.Request, err error) {
	code := "common.unavailable"
	status := 503
	if err != nil {
		switch err.Error() {
		case "common.validation_failed":
			code = err.Error()
			status = 400
		case "shared.signature_invalid", "shared.expired":
			code = err.Error()
			status = 401
		case "shared.binding_mismatch", "shared.revoked":
			code = err.Error()
			status = 403
		case "common.conflict":
			code = err.Error()
			status = 409
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeProblem(w, problem(status, code, http.StatusText(status), "placement entry rejected", requestID(r)))
}
func (g *Gateway) serveShared(w http.ResponseWriter, r *http.Request, id, path, clientIP, rid string) bool {
	e := g.Shared
	// The exchange path is reserved even before W wiring, so it cannot fall through to owner auth.
	exchange := path == "/_auriga/placement-ticket"
	if e == nil {
		if exchange {
			sharedError(w, r, nil)
			return true
		}
		return false
	}
	if e.Authority == nil {
		sharedError(w, r, nil)
		return true
	}
	shared, err := e.Authority.IsShared(r.Context(), id)
	if err != nil {
		sharedError(w, r, err)
		return true
	}
	if !shared {
		if exchange {
			sharedError(w, r, errors.New("shared.binding_mismatch"))
			return true
		}
		return false
	}
	if e.Store == nil || len(e.TicketKeys) == 0 || len(e.IdentityKey) != ed25519.PrivateKeySize {
		sharedError(w, r, nil)
		return true
	}
	target := g.instanceTarget(r, id)
	if target.Origin == "" || target.Mount == "" {
		sharedError(w, r, nil)
		return true
	}
	// Validate the exact escaped bytes that will reach node and plugin.
	canonical := r.URL.EscapedPath()
	if target.Mount != "/" {
		canonical = "/" + strings.TrimPrefix(canonical, target.Mount)
	}
	if !sharedPath(canonical) || !strictInstancePath(r.URL.Path, r.URL.RawPath) {
		sharedError(w, r, errors.New("shared.binding_mismatch"))
		return true
	}
	if exchange {
		if len(r.Header.Values("Origin")) > 1 {
			sharedError(w, r, errors.New("shared.binding_mismatch"))
			return true
		}
		// Cross-origin JSON exchange is allowed only from the installation portal.
		portal := g.instanceTarget(r, id).Origin
		if g.Cfg.Access.Valid() {
			o := g.Cfg.Access.PortalOrigin()
			portal = o.Scheme + "://" + originAuthority(o)
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != target.Origin {
			if len(r.Header.Values("Origin")) != 1 || origin != portal {
				sharedError(w, r, errors.New("shared.binding_mismatch"))
				return true
			}
			w.Header().Set("Access-Control-Allow-Origin", portal)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == "OPTIONS" {
			if r.Header.Get("Origin") != portal || r.Header.Get("Access-Control-Request-Method") != "POST" || strings.ToLower(r.Header.Get("Access-Control-Request-Headers")) != "content-type" {
				sharedError(w, r, errors.New("common.validation_failed"))
				return true
			}
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(204)
			return true
		}
		e.exchange(w, r, id, target, g.requestSecureCookies(r), g.Now())
		return true
	}
	if err = g.checkUpgradeOriginInstance(r, id); err != nil {
		sharedError(w, r, errors.New("shared.binding_mismatch"))
		return true
	}
	raw, err := uniqueInstanceCookie(r, instanceCookieName(g.requestSecureCookies(r), target.Mount))
	if err != nil {
		sharedError(w, r, errors.New("shared.revoked"))
		return true
	}
	b, err := e.Store.Lookup(r.Context(), raw)
	if err != nil {
		sharedError(w, r, err)
		return true
	}
	if b.Target != target || !sharedBinding(b.Binding) || b.Binding["physicalInstanceId"] != id || !g.Now().Before(b.ExpiresAt) {
		sharedError(w, r, errors.New("shared.binding_mismatch"))
		return true
	}
	ctx, cancel := context.WithDeadline(r.Context(), b.ExpiresAt)
	defer cancel()
	stop, err := e.Authority.Watch(ctx, b.Binding, b.PlatformSessionID, cancel)
	if err != nil || stop == nil {
		sharedError(w, r, err)
		return true
	}
	defer stop()
	if err = e.Authority.Check(ctx, b.Binding, b.PlatformSessionID); err != nil || ctx.Err() != nil {
		sharedError(w, r, err)
		return true
	}
	now := g.Now().Unix()
	claims := map[string]any{"schemaVersion": 2, "issuer": "auriga.edge", "audience": "auriga.shared.ingress", "binding": b.Binding, "platformSessionId": b.PlatformSessionID, "method": r.Method, "canonicalPath": canonical, "issuedAt": now, "expiresAt": now + 60, "jti": sharedJTI()}
	token, err := sharedSign(claims, e.IdentityKid, e.IdentityKey)
	if err != nil {
		sharedError(w, r, err)
		return true
	}
	route, err := g.Routes.Get(ctx, id)
	if err != nil || route.InstanceID != id || route.TenantID != b.Binding["tenantId"] || route.State != "running" {
		sharedError(w, r, nil)
		return true
	}
	upstream, tr, err := g.Ingress.target(route)
	if err != nil {
		sharedError(w, r, nil)
		return true
	}
	if r.ContentLength > maxInstanceBodyBytes {
		sharedError(w, r, errors.New("common.validation_failed"))
		return true
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxInstanceBodyBytes)
	}
	proxy := newReverseProxy(upstream, tr, false)
	base := proxy.Rewrite
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		base(pr)
		pr.Out.URL.Path = "/i/" + id + path
		pr.Out.URL.RawPath = "/i/" + id + canonical
		rewriteOutboundInstance(pr.Out, clientIP, token, rid, ensureTraceparent(r))
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del(instanceEntryHeader)
		if target.Mount != "/" {
			return mapInstanceResponse(resp, target, canonical, r.URL.RawQuery, g.requestSecureCookies(r))
		}
		return stripInstanceSetCookie(resp)
	}
	proxy.ServeHTTP(w, r.WithContext(ctx))
	return true
}
func (e *SharedEntry) exchange(w http.ResponseWriter, r *http.Request, id string, target InstanceTarget, secure bool, now time.Time) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(code string) { sharedError(w, r, errors.New(code)) }
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method != "POST" || r.URL.RawQuery != "" || media != "application/json" || err != nil {
		fail("common.validation_failed")
		return
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20000))
	var body map[string]json.RawMessage = make(map[string]json.RawMessage)
	opening, parseErr := d.Token()
	if parseErr != nil || opening != json.Delim('{') {
		fail("common.validation_failed")
		return
	}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || name != "ticket" || body[name] != nil {
			fail("common.validation_failed")
			return
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			fail("common.validation_failed")
			return
		}
		body[name] = value
	}
	closing, parseErr := d.Token()
	var extra any
	if parseErr != nil || closing != json.Delim('}') || len(body) != 1 || d.Decode(&extra) != io.EOF {
		fail("common.validation_failed")
		return
	}
	var token string
	if json.Unmarshal(body["ticket"], &token) != nil || len(token) > 16384 || !sharedTicketShape.MatchString(token) {
		fail("common.validation_failed")
		return
	}
	c, err := sharedVerify(token, e.TicketKeys, false, now.Unix())
	if err != nil {
		sharedError(w, r, err)
		return
	}
	b := c["binding"].(map[string]any)
	if b["physicalInstanceId"] != id || b["mode"] != "shared" {
		fail("shared.binding_mismatch")
		return
	}
	sid := sharedString(c["platformSessionId"])
	if err = e.Authority.Check(r.Context(), b, sid); err != nil {
		sharedError(w, r, err)
		return
	}
	cookie, err := randomID(32)
	if err != nil {
		sharedError(w, r, nil)
		return
	}
	cookie = sharedString(c["jti"]) + "." + cookie
	// Watch/Check gate all subsequent use. Cookie lifetime never substitutes for current IAM state.
	binding := SharedBrowserBinding{Binding: b, PlatformSessionID: sid, Target: target, ExpiresAt: now.Add(12 * time.Hour)}
	err = e.Store.ConsumeAndBind(r.Context(), sharedString(c["jti"]), time.Unix(sharedInt(c["expiresAt"]), 0), cookie, binding)
	if err != nil {
		sharedError(w, r, err)
		return
	}
	setInstanceCookie(w, secure, cookie, 0, target.Mount)
	w.Header().Set("Location", target.Mount)
	w.WriteHeader(http.StatusFound)
}

// Only the ticket exchange authenticates entirely from its JSON bearer ticket.
// This exemption never applies to ordinary instance writes or upgrades.
func (g *Gateway) sharedTicketRequest(r *http.Request) bool {
	if r.Method != "POST" {
		return false
	}
	if _, ok := g.instanceIDFromHost(requestHost(r)); ok {
		return r.URL.Path == "/_auriga/placement-ticket"
	}
	if g.Cfg.InstanceParentDomain != "" {
		return false
	}
	parts := strings.Split(r.URL.Path, "/")
	return len(parts) == 5 && parts[1] == "i" && canonicalInstanceID(parts[2]) && parts[3] == "_auriga" && parts[4] == "placement-ticket"
}

var sharedTicketShape = regexp.MustCompile("^[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+$")
