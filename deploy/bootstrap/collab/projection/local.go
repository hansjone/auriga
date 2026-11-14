package projection

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

const MaxFile = 1048576

var fileName = regexp.MustCompile(`^(publication\.json|bundle\.json|protocol\.json|leaf-[0-9]{3}\.(cert|key)\.pem)$`)
var hash = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Fault string

func (f Fault) Error() string { return string(f) }
func number(s string) (uint64, error) {
	n, e := strconv.ParseUint(s, 10, 64)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != s {
		return 0, errors.New("projection: invalid number")
	}
	return n, nil
}
func CheckSelector(s api.LocalProjectionSelector) error {
	for _, v := range []string{s.Generation, s.KeysetVersion, s.BundleVersion, s.MaterialRevision, s.Realm} {
		if _, e := number(v); e != nil {
			return Fault("invalid_chain")
		}
	}
	n, e := base64.RawURLEncoding.Strict().DecodeString(s.Nonce)
	if e != nil || len(n) != 32 || s.TenantId == "" || s.InstallationId == "" || (s.Component != "network" && s.Component != "broker" && s.Component != "edge") {
		return Fault("invalid_chain")
	}
	for _, d := range []string{s.PublicationDigest, s.BundleDigest, s.ManifestDigest} {
		if !hash.MatchString(d) {
			return Fault("invalid_chain")
		}
	}
	return nil
}
func Equal(a, b any) bool {
	x, e := Bytes(a)
	y, f := Bytes(b)
	return e == nil && f == nil && string(x) == string(y)
}
func CheckProgress(s api.LocalProjectionSelector, old *api.LocalProjectionSelector) error {
	if e := CheckSelector(s); e != nil {
		return e
	}
	if old == nil {
		return nil
	}
	if s.TenantId != old.TenantId || s.Realm != old.Realm || s.Component != old.Component || s.InstallationId != old.InstallationId {
		return Fault("scope_denied")
	}
	less := func(a, b string) bool { x, _ := number(a); y, _ := number(b); return x < y }
	if less(s.Generation, old.Generation) || (s.Generation == old.Generation && less(s.KeysetVersion, old.KeysetVersion)) || less(s.BundleVersion, old.BundleVersion) || less(s.MaterialRevision, old.MaterialRevision) {
		return Fault("version_rollback")
	}
	if s.MaterialRevision == old.MaterialRevision && !Equal(s, *old) {
		return Fault("same_version_conflict")
	}
	return nil
}

// CheckFiles is also used by the shared-vector adapter. It checks bytes, not a
// caller's claimed file digests. Trust-chain verification is an additional gate.
func CheckFiles(s api.LocalProjectionSelector, m api.LocalProjectionManifest, files map[string][]byte, published api.LocalProjectionSelector, old *api.LocalProjectionSelector) error {
	if e := CheckProgress(s, old); e != nil {
		return e
	}
	md, e := ObjectDigest(m)
	if e != nil || md != s.ManifestDigest {
		return Fault("invalid_chain")
	}
	sb, _ := Bytes(s)
	mb, _ := Bytes(m)
	var sm, mm map[string]json.RawMessage
	_ = json.Unmarshal(sb, &sm)
	_ = json.Unmarshal(mb, &mm)
	delete(sm, "manifest_digest")
	delete(mm, "files")
	if !Equal(sm, mm) {
		return Fault("invalid_chain")
	}
	if len(m.AdditionalProperties) != 0 || len(m.Files) < 2 || len(m.Files) > 128 || len(files) != len(m.Files) {
		return Fault("invalid_chain")
	}
	names := make([]string, 0, len(m.Files))
	seen := map[string]bool{}
	for _, f := range m.Files {
		b, ok := files[f.Name]
		if !fileName.MatchString(f.Name) || seen[f.Name] || len(f.AdditionalProperties) != 0 || !ok || len(b) > MaxFile || int(f.Size) != len(b) || Digest(b) != f.Sha256 {
			return Fault("invalid_chain")
		}
		seen[f.Name] = true
		names = append(names, f.Name)
	}
	if !sort.StringsAreSorted(names) || !seen["publication.json"] || !seen["bundle.json"] || !Equal(s, published) {
		return Fault("invalid_chain")
	}
	return nil
}
