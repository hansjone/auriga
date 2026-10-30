package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"os"
	"time"
)

// Optional listener on the SAME callout process. No independently supplied
// connection registry and no plaintext device bearer transport.
type SubmitHTTPConfig struct {
	Listen       string `json:"listen"`
	TLSCertFile  string `json:"tlsCertFile"`
	TLSKeyFile   string `json:"tlsKeyFile"`
	ClientCAFile string `json:"clientCAFile"`
}

func (r *Runtime) startSubmitHTTP(ctx context.Context, c *SubmitHTTPConfig) error {
	if c == nil {
		return nil
	}
	if c.Listen == "" || c.TLSCertFile == "" || c.TLSKeyFile == "" || c.ClientCAFile == "" {
		return errors.New("network: submit HTTPS listener requires address, TLS keypair and dedicated client CA")
	}
	raw, err := os.ReadFile(c.ClientCAFile)
	if err != nil {
		return errors.New("network: submit client CA unavailable")
	}
	roots := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(raw)) > 0 {
		raw = bytes.TrimSpace(raw)
		if !bytes.HasPrefix(raw, []byte("-----BEGIN CERTIFICATE-----")) {
			return errors.New("network: invalid submit client CA")
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("network: invalid submit client CA")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || cert.KeyUsage&x509.KeyUsageCertSign == 0 || time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
			return errors.New("network: invalid submit client CA")
		}
		roots.AddCert(cert)
		count++
		raw = rest
	}
	if count == 0 {
		return errors.New("network: invalid submit client CA")
	}
	pair, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return errors.New("network: submit TLS keypair unavailable")
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return errors.New("network: submit listener unavailable")
	}
	r.submitHTTP = &http.Server{Handler: r.SubmitHandler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768, BaseContext: func(net.Listener) context.Context { return ctx }}
	r.done.Go(func() {
		err := r.submitHTTP.ServeTLS(listener, "", "")
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case r.Errors <- errors.New("network: submit listener stopped"):
			default:
			}
		}
	})
	return nil
}
