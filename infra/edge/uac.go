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

var uacChallengePath = regexp.MustCompile(`^/api/iam/v1/login/uac/challenges/[0-9a-fA-F-]{36}(/complete)?$`)

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

// This is a server-side HTML hook, never an endpoint available to scripts.
// IAM is authoritative for provider validity, navigation eligibility and CAS.
func (g *Gateway) serveUacNavigation(w http.ResponseWriter, r *http.Request) bool {
	if g.Cfg.UacProviderID == "" || !g.Cfg.Access.Valid() || r.Method != http.MethodGet || (r.URL.Path != "/login" && r.URL.Path != "/instance-login.html") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	fail := func() {
		writeProblem(w, problem(503, "common.unavailable", "Service Unavailable", "sign-in temporarily unavailable", requestID(r)))
	}
	if g.IAMTransport == nil {
		fail()
		return true
	}
	token, err := readIAMServiceToken(g.Cfg.IAMRPC.ServiceTokenFile)
	if err != nil {
		fail()
		return true
	}
	txName := cookieName(g.requestSecureCookies(r), "auriga_portal_login_tx")
	old, _ := singleCookie(r, txName)
	suppression, _ := singleCookie(r, cookieName(g.requestSecureCookies(r), "auriga_portal_sso_suppression"))
	requestID, err := navigationRequestID()
	if err != nil {
		fail()
		return true
	}
	payload := map[string]any{
		"requestId":  requestID,
		"providerId": g.Cfg.UacProviderID, "origin": g.Cfg.Access.PortalURL(), "continuation": "portal",
		"fetchMetadata": map[string]string{"dest": r.Header.Get("Sec-Fetch-Dest"), "mode": r.Header.Get("Sec-Fetch-Mode"), "site": r.Header.Get("Sec-Fetch-Site"), "user": r.Header.Get("Sec-Fetch-User")},
	}
	if suppression != "" {
		payload["suppressionCookie"] = suppression
	}
	if old != "" {
		payload["transactionCookie"] = old
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		fail()
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	target := *g.iamURL
	target.Path = "/api/iam/v1/internal/uac/navigation-proofs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(raw))
	if err != nil {
		fail()
		return true
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("auriga-tenant-id", g.Cfg.Access.TenantID())
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: g.IAMTransport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		fail()
		return true
	}
	defer func() { _ = resp.Body.Close() }()
	var issued struct {
		Context           json.RawMessage `json:"context"`
		TransactionCookie string          `json:"transactionCookie"`
		TTL               int             `json:"ttlSeconds"`
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 32769))
	if err != nil || len(raw) > 32768 || resp.StatusCode != http.StatusCreated || json.Unmarshal(raw, &issued) != nil || issued.TTL != 120 || len(issued.TransactionCookie) < 43 {
		fail()
		return true
	}
	var contextValue struct {
		NavigationProof string    `json:"navigationProof"`
		ExpiresAt       time.Time `json:"expiresAt"`
		TransactionID   string    `json:"transactionId"`
		Purpose         string    `json:"purpose"`
		AutoEligible    bool      `json:"autoEligible"`
		AutoSuppressed  bool      `json:"autoSuppressed"`
	}
	if json.Unmarshal(issued.Context, &contextValue) != nil || contextValue.Purpose != "portal" || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(contextValue.NavigationProof) || !contextValue.ExpiresAt.After(g.Now()) {
		fail()
		return true
	}
	safe, err := json.Marshal(contextValue)
	if err != nil {
		fail()
		return true
	}
	file := "index.html"
	if r.URL.Path == "/instance-login.html" {
		file = "instance-login.html"
	}
	html, err := os.ReadFile(filepath.Join(g.Cfg.Static.PortalRoot, file))
	if err != nil {
		fail()
		return true
	}
	cookie := &http.Cookie{Name: txName, Value: issued.TransactionCookie, Path: "/", Secure: g.requestSecureCookies(r), HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 120}
	if cookie.Valid() != nil {
		fail()
		return true
	}
	http.SetCookie(w, cookie)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	html = bytes.Replace(html, []byte("</head>"), append(append([]byte(`<script type="application/json" id="auriga-uac-navigation">`), safe...), []byte("</script></head>")...), 1)
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
