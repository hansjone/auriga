package ingress

import (
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"
)

const identityHeader = "Auriga-Identity"

// stripAndForwardIdentity removes any client-supplied Auriga-Identity then
// restores the value that arrived from edge after mTLS (captured earlier).
func stripAndForwardIdentity(req *http.Request, edgeIdentity string) {
	req.Header.Del(identityHeader)
	// Also strip uncommon casings that Go's Header map may not normalize if set oddly.
	for k := range req.Header {
		if strings.EqualFold(k, identityHeader) {
			req.Header.Del(k)
		}
	}
	if edgeIdentity != "" {
		req.Header.Set(identityHeader, edgeIdentity)
	}
}

func (s *Server) newInstanceProxy(inst *Instance) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(inst.Upstream)
			pr.Out.Host = inst.Upstream.Host
			fwd := SelectForwardIdentity(pr.In.Header.Values(identityHeader), s.cfg.IdentityKeys, s.cfg.Now, inst.ID)
			stripAndForwardIdentity(pr.Out, fwd)
		},
		Transport: s.transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, http.StatusBadGateway, CodeInternal, "Bad Gateway", "upstream unavailable")
		},
	}
}

// serveInstanceProxy handles /i/<id>/...
func (s *Server) serveInstanceProxy(w http.ResponseWriter, r *http.Request, instanceID, rest string) {
	if len(s.cfg.IdentityKeys) == 0 {
		writeProblem(w, http.StatusServiceUnavailable, CodeUnauthorized, "Unavailable", "identity verification keys required")
		return
	}
	inst, ok := s.cfg.Registry.Lookup(instanceID)
	if !ok || inst.Upstream == nil {
		writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "instance not found")
		return
	}
	// Rewrite path to what the instance sees (strip /i/<id>).
	r.URL.Path = "/" + strings.TrimPrefix(rest, "/")
	if r.URL.Path == "/" && rest == "" {
		r.URL.Path = "/"
	}
	r.URL.RawPath = ""
	s.newInstanceProxy(inst).ServeHTTP(w, r)
}

func newProxyTransport() *http.Transport {
	return &http.Transport{
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 64, MaxIdleConnsPerHost: 16,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
