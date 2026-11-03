package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	connectTimeout = 10 * time.Second
	idleTimeout    = 5 * time.Minute
	tunnelLifetime = 30 * time.Minute
)

type dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Server serves public HTTPS tunnels without seeing TLS or provider credentials.
// It is reachable only from the node's isolated instance networks, never a host
// published port. Stop explicitly closes hijacked connections as well as HTTP.
type Server struct {
	dns         resolver
	dial        dialer
	mu          sync.Mutex
	closed      bool
	live        map[*tunnel]struct{}
	deniedHosts []string
	tunnels     sync.WaitGroup
}

// SetDeniedHosts installs trusted deployment origins before serving. Public
// management origins are denied too, even when their DNS answers are public.
func (s *Server) SetDeniedHosts(csv string) error {
	var hosts []string
	for _, value := range strings.Split(csv, ",") {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if address, err := netip.ParseAddr(host); err == nil {
			host = address.Unmap().String()
		} else if checked, err := targetHost(net.JoinHostPort(host, "443")); err != nil {
			return errors.New("invalid egress protected host")
		} else {
			host = checked
		}
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 || len(hosts) > 64 {
		return errors.New("invalid egress protected hosts")
	}
	s.deniedHosts = hosts
	return nil
}

func New() *Server {
	return &Server{dns: net.DefaultResolver, dial: &net.Dialer{Timeout: connectTimeout}, live: make(map[*tunnel]struct{})}
}

// WithDNSServer optionally isolates this application from split/fake-IP DNS.
// No system resolver is changed. An explicit resolver never falls back to the
// ambient one. The endpoint is trusted administrator configuration (including
// a private LAN resolver); only request target answers must be public.
func WithDNSServer(endpoint string) (*Server, error) {
	s := New()
	if endpoint == "" {
		return s, nil
	}
	host, port, err := net.SplitHostPort(endpoint)
	address, parseErr := netip.ParseAddr(host)
	number, portErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || portErr != nil || number < 1 || number > 65535 || address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" {
		return nil, errors.New("invalid egress DNS endpoint")
	}
	endpoint = net.JoinHostPort(address.Unmap().String(), strconv.Itoa(number))
	s.dns = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: connectTimeout}).DialContext(ctx, network, endpoint)
	}}
	return s, nil
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{Handler: s, ReadHeaderTimeout: connectTimeout, ReadTimeout: connectTimeout, WriteTimeout: connectTimeout, IdleTimeout: connectTimeout, MaxHeaderBytes: 16 << 10}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			s.closeTunnels()
			_ = server.Close()
		case <-done:
		}
	}()
	err := server.Serve(listener)
	s.closeTunnels()
	s.tunnels.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) closeTunnels() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for tunnel := range s.live {
		tunnel.close()
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/health" && r.URL.RawQuery == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	if r.Host != r.URL.Host || r.URL.Scheme != "" || r.URL.Opaque != "" || r.URL.Path != "" || r.URL.RawQuery != "" || r.ContentLength > 0 || len(r.TransferEncoding) != 0 {
		http.Error(w, "target denied", http.StatusForbidden)
		return
	}
	host, err := targetHost(r.URL.Host)
	if err != nil {
		http.Error(w, "target denied", http.StatusForbidden)
		return
	}
	for _, protected := range s.deniedHosts {
		if host == protected || strings.HasSuffix(host, "."+protected) {
			http.Error(w, "target denied", http.StatusForbidden)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), connectTimeout)
	defer cancel()
	targets, err := publicTargets(ctx, s.dns, host)
	if err != nil {
		http.Error(w, "target denied", http.StatusForbidden)
		return
	}
	var upstream net.Conn
	for _, target := range targets {
		// The validated address, never the original hostname, is dialled. A DNS
		// change after this lookup cannot redirect the connection into a LAN.
		upstream, err = s.dial.DialContext(ctx, "tcp", target)
		if err == nil {
			break
		}
	}
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "tunnel unavailable", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	t := &tunnel{client: client, upstream: upstream, deadline: time.Now().Add(tunnelLifetime)}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.live[t] = struct{}{}
	s.tunnels.Add(1)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.live, t)
		s.mu.Unlock()
		s.tunnels.Done()
	}()
	t.touch()
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	// Preserve TLS bytes already read beyond the CONNECT headers.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, copyErr := io.Copy(activityWriter{upstream, t}, activityReader{buffered, t}); copyErr != nil {
			t.close()
		} else {
			closeWrite(upstream)
		}
	}()
	if _, copyErr := io.Copy(activityWriter{client, t}, activityReader{upstream, t}); copyErr != nil {
		t.close()
	} else {
		closeWrite(client)
	}
	<-done
}

type tunnel struct {
	client, upstream net.Conn
	deadline         time.Time
	mu               sync.Mutex
}

func (t *tunnel) touch() {
	t.mu.Lock()
	defer t.mu.Unlock()
	deadline := time.Now().Add(idleTimeout)
	if t.deadline.Before(deadline) {
		deadline = t.deadline
	}
	// Activity in either direction keeps both halves alive; a long streamed
	// response must not expire merely because its request body already ended.
	_ = t.client.SetDeadline(deadline)
	_ = t.upstream.SetDeadline(deadline)
}

func (t *tunnel) close() {
	_ = t.client.Close()
	_ = t.upstream.Close()
}

type activityReader struct {
	io.Reader
	tunnel *tunnel
}

func (r activityReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.tunnel.touch()
	}
	return n, err
}

type activityWriter struct {
	io.Writer
	tunnel *tunnel
}

func (w activityWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n > 0 {
		w.tunnel.touch()
	}
	return n, err
}

func closeWrite(connection net.Conn) {
	if half, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
	}
}
