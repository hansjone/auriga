// Package runtimebundle issues and reads public Docker-only runtime materials.
// Local trust and image policy are independent inputs; a directory grants neither.
package runtimebundle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	MaxRuntimeBytes = 64 << 10
	MaxRootBytes    = 4 << 10
	maxPrivateBytes = 16 << 10
)

// ErrInvalid marks rejected input, trust, policy, keys or bundle structure.
var ErrInvalid = errors.New("invalid runtime bundle")

// Policy is trusted caller configuration, never inferred from bundle contents.
// AllowedImage must be the complete independently approved repo@sha256 locator.
type Policy struct {
	OS, Arch, AllowedImage string
}

// SignOptions requires explicit publisher inputs. It does not generate keys,
// timestamps or sequence numbers, or register roots with an installation.
type SignOptions struct {
	Image, ArtifactID, Version, OS, Arch string
	Sequence                             uint64
	IssuedAt                             *timestamppb.Timestamp
	RootPath, PrivateKeyPath             string
	ManifestPath, ConfigPath, OutputDir  string
}

// VerifyOptions requires a separately fixed public root and approved image.
type VerifyOptions struct {
	Dir, TrustedRootPath string
	Policy               Policy
}

// Material owns validated snapshots. Accessors return copies so assembly cannot
// accidentally change the bytes to which the successful verification applied.
type Material struct {
	runtime, root, manifest, config []byte
	dsh                             *nodev1.DshRuntime
	key                             *nodev1.ArtifactSigningKey
	bodyDigest, configID            string
	image                           releaseverify.VerifiedImage
}

func (m *Material) RuntimeBytes() []byte { return bytes.Clone(m.runtime) }
func (m *Material) RootBytes() []byte    { return bytes.Clone(m.root) }
func (m *Material) Manifest() []byte     { return bytes.Clone(m.manifest) }
func (m *Material) Config() []byte       { return bytes.Clone(m.config) }
func (m *Material) BodyDigest() string   { return m.bodyDigest }
func (m *Material) ConfigID() string     { return m.configID }

// Descriptors returns copies of the M, C and layer descriptors accepted by
// production VerifyOCI. Callers must still verify bytes read from a registry.
func (m *Material) Descriptors() (releaseverify.BlobDescriptor, releaseverify.BlobDescriptor, []releaseverify.BlobDescriptor) {
	return m.image.Descriptors()
}
func (m *Material) Runtime() *nodev1.DshRuntime {
	return proto.Clone(m.dsh).(*nodev1.DshRuntime)
}
func (m *Material) Root() *nodev1.ArtifactSigningKey {
	return proto.Clone(m.key).(*nodev1.ArtifactSigningKey)
}

// Sign reads all inputs and performs production trust/release/OCI verification
// before creating the new output directory. Existing paths are never replaced.
func Sign(o SignOptions) (*Material, error) {
	m, err := sign(o)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return m, nil
}

func sign(o SignOptions) (*Material, error) {
	keyRaw, key, ring, _, err := readRoot(o.RootPath)
	if err != nil {
		return nil, err
	}
	priv, err := readPrivate(o.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	defer clear(priv)
	if !bytes.Equal(priv.Public().(ed25519.PublicKey), key.PublicKey) {
		return nil, fmt.Errorf("publisher private key does not match fixed root")
	}
	manifest, err := readInput(o.ManifestPath, releaseverify.MaxManifestBytes, false)
	if err != nil {
		return nil, err
	}
	config, err := readInput(o.ConfigPath, releaseverify.MaxConfigBytes, false)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(manifest)
	e := &nodev1.RuntimeReleaseEnvelope{
		SchemaVersion: 1, Purpose: "dsh_runtime", Name: "dsh",
		ArtifactId: o.ArtifactID, DshVersion: o.Version, Os: o.OS, Arch: o.Arch,
		ImageDigest: "sha256:" + hex.EncodeToString(sum[:]), ReleaseSequence: o.Sequence,
		Signature: &nodev1.RuntimeReleaseSignature{Alg: "Ed25519", Kid: key.Kid},
	}
	if o.IssuedAt != nil {
		e.IssuedAt = proto.Clone(o.IssuedAt).(*timestamppb.Timestamp)
	}
	body, err := releaseverify.CanonicalReleaseBody(e)
	if err != nil {
		return nil, err
	}
	e.Signature.Value = base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, body))
	dsh := &nodev1.DshRuntime{ArtifactId: o.ArtifactID, Version: o.Version, Image: o.Image, ReleaseEnvelope: e}
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(dsh)
	if err != nil || len(raw) > MaxRuntimeBytes {
		return nil, fmt.Errorf("runtime protobuf exceeds size limit or cannot marshal")
	}
	m, err := validate(raw, keyRaw, key, ring, manifest, config, Policy{o.OS, o.Arch, o.Image})
	if err != nil {
		return nil, err
	}
	if err := writeNew(o.OutputDir, m); err != nil {
		return nil, err
	}
	return m, nil
}

