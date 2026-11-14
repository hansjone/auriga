package runtime

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	resourcev1 "github.com/hansjone/auriga/infra/gen/auriga/resource/v1"
)

// UnifiedProtocol verifies resource statements using installation-owned roots.
// It does not obtain roots from an envelope, archive, spec, or download response.
type UnifiedProtocol struct {
	Keys []*resourcev1.ResourceTrustKey
}
type unifiedFailure string

func (e unifiedFailure) Error() string { return string(e) }
func urequire(ok bool, reason string) {
	if !ok {
		panic(unifiedFailure(reason))
	}
}
func um(v any) map[string]any { x, ok := v.(map[string]any); urequire(ok, "schema"); return x }
func us(v any) string         { x, ok := v.(string); urequire(ok, "schema"); return x }
func ua(v any) []any          { x, ok := v.([]any); urequire(ok, "schema"); return x }
func un(v any) int64 {
	x, ok := v.(json.Number)
	urequire(ok, "schema")
	n, err := x.Int64()
	urequire(err == nil && n >= 0 && n <= 9007199254740991, "schema")
	return n
}
func ut(v any) time.Time {
	s := us(v)
	t, err := time.Parse(time.RFC3339Nano, s)
	urequire(err == nil && strings.HasSuffix(s, "Z"), "schema")
	return t
}
func uequal(a, b any) bool {
	x, e1 := unifiedCanonical(a)
	y, e2 := unifiedCanonical(b)
	return e1 == nil && e2 == nil && bytes.Equal(x, y)
}
func udigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func ucontains(values any, value any) bool {
	for _, item := range ua(values) {
		if uequal(item, value) {
			return true
		}
	}
	return false
}

var uUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var uDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var uName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var uVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// DecodeUnifiedJSON rejects duplicate keys, invalid UTF-8, trailing documents,
// excessively deep input, and numbers outside the JSON safe integer domain.
func DecodeUnifiedJSON(raw []byte) (map[string]any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid resource UTF-8")
	}
	if len(raw) > 2*1024*1024 {
		return nil, fmt.Errorf("resource JSON budget exceeded")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var parse func(int) (any, error)
	parse = func(depth int) (any, error) {
		if depth > 40 {
			return nil, fmt.Errorf("resource JSON depth exceeded")
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case json.Delim:
			if t == '{' {
				result := map[string]any{}
				for decoder.More() {
					keyToken, e := decoder.Token()
					if e != nil {
						return nil, e
					}
					key, ok := keyToken.(string)
					if !ok {
						return nil, fmt.Errorf("invalid object key")
					}
					if _, exists := result[key]; exists {
						return nil, fmt.Errorf("duplicate JSON member")
					}
					child, e := parse(depth + 1)
					if e != nil {
						return nil, e
					}
					result[key] = child
				}
				_, e := decoder.Token()
				return result, e
			}
			if t == '[' {
				result := []any{}
				for decoder.More() {
					child, e := parse(depth + 1)
					if e != nil {
						return nil, e
					}
					result = append(result, child)
				}
				_, e := decoder.Token()
				return result, e
			}
			return nil, fmt.Errorf("unexpected delimiter")
		default:
			return token, nil
		}
	}
	value, err := parse(0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing resource JSON")
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resource JSON must be an object")
	}
	return result, nil
}

