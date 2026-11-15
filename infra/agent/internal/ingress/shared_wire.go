package ingress

// Local wire validation for contracts/shared-dsh (not a new shared type contract).
import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"

	"unicode/utf8"
)

var sharedUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var errSharedInvalid = errors.New("shared.signature_invalid")

func sharedObject(v any, fields string) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	fs := strings.Fields(fields)
	if len(m) != len(fs) {
		return nil, false
	}
	for _, f := range fs {
		if _, ok := m[f]; !ok {
			return nil, false
		}
	}
	return m, true
}
func sharedNumber(v any, min int64) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, e := n.Int64()
	return e == nil && i >= min && i <= 9007199254740991
}
func sharedInt(v any) int64     { n, _ := v.(json.Number); i, _ := n.Int64(); return i }
func sharedString(v any) string { s, _ := v.(string); return s }
func sharedBinding(v any) bool {
	m, ok := sharedObject(v, "tenantId memberId placementId physicalInstanceId mode epoch revision")
	if !ok {
		return false
	}
	for _, f := range strings.Fields("tenantId memberId placementId physicalInstanceId") {
		if !sharedUUID.MatchString(sharedString(m[f])) {
			return false
		}
	}
	return (m["mode"] == "shared" || m["mode"] == "dedicated") && sharedNumber(m["epoch"], 1) && sharedNumber(m["revision"], 1)
}
func sharedEqual(a, b any) bool {
	x, _ := sharedMarshal(a)
	y, _ := sharedMarshal(b)
	return bytes.Equal(x, y)
}
func sharedPath(p string) bool {
	if len(p) == 0 || len(p) > 4096 || p[0] != '/' || strings.ContainsAny(p, "?#\\\r\n") || strings.Contains(p, "//") {
		return false
	}
	l := strings.ToLower(p)
	for _, s := range []string{"%2f", "%5c", "%2e", "%25"} {
		if strings.Contains(l, s) {
			return false
		}
	}
	d, e := url.PathUnescape(p)
	if e != nil || !utf8.ValidString(d) {
		return false
	}
	for _, r := range d {
		if r < 32 || r == 127 {
			return false
		}
	}
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return false
		}
	}
	return true
}
func sharedClaims(m map[string]any, identity bool) bool {
	fields := "schemaVersion binding platformSessionId audience issuedAt expiresAt jti"
	if identity {
		fields += " issuer method canonicalPath"
	}
	if _, ok := sharedObject(m, fields); !ok {
		return false
	}
	if !sharedBinding(m["binding"]) || !sharedUUID.MatchString(sharedString(m["platformSessionId"])) || !sharedUUID.MatchString(sharedString(m["jti"])) || !sharedNumber(m["issuedAt"], 0) || !sharedNumber(m["expiresAt"], 0) {
		return false
	}
	if identity {
		return sharedInt(m["schemaVersion"]) == 2 && m["issuer"] == "auriga.edge" && m["audience"] == "auriga.shared.ingress" && strings.Contains(" GET POST PUT PATCH DELETE HEAD OPTIONS ", " "+sharedString(m["method"])+" ") && sharedString(m["method"]) != "" && sharedPath(sharedString(m["canonicalPath"]))
	}
	return sharedInt(m["schemaVersion"]) == 1 && m["audience"] == "auriga.placement.exchange"
}

// Canonical round trip rejects duplicate keys, unknown fields, padding, floats and whitespace.
func sharedDecode(raw []byte) (map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var m map[string]any
	if d.Decode(&m) != nil {
		return nil, errSharedInvalid
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return nil, errSharedInvalid
	}
	b, e := sharedMarshal(m)
	if e != nil || !bytes.Equal(b, raw) {
		return nil, errSharedInvalid
	}
	return m, nil
}
func sharedVerify(token string, keys map[string]ed25519.PublicKey, identity bool, now int64) (map[string]any, error) {
	if len(token) > 16384 {
		return nil, errSharedInvalid
	}
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return nil, errSharedInvalid
	}
	parts := make([][]byte, 3)
	for i, s := range p {
		b, e := base64.RawURLEncoding.Strict().DecodeString(s)
		if e != nil || base64.RawURLEncoding.EncodeToString(b) != s {
			return nil, errSharedInvalid
		}
		parts[i] = b
	}
	h, e := sharedDecode(parts[0])
	if e != nil {
		return nil, e
	}
	if _, ok := sharedObject(h, "alg kid typ"); !ok {
		return nil, errSharedInvalid
	}
	typ := "auriga-shared-ticket+jwt"
	if identity {
		typ = "auriga-shared-identity+jwt"
	}
	kid := sharedString(h["kid"])
	key := keys[kid]
	if h["alg"] != "EdDSA" || h["typ"] != typ || len(kid) == 0 || len(kid) > 128 || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, []byte(p[0]+"."+p[1]), parts[2]) {
		return nil, errSharedInvalid
	}
	m, e := sharedDecode(parts[1])
	if e != nil || !sharedClaims(m, identity) {
		return nil, errSharedInvalid
	}
	iat, exp := sharedInt(m["issuedAt"]), sharedInt(m["expiresAt"])
	if iat > now || exp <= now || exp <= iat || exp-iat > 60 {
		return nil, errors.New("shared.expired")
	}
	return m, nil
}

// JavaScript canonical() uses literal UTF-8 and does not HTML-escape strings.
func sharedMarshal(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte(`\u2028`), []byte("\u2028"))
	raw = bytes.ReplaceAll(raw, []byte(`\u2029`), []byte("\u2029"))
	return raw, nil
}
