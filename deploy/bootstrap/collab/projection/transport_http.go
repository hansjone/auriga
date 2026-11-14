package projection

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"time"
)

// NewTransportHTTP constructs the K3 read client. It has a separate public
// bootstrap connection and protected mTLS identity; the private key and JWT are
// reread at each connection/request so expired installation credentials can be
// rotated without restarting the process.
func NewTransportHTTP(origin, ca, cert, key, token string) (func(context.Context, string, string, any) ([]byte, error), error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if ca != "" {
		b, e := os.ReadFile(ca)
		if e != nil {
			return nil, e
		}
		tc.RootCAs = x509.NewCertPool()
		if !tc.RootCAs.AppendCertsFromPEM(b) {
			return nil, Fault("invalid_chain")
		}
	}
	if (cert == "") != (key == "") {
		return nil, Fault("invalid_chain")
	}
	public := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: tc}}
	private := tc.Clone()
	if cert != "" {
		private.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			pair, e := tls.LoadX509KeyPair(cert, key)
			if e != nil {
				return nil, Fault("projection_pending")
			}
			return &pair, nil
		}
	}
	p := &Projector{Origin: origin, Client: public, ReceiptClient: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: private, DisableKeepAlives: true}}, ServiceTokenFile: token}
	return p.request, nil
}
