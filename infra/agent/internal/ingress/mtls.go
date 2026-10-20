package ingress

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strings"
)

const edgeURIPrefix = "auriga://edge"

// TLSConfig builds a server TLS config that requires a client cert issued by
// platformCA and bearing URI SAN auriga://edge (or auriga://edge/<id>).
func TLSConfig(serverCert tls.Certificate, platformCA *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    platformCA,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("%s: missing client certificate", CodeUnauthorized)
			}
			if !isEdgeCert(cs.PeerCertificates[0]) {
				return fmt.Errorf("%s: client is not edge", CodeUnauthorized)
			}
			return nil
		},
	}
}

func isEdgeCert(cert *x509.Certificate) bool {
	for _, u := range cert.URIs {
		s := u.String()
		if s == edgeURIPrefix || strings.HasPrefix(s, edgeURIPrefix+"/") {
			return true
		}
	}
	return false
}

// requireEdgePeer rejects requests without a verified edge client cert.
// Used for non-TLS listeners (unix / named pipe) that still carry TLS, or
// for defense-in-depth after TLS handshake.
func requireEdgePeer(r *http.Request) bool {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return false
	}
	return isEdgeCert(r.TLS.PeerCertificates[0])
}