func uobject(v any, required string, optional string) map[string]any {
	o := um(v)
	allowed := map[string]bool{}
	for _, key := range strings.Fields(required) {
		_, ok := o[key]
		urequire(ok, "schema")
		allowed[key] = true
	}
	for _, key := range strings.Fields(optional) {
		allowed[key] = true
	}
	for key := range o {
		urequire(allowed[key], "schema")
	}
	return o
}
func usignature(v any) {
	s := uobject(v, "alg kid value", "")
	urequire(s["alg"] == "Ed25519" && len(us(s["kid"])) > 0 && len(us(s["kid"])) <= 128, "schema")
	us(s["value"])
}
func uidentity(v any) {
	o := uobject(v, "tenantId packageId versionId kind version manifestDigest", "")
	for _, key := range []string{"tenantId", "packageId", "versionId"} {
		urequire(uUUID.MatchString(us(o[key])), "schema")
	}
	urequire(uDigest.MatchString(us(o["manifestDigest"])) && uVersion.MatchString(us(o["version"])), "schema")
	urequire(o["kind"] == "skill" || o["kind"] == "tool", "schema")
}
func uenvelope(v any, protocol int64) {
	o := uobject(v, "schemaVersion tenantId packageId versionId releaseSequence notAfter statement signature", "")
	urequire(un(o["schemaVersion"]) == protocol && un(o["releaseSequence"]) > 0, "schema")
	for _, key := range []string{"tenantId", "packageId", "versionId"} {
		urequire(uUUID.MatchString(us(o[key])), "schema")
	}
	ut(o["notAfter"])
	usignature(o["signature"])
	var s map[string]any
	if protocol == 1 {
		s = uobject(o["statement"], "purpose name version os arch digest issuedAt", "")
		urequire(s["purpose"] == "plugin" && uDigest.MatchString(us(s["digest"])), "schema")
		us(s["os"])
		us(s["arch"])
	} else {
		s = uobject(o["statement"], "purpose kind name version manifestDigest artifactDigests issuedAt", "")
		urequire(s["purpose"] == "resource" && (s["kind"] == "skill" || s["kind"] == "tool" || s["kind"] == "plugin") && uDigest.MatchString(us(s["manifestDigest"])), "schema")
		seen := map[string]bool{}
		urequire(len(ua(s["artifactDigests"])) <= 32, "schema")
		for _, value := range ua(s["artifactDigests"]) {
			digest := us(value)
			urequire(uDigest.MatchString(digest) && !seen[digest], "schema")
			seen[digest] = true
		}
	}
	urequire(len(us(s["name"])) > 0 && len(us(s["name"])) <= 128 && uVersion.MatchString(us(s["version"])), "schema")
	ut(s["issuedAt"])
}
func umarker(v any) {
	o := uobject(v, "schemaVersion purpose tenantId packageId firstVersionId firstReleaseSequence activatedAt signature", "")
	urequire(un(o["schemaVersion"]) == 2 && o["purpose"] == "migration" && un(o["firstReleaseSequence"]) > 0, "schema")
	for _, key := range []string{"tenantId", "packageId", "firstVersionId"} {
		urequire(uUUID.MatchString(us(o[key])), "schema")
	}
	ut(o["activatedAt"])
	usignature(o["signature"])
}
func uwindow(start, end, now any, max time.Duration) bool {
	a, b, n := ut(start), ut(end), ut(now)
	return !n.Before(a) && n.Before(b) && b.After(a) && b.Sub(a) <= max
}

func (p UnifiedProtocol) signed(document map[string]any, purpose string, now time.Time) bool {
	signature := um(document["signature"])
	sig, err := base64.RawURLEncoding.DecodeString(us(signature["value"]))
	if err != nil || len(sig) != 64 || base64.RawURLEncoding.EncodeToString(sig) != signature["value"] || signature["alg"] != "Ed25519" {
		return false
	}
	body := map[string]any{}
	for key, value := range document {
		if key != "signature" {
			body[key] = value
		}
	}
	raw, err := unifiedCanonical(body)
	if err != nil {
		return false
	}
	for _, key := range p.Keys {
		if key.GetKid() != signature["kid"] || key.GetPurpose() != purpose {
			continue
		}
		start, e1 := time.Parse(time.RFC3339Nano, key.GetNotBefore())
		end, e2 := time.Parse(time.RFC3339Nano, key.GetNotAfter())
		if e1 != nil || e2 != nil || now.Before(start) || !now.Before(end) || len(key.GetPublicKey()) != 32 {
			return false
		}
		if ed25519.Verify(ed25519.PublicKey(key.GetPublicKey()), raw, sig) {
			return true
		}
	}
	return false
}

