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

// Compose trace handling after route-specific callbacks without wrapping the
// ResponseWriter or changing its streaming/upgrade interfaces.
type traceReverseProxy struct{ *httputil.ReverseProxy }

func (p *traceReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parent := edgeTraceparent(r.Context())
	if !validTraceparent(parent) {
		p.ReverseProxy.ServeHTTP(w, r)
		return
	}
	proxy := *p.ReverseProxy // callbacks and response headers are request-local
	modify, onError := proxy.ModifyResponse, proxy.ErrorHandler
	proxy.ModifyResponse = func(response *http.Response) error {
		if modify != nil {
			if err := modify(response); err != nil {
				return err
			}
		}
		responseParent := parent
		if values := response.Header.Values(headerTraceparent); len(values) == 1 && validTraceparent(values[0]) && values[0][3:35] == parent[3:35] {
			responseParent = values[0] // retain a valid same-trace server child
		}
		if response.Header == nil {
			response.Header = make(http.Header)
		}
		response.Header.Set(headerTraceparent, responseParent)
		response.Header.Set("X-Trace-Id", parent[3:35])
		// ReverseProxy copies upstream fields with Add. Remove only these two
		// preinstalled fields so the final copy emits one value for each.
		w.Header().Del(headerTraceparent)
		w.Header().Del("X-Trace-Id")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set(headerTraceparent, parent)
		w.Header().Set("X-Trace-Id", parent[3:35])
		if onError != nil {
			onError(w, r, err)
		} else {
			w.WriteHeader(http.StatusBadGateway)
		}
	}
	proxy.ServeHTTP(w, r)
}

func newReverseProxy(target *url.URL, tr http.RoundTripper, keepHost bool) *traceReverseProxy {
	return &traceReverseProxy{&httputil.ReverseProxy{
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
			slog.Default().WarnContext(r.Context(), "upstream request failed", "target", target.Redacted(), "path", r.URL.Path, "error", err)
			writeProblem(w, problem(http.StatusBadGateway, "edge.upstream_error", "Bad Gateway", "upstream unavailable", requestID(r)))
		},
	}}
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
	stripUntrustedEntryHeaders(req.Header)
	req.Header.Del("X-Auriga-CSRF")
	req.Header.Del("X-Auriga-CSRF-Bootstrap")
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
