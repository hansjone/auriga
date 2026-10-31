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
	controlPolicy, err := s.Cfg.prepareControlIngress()
	if err != nil {
		return err
	}
	listeners := s.Cfg.Access.Listeners()
	entries := []struct {
		role   string
		config deployment.Listener
	}{{"portal", listeners.Portal}, {"admin", listeners.Admin}, {"instanceControl", listeners.InstanceControl}}
	if s.Cfg.ManagedIngress.Origin != "" {
		entry, err := s.Cfg.managedEntry()
		if err != nil {
			return err
		}
		entries = append(entries, struct {
			role   string
			config deployment.Listener
		}{"managed", deployment.Listener{Address: "0.0.0.0", Port: int32(entry.port), Tls: true}})
	}
	if s.Cfg.LiteLLMUI != nil {
		if err := s.Cfg.validateLiteLLMUI(); err != nil {
			return err
		}
		entries = append(entries, struct {
			role   string
			config deployment.Listener
		}{"relayAdmin", s.Cfg.LiteLLMUI.Listener})
	}
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
			certFile, keyFile := s.Cfg.TLS.CertFile, s.Cfg.TLS.KeyFile
			if entry.role == "managed" {
				certFile, keyFile = s.Cfg.ManagedIngress.CertFile, s.Cfg.ManagedIngress.KeyFile
			}
			cert, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return fmt.Errorf("listener TLS material: %w", err)
			}
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
			if s.Cfg.Access.IngressKind() == "trusted_proxy" && entry.role != "instanceControl" && entry.role != "managed" {
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
		if entry.role == "instanceControl" && controlPolicy != nil {
			socket = &controlIngressListener{Listener: socket, policy: controlPolicy}
		}
		if entry.role == "managed" {
			policy := *controlPolicy
			policy.port = int(entry.config.Port)
			socket = &controlIngressListener{Listener: socket, policy: &policy}
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