func umanifest(v any) map[string]any {
	o := uobject(v, "schemaVersion kind name version description author license permissions runtimes artifacts signature", "plugin skill tool")
	urequire(un(o["schemaVersion"]) == 1 && uName.MatchString(us(o["name"])) && len(us(o["name"])) <= 64 && uVersion.MatchString(us(o["version"])), "manifest_schema")
	author := uobject(o["author"], "displayName", "")
	urequire(len(us(author["displayName"])) > 0, "manifest_schema")
	us(o["description"])
	us(o["license"])
	permission := uobject(o["permissions"], "tools outboundHosts dataAccess", "")
	for _, key := range []string{"tools", "outboundHosts", "dataAccess"} {
		urequire(len(ua(permission[key])) <= 32, "manifest_schema")
		seen := map[string]bool{}
		for _, value := range ua(permission[key]) {
			s := us(value)
			urequire(!seen[s], "manifest_schema")
			seen[s] = true
		}
	}
	urequire(len(ua(o["runtimes"])) <= 8 && len(ua(o["artifacts"])) <= 32, "manifest_schema")
	for _, value := range ua(o["runtimes"]) {
		runtime := uobject(value, "kind versionRange", "")
		urequire(runtime["kind"] == "node" || runtime["kind"] == "python" || runtime["kind"] == "java", "manifest_schema")
		us(runtime["versionRange"])
	}
	signature := uobject(o["signature"], "mode algorithm", "")
	urequire(signature["mode"] == "detached" && signature["algorithm"] == "Ed25519", "manifest_schema")
	seen := map[string]bool{}
	for _, value := range ua(o["artifacts"]) {
		a := uobject(value, "id role digest sizeBytes mediaType os arch", "")
		name := us(a["id"])
		urequire(uName.MatchString(name) && !seen[name] && uDigest.MatchString(us(a["digest"])) && un(a["sizeBytes"]) <= 512*1024*1024, "manifest_schema")
		seen[name] = true
		urequire(a["role"] == "payload" || a["role"] == "source_snapshot" || a["role"] == "lockfile", "manifest_schema")
		us(a["os"])
		us(a["arch"])
		us(a["mediaType"])
	}
	switch o["kind"] {
	case "skill":
		s := uobject(o["skill"], "entry contentProfile dshVersionRange", "")
		urequire(s["entry"] == "SKILL.md" && s["contentProfile"] == "text-only" && len(ua(o["artifacts"])) > 0, "manifest_schema")
		us(s["dshVersionRange"])
		_, a := o["plugin"]
		_, b := o["tool"]
		urequire(!a && !b, "manifest_schema")
	case "plugin":
		s := uobject(o["plugin"], "packageName dshVersionRange", "")
		us(s["packageName"])
		us(s["dshVersionRange"])
	case "tool":
		urequire(um(o["tool"])["delivery"] == "online" || um(o["tool"])["delivery"] == "offline" || um(o["tool"])["delivery"] == "git", "manifest_schema")
	default:
		urequire(false, "manifest_schema")
	}
	return o
}

func (p UnifiedProtocol) release(input map[string]any) {
	e := um(input["envelope"])
	protocol := un(input["protocol"])
	uenvelope(e, protocol)
	urequire(p.signed(e, "release", ut(input["now"])), "signature")
	s := um(e["statement"])
	urequire(uwindow(s["issuedAt"], e["notAfter"], input["now"], 24*time.Hour), "release_expired")
	expected := um(input["expected"])
	for _, key := range []string{"tenantId", "packageId", "versionId"} {
		urequire(expected[key] == e[key], "identity")
	}
	urequire(protocol == 1 && expected["kind"] == "plugin" || protocol == 2 && expected["kind"] == s["kind"], "identity")
	if protocol == 1 {
		raw, err := base64.StdEncoding.DecodeString(us(input["rawArchiveBase64"]))
		urequire(err == nil && udigest(raw) == s["digest"], "manifest_binding")
		return
	}
	raw := []byte(us(input["rawManifest"]))
	urequire(len(raw) <= 256*1024, "manifest_schema")
	decoded, err := DecodeUnifiedJSON(raw)
	urequire(err == nil, "manifest_schema")
	manifest := umanifest(decoded)
	urequire(udigest(raw) == s["manifestDigest"] && manifest["kind"] == s["kind"] && manifest["name"] == s["name"] && manifest["version"] == s["version"], "manifest_binding")
	digests := map[string]bool{}
	for _, value := range ua(manifest["artifacts"]) {
		digests[us(um(value)["digest"])] = true
	}
	ordered := make([]string, 0, len(digests))
	for value := range digests {
		ordered = append(ordered, value)
	}
	sort.Strings(ordered)
	values := []any{}
	for _, value := range ordered {
		values = append(values, value)
	}
	urequire(uequal(values, s["artifactDigests"]), "manifest_binding")
	for _, value := range ua(manifest["artifacts"]) {
		artifact := um(value)
		data, ok := um(input["artifactBytes"])[us(artifact["id"])]
		urequire(ok, "artifact_binding")
		raw, err := base64.StdEncoding.DecodeString(us(data))
		urequire(err == nil && int64(len(raw)) == un(artifact["sizeBytes"]) && udigest(raw) == artifact["digest"], "artifact_binding")
	}
}

