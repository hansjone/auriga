package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type LiteLLMUILogin struct {
	Username      string `json:"username,omitempty"`
	PasswordFile  string `json:"passwordFile"`
	MasterKeyFile string `json:"masterKeyFile,omitempty"`
}

func readLiteLLMUISecret(filename string) ([]byte, error) {
	rejected := fmt.Errorf("LiteLLM UI credential unavailable")
	file, err := os.Open(filename)
	if err != nil {
		return nil, rejected
	}
	defer func() { _ = file.Close() }()
	secret, err := io.ReadAll(io.LimitReader(file, 16385))
	secret = bytes.TrimSpace(secret)
	if err != nil || len(secret) < 16 || len(secret) > 16384 {
		return nil, rejected
	}
	return secret, nil
}

// Use the pinned LiteLLM /v2/login endpoint. Do not forward any browser header,
// follow its redirects, log response bodies, or place credentials in a URL.
func (g *Gateway) loginLiteLLMUI(r *http.Request) (string, error) {
	login := g.Cfg.LiteLLMUI.Login
	if login == nil {
		return "", nil
	}
	rejected := fmt.Errorf("LiteLLM UI session exchange unavailable")
	password, err := readLiteLLMUISecret(login.PasswordFile)
	if err != nil {
		return "", rejected
	}
	username := login.Username
	if username == "" {
		username = "admin"
	}
	body, err := json.Marshal(map[string]string{"username": username, "password": string(password)})
	if err != nil {
		return "", rejected
	}
	u := *g.litellmURL
	u.Path = "/ai/v2/login"
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", rejected
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, Transport: liteLLMTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(request)
	if err != nil {
		return "", rejected
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", rejected
	}
	var result struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 32769))
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || len(result.Token) > 16384 {
		return "", rejected
	}
	parts := strings.Split(result.Token, ".")
	if len(parts) != 3 {
		return "", rejected
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || bytes.Contains(claims, password) || strings.Contains(result.Token, string(password)) {
		return "", rejected
	}
	var ui struct {
		Key  string `json:"key"`
		Role string `json:"user_role"`
		Exp  int64  `json:"exp"`
	}
	if json.Unmarshal(claims, &ui) != nil || ui.Role != "proxy_admin" || ui.Key == "" || ui.Exp <= g.Now().Unix() {
		return "", rejected
	}
	// The response is from the configured private LiteLLM origin, never a
	// client-supplied token. Preserve its native JWT and JS-readable cookie.
	return result.Token, nil
}

// LiteLLM's native admin UI key must never escape the online platform-session
// gate via the public model origin. Query the authoritative key row on every
// data request; an in-memory denylist would forget live UI keys on restart.
func (g *Gateway) checkLiteLLMExternalKey(r *http.Request) (int, string) {
	if g.Cfg.LiteLLMUI == nil || g.Cfg.LiteLLMUI.Login == nil {
		return 0, ""
	}
	login := g.Cfg.LiteLLMUI.Login
	filename := login.MasterKeyFile
	if filename == "" {
		filename = login.PasswordFile
	}
	master, err := readLiteLLMUISecret(filename)
	if err != nil {
		return 503, "common.unavailable"
	}
	hash := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
	u := *g.litellmURL
	u.Path, u.RawQuery = "/ai/key/info", fmt.Sprintf("key=%x", hash)
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return 503, "common.unavailable"
	}
	request.Header.Set("Authorization", "Bearer "+string(master))
	client := &http.Client{Timeout: 5 * time.Second, Transport: liteLLMTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(request)
	if err != nil {
		return 503, "common.unavailable"
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 404 {
		return 401, "common.unauthenticated"
	}
	if resp.StatusCode != 200 {
		return 503, "common.unavailable"
	}
	var result struct {
		Info *struct {
			TeamID *string `json:"team_id"`
		} `json:"info"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBodyBytes+1))
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Info == nil {
		return 503, "common.unavailable"
	}
	if result.Info.TeamID != nil && *result.Info.TeamID == "litellm-dashboard" {
		return 403, "common.forbidden"
	}
	return 0, ""
}
