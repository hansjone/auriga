// Package releaseverify verifies formal runtime declarations and raw OCI evidence.
// A successful declaration is not an activation or a rollback-store decision.
package releaseverify

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

const maxSafeInteger = 9007199254740991

// ErrInvalid marks all rejected declarations/evidence; detailed text is diagnostic.
var ErrInvalid = errors.New("invalid runtime release evidence")

var (
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	kidPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	pathComponent = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	hostLabel     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
)

// Target is trusted local policy, never derived from a remote declaration.
// Driver is docker, linux-process or windows-process. Docker requires an explicit
// AllowImage policy callback, e.g. policy.Policy.AllowImage; it receives the full
// independently syntax-checked, digest-pinned locator.
type Target struct {
	OS, Arch, Driver string
	AllowImage       func(string) error
}

// Partition is exactly the high-water-mark partition defined by runtime v1.
type Partition struct{ Purpose, Name, OS, Arch string }

// VerifiedRelease has no externally writable state. Its zero value is invalid.
// It records a verification, not continuing key validity or activation authority.
type VerifiedRelease struct {
	valid                                                              bool
	partition                                                          Partition
	sequence                                                           uint64
	body                                                               []byte
	bodyDigest, archiveDigest, imageDigest, image, artifactID, version string
	kid                                                                string
	signature                                                          []byte
	legacyKid                                                          string
	legacyBody, legacySignature                                        []byte
}

func (v VerifiedRelease) Valid() bool           { return v.valid }
func (v VerifiedRelease) Partition() Partition  { return v.partition }
func (v VerifiedRelease) Sequence() uint64      { return v.sequence }
func (v VerifiedRelease) BodyDigest() string    { return v.bodyDigest }
func (v VerifiedRelease) CanonicalBody() []byte { return bytes.Clone(v.body) }
func (v VerifiedRelease) ArchiveDigest() string { return v.archiveDigest }
func (v VerifiedRelease) ImageDigest() string   { return v.imageDigest }
func (v VerifiedRelease) Image() string         { return v.image }
func (v VerifiedRelease) ArtifactID() string    { return v.artifactID }
func (v VerifiedRelease) DSHVersion() string    { return v.version }

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Recheck re-verifies both signatures using the real clock at each key use.
// Every cached/start/restart/recovery use must recheck inside its activation guard.
func (v VerifiedRelease) Recheck(ring releasetrust.Ring) error {
	if !v.valid {
		return fmt.Errorf("%w: zero verified release", ErrInvalid)
	}
	if err := verifySignature(ring, v.kid, v.body, v.signature); err != nil {
		return err
	}
	if v.legacyKid != "" {
		return verifySignature(ring, v.legacyKid, v.legacyBody, v.legacySignature)
	}
	return nil
}

func verifySignature(ring releasetrust.Ring, kid string, body, signature []byte) error {
	if !kidPattern.MatchString(kid) || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature shape", ErrInvalid)
	}
	key, err := ring.Lookup(kid, time.Now())
	if err != nil {
		return fmt.Errorf("%w: signing key: %w", ErrInvalid, err)
	}
	if !ed25519.Verify(key, body, signature) {
		return fmt.Errorf("%w: bad signature", ErrInvalid)
	}
	return nil
}

