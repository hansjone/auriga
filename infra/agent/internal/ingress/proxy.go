package ingress

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
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
			delete(req.Header, k)
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
			if inst.Shared {
				fwd = pr.In.Header.Get(identityHeader)
			}
			stripAndForwardIdentity(pr.Out, fwd)
		},
		Transport: s.transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Default().Warn("instance upstream failed", "path", r.URL.Path, "error", err)
			writeProblem(w, http.StatusBadGateway, CodeInternal, "Bad Gateway", "upstream unavailable")
		},
	}
}

// serveInstanceProxy handles /i/<id>/...
func (s *Server) serveInstanceProxy(w http.ResponseWriter, r *http.Request, instanceID, rest string) {
	if len(s.cfg.IdentityKeys) == 0 && s.cfg.Shared == nil {
		writeProblem(w, http.StatusServiceUnavailable, CodeUnauthorized, "Unavailable", "identity verification keys required")
		return
	}
	inst, ok := s.cfg.Registry.Lookup(instanceID)
	if !ok || inst.Upstream == nil {
		writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "instance not found")
		return
	}
	if s.cfg.Shared != nil {
		registration, available := s.cfg.Shared.Authority.(interface {
			IsShared(context.Context, string) (bool, error)
		})
		if available {
			registered, err := registration.IsShared(r.Context(), instanceID)
			if err != nil || registered != inst.Shared {
				writeProblem(w, 503, "common.unavailable", "Unavailable", "pool authority and local process disagree")
				return
			}
		} else if !inst.Shared {
			// An unavailable registration authority must never choose owner entry.
			writeProblem(w, 503, "common.unavailable", "Unavailable", "pool registration authority required")
			return
		}
	}
	if !inst.Shared && len(s.cfg.IdentityKeys) == 0 {
		writeProblem(w, http.StatusServiceUnavailable, CodeUnauthorized, "Unavailable", "identity verification keys required")
		return
	}
	canonical := strings.TrimPrefix(r.URL.EscapedPath(), "/i/"+instanceID)
	if canonical == "" {
		canonical = "/"
	}
	if inst.Shared {
		if canonical == "/_auriga/shared-profile" {
			s.serveSharedProfile(w, r, inst)
			return
		}
		checked, cleanup, err := s.sharedRequest(r, instanceID, canonical)
		if err != nil {
			status := 403
			code := CodeUnauthorized
			if err.Error() == "common.unavailable" {
				status = 503
				code = "common.unavailable"
			}
			writeProblem(w, status, code, http.StatusText(status), "shared identity rejected")
			return
		}
		defer cleanup()
		r = checked
	}
	// Rewrite path to what the instance sees (strip /i/<id>).
	r.URL.Path = "/" + strings.TrimPrefix(rest, "/")
	if r.URL.Path == "/" && rest == "" {
		r.URL.Path = "/"
	}
	r.URL.RawPath = canonical
	s.newInstanceProxy(inst).ServeHTTP(w, r)
}

// Edge already verified W's short-lived probe. This route is available while
// placement is pending, but only after the ordinary Edge mTLS peer gate and
// only for the registered actual pool Host. It cannot proxy user credentials.
func (s *Server) serveSharedProfile(w http.ResponseWriter, r *http.Request, inst *Instance) {
	if r.TLS == nil || !requireEdgePeer(r) || r.Method != "GET" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Upgrade") != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeProblem(w, 403, CodeUnauthorized, "Forbidden", "invalid profile probe")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	target := *inst.Upstream
	target.Path, target.RawPath, target.RawQuery, target.Fragment = "/_auriga/shared-profile", "", "", ""
	request, err := http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		writeProblem(w, 502, CodeInternal, "Unavailable", "profile unavailable")
		return
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := s.transport.RoundTrip(request)
	if err != nil {
		writeProblem(w, 502, CodeInternal, "Unavailable", "profile unavailable")
		return
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(data) > 16384 || response.StatusCode != 200 || strings.Split(response.Header.Get("Content-Type"), ";")[0] != "application/json" || response.Header.Get("Set-Cookie") != "" || response.Header.Get("Location") != "" || !json.Valid(data) {
		writeProblem(w, 502, CodeInternal, "Unavailable", "profile unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func newProxyTransport() *http.Transport {
	return &http.Transport{
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 64, MaxIdleConnsPerHost: 16,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
