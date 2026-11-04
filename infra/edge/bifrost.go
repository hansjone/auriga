package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type RelayConfig struct {
	Origin            string `json:"origin"`
	AdminPasswordFile string `json:"adminPasswordFile"`
}

func (c Config) parseRelayOrigin() (*url.URL, error) {
	if c.Relay == nil {
		return nil, nil
	}
	raw := c.Relay.Origin
	u, err := url.Parse(raw)
	if err != nil || !c.Access.Valid() || c.Relay.AdminPasswordFile == "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return nil, fmt.Errorf("invalid Bifrost origin")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || u.Hostname() != "bifrost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("relay requires HTTPS or private deployment service")
		}
	}
	return u, nil
}

var relayTransport = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 165 * time.Second, IdleConnTimeout: 60 * time.Second, MaxIdleConnsPerHost: 32}

// A member VK must never bypass the live IAM/org checks at ai-gateway. There is
// no local allow cache: quota lookup uses the caller's VK on every public call.
func (g *Gateway) checkExternalRelayKey(r *http.Request) (int, string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.relayURL.String()+"/internal/keys/check", nil)
	if err != nil {
		return 503, "common.unavailable"
	}
	request.Header.Set("x-bf-vk", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	client := &http.Client{Transport: relayTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return 503, "common.unavailable"
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == 401 || response.StatusCode == 404 {
		return 401, "common.unauthenticated"
	}
	if response.StatusCode == 403 {
		return 403, "common.forbidden"
	}
	if response.StatusCode != 200 {
		return 503, "common.unavailable"
	}
	var quota struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
		Source string `json:"source"`
	}
	d := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if d.Decode(&quota) != nil || d.Decode(new(any)) != io.EOF || quota.Name == "" {
		return 503, "common.unavailable"
	}
	if !quota.Active || quota.Source != "external" || strings.HasPrefix(quota.Name, "auriga-member:") {
		return 403, "common.forbidden"
	}
	return 0, ""
}

func (g *Gateway) proxyExternalRelay(w http.ResponseWriter, r *http.Request, audience, rid string) {
	fail := func(status int, code string) {
		writeProblem(w, problem(status, code, http.StatusText(status), "relay request rejected", rid))
	}
	entry, ok := r.Context().Value(accessEntryKey{}).(accessEntry)
	if !ok || audience != "portal" || entry.role != "portal" {
		fail(403, "common.forbidden")
		return
	}
	method := ""
	switch r.URL.Path {
	case "/ai/v1/models":
		method = http.MethodGet
	case "/ai/v1/chat/completions":
		method = http.MethodPost
	default:
		fail(404, "common.not_found")
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		fail(405, "common.validation_failed")
		return
	}
	if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		fail(400, "common.validation_failed")
		return
	}
	auth := r.Header.Values("Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer sk-bf-") || len(auth[0]) <= len("Bearer sk-bf-") || len(auth[0]) > 16384 || strings.ContainsAny(strings.TrimPrefix(auth[0], "Bearer "), " ,\r\n\t") {
		fail(401, "common.unauthenticated")
		return
	}
	if !g.Cfg.bearerOriginAllowed(r) {
		fail(403, "common.forbidden")
		return
	}
	if method == http.MethodPost {
		var fields map[string]json.RawMessage
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRelayChatBodyBytes))
		if d.Decode(&fields) != nil || fields == nil || d.Decode(new(any)) != io.EOF {
			fail(400, "common.validation_failed")
			return
		}
		allowed := map[string]bool{}
		for _, name := range []string{"model", "messages", "stream", "stream_options", "max_tokens", "max_completion_tokens", "temperature", "top_p", "tools", "tool_choice", "response_format", "seed", "stop", "n", "presence_penalty", "frequency_penalty", "logit_bias", "logprobs", "top_logprobs", "parallel_tool_calls", "reasoning_effort", "service_tier", "user", "metadata"} {
			allowed[name] = true
		}
		for name := range fields {
			if !allowed[name] {
				fail(400, "common.validation_failed")
				return
			}
		}
		fingerprint := sha256.Sum256([]byte(strings.TrimPrefix(auth[0], "Bearer ")))
		fields["user"], _ = json.Marshal(fmt.Sprintf("external:%x", fingerprint))
		fields["metadata"] = json.RawMessage(`{"source":"external"}`)
		body, err := json.Marshal(fields)
		if err != nil {
			fail(400, "common.validation_failed")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength, r.GetBody = int64(len(body)), nil
	} else {
		r.Body, r.ContentLength = http.NoBody, 0
	}
	if status, code := g.checkExternalRelayKey(r); status != 0 {
		fail(status, code)
		return
	}
	proxy := newReverseProxy(g.relayURL, staticIdleTransport{base: relayTransport, timeout: staticBodyTimeout}, false)
	proxy.Rewrite = func(pr *httputil.ProxyRequest) {
		pr.SetURL(g.relayURL)
		pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/ai")
		pr.Out.URL.RawPath = ""
		pr.Out.Header = make(http.Header)
		pr.Out.Header.Set("Authorization", auth[0])
		pr.Out.Header.Set("Idempotency-Key", pr.In.Header.Get("Idempotency-Key"))
		pr.Out.Header.Set("Content-Type", "application/json")
		pr.Out.Header.Set("Accept", pr.In.Header.Get("Accept"))
		pr.Out.Header.Set("X-Request-Id", rid)
		pr.Out.Header.Set("traceparent", ensureTraceparent(r))
	}
	proxy.ModifyResponse = relayResponse
	proxy.ServeHTTP(w, r)
}

func relayResponse(response *http.Response) error {
	for name := range response.Header {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			response.Header.Del(name)
		}
	}
	response.Header.Del("Set-Cookie")
	response.Header.Set("Cache-Control", "no-store")
	response.Header.Set("X-Content-Type-Options", "nosniff")
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return errors.New("relay redirect rejected")
	}
	return nil
}
