package main

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Shared reverse-proxy transports (reused across requests).
var apiProxyTransport http.RoundTripper = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   32,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

func newReverseProxy(target *url.URL, tr http.RoundTripper, keepHost bool) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			// Only platform APIs need the browser Host for IAM tenant/audience routing.
			if keepHost && pr.In.Host != "" {
				pr.Out.Host = pr.In.Host
			}
		},
		Transport: tr,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Default().Warn("upstream request failed", "target", target.Redacted(), "path", r.URL.Path, "error", err)
			writeProblem(w, problem(http.StatusBadGateway, "edge.upstream_error", "Bad Gateway", "upstream unavailable", requestID(r)))
		},
	}
}

func rewriteOutboundAPI(req *http.Request, clientIP, bearer, requestID, traceparent string) {
	// Strip browser session cookies; keep non-auriga cookies only if any remain.
	if c := req.Header.Get("Cookie"); c != "" {
		stripped := stripAurigaCookies(c, true)
		if stripped == "" {
			req.Header.Del("Cookie")
		} else {
			req.Header.Set("Cookie", stripped)
		}
	}
	// Always drop browser session cookie names explicitly via strip; also drop Authorization from browser then set Bearer.
	req.Header.Del("Authorization")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	SetClientIPHeader(req.Header, clientIP)
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	if traceparent != "" {
		req.Header.Set("traceparent", traceparent)
	}
}

func rewriteOutboundInstance(req *http.Request, clientIP, identity, requestID, traceparent string) {
	req.Header.Del("Auriga-Identity")
	req.Header.Del("Authorization")
	if c := req.Header.Get("Cookie"); c != "" {
		stripped := stripAurigaCookies(c, false)
		if stripped == "" {
			req.Header.Del("Cookie")
		} else {
			req.Header.Set("Cookie", stripped)
		}
	} else {
		req.Header.Del("Cookie")
	}
	req.Header.Set("Auriga-Identity", identity)
	SetClientIPHeader(req.Header, clientIP)
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	if traceparent != "" {
		req.Header.Set("traceparent", traceparent)
	}
}

func parseUpstream(raw string) (*url.URL, error) {
	return url.Parse(raw)
}
