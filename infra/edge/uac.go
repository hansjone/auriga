package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const uacCorrelationHeader = "X-Auriga-Correlation-Id"

var uacUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var uacProof = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var uacDevicePage = regexp.MustCompile(`(?i)^/device/([BCDFGHJKLMNPQRSTVWXZ]{4}-?[BCDFGHJKLMNPQRSTVWXZ]{4})$`)
var uacChallengePath = regexp.MustCompile(`^/api/iam/v1/login/uac/challenges/[0-9a-fA-F-]{36}(/complete)?$`)

func ensureUacCorrelation(w http.ResponseWriter) {
	if !uacUUID.MatchString(w.Header().Get(uacCorrelationHeader)) {
		if id, err := navigationRequestID(); err == nil {
			w.Header().Set(uacCorrelationHeader, id)
		}
	}
}

func uacLoginOperation(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/iam/v1/login/uac/sso", "/api/iam/v1/login/uac/challenges":
		return r.Method == http.MethodPost
	}
	if !uacChallengePath.MatchString(r.URL.Path) {
		return false
	}
	if strings.HasSuffix(r.URL.Path, "/complete") {
		return r.Method == http.MethodPost
	}
	return r.Method == http.MethodGet || r.Method == http.MethodDelete
}

type uacNavigationContext struct {
	NavigationProof string    `json:"navigationProof"`
	ExpiresAt       time.Time `json:"expiresAt"`
	TransactionID   string    `json:"transactionId"`
	Purpose         string    `json:"purpose"`
	AutoEligible    bool      `json:"autoEligible"`
	AutoSuppressed  bool      `json:"autoSuppressed"`
}

