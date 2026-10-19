// Package identity manages node keys, CSRs, and on-disk credentials.
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/curve25519"
)

const (
	KeyFileName     = "node.key"
	CertFileName    = "node.crt"
	CAFileName      = "ca.crt"
	SealingFileName = "sealing.key"
)

// Material is the node identity persisted under <data>/agent/.
type Material struct {
	NodeID               string
	ControlEndpoint      string
	CertificateExpiresAt time.Time
	PrivateKey           *ecdsa.PrivateKey
	CertificatePEM       []byte // full chain PEM
	CAPEM                []byte
	SealingPrivate       [32]byte
	SealingPublic        [32]byte
	Dir                  string
}

// GenerateKeyPair creates an ECDSA P-256 key for the node certificate.
func GenerateKeyPair() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// GenerateSealingKey creates an X25519 key pair for SealedSecret.
func GenerateSealingKey() (priv, pub [32]byte, err error) {
	if _, err = rand.Read(priv[:]); err != nil {
		return priv, pub, err
	}
	curve25519.ScalarBaseMult(&pub, &priv)
	return priv, pub, nil
}

// CreateCSR builds a PKCS#10 CSR (DER) for the node key.
func CreateCSR(key *ecdsa.PrivateKey, commonName string) ([]byte, error) {
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: commonName},
	}
	return x509.CreateCertificateRequest(rand.Reader, tmpl, key)
}

// SPKISHA256Hex returns lowercase hex SHA-256 of the certificate's SubjectPublicKeyInfo.
func SPKISHA256Hex(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(sum[:])
}

// EnsureDir creates the agent credential directory with restrictive permissions.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return restrictDir(dir)
}

// Save writes key material to dir with tightened file permissions.
func Save(dir string, m *Material) error {
	if err := EnsureDir(dir); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(m.PrivateKey)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writePrivate(filepath.Join(dir, KeyFileName), keyPEM); err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(dir, SealingFileName), m.SealingPrivate[:]); err != nil {
		return err
	}
	if err := writePublic(filepath.Join(dir, CertFileName), m.CertificatePEM); err != nil {
		return err
	}
	if err := writePublic(filepath.Join(dir, CAFileName), m.CAPEM); err != nil {
		return err
	}
	m.Dir = dir
	return nil
}

// Load reads identity files from dir. Returns os.ErrNotExist if not enrolled.
func Load(dir string) (*Material, error) {
	keyPEM, err := os.ReadFile(filepath.Join(dir, KeyFileName))
	if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(filepath.Join(dir, CertFileName))
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, CAFileName))
	if err != nil {
		return nil, err
	}
	sealingRaw, err := os.ReadFile(filepath.Join(dir, SealingFileName))
	if err != nil {
		return nil, err
	}
	if len(sealingRaw) != 32 {
		return nil, fmt.Errorf("sealing key must be 32 bytes")
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid node key PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse node key: %w", err)
	}
	certs, err := parseCertPEM(certPEM)
	if err != nil {
		return nil, err
	}
	nodeID, err := NodeIDFromCert(certs[0])
	if err != nil {
		return nil, err
	}
	var sealingPriv [32]byte
	copy(sealingPriv[:], sealingRaw)
	var sealingPub [32]byte
	curve25519.ScalarBaseMult(&sealingPub, &sealingPriv)
	return &Material{
		NodeID:               nodeID,
		PrivateKey:           key,
		CertificatePEM:       certPEM,
		CAPEM:                caPEM,
		SealingPrivate:       sealingPriv,
		SealingPublic:        sealingPub,
		CertificateExpiresAt: certs[0].NotAfter,
		Dir:                  dir,
	}, nil
}

// NodeIDFromCert extracts auriga://node/<id> from URI SANs.
func NodeIDFromCert(cert *x509.Certificate) (string, error) {
	const prefix = "auriga://node/"
	for _, u := range cert.URIs {
		s := u.String()
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			return s[len(prefix):], nil
		}
	}
	return "", fmt.Errorf("certificate missing %s URI SAN", prefix)
}

// SealingKeyID returns the first 16 hex chars of SHA-256(sealing public key).
func SealingKeyID(pub [32]byte) string {
	sum := sha256.Sum256(pub[:])
	return hex.EncodeToString(sum[:8])
}

func parseCertPEM(pemBytes []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates in PEM")
	}
	return certs, nil
}

func writePrivate(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return restrictFile(path)
}

func writePublic(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return nil
}
