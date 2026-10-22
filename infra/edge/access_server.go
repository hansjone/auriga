package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/hansjone/auriga/infra/gen/deployment"
)

func (s *Server) runAccess(ctx context.Context) error {
	listeners := s.Cfg.Access.Listeners()
	entries := []struct {
		role   string
		config deployment.Listener
	}{{"portal", listeners.Portal}, {"admin", listeners.Admin}, {"instanceControl", listeners.InstanceControl}}
	var sockets []net.Listener
	var servers []*http.Server
	defer func() {
		for _, socket := range sockets {
			_ = socket.Close()
		}
		for _, server := range servers {
			_ = server.Close()
		}
	}()
	listen := s.Listen
	if listen == nil {
		listen = net.Listen
	}
	// Load every certificate and bind every socket before serving any traffic.
	for _, entry := range entries {
		var tlsConfig *tls.Config
		if entry.config.Tls {
			cert, err := tls.LoadX509KeyPair(s.Cfg.TLS.CertFile, s.Cfg.TLS.KeyFile)
			if err != nil {
				return fmt.Errorf("listener TLS material: %w", err)
			}
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
			if s.Cfg.Access.IngressKind() == "trusted_proxy" && entry.role != "instanceControl" {
				if s.Cfg.ProxyTLS.IdentityURI == "" {
					return fmt.Errorf("proxyTLS.identityUri is required")
				}
				ca, err := os.ReadFile(s.Cfg.ProxyTLS.CAFile)
				if err != nil {
					return err
				}
				roots := x509.NewCertPool()
				if !roots.AppendCertsFromPEM(ca) {
					return fmt.Errorf("proxyTLS CA bundle is empty")
				}
				tlsConfig.ClientCAs = roots
				tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
			}
		}
		socket, err := listen("tcp", net.JoinHostPort(entry.config.Address, strconv.Itoa(int(entry.config.Port))))
		if err != nil {
			return err
		}
		if tlsConfig != nil {
			socket = tls.NewListener(socket, tlsConfig)
		}
		sockets = append(sockets, socket)
		role := entry.role
		server := &http.Server{Handler: s.Handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
			BaseContext: func(net.Listener) context.Context { return context.WithValue(ctx, accessRoleKey{}, role) }}
		servers = append(servers, server)
	}
	errors := make(chan error, len(servers))
	for i, server := range servers {
		go func() { errors <- server.Serve(sockets[i]) }()
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, server := range servers {
			_ = server.Shutdown(shutdownCtx)
		}
		return nil
	case err := <-errors:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}
