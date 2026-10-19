package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

func newReverseProxy(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, problem(http.StatusBadGateway, "edge.upstream_error", "Bad Gateway", "upstream unavailable", requestID(r)))
		},
	}
}

func rewriteOutboundAPI(req *http.Request, clientIP, bearer, requestID, traceparent string) {
	// Strip browser session cookies; keep non-auriga cookies only if any remain.
	if c := req.Header.Get("Cookie"); c != "" {
		stripped := stripAurigaCookies(c)
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
		stripped := stripAurigaCookies(c)
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
