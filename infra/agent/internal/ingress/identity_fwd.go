package ingress

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// IdentityKeys maps kid → Ed25519 public key for Auriga-Identity verification.
type IdentityKeys map[string]ed25519.PublicKey

// SelectForwardIdentity returns the edge-signed compact JWS to forward, or "".
// Forged / invalid / multi-value headers are dropped (stripped).
func SelectForwardIdentity(values []string, keys IdentityKeys, now func() time.Time) string {
	if len(values) == 0 {
		return ""
	}
	// Prefer a single verified token; ignore extras.
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if len(keys) == 0 {
			// No keys configured: still only forward if exactly one value.
			if len(values) == 1 {
				return v
			}
			return ""
		}
		if verifyIdentityJWS(v, keys, now) {
			return v
		}
	}
	return ""
}

func verifyIdentityJWS(token string, keys IdentityKeys, now func() time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if json.Unmarshal(hdrRaw, &hdr) != nil {
		return false
	}
	if hdr.Alg != "EdDSA" || hdr.Typ != "auriga-id+jwt" {
		return false
	}
	pub, ok := keys[hdr.Kid]
	if !ok {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Iss string `json:"iss"`
		Exp int64  `json:"exp"`
		Iat int64  `json:"iat"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	if claims.Iss != "auriga-edge" {
		return false
	}
	t := time.Now()
	if now != nil {
		t = now()
	}
	skew := 5 * time.Second
	if claims.Exp == 0 || t.After(time.Unix(claims.Exp, 0).Add(skew)) {
		return false
	}
	if claims.Iat != 0 && t.Before(time.Unix(claims.Iat, 0).Add(-skew)) {
		return false
	}
	return true
}

// SignIdentityJWS is a test helper matching edge's Auriga-Identity format.
func SignIdentityJWS(priv ed25519.PrivateKey, kid string, claims map[string]any) (string, error) {
	hdr, _ := json.Marshal(map[string]string{"alg": "EdDSA", "kid": kid, "typ": "auriga-id+jwt"})
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	h := base64.RawURLEncoding.EncodeToString(hdr)
	p := base64.RawURLEncoding.EncodeToString(payload)
	sig := ed25519.Sign(priv, []byte(h+"."+p))
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
