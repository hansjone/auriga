package network

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"regexp"
	"time"
)

var opaqueID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var keyID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type ProtocolSigningConfig struct {
	PrivateKeyFile string `json:"privateKeyFile"`
	KeyID          string `json:"keyId"`
	KeysetVersion  string `json:"keysetVersion"`
	NotBefore      string `json:"notBefore"`
	NotAfter       string `json:"notAfter"`
}

type errorSigner struct {
	key        ed25519.PrivateKey
	cfg        ProtocolSigningConfig
	begin, end time.Time
}

func newErrorSigner(cfg ProtocolSigningConfig) (*errorSigner, error) {
	begin, e1 := time.Parse(time.RFC3339, cfg.NotBefore)
	end, e2 := time.Parse(time.RFC3339, cfg.NotAfter)
	if e1 != nil || e2 != nil || time.Now().Before(begin) || !time.Now().Before(end) || !keyID.MatchString(cfg.KeyID) || !validNetworkNumber(cfg.KeysetVersion) {
		return nil, errors.New("network: invalid protocol signing configuration")
	}
	raw, err := os.ReadFile(cfg.PrivateKeyFile)
	if err != nil {
		return nil, errors.New("network: protocol signing key unavailable")
	}
	defer clear(raw)
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, errors.New("network: protocol key must be Ed25519 PKCS8 PEM")
	}
	defer clear(block.Bytes)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("network: invalid protocol key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("network: protocol key must be Ed25519")
	}
	return &errorSigner{key: key, cfg: cfg, begin: begin, end: end}, nil
}

// Only signs our fixed ERROR shape. Every string is restricted to the ASCII
// contract alphabet, so encoding/json's sorted keys are exactly JCS here. This
// is deliberately NOT a general canonicalizer for collab's business payloads.
func (s *errorSigner) encode(b liveBinding, requestID string, ae *AppError) ([]byte, error) {
	now := time.Now().UTC().Truncate(time.Second)
	if now.Before(s.begin) || !now.Before(s.end) || !now.Before(b.NotAfter) {
		return nil, errors.New("network: protocol signer or connection expired")
	}
	if !opaqueID.MatchString(requestID) {
		requestID = ""
	}
	incarnation, nonce, epoch := b.Incarnation, b.ChannelNonce, b.Epoch
	if !opaqueID.MatchString(incarnation) {
		incarnation = "unbound"
	}
	if !opaqueID.MatchString(nonce) {
		nonce = b.Ingress
	}
	expiry := minTime(now.Add(time.Minute), minTime(s.end, b.NotAfter))
	var rid any
	if requestID != "" {
		rid = requestID
	}
	auth := map[string]any{"version": 1, "purpose": "protocol_response", "alg": "Ed25519", "kid": s.cfg.KeyID, "issuer_realm": b.Realm, "issued_at": now.Format(time.RFC3339), "not_after": expiry.UTC().Format(time.RFC3339), "audience": map[string]any{"realm": b.Realm, "node": b.Node, "incarnation": incarnation, "epoch": u64str(epoch)}, "request_id": rid, "channel_nonce": nonce, "keyset_version": s.cfg.KeysetVersion}
	copyError := *ae
	copyError.RequestID = requestID
	raw, err := EncodeError(&copyError, auth)
	if err != nil {
		return nil, err
	}
	message := append([]byte("Auriga.ServerEnvelope.v1\x00"), raw...)
	auth["signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(s.key, message))
	return EncodeError(&copyError, auth)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