// Verify validates protobuf shape, complete JCS signatures, local target/policy
// and DshRuntime mappings. It never accepts legacy signatures as authorization.
// JSON ingress must reject duplicate/unknown/missing keys before protobuf mapping.
func Verify(dsh *nodev1.DshRuntime, ring releasetrust.Ring, target Target) (VerifiedRelease, error) {
	v, err := verify(dsh, ring, target)
	if err != nil {
		return VerifiedRelease{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return v, nil
}

func verify(dsh *nodev1.DshRuntime, ring releasetrust.Ring, target Target) (VerifiedRelease, error) {
	if dsh == nil || !known(dsh.ProtoReflect()) || dsh.ReleaseEnvelope == nil {
		return VerifiedRelease{}, fmt.Errorf("missing release envelope or unknown protobuf fields")
	}
	e := dsh.ReleaseEnvelope
	body, err := releaseBody(e)
	if err != nil {
		return VerifiedRelease{}, err
	}
	if (target.OS != "linux" && target.OS != "windows") || (target.Arch != "amd64" && target.Arch != "arm64") || e.Os != target.OS || e.Arch != target.Arch {
		return VerifiedRelease{}, fmt.Errorf("target platform mismatch")
	}
	if e.ArtifactId != dsh.ArtifactId || e.DshVersion != dsh.Version || e.ArchiveDigest != dsh.Digest {
		return VerifiedRelease{}, fmt.Errorf("runtime declaration mapping mismatch")
	}
	archive := e.ArchiveDigest != ""
	if archive != (dsh.Url != "") {
		return VerifiedRelease{}, fmt.Errorf("archive locator/digest incomplete")
	}
	if !utf8.ValidString(dsh.Url) || !utf8.ValidString(dsh.Image) {
		return VerifiedRelease{}, fmt.Errorf("invalid locator UTF-8")
	}
	switch target.Driver {
	case "docker":
		if e.ImageDigest == "" || !pinnedImage(dsh.Image, e.ImageDigest) || target.AllowImage == nil {
			return VerifiedRelease{}, fmt.Errorf("image locator is not explicitly allowed and digest pinned")
		}
		if err := target.AllowImage(dsh.Image); err != nil {
			return VerifiedRelease{}, fmt.Errorf("image policy: %w", err)
		}
	case "linux-process", "windows-process":
		if target.Driver != target.OS+"-process" || dsh.Image != "" || e.ImageDigest != "" || !archive {
			return VerifiedRelease{}, fmt.Errorf("invalid native runtime mapping")
		}
	default:
		return VerifiedRelease{}, fmt.Errorf("unsupported local driver")
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(e.Signature.Value)
	if err != nil || base64.RawURLEncoding.EncodeToString(sig) != e.Signature.Value {
		return VerifiedRelease{}, fmt.Errorf("noncanonical release signature")
	}
	if err := verifySignature(ring, e.Signature.Kid, body, sig); err != nil {
		return VerifiedRelease{}, err
	}
	v := VerifiedRelease{valid: true, partition: Partition{e.Purpose, e.Name, e.Os, e.Arch}, sequence: e.ReleaseSequence, body: body, bodyDigest: digest(body), archiveDigest: e.ArchiveDigest, imageDigest: e.ImageDigest, image: dsh.Image, artifactID: e.ArtifactId, version: e.DshVersion, kid: e.Signature.Kid, signature: bytes.Clone(sig)}
	if dsh.Signed != nil {
		s := dsh.Signed.Statement
		if !archive || s == nil || s.Purpose != e.Purpose || s.Name != e.Name || s.Version != e.DshVersion || s.Os != e.Os || s.Arch != e.Arch || s.Digest != e.ArchiveDigest || s.IssuedAt == nil || s.IssuedAt.Seconds != e.IssuedAt.Seconds || s.IssuedAt.Nanos != e.IssuedAt.Nanos {
			return VerifiedRelease{}, fmt.Errorf("legacy archive mapping mismatch")
		}
		legacyBody, err := standaloneBody(s)
		if err != nil {
			return VerifiedRelease{}, err
		}
		if err := verifySignature(ring, dsh.Signed.SigningKid, legacyBody, dsh.Signed.Signature); err != nil {
			return VerifiedRelease{}, err
		}
		v.legacyKid, v.legacyBody, v.legacySignature = dsh.Signed.SigningKid, legacyBody, bytes.Clone(dsh.Signed.Signature)
	}
	return v, nil
}

func releaseBody(e *nodev1.RuntimeReleaseEnvelope) ([]byte, error) {
	if e == nil || !known(e.ProtoReflect()) || e.SchemaVersion != 1 || e.Purpose != "dsh_runtime" || e.Name != "dsh" || e.ArtifactId == "" || e.DshVersion == "" || (e.Os != "linux" && e.Os != "windows") || (e.Arch != "amd64" && e.Arch != "arm64") || e.ReleaseSequence < 1 || e.ReleaseSequence > maxSafeInteger || e.Signature == nil || e.Signature.Alg != "Ed25519" {
		return nil, fmt.Errorf("invalid release v1 shape")
	}
	if (e.ArchiveDigest != "" && !digestPattern.MatchString(e.ArchiveDigest)) || (e.ImageDigest != "" && !digestPattern.MatchString(e.ImageDigest)) || (e.ArchiveDigest == "" && e.ImageDigest == "") {
		return nil, fmt.Errorf("invalid release digest")
	}
	ts, err := timestamp(e.IssuedAt)
	if err != nil {
		return nil, err
	}
	return canonical(map[string]string{"purpose": e.Purpose, "name": e.Name, "artifactId": e.ArtifactId, "dshVersion": e.DshVersion, "os": e.Os, "arch": e.Arch, "archiveDigest": e.ArchiveDigest, "imageDigest": e.ImageDigest, "issuedAt": ts}, map[string]uint64{"schemaVersion": uint64(e.SchemaVersion), "releaseSequence": e.ReleaseSequence})
}

func standaloneBody(s *nodev1.ArtifactStatement) ([]byte, error) {
	if s == nil || !known(s.ProtoReflect()) {
		return nil, fmt.Errorf("invalid standalone protobuf")
	}
	ts, err := timestamp(s.IssuedAt)
	if err != nil {
		return nil, err
	}
	return canonical(map[string]string{"purpose": s.Purpose, "name": s.Name, "version": s.Version, "os": s.Os, "arch": s.Arch, "digest": s.Digest, "issuedAt": ts}, nil)
}

func pinnedImage(image, sum string) bool {
	repo, suffix, ok := strings.Cut(image, "@")
	return ok && suffix == sum && repository(repo)
}

func repository(repo string) bool {
	if len(repo) > 255 {
		return false
	}
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return false
	}
	host := parts[0]
	explicitPort := strings.Contains(host, ":")
	if explicitPort {
		var port string
		host, port, _ = strings.Cut(host, ":")
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != port {
			return false
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil || ip.String() != host {
			return false
		}
	} else {
		if len(host) > 253 || (!strings.Contains(host, ".") && host != "localhost" && !explicitPort) {
			return false
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) > 63 || !hostLabel.MatchString(label) {
				return false
			}
		}
	}
	for _, part := range parts[1:] {
		if !pathComponent.MatchString(part) {
			return false
		}
	}
	return true
}
