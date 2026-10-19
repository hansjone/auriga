package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// IdentitySigner signs Auriga-Identity compact JWS (EdDSA / Ed25519).
type IdentitySigner interface {
	Kid() string
	Sign(claims IdentityClaims) (string, error)
	PublicKey() ed25519.PublicKey
}

type IdentityClaims struct {
	Iss  string `json:"iss"`
	Aud  string `json:"aud"`
	Sub  string `json:"sub"`
	Tid  string `json:"tid"`
	Sid  string `json:"sid"`
	Name string `json:"name,omitempty"`
	Mtyp string `json:"mtyp,omitempty"`
	Iat  int64  `json:"iat"`
	Exp  int64  `json:"exp"`
	Jti  string `json:"jti"`
	Htm  string `json:"htm"`
	Htu  string `json:"htu"`
	Pv   int    `json:"pv"`
}

type identityHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// FileIdentitySigner loads CURRENT Ed25519 private key from a JWKS-like JSON file.
// Format (install placeholder until iam IdentityHeaderKeyService exists):
//
//	{"keys":[{"kty":"OKP","crv":"Ed25519","x":"...","d":"...","kid":"...","alg":"EdDSA","state":"CURRENT"}]}
type FileIdentitySigner struct {
	kid    string
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey
	now    func() time.Time
	maxTTL time.Duration
}

type jwkKeyFile struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty   string `json:"kty"`
	Crv   string `json:"crv"`
	X     string `json:"x"`
	D     string `json:"d"`
	Kid   string `json:"kid"`
	Alg   string `json:"alg"`
	Use   string `json:"use"`
	State string `json:"state"`
}

func LoadIdentitySigner(path string) (*FileIdentitySigner, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identity keys: %w", err)
	}
	var file jwkKeyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse identity keys: %w", err)
	}
	var chosen *jwkKey
	for i := range file.Keys {
		k := &file.Keys[i]
		if strings.EqualFold(k.State, "CURRENT") || strings.EqualFold(k.State, "KEY_STATE_CURRENT") {
			chosen = k
			break
		}
	}
	if chosen == nil {
		for i := range file.Keys {
			if file.Keys[i].D != "" {
				chosen = &file.Keys[i]
				break
			}
		}
	}
	if chosen == nil || chosen.D == "" || chosen.Kid == "" {
		return nil, fmt.Errorf("no CURRENT Ed25519 private key with kid in %s", path)
	}
	if chosen.Kty != "" && chosen.Kty != "OKP" {
		return nil, fmt.Errorf("unsupported kty %q", chosen.Kty)
	}
	if chosen.Crv != "" && chosen.Crv != "Ed25519" {
		return nil, fmt.Errorf("unsupported crv %q", chosen.Crv)
	}
	seed, err := base64.RawURLEncoding.DecodeString(chosen.D)
	if err != nil {
		return nil, fmt.Errorf("decode d: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("ed25519 seed must be %d bytes", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	if chosen.X != "" {
		x, err := base64.RawURLEncoding.DecodeString(chosen.X)
		if err != nil {
			return nil, fmt.Errorf("decode x: %w", err)
		}
		if !ed25519.PublicKey(x).Equal(pub) {
			return nil, fmt.Errorf("public x does not match private d")
		}
	}
	return &FileIdentitySigner{
		kid:    chosen.Kid,
		priv:   priv,
		pub:    pub,
		now:    time.Now,
		maxTTL: 60 * time.Second,
	}, nil
}

func (s *FileIdentitySigner) Kid() string                  { return s.kid }
func (s *FileIdentitySigner) PublicKey() ed25519.PublicKey { return s.pub }

func (s *FileIdentitySigner) Sign(claims IdentityClaims) (string, error) {
	if claims.Iss == "" {
		claims.Iss = "auriga-edge"
	}
	if claims.Pv == 0 {
		claims.Pv = 1
	}
	now := s.now().UTC()
	if claims.Iat == 0 {
		claims.Iat = now.Unix()
	}
	if claims.Exp == 0 {
		claims.Exp = claims.Iat + int64(s.maxTTL/time.Second)
	}
	if claims.Exp > claims.Iat+60 {
		return "", fmt.Errorf("exp must be <= iat+60")
	}
	if claims.Jti == "" {
		jti, err := randomID(16)
		if err != nil {
			return "", err
		}
		claims.Jti = jti
	}
	hdr := identityHeader{Alg: "EdDSA", Kid: s.kid, Typ: "auriga-id+jwt"}
	hb, err := json.Marshal(hdr)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	hEnc := base64.RawURLEncoding.EncodeToString(hb)
	pEnc := base64.RawURLEncoding.EncodeToString(pb)
	signingInput := hEnc + "." + pEnc
	sig := ed25519.Sign(s.priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyIdentityToken verifies a compact JWS with the given public key (tests / local checks).
func VerifyIdentityToken(token string, pub ed25519.PublicKey) (IdentityClaims, identityHeader, error) {
	var zero IdentityClaims
	var hdr identityHeader
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return zero, hdr, fmt.Errorf("invalid jws")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, hdr, err
	}
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return zero, hdr, err
	}
	if hdr.Alg != "EdDSA" || hdr.Typ != "auriga-id+jwt" {
		return zero, hdr, fmt.Errorf("unexpected header")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return zero, hdr, err
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return zero, hdr, fmt.Errorf("bad signature")
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, hdr, err
	}
	var claims IdentityClaims
	if err := json.Unmarshal(pb, &claims); err != nil {
		return zero, hdr, err
	}
	return claims, hdr, nil
}

func WriteTestIdentityKeys(path, kid string) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	seed := priv.Seed()
	file := jwkKeyFile{Keys: []jwkKey{{
		Kty:   "OKP",
		Crv:   "Ed25519",
		X:     base64.RawURLEncoding.EncodeToString(pub),
		D:     base64.RawURLEncoding.EncodeToString(seed),
		Kid:   kid,
		Alg:   "EdDSA",
		Use:   "sig",
		State: "CURRENT",
	}}}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

func randomID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
