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
	CertificateNotBefore time.Time
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

// Enrolled reports whether identity files already exist.
func Enrolled(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, KeyFileName))
	return err == nil
}

// EnsureDir creates the agent credential directory with restrictive permissions.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return restrictDir(dir)
}

// Save writes key material atomically (temp + rename) with tightened permissions.
// Public material (cert, CA) is switched first; private keys last.
func Save(dir string, m *Material) error {
	if err := EnsureDir(dir); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(m.PrivateKey)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := atomicWrite(filepath.Join(dir, CertFileName), m.CertificatePEM, true); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, CAFileName), m.CAPEM, true); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, KeyFileName), keyPEM, true); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, SealingFileName), m.SealingPrivate[:], true); err != nil {
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
	certs, err := ParseCertPEM(certPEM)
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
		CertificateNotBefore: certs[0].NotBefore,
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

// ParseCertPEM parses a PEM certificate chain.
func ParseCertPEM(pemBytes []byte) ([]*x509.Certificate, error) {
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

// PublicKeysEqual reports whether cert's ECDSA public key matches priv.
func PublicKeysEqual(cert *x509.Certificate, priv *ecdsa.PrivateKey) bool {
	if cert == nil || priv == nil {
		return false
	}
	return priv.PublicKey.Equal(cert.PublicKey)
}

func atomicWrite(path string, data []byte, private bool) error {
	dir := filepath.Dir(path)
	var tmp string
	var f *os.File
	var err error
	for i := 0; i < 5; i++ {
		suffix := make([]byte, 4)
		if _, err := rand.Read(suffix); err != nil {
			return err
		}
		tmp = filepath.Join(dir, fmt.Sprintf(".%s.tmp.%x", filepath.Base(path), suffix))
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return err
		}
	}
	if f == nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if private {
		if err := restrictFile(tmp); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	} else {
		_ = os.Chmod(tmp, 0o600)
		if err := restrictFile(tmp); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return restrictFile(path)
}
