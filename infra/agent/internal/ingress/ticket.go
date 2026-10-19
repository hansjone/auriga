package ingress

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FileOp is a ticket-authorized filesystem operation.
type FileOp string

const (
	OpRead   FileOp = "read"
	OpWrite  FileOp = "write"
	OpDelete FileOp = "delete"
	OpList   FileOp = "list"
	OpZip    FileOp = "zip"
)

// FileTicketClaims are workspace-issued short-lived file access claims.
// Compact JWS (EdDSA), typ=auriga-file+jwt.
type FileTicketClaims struct {
	Iss        string   `json:"iss"`
	Aud        string   `json:"aud"`
	IID        string   `json:"iid"`
	PathPrefix string   `json:"path_prefix"`
	Ops        []string `json:"ops"`
	Iat        int64    `json:"iat"`
	Exp        int64    `json:"exp"`
	Jti        string   `json:"jti"`
}

type ticketHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// TicketVerifier verifies workspace file tickets.
type TicketVerifier struct {
	Keys map[string]ed25519.PublicKey // kid -> pub
	Now  func() time.Time
	// ClockSkew is allowed skew when checking iat/exp (default 5s).
	ClockSkew time.Duration
}

// Verify parses and validates a compact JWS ticket for the given instance and op.
func (v *TicketVerifier) Verify(token, instanceID string, op FileOp, relPath string) (*FileTicketClaims, error) {
	if v == nil || len(v.Keys) == 0 {
		return nil, fmt.Errorf("%s: no ticket keys", CodeTicketInvalid)
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	skew := v.ClockSkew
	if skew == 0 {
		skew = 5 * time.Second
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%s: malformed jws", CodeTicketInvalid)
	}
	hdrRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%s: bad header", CodeTicketInvalid)
	}
	var hdr ticketHeader
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil {
		return nil, fmt.Errorf("%s: bad header json", CodeTicketInvalid)
	}
	if hdr.Alg != "EdDSA" || hdr.Typ != "auriga-file+jwt" {
		return nil, fmt.Errorf("%s: unsupported alg/typ", CodeTicketInvalid)
	}
	pub, ok := v.Keys[hdr.Kid]
	if !ok {
		return nil, fmt.Errorf("%s: unknown kid", CodeTicketInvalid)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("%s: bad signature encoding", CodeTicketInvalid)
	}
	signingInput := parts[0] + "." + parts[1]
	if !ed25519.Verify(pub, []byte(signingInput), sig) {
		return nil, fmt.Errorf("%s: signature mismatch", CodeTicketInvalid)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%s: bad payload", CodeTicketInvalid)
	}
	var c FileTicketClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("%s: bad claims", CodeTicketInvalid)
	}
	if c.Iss != "auriga-workspace" || c.Aud != "auriga-agent" {
		return nil, fmt.Errorf("%s: bad iss/aud", CodeTicketInvalid)
	}
	t := now()
	if c.Exp == 0 || t.After(time.Unix(c.Exp, 0).Add(skew)) {
		return nil, fmt.Errorf("%s: expired", CodeTicketExpired)
	}
	if c.Iat != 0 && t.Before(time.Unix(c.Iat, 0).Add(-skew)) {
		return nil, fmt.Errorf("%s: not yet valid", CodeTicketInvalid)
	}
	if c.IID != instanceID {
		return nil, fmt.Errorf("%s: instance mismatch", CodeTicketInstanceMismatch)
	}
	if !opAllowed(c.Ops, op) {
		return nil, fmt.Errorf("%s: op %s not allowed", CodeTicketOpDenied, op)
	}
	prefix := strings.TrimPrefix(c.PathPrefix, "/")
	rel := strings.TrimPrefix(relPath, "/")
	if prefix != "" && prefix != "." {
		if rel != prefix && !strings.HasPrefix(rel, strings.TrimSuffix(prefix, "/")+"/") {
			return nil, fmt.Errorf("%s: path outside ticket prefix", CodeTicketPathDenied)
		}
	}
	return &c, nil
}

func opAllowed(ops []string, want FileOp) bool {
	for _, o := range ops {
		if FileOp(o) == want {
			return true
		}
	}
	return false
}

// SignFileTicket is a test/helper signer (workspace side).
func SignFileTicket(priv ed25519.PrivateKey, kid string, c FileTicketClaims) (string, error) {
	hdr := ticketHeader{Alg: "EdDSA", Kid: kid, Typ: "auriga-file+jwt"}
	hb, err := json.Marshal(hdr)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	h := base64.RawURLEncoding.EncodeToString(hb)
	p := base64.RawURLEncoding.EncodeToString(pb)
	sig := ed25519.Sign(priv, []byte(h+"."+p))
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// ExtractTicketToken reads ticket from Authorization Bearer or ?ticket=.
func ExtractTicketToken(authHeader, queryTicket string) string {
	if queryTicket != "" {
		return queryTicket
	}
	const p = "Bearer "
	if strings.HasPrefix(authHeader, p) {
		return strings.TrimSpace(authHeader[len(p):])
	}
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}
	return ""
}