// Only an actual portal document GET can issue a navigation transaction.
func (g *Gateway) serveUacNavigation(w http.ResponseWriter, r *http.Request) bool {
	device := uacDevicePage.FindStringSubmatch(r.URL.Path)
	if g.Cfg.UacProviderID == "" || !g.Cfg.Access.Valid() || r.Method != http.MethodGet || (r.URL.Path != "/login" && r.URL.Path != "/instance-login.html" && device == nil) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	ensureUacCorrelation(w)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	fail := func(status int) {
		writeProblem(w, problem(status, map[int]string{400: "common.validation_failed", 403: "common.forbidden", 404: "common.not_found", 503: "common.unavailable"}[status], http.StatusText(status), "sign-in request unavailable", requestID(r)))
	}
	metadata := map[string]string{}
	for key, header := range map[string]string{"dest": "Sec-Fetch-Dest", "mode": "Sec-Fetch-Mode", "site": "Sec-Fetch-Site", "user": "Sec-Fetch-User"} {
		if len(r.Header.Values(header)) > 1 {
			fail(403)
			return true
		}
		if value := r.Header.Get(header); value != "" {
			metadata[key] = value
		}
	}
	if metadata["dest"] != "" && metadata["dest"] != "document" || metadata["mode"] != "" && metadata["mode"] != "navigate" || metadata["site"] == "cross-site" || strings.Contains(strings.ToLower(r.Header.Get("Sec-Purpose")+" "+r.Header.Get("Purpose")), "prefetch") || len(r.Header.Values("Origin")) > 1 || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != g.Cfg.Access.PortalURL() {
		fail(403)
		return true
	}
	if g.IAMTransport == nil {
		fail(503)
		return true
	}
	if !uacUUID.MatchString(g.Cfg.UacProviderID) {
		fail(503)
		return true
	}
	token, err := readIAMServiceToken(g.Cfg.IAMRPC.ServiceTokenFile)
	if err != nil {
		fail(503)
		return true
	}
	secure := g.requestSecureCookies(r)
	names := []string{cookieName(secure, "auriga_portal_login_tx"), cookieName(secure, "auriga_portal_sso_suppression"), cookieName(secure, csrfCookieBase())}
	values := make([]string, len(names))
	for i, name := range names {
		count := 0
		for _, c := range r.Cookies() {
			if c.Name == name {
				count++
				values[i] = c.Value
			}
		}
		if count > 1 {
			fail(400)
			return true
		}
	}
	requestID, err := navigationRequestID()
	if err != nil {
		fail(503)
		return true
	}
	purpose, endpoint := "portal", "/api/iam/v1/internal/uac/navigation-proofs"
	if device != nil {
		purpose, endpoint = "device", "/api/iam/v1/internal/uac/device-navigation-proofs"
	}
	payload := map[string]any{"requestId": requestID, "providerId": g.Cfg.UacProviderID, "origin": g.Cfg.Access.PortalURL(), "continuation": purpose, "fetchMetadata": metadata}
	if device != nil {
		payload["userCode"] = strings.ToUpper(device[1])
	}
	for i, key := range []string{"transactionCookie", "suppressionCookie", "csrfCookie"} {
		if values[i] != "" {
			payload[key] = values[i]
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		fail(503)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	target := *g.iamURL
	target.Path = endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(raw))
	if err != nil {
		fail(503)
		return true
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("auriga-tenant-id", g.Cfg.Access.TenantID())
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: g.IAMTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		fail(503)
		return true
	}
	defer func() { _ = resp.Body.Close() }()
	correlation := resp.Header.Get(uacCorrelationHeader)
	if len(resp.Header.Values(uacCorrelationHeader)) != 1 || !uacUUID.MatchString(correlation) {
		fail(503)
		return true
	}
	w.Header().Set(uacCorrelationHeader, correlation)
	if resp.StatusCode != http.StatusCreated {
		if resp.StatusCode == 404 && device != nil {
			fail(404)
		} else {
			fail(503)
		}
		return true
	}
	var issued struct {
		Context           uacNavigationContext `json:"context"`
		TransactionCookie string               `json:"transactionCookie"`
		CSRFCookie        string               `json:"csrfCookie"`
		TTL               int                  `json:"ttlSeconds"`
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil || len(raw) > 32768 || json.Unmarshal(raw, &issued) != nil || issued.TTL != 120 || len(issued.TransactionCookie) < 43 || len(issued.TransactionCookie) > 16384 {
		fail(503)
		return true
	}
	nav := issued.Context
	if nav.Purpose != purpose || !uacProof.MatchString(nav.NavigationProof) || !uacUUID.MatchString(nav.TransactionID) || !nav.ExpiresAt.After(g.Now()) || nav.ExpiresAt.After(g.Now().Add(121*time.Second)) || purpose == "device" && nav.AutoEligible {
		fail(503)
		return true
	}
	// IAM validates/reuses an existing CSRF; new values are internal-hop only.
	csrf := issued.CSRFCookie
	if csrf == "" {
		csrf = values[2]
	}
	if !uacProof.MatchString(csrf) {
		fail(503)
		return true
	}
	safe, err := json.Marshal(nav)
	if err != nil {
		fail(503)
		return true
	}
	file := "index.html"
	if r.URL.Path == "/instance-login.html" {
		file = "instance-login.html"
	}
	html, err := os.ReadFile(filepath.Join(g.Cfg.Static.PortalRoot, file))
	if err != nil || !bytes.Contains(html, []byte("</head>")) {
		fail(503)
		return true
	}
	if file == "index.html" {
		html, err = injectAccessMode(html, g.Cfg.Access.Mode())
		if err != nil {
			fail(503)
			return true
		}
	}
	txCookie := &http.Cookie{Name: names[0], Value: issued.TransactionCookie, Path: "/", Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 120}
	csrfCookie := &http.Cookie{Name: names[2], Value: csrf, Path: "/", Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 120}
	if txCookie.Valid() != nil || csrfCookie.Valid() != nil {
		fail(503)
		return true
	}
	http.SetCookie(w, txCookie)
	if csrf != values[2] {
		http.SetCookie(w, csrfCookie)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	addition := []byte("<meta name=\"auriga-uac-provider\" content=\"" + g.Cfg.UacProviderID + "\"><meta name=\"auriga-uac-correlation\" content=\"" + correlation + "\"><script type=\"application/json\" id=\"auriga-uac-navigation\">")
	addition = append(addition, safe...)
	addition = append(addition, []byte("</script></head>")...)
	html = bytes.Replace(html, []byte("</head>"), addition, 1)
	if g.Log != nil {
		g.Log.InfoContext(r.Context(), "UAC navigation issued", "correlationId", correlation, "purpose", purpose, "requestId", requestID)
	}
	_, _ = w.Write(html)
	return true
}
func navigationRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
