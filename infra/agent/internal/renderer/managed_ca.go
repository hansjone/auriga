package renderer

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"time"
)

// The agent operator alone selects this public TLS CA. It is distinct from
// protocol signing roots and can never be supplied by Profile.Env or a model.
func managedCA() ([]byte, error) {
	path := os.Getenv("AURIGA_AGENT_MANAGED_CA_FILE")
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, fmt.Errorf("renderer: invalid managed CA file")
	}
	rest := raw
	count := 0
	now := time.Now()
	for len(bytes.TrimSpace(rest)) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fmt.Errorf("renderer: public managed CA certificates required")
		}
		// pem.Decode may skip arbitrary leading bytes; refuse such a mixed file.
		if !bytes.HasPrefix(bytes.TrimSpace(rest), []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, fmt.Errorf("renderer: invalid managed CA prefix")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, fmt.Errorf("renderer: valid managed TLS CA required")
		}
		count++
		rest = next
	}
	if count == 0 {
		return nil, fmt.Errorf("renderer: empty managed CA")
	}
	return raw, nil
}
