package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultNetworkWSSPath is the portal-facing NATS-over-WSS upgrade path (N1).
const DefaultNetworkWSSPath = "/net/v1/ws"

// NetworkWSSConfig terminates TLS/Origin at edge and reverse-proxies Upgrade to
// nats-server's websocket listener. NATS CONNECT / auth callout stay on the
// Broker; this path never trusts payload-claimed tenant/node.
type NetworkWSSConfig struct {
	Enabled        bool     `json:"enabled"`
	Path           string   `json:"path"`
	UpstreamURL    string   `json:"upstreamUrl"` // http(s)://host:port/ or ws(s)://…
	CAFile         string   `json:"caFile"`      // optional dedicated upstream trust bundle; empty = system roots
	ServerName     string   `json:"serverName"`  // optional certificate name; empty = upstream hostname
	MaxConcurrent  int      `json:"maxConcurrent"`
	AllowedOrigins []string `json:"allowedOrigins"` // empty → use portalHost when wired
}

// NetworkWSS is the precise upgrade router for the network data plane.
type NetworkWSS struct {
	Path           string
	AllowedOrigins []string
	proxy          *httputil.ReverseProxy
	maxConcurrent  int
	active         atomic.Int64
}

func newNetworkWSS(cfg NetworkWSSConfig, portalHost string) (*NetworkWSS, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.UpstreamURL == "" {
		return nil, fmt.Errorf("networkWss.upstreamUrl is required when enabled")
	}
	u, err := url.Parse(cfg.UpstreamURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") {
		return nil, fmt.Errorf("networkWss.upstreamUrl must be an HTTP(S)/WS(S) URL without credentials or fragment")
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	}
	tlsConfig, err := networkWSSTLS(cfg, u.Scheme)
	if err != nil {
		return nil, err
	}
	path := cfg.Path
	if path == "" {
		path = DefaultNetworkWSSPath
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("networkWSS.path must start with /")
	}
	max := cfg.MaxConcurrent
	if max <= 0 {
		max = 256
	}
	origins := append([]string{}, cfg.AllowedOrigins...)
	if len(origins) == 0 && portalHost != "" {
		origins = []string{"https://" + portalHost, "http://" + portalHost}
	}
	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// No ResponseHeaderTimeout: websocket upgrades stay open.
		MaxIdleConns:        32,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     tlsConfig,
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.Out.Host = u.Host
			// NATS WSS path is usually "/" on the Broker listener; keep query.
			if u.Path == "" || u.Path == "/" {
				pr.Out.URL.Path = "/"
			}
			stripUntrustedEntryHeaders(pr.Out.Header)
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Auriga-Identity")
		},
		Transport: tr,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, problem(http.StatusBadGateway, "edge.upstream_error", "Bad Gateway", "network wss upstream unavailable", requestID(r)))
		},
	}
	return &NetworkWSS{
		Path:           path,
		AllowedOrigins: origins,
		proxy:          proxy,
		maxConcurrent:  max,
	}, nil
}

func (n *NetworkWSS) Match(path string) bool {
	return n != nil && path == n.Path
}

func (n *NetworkWSS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := requestID(r)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeProblem(w, problem(http.StatusMethodNotAllowed, "edge.bad_method", "Method Not Allowed", "network wss requires GET", rid))
		return
	}
	if !isUpgradeRequest(r) {
		writeProblem(w, problem(http.StatusBadRequest, "edge.upgrade_required", "Bad Request", "Upgrade: websocket required", rid))
		return
	}
	if err := checkNetworkWSSOrigin(r, n.AllowedOrigins); err != nil {
		writeProblem(w, problem(http.StatusForbidden, "edge.bad_origin", "Forbidden", err.Error(), rid))
		return
	}
	cur := n.active.Add(1)
	defer n.active.Add(-1)
	if int(cur) > n.maxConcurrent {
		writeProblem(w, problem(http.StatusServiceUnavailable, "edge.overloaded", "Service Unavailable", "network wss concurrency limit", rid))
		return
	}
	n.proxy.ServeHTTP(w, r)
}

func checkNetworkWSSOrigin(r *http.Request, allowed []string) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return fmt.Errorf("origin required")
	}
	if len(allowed) == 0 {
		return fmt.Errorf("no allowed origins configured")
	}
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(origin, "/")) {
			return nil
		}
	}
	return fmt.Errorf("origin not allowed")
}
