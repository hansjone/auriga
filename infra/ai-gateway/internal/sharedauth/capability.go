// Package sharedauth implements the K v1 capability boundary for the self-owned
// shared path. It does not repair native DSH owner or filesystem isolation.
package sharedauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

const Model = "auriga.shared.model"
const Message = "auriga.shared.message"
const maxInt int64 = 9007199254740991

type fault string

func (e fault) Error() string { return string(e) }

var ErrInvalid error = fault("common.validation_failed")
var ErrBinding error = fault("shared.binding_mismatch")
var ErrRevoked error = fault("shared.revoked")
var ErrUnavailable error = fault("common.unavailable")
var ErrExpired error = fault("shared.expired")
var ErrSignature error = fault("shared.signature_invalid")

// Binding is the explicit JSON mapping of K SessionBinding, not protojson.
// Value semantics prevent requests from sharing mutable current-user state.
type Binding struct {
	TenantID           string `json:"tenantId"`
	MemberID           string `json:"memberId"`
	PlacementID        string `json:"placementId"`
	PhysicalInstanceID string `json:"physicalInstanceId"`
	Mode               string `json:"mode"`
	Epoch              int64  `json:"epoch"`
	Revision           int64  `json:"revision"`
	SessionID          string `json:"sessionId"`
	PlatformSessionID  string `json:"platformSessionId"`
	AuthGeneration     int64  `json:"authGeneration"`
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func UUID(s string) bool    { return uuidPattern.MatchString(s) }
func positive(n int64) bool { return n > 0 && n <= maxInt }
func (b Binding) Valid() bool {
	return UUID(b.TenantID) && UUID(b.MemberID) && UUID(b.PlacementID) && UUID(b.PhysicalInstanceID) && UUID(b.SessionID) && UUID(b.PlatformSessionID) && (b.Mode == "shared" || b.Mode == "dedicated") && positive(b.Epoch) && positive(b.Revision) && positive(b.AuthGeneration)
}

type Claims struct {
	SchemaVersion     int64    `json:"schemaVersion"`
	Binding           Binding  `json:"binding"`
	TaskID            string   `json:"taskId"`
	Audience          string   `json:"audience"`
	Scopes            []string `json:"scopes"`
	CredentialVersion int64    `json:"credentialVersion"`
	IssuedAt          int64    `json:"issuedAt"`
	ExpiresAt         int64    `json:"expiresAt"`
	JTI               string   `json:"jti"`
}
type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// Lookup comes from an authenticated internal caller. Tenant must be supplied
// by the trusted transport, never inferred from an unverified capability.
type Lookup struct{ TenantID, SessionID, TaskID, Audience, JTI string }
type Snapshot struct {
	Binding           Binding
	CredentialVersion int64
	Active            bool
	Scopes            []string
}

// Authority MUST read registered session ownership, current placement/IAM login,
// task liveness, member ACL and audience credential version on EVERY call. JTI
// revocation must be checked when supplied. Unreachable authority fails closed.
// No token database or expiry-only allow cache is implemented here.
type Authority interface {
	Resolve(context.Context, Lookup) (Snapshot, error)
}
type Gate struct {
	authority Authority
	keys      map[string]ed25519.PublicKey
	now       func() time.Time
}

func New(a Authority, keys map[string]ed25519.PublicKey, now func() time.Time) (*Gate, error) {
	if a == nil || now == nil || len(keys) == 0 {
		return nil, ErrInvalid
	}
	g := &Gate{authority: a, keys: map[string]ed25519.PublicKey{}, now: now}
	for k, v := range keys {
		if k == "" || len(k) > 128 || len(v) != ed25519.PublicKeySize {
			return nil, ErrInvalid
		}
		g.keys[k] = slices.Clone(v)
	}
	return g, nil
}
func (g *Gate) Resolve(ctx context.Context, q Lookup) (Snapshot, error) {
	if !UUID(q.TenantID) || !UUID(q.SessionID) || !UUID(q.TaskID) || (q.Audience != Model && q.Audience != Message) {
		return Snapshot{}, ErrInvalid
	}
	s, e := g.authority.Resolve(ctx, q)
	if e != nil || ctx.Err() != nil {
		return Snapshot{}, ErrUnavailable
	}
	if !s.Active {
		return Snapshot{}, ErrRevoked
	}
	if !s.Binding.Valid() || !positive(s.CredentialVersion) {
		return Snapshot{}, ErrUnavailable
	}
	if s.Binding.TenantID != q.TenantID || s.Binding.SessionID != q.SessionID {
		return Snapshot{}, ErrBinding
	}
	s.Scopes = slices.Clone(s.Scopes)
	return s, nil
}
func allowed(a, s string) bool {
	return a == Model && s == "model.invoke" || a == Message && (s == "message.send" || s == "message.receive")
}
func valid(c Claims) bool {
	if c.SchemaVersion != 1 || !c.Binding.Valid() || !UUID(c.TaskID) || !UUID(c.JTI) || !positive(c.CredentialVersion) || c.IssuedAt < 0 || c.IssuedAt > maxInt || c.ExpiresAt > maxInt || len(c.Scopes) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range c.Scopes {
		if !allowed(c.Audience, s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func (g *Gate) check(ctx context.Context, c Claims, tenant, session, task, audience, scope string) error {
	if !valid(c) {
		return ErrInvalid
	}
	now := g.now().Unix()
	if c.IssuedAt > now || c.ExpiresAt <= now || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt-c.IssuedAt > 600 {
		return ErrExpired
	}
	if c.Binding.TenantID != tenant || c.Binding.SessionID != session || c.TaskID != task || c.Audience != audience {
		return ErrBinding
	}
	if !allowed(audience, scope) || !slices.Contains(c.Scopes, scope) {
		return ErrRevoked
	}
	s, e := g.Resolve(ctx, Lookup{tenant, session, task, audience, c.JTI})
	if e != nil {
		return e
	}
	if s.Binding != c.Binding {
		return ErrBinding
	}
	if s.CredentialVersion != c.CredentialVersion {
		return ErrRevoked
	}
	for _, scope := range c.Scopes {
		if !slices.Contains(s.Scopes, scope) {
			return ErrRevoked
		}
	}
	// Online checks can consume the remaining lifetime; do not authorize a
	// response that became expired while waiting on W/IAM/P.
	if g.now().Unix() >= c.ExpiresAt {
		return ErrExpired
	}
	return nil
}

// Canonical uses the K subset (safe integers and ASCII protocol values). Round
// trip comparison rejects duplicate keys, unknown fields and alternate encoding.
func canonical(v any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e = d.Decode(&value); e != nil {
		return nil, e
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	e = enc.Encode(value)
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), e
}
func decode(part string, v any) error {
	b, e := base64.RawURLEncoding.Strict().DecodeString(part)
	if e != nil {
		return ErrSignature
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return ErrSignature
	}
	c, e := canonical(v)
	if e != nil || !bytes.Equal(c, b) {
		return ErrSignature
	}
	return nil
}
func (g *Gate) Verify(ctx context.Context, token, tenant, session, task, audience, scope string) (Claims, error) {
	var c Claims
	p := strings.Split(token, ".")
	if len(token) > 16384 || len(p) != 3 {
		return c, ErrSignature
	}
	var h header
	if decode(p[0], &h) != nil || decode(p[1], &c) != nil || h.Alg != "EdDSA" || h.Typ != "auriga-shared-capability+jwt" {
		return Claims{}, ErrSignature
	}
	sig, e := base64.RawURLEncoding.Strict().DecodeString(p[2])
	key := g.keys[h.Kid]
	if e != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, []byte(p[0]+"."+p[1]), sig) {
		return Claims{}, ErrSignature
	}
	if e = g.check(ctx, c, tenant, session, task, audience, scope); e != nil {
		return Claims{}, e
	}
	return c, nil
}

// Issue accepts only a trusted Lookup, never caller-supplied ownership. Signing
// keys are runtime configuration; upstream model keys never enter a capability.
func (g *Gate) Issue(ctx context.Context, q Lookup, scopes []string, ttl time.Duration, kid string, key ed25519.PrivateKey) (string, error) {
	if ttl < time.Second || ttl > 600*time.Second || ttl%time.Second != 0 || len(key) != ed25519.PrivateKeySize || !bytes.Equal(g.keys[kid], key.Public().(ed25519.PublicKey)) {
		return "", ErrInvalid
	}
	q.JTI = ""
	s, e := g.Resolve(ctx, q)
	if e != nil {
		return "", e
	}
	now := g.now().Unix()
	c := Claims{1, s.Binding, q.TaskID, q.Audience, slices.Clone(scopes), s.CredentialVersion, now, now + int64(ttl/time.Second), uuid.NewString()}
	if e = g.check(ctx, c, q.TenantID, q.SessionID, q.TaskID, q.Audience, first(scopes)); e != nil {
		return "", e
	}
	h, _ := canonical(header{"EdDSA", kid, "auriga-shared-capability+jwt"})
	b, _ := canonical(c)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(b)
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(input))), nil
}
func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

// Recheck is used before delivering stream chunks and queued side effects.
func (g *Gate) Recheck(ctx context.Context, c Claims, scope string) error {
	return g.check(ctx, c, c.Binding.TenantID, c.Binding.SessionID, c.TaskID, c.Audience, scope)
}
