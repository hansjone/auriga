package runtimeimport

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
	"github.com/hansjone/auriga/infra/agent/runtimebundle"
)

// transportBarrier is an owned loopback hop. ORAS sees only this listener;
// every outbound dial is pinned to the independently approved registry.
type transportBarrier struct {
	approved, repo, tag string
	manifest            string
	allowed             map[string]bool
	maxBytes            int64
	listener            net.Listener
	server              *http.Server
	client              *http.Client
	transport           *http.Transport
	ctx                 context.Context
	cancel              context.CancelFunc
	done                chan struct{}
	blocked             atomic.Bool
	mu                  sync.Mutex
	closing             bool
	active              sync.WaitGroup
	closeOnce           sync.Once
}

func newTransportBarrier(ctx context.Context, approved, repo, tag string, snapshot runtimebundle.LayoutSnapshot, manifest, config releaseverify.BlobDescriptor, layers []releaseverify.BlobDescriptor) (*transportBarrier, error) {
	if snapshot.TotalBytes <= 0 || snapshot.TotalBytes == math.MaxInt64 || ctx.Err() != nil {
		return nil, errors.New("transport cannot start")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("cannot create local transport listener")
	}
	barrierCtx, cancel := context.WithCancel(ctx)
	b := &transportBarrier{approved: approved, repo: repo, tag: tag, manifest: manifest.Digest,
		allowed: map[string]bool{}, maxBytes: snapshot.TotalBytes, listener: listener,
		ctx: barrierCtx, cancel: cancel, done: make(chan struct{})}
	for _, d := range append([]releaseverify.BlobDescriptor{manifest, config}, layers...) {
		b.allowed[d.Digest] = true
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	b.transport = &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", approved)
		}}
	b.client = &http.Client{Transport: b.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		defer close(b.done)
		_ = b.server.Serve(listener)
	}()
	return b, nil
}

func (b *transportBarrier) Address() string { return b.listener.Addr().String() }
func (b *transportBarrier) Blocked() bool   { return b.blocked.Load() }

func (b *transportBarrier) Close() {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closing = true
		b.mu.Unlock()
		b.cancel()
		_ = b.server.Close()
		b.active.Wait()
		<-b.done
		b.transport.CloseIdleConnections()
	})
}

func (b *transportBarrier) reject(w http.ResponseWriter) {
	b.blocked.Store(true)
	w.Header().Set("Connection", "close")
	http.Error(w, "registry transport rejected", http.StatusBadGateway)
}

