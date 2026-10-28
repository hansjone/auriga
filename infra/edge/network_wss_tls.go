package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
)

func networkWSSTLS(cfg NetworkWSSConfig, scheme string) (*tls.Config, error) {
	if scheme != "https" {
		if cfg.CAFile != "" || cfg.ServerName != "" {
			return nil, fmt.Errorf("networkWss TLS settings require an HTTPS/WSS upstream")
		}
		return nil, nil
	}
	if cfg.ServerName != "" && net.ParseIP(cfg.ServerName) == nil {
		name, _, err := parseAuthority(cfg.ServerName, "https")
		if err != nil || !strings.EqualFold(name, cfg.ServerName) {
			return nil, fmt.Errorf("networkWss.serverName must be a DNS name or IP address without a port")
		}
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}
	if cfg.CAFile == "" {
		return tlsConfig, nil
	}
	// Match the trusted HTTPS hops: use only the configured public CA bundle,
	// without borrowing another hop's credentials or the system trust store.
	info, err := os.Lstat(cfg.CAFile)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("networkWss.caFile must be a readable regular file")
	}
	ca, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("networkWss.caFile must be a readable regular file")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("networkWss.caFile must contain PEM certificates")
	}
	tlsConfig.RootCAs = roots
	return tlsConfig, nil
}