func (p UnifiedProtocol) migration(input map[string]any) {
	proof := uobject(input["proof"], "schemaVersion legacyEnvelope unifiedEnvelope legacyArtifactId activatedAt marker", "")
	urequire(un(proof["schemaVersion"]) == 2, "schema")
	a, e, marker := um(proof["legacyEnvelope"]), um(proof["unifiedEnvelope"]), um(proof["marker"])
	uenvelope(a, 1)
	uenvelope(e, 2)
	umarker(marker)
	urequire(p.signed(marker, "release", ut(input["now"])), "migration_signature")
	for _, key := range []string{"tenantId", "packageId", "versionId", "releaseSequence"} {
		urequire(uequal(a[key], e[key]), "migration_binding")
	}
	urequire(marker["tenantId"] == e["tenantId"] && marker["packageId"] == e["packageId"] && proof["activatedAt"] == marker["activatedAt"] && !ut(marker["activatedAt"]).After(ut(input["now"])), "migration_binding")
	urequire(un(marker["firstReleaseSequence"]) <= un(e["releaseSequence"]) && (un(marker["firstReleaseSequence"]) != un(e["releaseSequence"]) || marker["firstVersionId"] == e["versionId"]), "migration_binding")
	expected := map[string]any{"tenantId": e["tenantId"], "packageId": e["packageId"], "versionId": e["versionId"], "kind": "plugin"}
	p.release(map[string]any{"protocol": json.Number("1"), "envelope": a, "rawArchiveBase64": input["rawArchiveBase64"], "expected": expected, "now": input["now"]})
	p.release(map[string]any{"protocol": json.Number("2"), "envelope": e, "rawManifest": input["rawManifest"], "artifactBytes": input["artifactBytes"], "expected": expected, "now": input["now"]})
	manifest, err := DecodeUnifiedJSON([]byte(us(input["rawManifest"])))
	urequire(err == nil, "manifest_schema")
	s := um(e["statement"])
	urequire(s["kind"] == "plugin" && um(manifest["plugin"])["packageName"] == um(a["statement"])["name"] && um(a["statement"])["version"] == s["version"] && input["previousManifestDigest"] == s["manifestDigest"], "migration_binding")
	found := false
	for _, value := range ua(manifest["artifacts"]) {
		artifact := um(value)
		found = found || artifact["id"] == proof["legacyArtifactId"] && artifact["role"] == "payload" && artifact["digest"] == um(a["statement"])["digest"]
	}
	urequire(found, "migration_binding")
}

