package projection

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// TransportHTTP keeps the management authentication provenance in local
// configuration. No response field can turn an anonymous client into a ledger.
type TransportHTTP struct {
	projector  *Projector
	management bool
}

// SelectorLedgerResponse carries local transport provenance outside the wire
// object. Only the authenticated management client can construct an accepted one.
type SelectorLedgerResponse struct {
	body          []byte
	authenticated bool
}

func (h *TransportHTTP) Request(ctx context.Context, method, path string, v any) ([]byte, error) {
	return h.projector.request(ctx, method, path, v)
}

func (h *TransportHTTP) ExpectedSelectors(ctx context.Context, q api.ExpectedSelectorsQuery) (SelectorLedgerResponse, error) {
	if !h.management {
		return SelectorLedgerResponse{}, Fault("scope_denied")
	}
	raw, e := h.Request(ctx, "POST", "/api/collab/v2/network/transport/expected-selectors", q)
	if e != nil {
		return SelectorLedgerResponse{}, e
	}
	return SelectorLedgerResponse{body: raw, authenticated: true}, nil
}

// NewTransportHTTP constructs the K3 read client. It has a separate public
// bootstrap connection and protected mTLS identity; the private key and JWT are
// reread at each connection/request so expired installation credentials can be
// rotated without restarting the process.
func NewTransportHTTP(origin, ca, cert, key, token string) (*TransportHTTP, error) {
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
	return &TransportHTTP{projector: p, management: cert != "" && key != "" && token != ""}, nil
}
