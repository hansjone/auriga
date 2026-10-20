package ingress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config configures the edge-facing ingress server.
type Config struct {
	// ListenAddr is host:port for multi-node (empty means local socket/pipe only).
	ListenAddr string
	// LocalPath is unix socket path or Windows pipe name for single-node.
	LocalPath string
	// ServerCert is the node certificate (presented to edge).
	ServerCert tls.Certificate
	// PlatformCA verifies edge client certificates.
	PlatformCA   *x509.CertPool
	Registry     Registry
	Tickets      *TicketVerifier
	IdentityKeys IdentityKeys
	Executor     IdentityExecutor
	Logger       *slog.Logger
	Now          func() time.Time
	// MaxLogBytes per instance rolling log (default 50 MiB).
	MaxLogBytes int64
	// File operation limits (defaults: 64 MiB upload, 256 MiB zip, 10000 entries).
	MaxUploadBytes int64
	MaxZipBytes    int64
	MaxZipEntries  int
}

// Server is the mTLS ingress for edge.
type Server struct {
	transport *http.Transport
	cfg       Config
	mux       *http.ServeMux
	httpSrv   *http.Server
	metrics   *metrics
	mu        sync.Mutex
	listeners []net.Listener
}

// New builds a Server (does not listen yet).
func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxLogBytes <= 0 {
		cfg.MaxLogBytes = 50 << 20
	}
	if cfg.Executor == nil {
		cfg.Executor = DirectExecutor{}
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = 64 << 20
	}
	if cfg.MaxZipBytes <= 0 {
		cfg.MaxZipBytes = 256 << 20
	}
	if cfg.MaxZipEntries <= 0 {
		cfg.MaxZipEntries = 10000
	}
	s := &Server{cfg: cfg, metrics: newMetrics(), transport: newProxyTransport()}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/", s.route)
	s.mux.Handle("/metrics", s.metrics.handler())
	s.httpSrv = &http.Server{
		Handler:           s.withEdgeGate(s.mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func (s *Server) withEdgeGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All paths (including /metrics) require edge mTLS when TLS is used.
		if r.TLS != nil && !requireEdgePeer(r) {
			writeProblem(w, http.StatusUnauthorized, CodeUnauthorized, "Unauthorized", "edge client certificate required")
			return
		}
		s.metrics.requests.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/i/"):
		id, rest := splitID(strings.TrimPrefix(path, "/i/"))
		if id == "" {
			writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "missing instance id")
			return
		}
		s.serveInstanceProxy(w, r, id, rest)
	case strings.HasPrefix(path, "/files/"):
		id, rest := splitID(strings.TrimPrefix(path, "/files/"))
		if id == "" {
			writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "missing instance id")
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		s.serveFiles(rec, r, id, rest)
	case strings.HasPrefix(path, "/logs/"):
		id, rest := splitID(strings.TrimPrefix(path, "/logs/"))
		if id == "" || rest != "" {
			writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "expected /logs/<id>")
			return
		}
		s.serveLogs(w, r, id)
	default:
		writeProblem(w, http.StatusNotFound, CodeInstanceNotFound, "Not Found", "unknown path")
	}
}

func splitID(rest string) (id, remainder string) {
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", ""
	}
	i := strings.IndexByte(rest, '/')
	if i < 0 {
		return rest, ""
	}
	return rest[:i], rest[i+1:]
}

// Start listens on configured endpoints and serves until ctx cancel or Close.
func (s *Server) Start(ctx context.Context) error {
	tlsCfg := TLSConfig(s.cfg.ServerCert, s.cfg.PlatformCA)
	var firstErr error
	add := func(ln net.Listener) {
		s.mu.Lock()
		s.listeners = append(s.listeners, ln)
		s.mu.Unlock()
		go func() {
			err := s.httpSrv.Serve(tls.NewListener(ln, tlsCfg))
			if err != nil && err != http.ErrServerClosed {
				s.cfg.Logger.Error("ingress serve", "err", err)
			}
		}()
	}

	if s.cfg.ListenAddr != "" {
		ln, err := net.Listen("tcp", s.cfg.ListenAddr)
		if err != nil {
			return fmt.Errorf("listen tcp: %w", err)
		}
		add(ln)
	}
	if s.cfg.LocalPath != "" {
		ln, err := ListenLocal(s.cfg.LocalPath)
		if err != nil {
			if s.cfg.ListenAddr == "" {
				return err
			}
			s.cfg.Logger.Warn("local listen failed", "err", err)
			firstErr = err
		} else {
			add(ln)
		}
	}
	s.mu.Lock()
	n := len(s.listeners)
	s.mu.Unlock()
	if n == 0 {
		if firstErr != nil {
			return firstErr
		}
		return fmt.Errorf("ingress: no listen address configured")
	}
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	return nil
}

// Addr returns the first TCP listener address (for tests).
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ln := range s.listeners {
		if a := ln.Addr(); a.Network() == "tcp" {
			return a
		}
	}
	if len(s.listeners) > 0 {
		return s.listeners[0].Addr()
	}
	return nil
}

// Close shuts down the HTTP server and listeners.
func (s *Server) Close() error {
	s.transport.CloseIdleConnections()
	defer s.transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.httpSrv.Shutdown(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ln := range s.listeners {
		_ = ln.Close()
	}
	s.listeners = nil
	return err
}

// Handler exposes the HTTP handler for httptest tests without TLS.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ServeTLSConn is used by tests that dial with a custom tls.Config.
func (s *Server) ServeTLSForTest(ln net.Listener) error {
	tlsCfg := TLSConfig(s.cfg.ServerCert, s.cfg.PlatformCA)
	s.mu.Lock()
	s.listeners = append(s.listeners, ln)
	s.mu.Unlock()
	return s.httpSrv.Serve(tls.NewListener(ln, tlsCfg))
}
