package network

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"
)

// Optional listener on the SAME callout process. No independently supplied
// connection registry and no plaintext device bearer transport.
type SubmitHTTPConfig struct {
	Listen      string `json:"listen"`
	TLSCertFile string `json:"tlsCertFile"`
	TLSKeyFile  string `json:"tlsKeyFile"`
}

func (r *Runtime) startSubmitHTTP(ctx context.Context, c *SubmitHTTPConfig) error {
	if c == nil {
		return nil
	}
	if c.Listen == "" || c.TLSCertFile == "" || c.TLSKeyFile == "" {
		return errors.New("network: submit HTTPS listener requires address and TLS keypair")
	}
	pair, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return errors.New("network: submit TLS keypair unavailable")
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return errors.New("network: submit listener unavailable")
	}
	r.submitHTTP = &http.Server{Handler: r.SubmitHandler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32768, BaseContext: func(net.Listener) context.Context { return ctx }}
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
