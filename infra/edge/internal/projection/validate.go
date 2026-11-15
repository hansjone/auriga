// Package projection implements the frozen local projection protocol. Its file
// checks do not substitute for the reader's root-chain and TLS material verifier.
package projection

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"unicode/utf8"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

// Canonical rejects duplicate keys and returns JCS for the integer-only local
// models. Floating point values are not part of these signed protocol models.
func Canonical(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var parse func(int) (any, error)
	parse = func(depth int) (any, error) {
		if depth > 64 {
			return nil, fmt.Errorf("JSON nesting limit")
		}
		t, e := d.Token()
		if e != nil {
			return nil, e
		}
		switch t {
		case json.Delim('{'):
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				key, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("invalid key")
				}
				if _, ok = m[key]; ok {
					return nil, fmt.Errorf("duplicate key")
				}
				v, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				m[key] = v
			}
			_, e = d.Token()
			return m, e
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, e := parse(depth + 1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			_, e = d.Token()
			return a, e
		default:
			return t, nil
		}
	}
	value, e := parse(0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return encodeCanonical(value)
}

func Bytes(value any) ([]byte, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	return Canonical(raw)
}
func Digest(raw []byte) string { s := sha256.Sum256(raw); return "sha256:" + hex.EncodeToString(s[:]) }
func Equal(a, b any) bool {
	x, e := Bytes(a)
	if e != nil {
		return false
	}
	y, e := Bytes(b)
	return e == nil && bytes.Equal(x, y)
}
func number(s string) (uint64, error) {
	n, e := strconv.ParseUint(s, 10, 64)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != s {
		return 0, fmt.Errorf("invalid sequence")
	}
	return n, nil
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var filePattern = regexp.MustCompile(`^(publication\.json|bundle\.json|protocol\.json|leaf-[0-9]{3}\.(cert|key)\.pem)$`)

func SelectorValid(s api.LocalProjectionSelector) bool {
	if !knownFields(reflect.ValueOf(s)) {
		return false
	}
	for _, v := range []string{s.Realm, s.Generation, s.KeysetVersion, s.BundleVersion, s.MaterialRevision} {
		if _, e := number(v); e != nil {
			return false
		}
	}
	for _, v := range []string{s.ManifestDigest, s.PublicationDigest, s.BundleDigest} {
		if !digestPattern.MatchString(v) {
			return false
		}
	}
	nonce, e := base64.RawURLEncoding.Strict().DecodeString(s.Nonce)
	return e == nil && len(nonce) == 32 && base64.RawURLEncoding.EncodeToString(nonce) == s.Nonce && utf8.RuneCountInString(s.TenantId) > 0 && utf8.RuneCountInString(s.TenantId) <= 128 && utf8.RuneCountInString(s.InstallationId) > 0 && utf8.RuneCountInString(s.InstallationId) <= 128 && (s.Component == "edge" || s.Component == "network" || s.Component == "broker")
}

// Facts come exclusively from the reader's durable journal and active handles.
type Facts struct{ Persisted, Loaded, Failed bool }

// Validate is shared by the live reader and the common vector adapter.
func Validate(r api.LocalProjectionRequest, m api.LocalProjectionManifest, files map[string][]byte, published api.LocalProjectionSelector, previous *api.LocalProjectionSelector, f Facts) string {
	s := r.Selector
	if !SelectorValid(s) || !knownFields(reflect.ValueOf(r)) || !knownFields(reflect.ValueOf(m)) || (r.Operation != "reload" && r.Operation != "confirm") {
		return "invalid_chain"
	}
	if previous != nil {
		if !SelectorValid(*previous) {
			return "invalid_chain"
		}
		g, _ := number(s.Generation)
		og, _ := number(previous.Generation)
		k, _ := number(s.KeysetVersion)
		ok, _ := number(previous.KeysetVersion)
		b, _ := number(s.BundleVersion)
		ob, _ := number(previous.BundleVersion)
		v, _ := number(s.MaterialRevision)
		ov, _ := number(previous.MaterialRevision)
		if g < og || (g == og && (k < ok || b < ob)) || v < ov {
			return "version_rollback"
		}
		if v == ov && !Equal(s, *previous) {
			return "same_version_conflict"
		}
		if s.TenantId != previous.TenantId || s.Realm != previous.Realm || s.InstallationId != previous.InstallationId || s.Component != previous.Component {
			return "invalid_chain"
		}
	}
	raw, e := Bytes(m)
	if e != nil || Digest(raw) != s.ManifestDigest || len(m.AdditionalProperties) != 0 {
		return "invalid_chain"
	}
	// Binding fields are compared from generated JSON, without another wire DTO.
	a, _ := Bytes(s)
	var binding map[string]any
	var content map[string]any
	_ = json.Unmarshal(a, &binding)
	_ = json.Unmarshal(raw, &content)
	delete(binding, "manifest_digest")
	delete(content, "files")
	if !Equal(binding, content) || len(m.Files) < 2 || len(m.Files) > 128 || len(files) != len(m.Files) {
		return "invalid_chain"
	}
	last := ""
	hasPublication, hasBundle := false, false
	for _, file := range m.Files {
		if len(file.AdditionalProperties) != 0 || !filePattern.MatchString(file.Name) || file.Name <= last || file.Size < 1 || file.Size > 1048576 {
			return "invalid_chain"
		}
		last = file.Name
		data, exists := files[file.Name]
		if !exists || len(data) != int(file.Size) || Digest(data) != file.Sha256 {
			return "invalid_chain"
		}
		hasPublication = hasPublication || file.Name == "publication.json"
		hasBundle = hasBundle || file.Name == "bundle.json"
	}
	if !hasPublication || !hasBundle || !Equal(s, published) {
		return "invalid_chain"
	}
	if !f.Persisted || (r.Operation == "confirm" && !f.Loaded) || f.Failed {
		return "projection_pending"
	}
	return "accept"
}
