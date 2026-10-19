package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Server runs the HTTP(S) listener. Tests inject a custom BaseContext / listener.
type Server struct {
	Cfg     Config
	Handler http.Handler
	Log     *slog.Logger
	// optional overrides for tests
	Listen   func(network, address string) (net.Listener, error)
	Serve    func(ln net.Listener, h http.Handler) error
	ServeTLS func(ln net.Listener, h http.Handler, certFile, keyFile string) error
}

func (s *Server) Run(ctx context.Context) error {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	listenFn := s.Listen
	if listenFn == nil {
		listenFn = net.Listen
	}
	ln, err := listenFn("tcp", s.Cfg.Listen)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	srv := &http.Server{
		Handler:           s.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No ReadTimeout: large/slow instance uploads must not be cut at 30s.
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
		BaseContext:    func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	mode := strings.ToLower(s.Cfg.TLS.Mode)
	if mode == "" {
		mode = "off"
	}
	s.Log.Info("edge listening", "addr", ln.Addr().String(), "tls", mode)

	switch mode {
	case "off":
		serve := s.Serve
		if serve == nil {
			serve = func(ln net.Listener, h http.Handler) error {
				srv.Handler = h
				return srv.Serve(ln)
			}
		}
		err = serve(ln, s.Handler)
	case "files":
		serveTLS := s.ServeTLS
		if serveTLS == nil {
			serveTLS = func(ln net.Listener, h http.Handler, certFile, keyFile string) error {
				srv.Handler = h
				srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
				return srv.ServeTLS(ln, certFile, keyFile)
			}
		}
		err = serveTLS(ln, s.Handler, s.Cfg.TLS.CertFile, s.Cfg.TLS.KeyFile)
	case "certmagic":
		err = s.serveCertMagic(ctx, ln, srv)
	default:
		err = fmt.Errorf("unknown tls mode %q", mode)
	}
	if err == http.ErrServerClosed || err == context.Canceled {
		return nil
	}
	return err
}

// serveCertMagic is intentionally conservative: without a real ACME path in CI,
// we validate config and refuse to perform live issuance unless AURIGA_EDGE_CERTMAGIC_ENABLE=1.
// Production installs set that env (or use external reverse proxy / DNS-01 via installer later).
func (s *Server) serveCertMagic(ctx context.Context, ln net.Listener, srv *http.Server) error {
	if osGetenv("AURIGA_EDGE_CERTMAGIC_ENABLE") != "1" {
		return fmt.Errorf("tls.mode=certmagic requires AURIGA_EDGE_CERTMAGIC_ENABLE=1 (use tls.mode=off behind an external reverse proxy, or files, in CI/dev); domains=%v", s.Cfg.TLS.Domains)
	}
	// Lazy import avoidance: use standard library GetCertificate placeholder until installer wires ACME.
	// Real CertMagic wiring is left for installer/E2; interface is ready.
	_ = ctx
	_ = ln
	_ = srv
	return fmt.Errorf("certmagic ACME manager not bundled in this skeleton; set tls.mode=files or off, or wire CertMagic in installer (see docs/dev/edge.md)")
}

func osGetenv(k string) string {
	return strings.TrimSpace(os.Getenv(k))
}
