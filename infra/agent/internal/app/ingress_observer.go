package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hansjone/auriga/infra/agent/internal/config"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/ingress"
	"github.com/hansjone/auriga/infra/agent/internal/observer"
	"github.com/hansjone/auriga/infra/agent/internal/tlsutil"
)

// startIngressAndObserver wires A3 ingress + observer (append-only).
// Instance registry/targets stay empty until runtime exposes endpoints (主控接线).
func startIngressAndObserver(ctx context.Context, cfg config.Config, logger *slog.Logger, listenAddr, localPath string) (func(), string, error) {
	mat, err := identity.Load(cfg.AgentDir())
	if err != nil {
		return nil, "", fmt.Errorf("ingress identity: %w", err)
	}
	keyPEM, err := marshalECDSAKeyPEM(mat.PrivateKey)
	if err != nil {
		return nil, "", err
	}
	tlsCert, err := tlsutil.LoadTLSCertificate(mat.CertificatePEM, keyPEM)
	if err != nil {
		return nil, "", fmt.Errorf("ingress tls cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(mat.CAPEM) {
		return nil, "", fmt.Errorf("ingress: no CA certs")
	}

	if localPath == "" {
		if v := os.Getenv("AURIGA_AGENT_INGRESS_LOCAL"); v != "" {
			localPath = v
		} else if runtime.GOOS == "windows" {
			localPath = "auriga-agent-ingress"
		} else {
			localPath = filepath.Join(cfg.AgentDir(), "ingress.sock")
		}
	}
	if listenAddr == "" {
		listenAddr = os.Getenv("AURIGA_AGENT_INGRESS_LISTEN")
	}

	ticketPath := os.Getenv("AURIGA_AGENT_FILE_TICKET_KEYS")
	if ticketPath == "" {
		ticketPath = filepath.Join(cfg.AgentDir(), "file-ticket-keys.json")
	}
	idPath := os.Getenv("AURIGA_AGENT_IDENTITY_KEYS")
	if idPath == "" {
		idPath = filepath.Join(cfg.AgentDir(), "identity-keys.json")
	}

	srv := ingress.New(ingress.Config{
		ListenAddr:   listenAddr,
		LocalPath:    localPath,
		ServerCert:   tlsCert,
		PlatformCA:   pool,
		Registry:     ingress.NewMapRegistry(),
		Tickets:      &ingress.TicketVerifier{Keys: loadEd25519Pubs(ticketPath, logger)},
		IdentityKeys: ingress.IdentityKeys(loadEd25519Pubs(idPath, logger)),
		Logger:       logger,
	})
	if err := srv.Start(ctx); err != nil {
		_ = srv.Close()
		return nil, "", err
	} else {
		logger.Info("ingress started", "listen", listenAddr, "local", localPath)
	}
	endpoint, err := ingressEndpoint(listenAddr, localPath, cfg.IngressAdvertiseHost, srv.Addr().String())
	if err != nil {
		_ = srv.Close()
		return nil, "", err
	}

	obs := observer.New(observer.Config{
		Source:    &observer.SliceTargets{},
		Reporter:  observer.LogReporter{Log: logger},
		Collector: observer.DefaultCollector(),
		Logger:    logger,
	})
	obs.Start(ctx)

	return func() {
		obs.Stop()
		_ = srv.Close()
	}, endpoint, nil
}

func ingressEndpoint(listenAddr, localPath, advertiseHost, actualAddr string) (string, error) {
	if listenAddr != "" {
		host, port, err := net.SplitHostPort(actualAddr)
		if err != nil {
			return "", err
		}
		if advertiseHost != "" {
			host = advertiseHost
		}
		if host == "" || host == "0.0.0.0" || host == "::" || strings.ContainsAny(host, "/@?#") {
			return "", fmt.Errorf("ingress needs a reachable advertise host")
		}
		return (&url.URL{Scheme: "https", Host: net.JoinHostPort(host, port)}).String(), nil
	}
	if runtime.GOOS == "windows" {
		pipe := strings.TrimPrefix(localPath, `\\.\pipe\`)
		if pipe == "" || strings.ContainsAny(pipe, `/\`) {
			return "", fmt.Errorf("invalid ingress pipe name")
		}
		return "npipe:////./pipe/" + pipe, nil
	}
	if !filepath.IsAbs(localPath) {
		return "", fmt.Errorf("ingress socket must be absolute")
	}
	return (&url.URL{Scheme: "unix", Path: localPath}).String(), nil
}

func marshalECDSAKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func loadEd25519Pubs(path string, logger *slog.Logger) map[string]ed25519.PublicKey {
	out := map[string]ed25519.PublicKey{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var file struct {
		Keys []struct {
			Kty string `json:"kty"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		if logger != nil {
			logger.Warn("parse keys file", "path", path, "err", err)
		}
		return out
	}
	for _, k := range file.Keys {
		if k.Kid == "" || k.X == "" {
			continue
		}
		if k.Kty != "" && k.Kty != "OKP" {
			continue
		}
		if k.Crv != "" && k.Crv != "Ed25519" {
			continue
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			continue
		}
		out[k.Kid] = ed25519.PublicKey(x)
	}
	return out
}
