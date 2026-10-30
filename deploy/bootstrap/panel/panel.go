// Package panel owns the installation's independent panel signing key and the
// publication of the self-contained panel shipped in the edge image.
package panel

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Lifetime = 30 * 24 * time.Hour
const RenewBefore = 7 * 24 * time.Hour

// Generate is called only for a new, receipted installation. The private key is
// never copied into edge or instance runtime materials.
func Generate(out string) error {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	defer clear(key)
	if err := createFile(filepath.Join(out, "panel-signing.key"), key, 0600); err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{"keys": []any{map[string]string{"kid": keyID(pub), "kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig", "x": base64.RawURLEncoding.EncodeToString(pub)}}})
	if err != nil {
		return err
	}
	return createFile(filepath.Join(out, "panel-keys.json"), raw, 0644)
}

func createFile(path string, raw []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(raw); err != nil {
		return err
	}
	return f.Sync()
}

func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "panel-" + hex.EncodeToString(sum[:16])
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// All signed strings are ASCII (fixed fields, validated version and hashed
// filename). Sorted JSON keys and integer protocol fields therefore produce
// exactly RFC8785 JCS bytes, without a general purpose JSON canonicalizer.
type body struct {
	Element          string            `json:"element"`
	Entry            string            `json:"entry"`
	ExpiresAt        string            `json:"expiresAt"`
	Integrity        map[string]string `json:"integrity"`
	IssuedAt         string            `json:"issuedAt"`
	MinPluginVersion string            `json:"minPluginVersion"`
	Protocol         int               `json:"protocol"`
	SchemaVersion    int               `json:"schemaVersion"`
	Version          string            `json:"version"`
}
type signature struct {
	Alg   string `json:"alg"`
	Kid   string `json:"kid"`
	Value string `json:"value"`
}
type manifest struct {
	body
	Signature signature `json:"signature"`
}

func versionParts(v string) ([]uint64, error) {
	if !semver.MatchString(v) {
		return nil, fmt.Errorf("panel version must be canonical major.minor.patch")
	}
	parts := make([]uint64, 3)
	for i, s := range strings.Split(v, ".") {
		n, err := strconv.ParseUint(s, 10, 53)
		if err != nil {
			return nil, fmt.Errorf("panel version out of range")
		}
		parts[i] = n
	}
	return parts, nil
}

// Publish signs bytes from the image, retains old hashed files for readers with
// an in-flight manifest, and atomically replaces the public manifest last.
func Publish(keyFile, dist, out, version string, now time.Time) error {
	return withOutputLock(out, func() error { return publish(keyFile, dist, out, version, now) })
}

func publish(keyFile, dist, out, version string, now time.Time) error {
	key, err := readRegular(keyFile, ed25519.PrivateKeySize)
	if err != nil {
		return fmt.Errorf("read panel signing key: %w", err)
	}
	defer clear(key)
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid panel signing key")
	}
	private := ed25519.PrivateKey(key)
	derived := ed25519.NewKeyFromSeed(private.Seed())
	defer clear(derived)
	if !private.Equal(derived) {
		return fmt.Errorf("invalid panel signing key")
	}
	pub := private.Public().(ed25519.PublicKey)
	code, err := readRegular(filepath.Join(dist, "auriga-team-panel.js"), 4<<20)
	if err != nil || len(code) == 0 {
		return fmt.Errorf("missing self-contained panel bundle")
	}
	sum := sha256.Sum256(code)
	entry := "auriga-team-panel-" + hex.EncodeToString(sum[:]) + ".js"
	sri := sha512.Sum384(code)
	automatic := version == ""
	var old manifest
	previous, err := readRegular(filepath.Join(out, "manifest.json"), 256<<10)
	if err == nil {
		old, err = verifiedManifest(previous, pub)
		if err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	derivedVersion, err := automaticVersion(out, old, entry, pub)
	if err != nil {
		return err
	}
	if automatic {
		version = derivedVersion
	}
	next, err := versionParts(version)
	if err != nil {
		return err
	}
	if len(previous) != 0 {
		prev, err := versionParts(old.Version)
		if err != nil {
			return err
		}
		comparison := 0
		for i := range next {
			if next[i] < prev[i] {
				comparison = -1
				break
			}
			if next[i] > prev[i] {
				comparison = 1
				break
			}
		}
		issued, err := time.Parse(time.RFC3339, old.IssuedAt)
		if err != nil || now.Before(issued) || comparison < 0 || comparison == 0 && old.Entry != entry {
			return fmt.Errorf("panel rollback or changed bytes without version increase")
		}
		expires, err := time.Parse(time.RFC3339, old.ExpiresAt)
		if err != nil {
			return fmt.Errorf("invalid previous panel expiry")
		}
		if automatic && comparison == 0 && expires.Sub(now) > RenewBefore {
			return writePublic(filepath.Join(out, entry), code)
		}
		// Retired, signed manifests are the content-to-version ledger. Save before
		// replacing the public manifest, so interruption cannot lose an old digest.
		if old.Entry != entry {
			if err := archiveManifest(out, old.Version, previous); err != nil {
				return err
			}
		}
	}
	m := manifest{body: body{Element: "auriga-team-panel", Entry: entry, ExpiresAt: now.UTC().Add(Lifetime).Format(time.RFC3339), Integrity: map[string]string{entry: "sha384-" + base64.StdEncoding.EncodeToString(sri[:])}, IssuedAt: now.UTC().Format(time.RFC3339), MinPluginVersion: "0.0.0", Protocol: 1, SchemaVersion: 1, Version: version}}
	signed, err := json.Marshal(m.body)
	if err != nil {
		return err
	}
	m.Signature = signature{Alg: "EdDSA", Kid: keyID(pub), Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, signed))}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := writePublic(filepath.Join(out, entry), code); err != nil {
		return err
	}
	return writePublic(filepath.Join(out, "manifest.json"), raw)
}

func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("invalid panel file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("panel file too large")
	}
	return raw, nil
}

func writePublic(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".panel-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0644); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
