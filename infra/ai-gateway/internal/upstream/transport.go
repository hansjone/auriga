// Package upstream implements a single, bounded OpenAI-compatible submission.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
)

type Config struct {
	AllowedHosts     []string
	MaxResponseBytes int64
	MaxEventBytes    int
}

// Client contains deployment policy only, never a credential or request.
type Client struct {
	allowed map[string]bool
	limits  modelprotocol.Limits
	lookup  func(context.Context, string) ([]netip.Addr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

var dnsName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func canonicalHost(host string) string {
	if ip, err := netip.ParseAddr(host); err == nil && ip.Zone() == "" {
		return ip.Unmap().String()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func New(c Config) (*Client, error) {
	if c.MaxResponseBytes < 1 || c.MaxResponseBytes > 1<<24 || c.MaxEventBytes < 1 || c.MaxEventBytes > modelprotocol.EventLimit {
		return nil, errors.New("invalid upstream configuration")
	}
	v := &Client{allowed: map[string]bool{}, limits: modelprotocol.Limits{MaxBytes: c.MaxResponseBytes, MaxEventBytes: c.MaxEventBytes, MaxEvents: int(c.MaxResponseBytes)}, dial: (&net.Dialer{}).DialContext}
	// The resolver method also takes a network; keep that detail private.
	v.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	for _, host := range c.AllowedHosts {
		h := canonicalHost(host)
		ip, err := netip.ParseAddr(h)
		if len(h) > 253 || (err != nil && !dnsName.MatchString(h)) || (err == nil && ip.Zone() != "") {
			return nil, errors.New("invalid upstream configuration")
		}
		v.allowed[h] = true
	}
	return v, nil
}

func fault(code string) *modelprotocol.Error {
	message := "The model provider is unavailable"
	if code == "ai.upstream_rejected" {
		message = "The model provider rejected the request"
	}
	if code == "ai.upstream_timeout" {
		message = "The model provider timed out"
	}
	return &modelprotocol.Error{Code: code, Message: message}
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Shared-address space includes several cloud metadata endpoints. Reject
	// IPv6 translation/tunnel prefixes too: they can encode forbidden IPv4.
	for _, prefix := range []string{"100.64.0.0/10", "0.0.0.0/8", "192.0.0.0/24", "168.63.129.16/32", "240.0.0.0/4", "fec0::/10", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "::/96"} { // gitleaks:allow
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

func (c *Client) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		var err error
		ips, err = c.lookup(ctx, host)
		if err != nil {
			return nil, fault("ai.upstream_unavailable")
		}
	}
	if len(ips) == 0 {
		return nil, fault("ai.upstream_unavailable")
	}
	for _, ip := range ips {
		if !ip.IsValid() || ip.Zone() != "" || (!c.allowed[host] && !publicIP(ip)) {
			return nil, fault("ai.upstream_unavailable")
		}
	}
	return ips, nil
}

func (c *Client) endpoint(base string) (*url.URL, error) {
	u, err := url.Parse(base)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Hostname() == "" {
		return nil, fault("ai.upstream_unavailable")
	}
	host := canonicalHost(u.Hostname())
	if ip, err := netip.ParseAddr(host); (err != nil && !dnsName.MatchString(host)) || (err == nil && ip.Zone() != "") {
		return nil, fault("ai.upstream_unavailable")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !c.allowed[host]) {
		return nil, fault("ai.upstream_unavailable")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	u.RawPath = ""
	return u, nil
}

func (c *Client) Transport(ctx context.Context, s relay.Submission, secret []byte) (relay.Exchange, error) {
	proof := make(chan struct{}) // Failure, Close and cancellation never close it.
	x := relay.Exchange{Stopped: proof}
	u, err := c.endpoint(s.Target.BaseURL)
	if err != nil || len(secret) == 0 || s.Target.UpstreamModel == "" || s.Target.ConnectTimeoutSeconds < 1 || s.Target.ConnectTimeoutSeconds > 300 || ctx.Err() != nil {
		return x, fault("ai.upstream_unavailable")
	}
	host := canonicalHost(u.Hostname())
	connect := time.Duration(s.Target.ConnectTimeoutSeconds) * time.Second
	checkCtx, cancel := context.WithTimeout(ctx, connect)
	_, err = c.resolve(checkCtx, host)
	cancel()
	if err != nil {
		return x, fault("ai.upstream_unavailable")
	}
	wire := s.Request
	wire.Model = s.Target.UpstreamModel
	b, err := json.Marshal(wire)
	if err != nil {
		return x, fault("ai.upstream_rejected")
	}
	if wire.GetStream() {
		var fields map[string]json.RawMessage
		if json.Unmarshal(b, &fields) != nil {
			return x, fault("ai.upstream_rejected")
		}
		fields["stream_options"] = json.RawMessage(`{"include_usage":true}`)
		b, err = json.Marshal(fields)
		if err != nil {
			return x, fault("ai.upstream_rejected")
		}
	}
	request, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(b))
	if err != nil {
		return x, fault("ai.upstream_unavailable")
	}
	request.GetBody = nil // No replay, including the standard transport retry path.
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Authorization", "Bearer "+string(secret))
	defer request.Header.Del("Authorization")
	written := make(chan struct{})
	request = request.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { close(written) }}))
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, TLSHandshakeTimeout: connect, MaxResponseHeaderBytes: 32768,
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			dialCtx, stop := context.WithTimeout(dialCtx, connect)
			defer stop()
			h, port, e := net.SplitHostPort(address)
			if e != nil || canonicalHost(h) != host {
				return nil, fault("ai.upstream_unavailable")
			}
			// Resolve again at the actual connection boundary, validate every
			// answer, then dial exactly one numeric IP (no second DNS lookup).
			ips, e := c.resolve(dialCtx, host)
			if e != nil {
				return nil, e
			}
			conn, e := c.dial(dialCtx, network, net.JoinHostPort(ips[0].String(), port))
			if e != nil {
				return nil, fault("ai.upstream_unavailable")
			}
			remote, ok := conn.RemoteAddr().(*net.TCPAddr)
			if !ok || (!c.allowed[host] && !publicIP(remote.AddrPort().Addr())) {
				_ = conn.Close()
				return nil, fault("ai.upstream_unavailable")
			}
			return conn, nil
		}}
	defer tr.CloseIdleConnections()
	// RoundTrip has no redirect handling, cookie jar, proxy environment or client
	// timeout. Only Core owns response first-byte, idle and total timeouts.
	response, err := tr.RoundTrip(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return x, fault("ai.upstream_unavailable")
	}
	// The writer must no longer borrow request headers when we erase auth.
	select {
	case <-written:
	case <-ctx.Done():
		_ = response.Body.Close()
		return x, fault("ai.upstream_unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close() // Never read, return or log an upstream error body.
		code := "ai.upstream_unavailable"
		if response.StatusCode < 500 {
			code = "ai.upstream_rejected"
		}
		if response.StatusCode == 408 || response.StatusCode == 504 {
			code = "ai.upstream_timeout"
		}
		payload, _ := json.Marshal(fault(code).Envelope(s.RequestID))
		if wire.GetStream() {
			payload = append(append([]byte("data: "), payload...), '\n', '\n')
		}
		x.Body = io.NopCloser(bytes.NewReader(payload))
		return x, nil
	}
	x.Body = newBody(ctx, response.Body, s, c.limits, proof)
	return x, nil
}
