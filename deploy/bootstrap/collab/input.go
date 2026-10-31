package collab

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// Only installation projection: Java RootKeyPublication/SigningKeyLoader remain
// authoritative for signatures, schema, state and PostgreSQL anti-rollback.
type Input struct {
	TenantID         string            `json:"tenantId"`
	Realm            string            `json:"realm"`
	RootKid          string            `json:"rootKid"`
	RootFingerprint  string            `json:"rootFingerprint"`
	MinimumVersion   string            `json:"minimumVersion"`
	RootPublicFile   string            `json:"rootPublicFile"`
	SignedFile       string            `json:"signedFile"`
	AnnouncementFile string            `json:"announcementFile"`
	StateFile        string            `json:"stateFile"`
	PrivateFiles     map[string]string `json:"privateFiles"`
	Files            map[string][]byte `json:"-"`
	Protocol         map[string]string `json:"-"`
}

var Leaves = []string{"root.der", "signed.json", "announcement.json", "state.json", "business.der", "protocol.der", "management.der", "collab.properties", "browser.crt", "ca.crt", "server.crt", "server.key", "client.crt", "client.key"}
var KidPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func (c *Input) Clear() {
	if c != nil {
		for _, raw := range c.Files {
			clear(raw)
		}
	}
}
func (c *Input) Digest() string {
	if c == nil {
		return "disabled"
	}
	h := sha256.New()
	raw, _ := json.Marshal(c)
	_, _ = h.Write(raw)
	for _, name := range Leaves {
		_, _ = h.Write(c.Files[name])
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func Read(path, tenant string) (*Input, error) {
	if path == "" {
		return nil, nil
	}
	invalid := fmt.Errorf("invalid explicit collab installation input")
	raw, err := ReadFile(path, 65536, false)
	if err != nil {
		return nil, invalid
	}
	c := &Input{Files: map[string][]byte{}}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(c) != nil || decoder.Decode(new(any)) != io.EOF || c.TenantID != tenant || !KidPattern.MatchString(c.RootKid) {
		return nil, invalid
	}
	realm, e := strconv.ParseUint(c.Realm, 10, 64)
	minimum, e2 := strconv.ParseInt(c.MinimumVersion, 10, 64)
	if e != nil || realm == 0 || strconv.FormatUint(realm, 10) != c.Realm || e2 != nil || minimum < 1 || strconv.FormatInt(minimum, 10) != c.MinimumVersion {
		return nil, invalid
	}
	read := func(name, source string) error {
		if source == "" {
			return invalid
		}
		if !filepath.IsAbs(source) {
			source = filepath.Join(filepath.Dir(path), source)
		}
		secret := name == "business.der" || name == "protocol.der" || name == "management.der"
		b, err := ReadFile(source, 65536, secret)
		if err != nil {
			return invalid
		}
		c.Files[name] = b
		return nil
	}
	for name, source := range map[string]string{"root.der": c.RootPublicFile, "signed.json": c.SignedFile, "announcement.json": c.AnnouncementFile, "state.json": c.StateFile} {
		if read(name, source) != nil {
			c.Clear()
			return nil, invalid
		}
	}
	pub, err := x509.ParsePKIXPublicKey(c.Files["root.der"])
	sum := sha256.Sum256(c.Files["root.der"])
	if _, ok := pub.(ed25519.PublicKey); err != nil || !ok || c.RootFingerprint != fmt.Sprintf("sha256:%x", sum) {
		c.Clear()
		return nil, invalid
	}
	var signed struct {
		Keyset struct {
			Realm   string `json:"realm"`
			Version string `json:"keyset_version"`
			Issued  string `json:"issued_at"`
			Until   string `json:"valid_until"`
			Keys    []struct {
				Kid     string `json:"kid"`
				Purpose string `json:"purpose"`
				Public  string `json:"public_key_spki"`
				Before  string `json:"not_before"`
				After   string `json:"not_after"`
				State   string `json:"state"`
			} `json:"keys"`
		} `json:"keyset"`
	}
	if json.Unmarshal(c.Files["signed.json"], &signed) != nil {
		c.Clear()
		return nil, invalid
	}
	p := signed.Keyset
	version, e := strconv.ParseInt(p.Version, 10, 64)
	issued, e2 := time.Parse(time.RFC3339, p.Issued)
	until, e3 := time.Parse(time.RFC3339, p.Until)
	now := time.Now()
	if e != nil || version < minimum || e2 != nil || e3 != nil || now.Before(issued) || !now.Before(until) || until.Sub(issued) <= 0 || until.Sub(issued) > 24*time.Hour || p.Realm != c.Realm || len(p.Keys) != 3 || len(c.PrivateFiles) != 3 {
		c.Clear()
		return nil, invalid
	}
	purposes := map[string]string{"business_delivery": "business.der", "protocol_response": "protocol.der", "management_state": "management.der"}
	seen := map[string]bool{}
	for _, key := range p.Keys {
		name, ok := purposes[key.Purpose]
		if !ok || seen[name] || !KidPattern.MatchString(key.Kid) || key.State != "active" || read(name, c.PrivateFiles[key.Kid]) != nil {
			c.Clear()
			return nil, invalid
		}
		seen[name] = true
		private, err := x509.ParsePKCS8PrivateKey(c.Files[name])
		secret, ok := private.(ed25519.PrivateKey)
		before, eb := time.Parse(time.RFC3339, key.Before)
		after, ea := time.Parse(time.RFC3339, key.After)
		if err != nil || !ok || eb != nil || ea != nil || now.Before(before) || !now.Before(after) {
			c.Clear()
			return nil, invalid
		}
		der, _ := x509.MarshalPKIXPublicKey(secret.Public())
		if base64.RawURLEncoding.EncodeToString(der) != key.Public || bytes.Equal(der, c.Files["root.der"]) {
			c.Clear()
			return nil, invalid
		}
		if key.Purpose == "protocol_response" {
			c.Protocol = map[string]string{"keyId": key.Kid, "keysetVersion": p.Version, "notBefore": key.Before, "notAfter": key.After, "privateKeyFile": "/etc/auriga/network/protocol.key"}
		}
	}
	return c, nil
}

func ReadFile(path string, limit int64, secret bool) ([]byte, error) {
	// Same-file checks and no symlink ancestors, before reading private inputs.
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("collab input metadata refused")
		}
		if p == filepath.Dir(p) || p == "." {
			break
		}
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() == 0 || before.Size() > limit {
		return nil, fmt.Errorf("collab input refused")
	}
	if !sourceMetadata(before, secret) {
		return nil, fmt.Errorf("collab input ownership refused")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("collab input changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	final, se := os.Lstat(path)
	if err != nil || se != nil || !os.SameFile(opened, final) || !final.ModTime().Equal(before.ModTime()) || int64(len(raw)) != before.Size() {
		clear(raw)
		return nil, fmt.Errorf("collab input changed")
	}
	return raw, nil
}
