package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// NodeIngress authenticates the destination by platform CA and exact node URI SAN.
// Addresses are locators, never credentials. No HTTP request is sent before verification.
type NodeIngress struct {
	cert       tls.Certificate
	roots      *x509.CertPool
	mu         sync.Mutex
	transports map[string]*http.Transport
}

type NodeTLSConfig struct {
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	CAFile   string `json:"caFile"`
}

func loadNodeIngress(cfg NodeTLSConfig) (*NodeIngress, error) {
	if cfg.CertFile == "" && cfg.KeyFile == "" && cfg.CAFile == "" {
		return nil, nil
	}
	for _, path := range []string{cfg.CertFile, cfg.KeyFile, cfg.CAFile} {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("node TLS material must be a regular file")
		}
		if err := restrictTLSFile(path); err != nil {
			return nil, err
		}
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("node CA bundle is empty")
	}
	return &NodeIngress{cert: cert, roots: roots, transports: make(map[string]*http.Transport)}, nil
}

func parseIngress(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("invalid ingress")
	}
	switch u.Scheme {
	case "https":
		if u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("invalid HTTPS ingress")
		}
	case "unix":
		if u.Host != "" || !strings.HasPrefix(u.Path, "/") {
			return nil, fmt.Errorf("invalid unix ingress")
		}
	case "npipe":
		if u.Host != "" || !strings.HasPrefix(u.Path, "//./pipe/") || len(u.Path) <= len("//./pipe/") {
			return nil, fmt.Errorf("invalid pipe ingress")
		}
	default:
		return nil, fmt.Errorf("ingress must use https, unix or npipe")
	}
	return u, nil
}

func (n *NodeIngress) target(route *InstanceRoute) (*url.URL, http.RoundTripper, error) {
	if n == nil {
		return nil, nil, fmt.Errorf("node mTLS is not configured")
	}
	if !validRouteID(route.NodeID) || !validRouteID(route.InstanceID) {
		return nil, nil, fmt.Errorf("invalid route identity")
	}
	u, err := parseIngress(route.Ingress)
	if err != nil {
		return nil, nil, err
	}
	key := route.NodeID + "|" + route.Ingress
	n.mu.Lock()
	defer n.mu.Unlock()
	tr := n.transports[key]
	if tr == nil {
		// Bound the pool, and remove previous endpoints when a node changes address.
		for k, old := range n.transports {
			if strings.HasPrefix(k, route.NodeID+"|") || len(n.transports) >= 256 {
				old.CloseIdleConnections()
				delete(n.transports, k)
			}
		}
		cfg := &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{n.cert},
			// Node identity is URI-based, including on sockets without DNS names.
			// The callback replaces *all* default chain verification, not just name checking.
			InsecureSkipVerify: true, //nolint:gosec // full CA/EKU/time/URI verification below
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return fmt.Errorf("node certificate missing")
				}
				leaf := cs.PeerCertificates[0]
				intermediates := x509.NewCertPool()
				for _, c := range cs.PeerCertificates[1:] {
					intermediates.AddCert(c)
				}
				if _, err := leaf.Verify(x509.VerifyOptions{Roots: n.roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
					return err
				}
				if len(leaf.URIs) != 1 || leaf.URIs[0].String() != "auriga://node/"+route.NodeID {
					return fmt.Errorf("wrong node certificate identity")
				}
				return nil
			},
		}
		tr = &http.Transport{TLSClientConfig: cfg, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, ExpectContinueTimeout: time.Second}
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		tr.DialContext = dialer.DialContext
		if u.Scheme != "https" {
			endpoint := *u
			tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return dialLocalIngress(ctx, &endpoint) }
		}
		n.transports[key] = tr
	}
	target := &url.URL{Scheme: "https", Host: u.Host, Path: "/i/" + route.InstanceID}
	if u.Scheme != "https" {
		target.Host = "node.invalid"
	}
	return target, tr, nil
}

func (n *NodeIngress) Close() {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, tr := range n.transports {
		tr.CloseIdleConnections()
		delete(n.transports, k)
	}
}

func validRouteID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