// Verify reads a strict directory and uses production Trust, Verify and VerifyOCI.
// It does not update identity, keyrings, high-water marks, intent or deployments.
func Verify(o VerifyOptions) (*Material, error) {
	m, err := verify(o)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return m, nil
}

func verify(o VerifyOptions) (*Material, error) {
	dir, err := absolutePath(o.Dir)
	if err != nil {
		return nil, err
	}
	rootPath, err := absolutePath(o.TrustedRootPath)
	if err != nil {
		return nil, err
	}
	if rel, err := filepath.Rel(dir, rootPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("trusted root must be fixed outside the supplied bundle directory")
	}
	_, key, ring, externalRoot, err := readRoot(o.TrustedRootPath)
	if err != nil {
		return nil, err
	}
	r, err := openDirectory(o.Dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	if err := exactNames(r, ".", "runtime.pb", "runtime-keys", "runtime-proofs"); err != nil {
		return nil, err
	}
	if err := exactNames(r, "runtime-keys", "root.pb"); err != nil {
		return nil, err
	}
	raw, err := readMember(r, "runtime.pb", MaxRuntimeBytes)
	if err != nil {
		return nil, err
	}
	rootFile, err := openMember(r, "runtime-keys/root.pb", false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rootFile.Close() }()
	bundledRoot, err := rootFile.Stat()
	if err != nil {
		return nil, err
	}
	// Compare identities from the handles supplying the actual root bytes;
	// 8.3/long-name aliases cannot turn the bundled root into an external root.
	if os.SameFile(externalRoot, bundledRoot) {
		return nil, fmt.Errorf("trusted root must be fixed outside the supplied bundle directory")
	}
	rootRaw, err := readBounded(rootFile, MaxRootBytes)
	if err != nil {
		return nil, err
	}
	inside := new(nodev1.ArtifactSigningKey)
	if err := strictProto(rootRaw, inside); err != nil || !proto.Equal(inside, key) {
		return nil, fmt.Errorf("bundle root differs from independently fixed root")
	}
	dsh := new(nodev1.DshRuntime)
	if err := strictProto(raw, dsh); err != nil {
		return nil, err
	}
	// Verify before using the signed digest as a directory component.
	v, err := verifyDeclaration(dsh, ring, o.Policy)
	if err != nil {
		return nil, err
	}
	name := strings.TrimPrefix(v.ImageDigest(), "sha256:")
	if err := exactNames(r, "runtime-proofs", name); err != nil {
		return nil, err
	}
	proof := "runtime-proofs/" + name
	if err := exactNames(r, proof, "manifest.json", "config.json"); err != nil {
		return nil, err
	}
	manifest, err := readMember(r, proof+"/manifest.json", releaseverify.MaxManifestBytes)
	if err != nil {
		return nil, err
	}
	config, err := readMember(r, proof+"/config.json", releaseverify.MaxConfigBytes)
	if err != nil {
		return nil, err
	}
	return validate(raw, rootRaw, key, ring, manifest, config, o.Policy)
}

func validate(raw, rootRaw []byte, key *nodev1.ArtifactSigningKey, ring releasetrust.Ring, manifest, config []byte, p Policy) (*Material, error) {
	dsh := new(nodev1.DshRuntime)
	if err := strictProto(raw, dsh); err != nil {
		return nil, err
	}
	v, err := verifyDeclaration(dsh, ring, p)
	if err != nil {
		return nil, err
	}
	image, err := releaseverify.VerifyOCI(v, ring, manifest, config)
	if err != nil {
		return nil, err
	}
	return &Material{runtime: bytes.Clone(raw), root: bytes.Clone(rootRaw), manifest: bytes.Clone(manifest), config: bytes.Clone(config), dsh: dsh, key: proto.Clone(key).(*nodev1.ArtifactSigningKey), bodyDigest: v.BodyDigest(), configID: image.ConfigID(), image: image}, nil
}

func verifyDeclaration(dsh *nodev1.DshRuntime, ring releasetrust.Ring, p Policy) (releaseverify.VerifiedRelease, error) {
	if dsh.Digest != "" || dsh.Url != "" || len(dsh.Signature) != 0 || dsh.SigningKid != "" || dsh.Signed != nil || dsh.GetReleaseEnvelope().GetArchiveDigest() != "" {
		return releaseverify.VerifiedRelease{}, fmt.Errorf("only Docker-only runtime declarations are supported")
	}
	return releaseverify.Verify(dsh, ring, releaseverify.Target{OS: p.OS, Arch: p.Arch, Driver: "docker", AllowImage: func(image string) error {
		if p.AllowedImage == "" || image != p.AllowedImage {
			return fmt.Errorf("image differs from independently approved locator")
		}
		return nil
	}})
}

func readRoot(path string) ([]byte, *nodev1.ArtifactSigningKey, releasetrust.Ring, os.FileInfo, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return nil, nil, releasetrust.Ring{}, nil, err
	}
	f, err := openOrdinary(absolute, false)
	if err != nil {
		return nil, nil, releasetrust.Ring{}, nil, err
	}
	defer func() { _ = f.Close() }()
	raw, err := readBounded(f, MaxRootBytes)
	if err != nil {
		return nil, nil, releasetrust.Ring{}, nil, err
	}
	identity, err := f.Stat()
	if err != nil {
		return nil, nil, releasetrust.Ring{}, nil, err
	}
	key := new(nodev1.ArtifactSigningKey)
	if err := strictProto(raw, key); err != nil {
		return nil, nil, releasetrust.Ring{}, nil, err
	}
	if key.EndorsedByKid != "" || len(key.Endorsement) != 0 {
		return nil, nil, releasetrust.Ring{}, nil, fmt.Errorf("explicit publisher key must be a fixed bootstrap root")
	}
	ring, err := releasetrust.Merge(releasetrust.Ring{}, []*nodev1.ArtifactSigningKey{key}, true, time.Now())
	return raw, key, ring, identity, err
}

func strictProto(raw []byte, m proto.Message) error {
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, m); err != nil {
		return fmt.Errorf("malformed protobuf: %w", err)
	}
	// Deterministic encoding rejects duplicate fields and nonminimal wire aliases;
	// production Verify/Trust still checks unknown fields recursively.
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil || !bytes.Equal(raw, canonical) {
		return fmt.Errorf("noncanonical protobuf encoding")
	}
	return nil
}

func readPrivate(path string) (ed25519.PrivateKey, error) {
	raw, err := readInput(path, maxPrivateBytes, true)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	// pem.Decode otherwise skips arbitrary text before the block.
	if !bytes.HasPrefix(raw, []byte("-----BEGIN PRIVATE KEY-----\n")) && !bytes.HasPrefix(raw, []byte("-----BEGIN PRIVATE KEY-----\r\n")) {
		return nil, fmt.Errorf("private key must be one PKCS8 PRIVATE KEY PEM block")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("private key must be one PKCS8 PRIVATE KEY PEM block")
	}
	defer clear(block.Bytes)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("invalid PKCS8 private key")
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok || len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("only Ed25519 PKCS8 private keys are supported")
	}
	return priv, nil
}
