package network

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const CertificateWarningWindow = 30 * 24 * time.Hour

var (
	ErrCertificateExpired = errors.New("network certificate expired")
	ErrCertificateInvalid = errors.New("network certificate invalid")
)

func readCertificate(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrCertificateInvalid
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, ErrCertificateInvalid
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, ErrCertificateInvalid
	}
	return cert, nil
}

// CheckCertificates inspects both CAs and all leaf identities without modifying
// material. Output contains only fixed filenames, status labels and UTC expiry.
func CheckCertificates(dir string, now time.Time, output io.Writer) error {
	var result error
	for _, name := range []string{"server-ca.pem", "client-ca.pem", "submit.pem", "edge.pem", "broker.pem", "broker-client.pem"} {
		cert, err := readCertificate(filepath.Join(dir, name))
		if err != nil || now.Before(cert.NotBefore) {
			_, _ = fmt.Fprintf(output, "network_certificate_invalid file=%s\n", name)
			result = errors.Join(result, ErrCertificateInvalid)
			continue
		}
		status := "ok"
		if !now.Before(cert.NotAfter) {
			status = "expired"
			result = errors.Join(result, ErrCertificateExpired)
		} else if cert.NotAfter.Sub(now) <= CertificateWarningWindow {
			status = "expiring"
		}
		_, _ = fmt.Fprintf(output, "network_certificate_%s file=%s not_after=%s\n", status, name, cert.NotAfter.UTC().Format(time.RFC3339))
	}
	return result
}
