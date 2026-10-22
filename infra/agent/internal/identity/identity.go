// Package identity manages node keys, CSRs, and on-disk credentials.
package identity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
)

// Concurrent renewal RPCs may finish together. Coordinate the multi-file IO
// locally so readers never observe an in-progress Save and Windows rename is
// not attempted while another renewal still has an identity file open.
var identityIOLocks sync.Map

func identityIOLock(dir string) *sync.RWMutex {
	path := CanonicalDir(dir)
	lock, _ := identityIOLocks.LoadOrStore(path, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}

// CanonicalDir is shared by enrollment's certificate+metadata commit lock.
func CanonicalDir(dir string) string {
	path, err := filepath.Abs(dir)
	if err != nil {
		path = filepath.Clean(dir)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

const (
	KeyFileName        = "node.key"
	CertFileName       = "node.crt"
	CAFileName         = "ca.crt"
	SealingFileName    = "sealing.key"
	TrustGuardFileName = "release-trust.guard"
)

// TrustGuard is permanent initialization evidence outside state.db. Its presence
// prevents a missing/replaced database from silently becoming a fresh bootstrap.
type TrustGuard struct {
	Version     int    `json:"version"`
	Binding     string `json:"binding"`
	Roots       string `json:"roots"`
	Trusted     bool   `json:"trusted"`
	Certificate string `json:"initial_certificate_sha256"`
}

func CertificateDigest(cert []byte) string {
	sum := sha256.Sum256(cert)
	return hex.EncodeToString(sum[:])
}

// CheckFreshEnrollment must run before creating a database or replacing identity.
// Force deliberately cannot override these historical initialization traces.
func CheckFreshEnrollment(dir string) error {
	return checkFreshEnrollment(dir, os.ReadDir)
}

// readDir is a private IO boundary: the parent path is always literal, never a
// glob expression, and read failures must not be confused with empty history.
func checkFreshEnrollment(dir string, readDir func(string) ([]os.DirEntry, error)) error {
	literal, err := initializationDirectoryTarget(dir)
	if err != nil {
		return err
	}
	if cleaned := filepath.Clean(dir); cleaned != dir {
		normalized, err := initializationDirectoryTarget(cleaned)
		if err != nil {
			return err
		}
		same := literal == normalized
		if runtime.GOOS == "windows" {
			same = strings.EqualFold(literal, normalized)
		}
		if !same {
			return fmt.Errorf("enrollment directory has ambiguous symlink/parent traversal; use resolved absolute directory %q", literal)
		}
	}
	// Inspect the same resolved target that Join's filepath.Join-based writers
	// will use, including a missing component followed by '..'. It is still a
	// literal ReadDir path: brackets/wildcards are never patterns.
	entries, err := readDir(literal)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read enrollment directory: %w", err)
	}
	for _, entry := range entries {
		for _, name := range []string{KeyFileName, CertFileName, CAFileName, SealingFileName, TrustGuardFileName, "state.db"} {
			if entry.Name() == name {
				return fmt.Errorf("already enrolled or initialization evidence exists (%s); use certificate renewal; --force cannot replace trust", name)
			}
			if strings.HasPrefix(entry.Name(), "."+name+".tmp.") {
				return fmt.Errorf("incomplete identity initialization; protected local recovery required")
			}
		}
	}
	return nil
}

// initializationDirectoryTarget resolves the literal existing prefix before
// cleaning or creation, retaining the missing suffix for target comparison.
// It also catches Windows' not-found errors below regular files.
func initializationDirectoryTarget(dir string) (target string, err error) {
	var missing []string
	if dir == "" {
		return "", fmt.Errorf("empty enrollment directory")
	}
	separators := string(os.PathSeparator)
	if runtime.GOOS == "windows" {
		separators = `/\`
	}
	for ancestor := dir; ; {
		info, err := os.Lstat(ancestor)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				info, err = os.Stat(ancestor)
				if err != nil {
					return "", err
				}
			}
			if !info.IsDir() {
				return "", fmt.Errorf("enrollment ancestor is not a directory")
			}
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			resolved, err = filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			target = resolved
			for i := len(missing) - 1; i >= 0; i-- {
				target = filepath.Join(target, missing[i])
			}
			return target, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect enrollment directory: %w", err)
		}
		// Split, unlike Dir/Abs, does not clean a symlink/.. component before the
		// filesystem resolves it. Resolve first, then make the result absolute.
		parent, leaf := filepath.Split(strings.TrimRight(ancestor, separators))
		missing = append(missing, leaf)
		parent = strings.TrimRight(parent, separators)
		if parent == "" {
			if filepath.IsAbs(ancestor) {
				parent = string(os.PathSeparator)
			} else {
				parent = "."
			}
		} else if parent == filepath.VolumeName(parent) {
			parent += string(os.PathSeparator)
		}
		if parent == ancestor {
			return "", fmt.Errorf("enrollment has no existing directory ancestor")
		}
		ancestor = parent
	}
}

// TrustBinding excludes the renewable leaf certificate but binds its node ID,
// stable private-key public part, CA bytes, and sealing public key.
func TrustBinding(m *Material) (string, error) {
	if m == nil || m.PrivateKey == nil || m.NodeID == "" {
		return "", fmt.Errorf("identity binding incomplete")
	}
	certs, err := ParseCertPEM(m.CertificatePEM)
	if err != nil {
		return "", err
	}
	if !PublicKeysEqual(certs[0], m.PrivateKey) {
		return "", fmt.Errorf("identity key/certificate mismatch")
	}
	id, err := NodeIDFromCert(certs[0])
	if err != nil || id != m.NodeID {
		return "", fmt.Errorf("identity node binding mismatch")
	}
	pub, err := x509.MarshalPKIXPublicKey(&m.PrivateKey.PublicKey)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(struct {
		Node                string
		Public, CA, Sealing []byte
	}{m.NodeID, pub, m.CAPEM, m.SealingPublic[:]})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// CreateTrustGuard exclusively claims initialization. Never remove it on errors:
// a partial write is intentional fail-closed evidence for subsequent attempts.
func CreateTrustGuard(dir string, g TrustGuard) error {
	return createTrustGuard(dir, g, syncTrustGuard)
}

func createTrustGuard(dir string, g TrustGuard, persist func(*os.File, string) error) error {
	if err := CheckFreshEnrollment(dir); err != nil {
		return err
	}
	if err := EnsureDir(dir); err != nil {
		return err
	}
	absDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	absDir, err = filepath.Abs(absDir)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	// Join only the already resolved directory: Join on the original literal
	// symlink/.. path would clean it before the filesystem could resolve it.
	f, err := os.OpenFile(filepath.Join(absDir, TrustGuardFileName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := restrictFile(f.Name()); err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		return err
	}
	persistErr := persist(f, absDir)
	closeErr := f.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close initialization guard: %w", closeErr)
	}
	return errors.Join(persistErr, closeErr)
}

func ReadTrustGuard(dir string) (TrustGuard, error) {
	var g TrustGuard
	path := filepath.Join(dir, TrustGuardFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return g, err
	}
	if !info.Mode().IsRegular() {
		return g, fmt.Errorf("invalid release trust guard file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return g, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return g, fmt.Errorf("invalid release trust guard: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return g, fmt.Errorf("trailing release trust guard data")
	}
	if g.Version != 1 || len(g.Binding) != 64 || len(g.Certificate) != 64 || (g.Trusted && len(g.Roots) != 64) || (!g.Trusted && g.Roots != "") {
		return g, fmt.Errorf("invalid release trust guard binding")
	}
	return g, nil
}

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
// Private keys are switched first: a failed key write cannot publish a new
// certificate with the old key. This is not a multi-file transaction.
func Save(dir string, m *Material) error {
	lock := identityIOLock(dir)
	lock.Lock()
	defer lock.Unlock()
	if err := EnsureDir(dir); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(m.PrivateKey)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := atomicWrite(filepath.Join(dir, KeyFileName), keyPEM, true); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, SealingFileName), m.SealingPrivate[:], true); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, CAFileName), m.CAPEM, true); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, CertFileName), m.CertificatePEM, true); err != nil {
		return err
	}
	m.Dir = dir
	return nil
}

// Load reads identity files from dir. Returns os.ErrNotExist if not enrolled.
func Load(dir string) (*Material, error) {
	lock := identityIOLock(dir)
	lock.RLock()
	defer lock.RUnlock()
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