// Evaluate executes the same production rules for downloaded data and persisted
// migration/high-watermark/clock records. Errors are stable protocol reasons.
func (p UnifiedProtocol) Evaluate(operation string, input map[string]any) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			if e, ok := failure.(unifiedFailure); ok {
				err = e
			} else {
				err = fmt.Errorf("malformed unified input")
			}
		}
	}()
	p.evaluate(operation, input)
	return nil
}
func (p UnifiedProtocol) evaluate(operation string, input map[string]any) {
	switch operation {
	case "revocation_list":
		list := uobject(input["list"], "schemaVersion tenantId revision issuedAt expiresAt revokedKids revokedArtifacts signature", "")
		urequire(un(list["schemaVersion"]) == 1 && un(list["revision"]) > 0 && len(ua(list["revokedKids"])) <= 2048 && len(ua(list["revokedArtifacts"])) <= 8192, "schema")
		usignature(list["signature"])
		urequire(p.signed(list, "revocation", ut(input["now"])), "revocation_signature")
		urequire(list["tenantId"] == input["tenantId"], "revocation_tenant")
		urequire(uwindow(list["issuedAt"], list["expiresAt"], input["now"], 24*time.Hour), "revocation_expired")
	case "release":
		p.release(input)
	case "migration":
		p.migration(input)
	case "revocation":
		release := um(input["release"])
		p.release(release)
		e := um(release["envelope"])
		list := uobject(input["list"], "schemaVersion tenantId revision issuedAt expiresAt revokedKids revokedArtifacts signature", "")
		urequire(un(list["schemaVersion"]) == 1 && un(list["revision"]) > 0, "schema")
		ut(list["issuedAt"])
		ut(list["expiresAt"])
		usignature(list["signature"])
		urequire(p.signed(list, "revocation", ut(release["now"])), "revocation_signature")
		urequire(list["tenantId"] == e["tenantId"], "revocation_tenant")
		urequire(uwindow(list["issuedAt"], list["expiresAt"], release["now"], 24*time.Hour), "revocation_expired")
		urequire(!ucontains(list["revokedKids"], um(e["signature"])["kid"]), "revoked")
		s := um(e["statement"])
		for _, value := range ua(list["revokedArtifacts"]) {
			a := uobject(value, "packageId versionId digest", "")
			same := a["packageId"] == e["packageId"] && a["versionId"] == e["versionId"]
			if un(e["schemaVersion"]) == 1 {
				urequire(!same || a["digest"] != s["digest"], "revoked")
			} else {
				urequire(!same && a["digest"] != s["manifestDigest"] && !ucontains(s["artifactDigests"], a["digest"]), "revoked")
			}
		}
	case "watermark":
		release := um(input["release"])
		p.release(release)
		w, e := um(input["watermark"]), um(release["envelope"])
		urequire(w["tenantId"] == e["tenantId"] && w["packageId"] == e["packageId"], "watermark_tenant")
		urequire(un(e["releaseSequence"]) >= un(w["releaseSequence"]), "rollback")
		if un(e["releaseSequence"]) > un(w["releaseSequence"]) {
			return
		}
		urequire(w["versionId"] == e["versionId"], "watermark_alias")
		s := um(e["statement"])
		digest := s["manifestDigest"]
		if un(e["schemaVersion"]) == 1 {
			digest = s["digest"]
		}
		if ucontains(w["verifiedDigestAliases"], digest) {
			return
		}
		urequire(input["migration"] != nil, "watermark_alias")
		p.migration(um(input["migration"]))
		proof := um(um(input["migration"])["proof"])
		urequire(uequal(e, proof["unifiedEnvelope"]) && ucontains(w["verifiedDigestAliases"], um(um(proof["legacyEnvelope"])["statement"])["digest"]), "watermark_alias")
	case "renewal":
		marker, e := um(input["marker"]), um(input["envelope"])
		umarker(marker)
		urequire(p.signed(marker, "release", ut(input["now"])), "migration_signature")
		urequire(marker["tenantId"] == e["tenantId"] && marker["packageId"] == e["packageId"] && marker["activatedAt"] == input["persistedActivatedAt"], "migration_binding")
		cutoff := ut(marker["activatedAt"]).Add(24 * time.Hour)
		urequire(ut(input["now"]).Before(cutoff) && !ut(e["notAfter"]).After(cutoff), "renewal_cutoff")
		p.release(input)
	case "downgrade":
		a := uobject(input["authorization"], "schemaVersion purpose authorizationId instanceId identity fromReleaseSequence toReleaseSequence issuedAt expiresAt reason signature", "")
		urequire(un(a["schemaVersion"]) == 2 && a["purpose"] == "downgrade", "schema")
		uidentity(a["identity"])
		usignature(a["signature"])
		urequire(p.signed(a, "downgrade", ut(input["now"])), "downgrade_signature")
		urequire(uequal(a["identity"], input["identity"]) && a["instanceId"] == input["instanceId"] && uequal(a["fromReleaseSequence"], input["fromReleaseSequence"]) && uequal(a["toReleaseSequence"], input["toReleaseSequence"]) && un(a["toReleaseSequence"]) < un(a["fromReleaseSequence"]), "downgrade_scope")
		urequire(uwindow(a["issuedAt"], a["expiresAt"], input["now"], 10*time.Minute), "downgrade_expired")
		urequire(!ucontains(input["usedAuthorizationIds"], a["authorizationId"]), "downgrade_replay")
	case "clock":
		c := um(input["clock"])
		urequire(input["bootId"] == c["bootId"], "clock_reverify")
		mono, last := un(input["monotonicNowMillis"]), un(c["lastMonotonicMillis"])
		urequire(mono >= last && !ut(input["wallNow"]).Before(ut(c["maxObservedTime"]).Add(-120*time.Second)), "clock_rollback")
		effective := ut(input["wallNow"])
		advance := ut(c["maxObservedTime"]).Add(time.Duration(mono-last) * time.Millisecond)
		if advance.After(effective) {
			effective = advance
		}
		if ut(c["lastTrustedTime"]).After(effective) {
			effective = ut(c["lastTrustedTime"])
		}
		urequire(effective.Before(ut(input["leaseExpiresAt"])) && mono < un(c["leaseDeadlineMonotonicMillis"]), "lease_expired")
	default:
		urequire(false, "unknown_operation")
	}
}
