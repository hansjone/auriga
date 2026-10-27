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
	"sync"
	"sync/atomic"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
)

type Config struct {
	AllowedHosts     []HostRule
	MaxResponseBytes int64
	MaxEventBytes    int
}

// HostRule trusts a deployment host except for loopback and metadata peers.
// Plain HTTP needs its own opt-in.
type HostRule struct {
	Host           string `json:"host"`
	AllowPlaintext bool   `json:"allowPlaintext"`
}

// Client contains deployment policy only, never a credential or request.
type Client struct {
	allowed map[string]HostRule
	limits  modelprotocol.Limits
	lookup  func(context.Context, string) ([]netip.Addr, error)
	dial    func(context.Context, string, string) (net.Conn, error)
}

var dnsName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func canonicalHost(host string) string {
	if ip, err := netip.ParseAddr(host); err == nil && ip.Zone() == "" {
		return ip.String()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func New(c Config) (*Client, error) {
	if c.MaxResponseBytes < 1 || c.MaxResponseBytes > 32<<20 || c.MaxEventBytes < 1 || c.MaxEventBytes > modelprotocol.EventLimit {
		return nil, errors.New("invalid upstream configuration")
	}
	v := &Client{allowed: map[string]HostRule{}, limits: modelprotocol.Limits{MaxBytes: c.MaxResponseBytes, MaxEventBytes: c.MaxEventBytes, MaxEvents: int(c.MaxResponseBytes)}, dial: (&net.Dialer{}).DialContext}
	// The resolver method also takes a network; keep that detail private.
	v.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	for _, rule := range c.AllowedHosts {
		h := canonicalHost(rule.Host)
		ip, err := netip.ParseAddr(h)
		if len(h) > 253 || (err != nil && !dnsName.MatchString(h)) || (err == nil && ip.Zone() != "") {
			return nil, errors.New("invalid upstream configuration")
		}
		if err == nil && !trustedIP(ip) {
			return nil, errors.New("invalid upstream configuration")
		}
		if _, duplicate := v.allowed[h]; duplicate {
			return nil, errors.New("invalid upstream configuration")
		}
		v.allowed[h] = rule
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
	return safeIP(ip) && !privateIP(ip)
}

func privateIP(ip netip.Addr) bool {
	return ip.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

func safeIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Strict defaults for unlisted hosts include transition encodings,
	// documentation ranges and benchmark/discard networks.
	for _, prefix := range []string{"0.0.0.0/8", "192.0.0.0/24", "168.63.129.16/32", "100.100.100.200/32", "fd00:ec2::254/128", "198.18.0.0/15", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32", "3fff::/20", "100::/64", "240.0.0.0/4", "fec0::/10", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "2001::/32", "::/96", "::ffff:0:0/96", "::ffff:0:0:0/96"} { // gitleaks:allow
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

func (c *Client) allows(host string, ip netip.Addr) bool {
	_, listed := c.allowed[host]
	if !listed {
		return publicIP(ip)
	}
	return trustedIP(ip)
}

// Use the same deployment trust boundary at construction and connection time.
func trustedIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return false
	}
	for _, metadata := range []string{"169.254.169.254", "fd00:ec2::254", "168.63.129.16", "100.100.100.200"} { // gitleaks:allow
		if ip == netip.MustParseAddr(metadata) {
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
		if !c.allows(host, ip) {
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
	if u.Scheme != "https" && (u.Scheme != "http" || !c.allowed[host].AllowPlaintext) {
		return nil, fault("ai.upstream_unavailable")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	u.RawPath = ""
	return u, nil
}

func (c *Client) Transport(ctx context.Context, s relay.Submission, secret []byte) (relay.Exchange, error) {
	proof := make(chan struct{})
	var stoppedOnce sync.Once
	stopped := func() { stoppedOnce.Do(func() { close(proof) }) }
	// No connection has been assigned before these checks complete.
	var assigned atomic.Bool
	defer func() {
		if !assigned.Load() {
			stopped()
		}
	}()
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
	written := make(chan struct{})
	var writtenOnce sync.Once
	request = request.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { assigned.Store(true) }, WroteRequest: func(httptrace.WroteRequestInfo) { writtenOnce.Do(func() { close(written) }) }}))
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, TLSHandshakeTimeout: connect, MaxResponseHeaderBytes: 32768,
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			dialCtx, stop := context.WithTimeout(dialCtx, connect)
			defer stop()
			h, port, e := net.SplitHostPort(address)
			if e != nil || canonicalHost(h) != host {
				return nil, fault("ai.upstream_unavailable")
			}
			// Resolve again at the connection boundary and validate every answer.
			// Address fallback is connection establishment, never HTTP replay.
			ips, e := c.resolve(dialCtx, host)
			if e != nil {
				return nil, e
			}
			for _, ip := range ips {
				conn, e := c.dial(dialCtx, network, net.JoinHostPort(ip.String(), port))
				if e != nil {
					if dialCtx.Err() != nil {
						break
					}
					continue
				}
				remote, ok := conn.RemoteAddr().(*net.TCPAddr)
				if !ok || !c.allows(host, remote.AddrPort().Addr()) || remote.AddrPort().Addr().Unmap() != ip.Unmap() {
					_ = conn.Close()
					return nil, fault("ai.upstream_unavailable")
				}
				return conn, nil
			}
			return nil, fault("ai.upstream_unavailable")
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
		request.Header.Del("Authorization") // The writer has finished borrowing it.
	case <-ctx.Done():
		_ = response.Body.Close()
		return x, fault("ai.upstream_unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Only a fully consumed and closed error response proves that the
		// provider finished. Discard within the same deployment byte bound;
		// truncation, cancellation and I/O errors leave execution ambiguous.
		n, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, c.limits.MaxBytes+1))
		closeErr := response.Body.Close()
		if readErr == nil && closeErr == nil && n <= c.limits.MaxBytes {
			stopped()
		}
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