func (b *transportBarrier) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	if b.closing {
		b.mu.Unlock()
		b.reject(w)
		return
	}
	b.active.Add(1)
	b.mu.Unlock()
	defer b.active.Done()
	if !b.validRequest(r) || r.ContentLength > b.maxBytes || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" {
		b.reject(w)
		return
	}
	upstreamCtx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(b.ctx, cancel)
	defer func() { stop(); cancel() }()
	u := &url.URL{Scheme: "http", Host: b.approved, Path: r.URL.Path, RawQuery: r.URL.RawQuery}
	var body io.Reader
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		body = http.MaxBytesReader(w, r.Body, b.maxBytes)
	}
	request, err := http.NewRequestWithContext(upstreamCtx, r.Method, u.String(), body)
	if err != nil {
		b.reject(w)
		return
	}
	request.Host = b.approved
	request.ContentLength = r.ContentLength
	for _, name := range []string{"Accept", "Content-Type", "Content-Range", "Docker-Content-Digest", "Range", "User-Agent"} {
		for _, value := range r.Header.Values(name) {
			request.Header.Add(name, value)
		}
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := b.client.Do(request)
	if err != nil {
		b.reject(w)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 300 && response.StatusCode < 400 || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusProxyAuthRequired || response.Header.Get("WWW-Authenticate") != "" || response.Header.Get("Proxy-Authenticate") != "" || response.Header.Get("Content-Encoding") != "" || response.ContentLength > b.maxBytes {
		b.reject(w)
		return
	}
	locations := response.Header.Values("Location")
	if len(locations) > 1 || response.StatusCode == http.StatusAccepted && len(locations) != 1 {
		b.reject(w)
		return
	}
	if len(locations) == 1 {
		rewritten, ok := b.rewriteLocation(locations[0], r, response.StatusCode)
		if !ok {
			b.reject(w)
			return
		}
		w.Header().Set("Location", rewritten)
	}
	for _, name := range []string{"Content-Type", "Docker-Content-Digest", "Docker-Upload-UUID", "ETag", "Range", "Content-Range"} {
		for _, value := range response.Header.Values(name) {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	n, copyErr := io.Copy(w, io.LimitReader(response.Body, b.maxBytes+1))
	if copyErr != nil || n > b.maxBytes {
		b.blocked.Store(true)
	}
}

func (b *transportBarrier) validRequest(r *http.Request) bool {
	if r.URL.IsAbs() || r.URL.Host != "" || r.URL.User != nil || r.URL.Fragment != "" || len(r.URL.Path) > len(b.repo)+256 || len(r.URL.RawQuery) > 4096 || !cleanTransportPath(r.URL) {
		return false
	}
	path := r.URL.Path
	if path == "/v2/" {
		return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.RawQuery == ""
	}
	base := "/v2/" + b.repo + "/"
	if !strings.HasPrefix(path, base) {
		return false
	}
	suffix := strings.TrimPrefix(path, base)
	if strings.HasPrefix(suffix, "blobs/uploads/") {
		id := strings.TrimPrefix(suffix, "blobs/uploads/")
		if id == "" {
			return r.Method == http.MethodPost && b.validQuery(r.URL.RawQuery, false)
		}
		return repoPart.MatchString(id) && (r.Method == http.MethodPatch || r.Method == http.MethodPut) && b.validQuery(r.URL.RawQuery, r.Method == http.MethodPut)
	}
	if strings.HasPrefix(suffix, "blobs/") {
		digest := strings.TrimPrefix(suffix, "blobs/")
		return b.allowed[digest] && (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.RawQuery == ""
	}
	if strings.HasPrefix(suffix, "manifests/") {
		ref := strings.TrimPrefix(suffix, "manifests/")
		return (ref == b.tag || ref == b.manifest) && (r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodPut) && r.URL.RawQuery == ""
	}
	return false
}

func (b *transportBarrier) validQuery(raw string, requireDigest bool) bool {
	if raw == "" {
		return !requireDigest
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return false
	}
	for name, values := range query {
		if len(values) != 1 || name != "_state" && name != "digest" {
			return false
		}
		if name == "digest" && !b.allowed[values[0]] {
			return false
		}
	}
	return !requireDigest || query.Has("digest")
}

func cleanTransportPath(u *url.URL) bool {
	if strings.Contains(u.EscapedPath(), "%") || strings.Contains(u.Path, "//") || !strings.HasPrefix(u.Path, "/") {
		return false
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func (b *transportBarrier) rewriteLocation(raw string, request *http.Request, status int) (string, bool) {
	if len(raw) == 0 || len(raw) > 4096 || strings.TrimSpace(raw) != raw {
		return "", false
	}
	location, err := url.Parse(raw)
	if err != nil || location.User != nil || location.Fragment != "" || location.Opaque != "" || strings.HasPrefix(raw, "//") || strings.Contains(location.EscapedPath(), "%") {
		return "", false
	}
	if location.Scheme != "" && (location.Scheme != "http" || location.Host != b.approved) || location.Scheme == "" && location.Host != "" {
		return "", false
	}
	for _, segment := range strings.Split(location.Path, "/") {
		if segment == "." || segment == ".." {
			return "", false
		}
	}
	base := &url.URL{Scheme: "http", Host: b.approved, Path: request.URL.Path, RawQuery: request.URL.RawQuery}
	resolved := base.ResolveReference(location)
	if resolved.Scheme != "http" || resolved.Host != b.approved || !cleanTransportPath(resolved) {
		return "", false
	}
	path := resolved.Path
	approvedBase := "/v2/" + b.repo + "/"
	switch status {
	case http.StatusAccepted:
		prefix := approvedBase + "blobs/uploads/"
		if !strings.HasPrefix(path, prefix) || !repoPart.MatchString(strings.TrimPrefix(path, prefix)) || !b.validQuery(resolved.RawQuery, false) {
			return "", false
		}
	case http.StatusCreated:
		blob := strings.TrimPrefix(path, approvedBase+"blobs/")
		manifest := strings.TrimPrefix(path, approvedBase+"manifests/")
		if !strings.HasPrefix(path, approvedBase) || !b.allowed[blob] && manifest != b.tag && manifest != b.manifest || resolved.RawQuery != "" {
			return "", false
		}
	default:
		return "", false
	}
	resolved.Host = b.Address()
	return resolved.String(), true
}
