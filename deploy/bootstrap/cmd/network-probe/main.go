// A private deployment readiness probe: no token, cookie or TLS bypass.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/network"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	if err := network.CheckCertificates(os.Args[2], time.Now(), os.Stderr); err != nil {
		os.Exit(1)
	}
	if os.Args[1] != "--certificates" && !ready(os.Args[1], os.Args[2]) {
		os.Exit(1)
	}
}

func ready(origin, root string) bool {
	pair, err := tls.LoadX509KeyPair(filepath.Join(root, "edge.pem"), filepath.Join(root, "edge.key"))
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(root, "server-ca.pem"))
	if err != nil {
		return false
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return false
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{pair}, ServerName: "submit.network.internal"}, Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Post(origin+"/api/ai/v1/network/submit", "application/json", nil)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8192))
	return err == nil && response.StatusCode == http.StatusUnauthorized && strings.Contains(string(body), "network.unauthorized")
}
