package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxProofBytes = 4 << 20

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var fixtureImagePattern = regexp.MustCompile(`^127\.0\.0\.1:5000/auriga/(fake|real)-dsh@sha256:[a-f0-9]{64}$`)

// Fixed ASCII field order and safe integers need no general JSON canonicalizer.
// Strings retain their original Unicode scalars and use ECMAScript JSON escapes.
func runtimeReleaseBody(e *nodev1.RuntimeReleaseEnvelope) ([]byte, error) {
	if e == nil || len(e.ProtoReflect().GetUnknown()) != 0 || e.SchemaVersion != 1 || e.Purpose != "dsh_runtime" || e.Name != "dsh" || e.ArtifactId == "" || e.DshVersion == "" || (e.Os != "linux" && e.Os != "windows") || (e.Arch != "amd64" && e.Arch != "arm64") || e.ReleaseSequence < 1 || e.ReleaseSequence > 9007199254740991 || (e.ArchiveDigest == "" && e.ImageDigest == "") || (e.ArchiveDigest != "" && !digestPattern.MatchString(e.ArchiveDigest)) || (e.ImageDigest != "" && !digestPattern.MatchString(e.ImageDigest)) || e.IssuedAt == nil || len(e.IssuedAt.ProtoReflect().GetUnknown()) != 0 || e.IssuedAt.CheckValid() != nil {
		return nil, fmt.Errorf("invalid runtime release envelope")
	}
	for _, s := range []string{e.ArtifactId, e.DshVersion} {
		if !utf8.ValidString(s) {
			return nil, fmt.Errorf("invalid runtime release Unicode")
		}
	}
	fields := []struct{ key, value string }{
		{"arch", e.Arch}, {"archiveDigest", e.ArchiveDigest}, {"artifactId", e.ArtifactId},
		{"dshVersion", e.DshVersion}, {"imageDigest", e.ImageDigest},
		{"issuedAt", e.IssuedAt.AsTime().UTC().Format(time.RFC3339Nano)},
		{"name", e.Name}, {"os", e.Os}, {"purpose", e.Purpose},
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, field := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jcsString(field.key))
		b.WriteByte(':')
		b.WriteString(jcsString(field.value))
	}
	fmt.Fprintf(&b, ",\"releaseSequence\":%d,\"schemaVersion\":1}", e.ReleaseSequence)
	return []byte(b.String()), nil
}

func jcsString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func signRuntimeRelease(dsh *nodev1.DshRuntime, priv ed25519.PrivateKey, now time.Time) error {
	e := &nodev1.RuntimeReleaseEnvelope{SchemaVersion: 1, Purpose: "dsh_runtime", Name: "dsh", ArtifactId: dsh.ArtifactId, DshVersion: dsh.Version, Os: "linux", Arch: "amd64", ImageDigest: digestOf(dsh.Image), ReleaseSequence: 1, IssuedAt: timestamppb.New(now.UTC())}
	body, err := runtimeReleaseBody(e)
	if err != nil {
		return err
	}
	if len(priv) != ed25519.PrivateKeySize {
		return fmt.Errorf("runtime signer unavailable")
	}
	e.Signature = &nodev1.RuntimeReleaseSignature{Alg: "Ed25519", Kid: "e2e-runtime-root", Value: base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, body))}
	dsh.ReleaseEnvelope = e
	return nil
}

func validateRuntimeTemplate(out string) error {
	entries, err := os.ReadDir(filepath.Join(out, "templates"))
	if err != nil || len(entries) != 1 {
		return fmt.Errorf("invalid runtime template")
	}
	raw, err := readProofFile(filepath.Join(out, "templates", entries[0].Name()))
	if err != nil {
		return err
	}
	var spec nodev1.InstanceSpec
	if proto.Unmarshal(raw, &spec) != nil || len(spec.ProtoReflect().GetUnknown()) != 0 || spec.Runtime != nodev1.RuntimeKind_RUNTIME_KIND_DOCKER {
		return fmt.Errorf("invalid runtime template")
	}
	d := spec.Dsh
	if d == nil || len(d.ProtoReflect().GetUnknown()) != 0 || !fixtureImagePattern.MatchString(d.Image) || d.Digest != "" || d.Url != "" || d.Signed != nil || d.ReleaseEnvelope == nil {
		return fmt.Errorf("invalid Docker-only runtime template")
	}
	e := d.ReleaseEnvelope
	body, err := runtimeReleaseBody(e)
	if err != nil {
		return err
	}
	s := e.Signature
	if e.ArtifactId != d.ArtifactId || e.DshVersion != d.Version || e.ArchiveDigest != "" || e.ImageDigest != digestOf(d.Image) || e.Os != "linux" || e.Arch != "amd64" || s == nil || len(s.ProtoReflect().GetUnknown()) != 0 || s.Alg != "Ed25519" || s.Kid != "e2e-runtime-root" {
		return fmt.Errorf("runtime release mapping mismatch")
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(s.Value)
	if err != nil || len(signature) != 64 || base64.RawURLEncoding.EncodeToString(signature) != s.Value {
		return fmt.Errorf("invalid runtime signature encoding")
	}
	root, err := os.ReadFile(filepath.Join(out, "runtime-keys", "root.pb"))
	if err != nil {
		return err
	}
	var key nodev1.ArtifactSigningKey
	if proto.Unmarshal(root, &key) != nil || len(key.PublicKey) != 32 || !ed25519.Verify(key.PublicKey, body, signature) {
		return fmt.Errorf("invalid runtime release signature")
	}
	_, _, err = loadRuntimeProofs(filepath.Join(out, "runtime-proofs"), d.Image)
	return err
}

func readProofFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > maxProofBytes {
		return nil, fmt.Errorf("invalid runtime proof file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, statErr := f.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(f, maxProofBytes+1))
	after, afterErr := os.Lstat(path)
	if statErr != nil || readErr != nil || afterErr != nil || !os.SameFile(before, opened) || !os.SameFile(opened, after) || len(raw) != int(before.Size()) || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("runtime proof changed while reading")
	}
	return raw, nil
}

// Validate duplicates at all depths before unmarshalling typed fields. Remote
// descriptors never supply paths or URLs and JSON bytes remain unmodified.
func strictProofJSON(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("invalid proof Unicode")
	}
	// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Reject them
	// before decoding, so malformed strings cannot collapse into valid values.
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return fmt.Errorf("invalid proof Unicode escape")
		}
		cp, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return fmt.Errorf("invalid proof Unicode escape")
		}
		i += 4
		if cp >= 0xdc00 && cp <= 0xdfff {
			return fmt.Errorf("unpaired proof Unicode surrogate")
		}
		if cp >= 0xd800 && cp <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return fmt.Errorf("unpaired proof Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return fmt.Errorf("unpaired proof Unicode surrogate")
			}
			i += 6
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		if (token == json.Delim('{') || token == json.Delim('[')) && depth >= 32 {
			return fmt.Errorf("proof JSON nesting limit")
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate proof JSON key")
				}
				seen[name] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid proof JSON object")
			}
		case json.Delim('['):
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid proof JSON array")
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing proof JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target)
}

// Mirror the structural contexts protected by releaseverify's fixed Moby 27
// boundary. Never recurse into user-keyed maps or arbitrary metadata extensions.
func runtimeCanonicalMembers(object map[string]any, names ...string) error {
	for key := range object {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return fmt.Errorf("noncanonical structural JSON member %q (expected %q)", key, name)
			}
		}
	}
	return nil
}

func runtimeOnlyMembers(object map[string]any, names ...string) error {
	if err := runtimeCanonicalMembers(object, names...); err != nil {
		return err
	}
	for key := range object {
		known := false
		for _, name := range names {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unsupported structural JSON member %q", key)
		}
	}
	return nil
}

func runtimeStructuralMembers(manifest, config map[string]any) error {
	object := func(value any) map[string]any { m, _ := value.(map[string]any); return m }
	check := runtimeCanonicalMembers
	if err := check(manifest, "schemaVersion", "mediaType", "config", "layers", "annotations", "artifactType", "subject", "manifests"); err != nil {
		return err
	}
	descriptor := []string{"mediaType", "digest", "size", "annotations"}
	if err := runtimeOnlyMembers(object(manifest["config"]), descriptor...); err != nil {
		return err
	}
	layers, _ := manifest["layers"].([]any)
	for _, layer := range layers {
		if err := runtimeOnlyMembers(object(layer), descriptor...); err != nil {
			return err
		}
	}
	if err := check(config, "created", "author", "architecture", "os", "os.version", "os.features", "variant", "config", "rootfs", "history", "id", "parent", "comment", "container", "container_config", "docker_version", "Size", "artifactType", "subject", "manifests", "mediaType"); err != nil {
		return err
	}
	healthFields := []string{"Test", "Interval", "Timeout", "StartPeriod", "StartInterval", "Retries"}
	historical := object(config["container_config"])
	if err := check(historical, "Hostname", "Domainname", "User", "AttachStdin", "AttachStdout", "AttachStderr", "ExposedPorts", "Tty", "OpenStdin", "StdinOnce", "Env", "Cmd", "Healthcheck", "ArgsEscaped", "Image", "Volumes", "WorkingDir", "Entrypoint", "NetworkDisabled", "MacAddress", "OnBuild", "Labels", "StopSignal", "StopTimeout", "Shell"); err != nil {
		return err
	}
	if err := check(object(historical["Healthcheck"]), healthFields...); err != nil {
		return err
	}
	if err := runtimeOnlyMembers(object(config["rootfs"]), "type", "diff_ids"); err != nil {
		return err
	}
	execution := object(config["config"])
	if err := runtimeOnlyMembers(execution, "User", "WorkingDir", "StopSignal", "Env", "Entrypoint", "Cmd", "OnBuild", "Shell", "ArgsEscaped", "Labels", "ExposedPorts", "Volumes", "Healthcheck"); err != nil {
		return err
	}
	if err := runtimeOnlyMembers(object(execution["Healthcheck"]), healthFields...); err != nil {
		return err
	}
	history, _ := config["history"].([]any)
	for _, entry := range history {
		if err := check(object(entry), "created", "created_by", "author", "comment", "empty_layer"); err != nil {
			return err
		}
	}
	return nil
}

func proofDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func loadRuntimeProofs(root, image string) ([]byte, []byte, error) {
	if !fixtureImagePattern.MatchString(image) {
		return nil, nil, fmt.Errorf("image must use exact local fixture repository and digest")
	}
	leaf := strings.TrimPrefix(digestOf(image), "sha256:")
	for _, path := range []string{root, filepath.Join(root, leaf)} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("invalid runtime proof directory")
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, leaf))
	if err != nil || len(entries) != 2 || entries[0].Name() != "config.json" || entries[1].Name() != "manifest.json" {
		return nil, nil, fmt.Errorf("incomplete runtime proof directory")
	}
	m, err := readProofFile(filepath.Join(root, leaf, "manifest.json"))
	if err != nil {
		return nil, nil, err
	}
	c, err := readProofFile(filepath.Join(root, leaf, "config.json"))
	if err != nil {
		return nil, nil, err
	}
	if err := verifyRuntimeProof(image, m, c); err != nil {
		return nil, nil, err
	}
	return m, c, nil
}

func verifyRuntimeProof(image string, m, c []byte) error {
	if !fixtureImagePattern.MatchString(image) || len(m) < 1 || len(c) < 1 || len(m) > maxProofBytes || len(c) > maxProofBytes || proofDigest(m) != digestOf(image) {
		return fmt.Errorf("runtime manifest digest/size mismatch")
	}
	var rawManifest, rawConfig map[string]any
	if err := strictProofJSON(m, &rawManifest); err != nil {
		return err
	}
	if err := strictProofJSON(c, &rawConfig); err != nil {
		return err
	}
	if err := runtimeStructuralMembers(rawManifest, rawConfig); err != nil {
		return err
	}
	var manifest struct {
		SchemaVersion int    `json:"schemaVersion"`
		MediaType     string `json:"mediaType"`
		Config        struct {
			MediaType string          `json:"mediaType"`
			Digest    string          `json:"digest"`
			Size      int64           `json:"size"`
			URLs      json.RawMessage `json:"urls"`
			Data      json.RawMessage `json:"data"`
		} `json:"config"`
		Layers    []map[string]json.RawMessage `json:"layers"`
		Manifests json.RawMessage              `json:"manifests"`
		URLs      json.RawMessage              `json:"urls"`
		Data      json.RawMessage              `json:"data"`
	}
	var config struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	}
	if err := strictProofJSON(m, &manifest); err != nil {
		return err
	}
	if err := strictProofJSON(c, &config); err != nil {
		return err
	}
	configType := map[string]string{"application/vnd.oci.image.manifest.v1+json": "application/vnd.oci.image.config.v1+json", "application/vnd.docker.distribution.manifest.v2+json": "application/vnd.docker.container.image.v1+json"}[manifest.MediaType]
	if manifest.SchemaVersion != 2 || configType == "" || manifest.Config.MediaType != configType || manifest.Config.Size != int64(len(c)) || manifest.Config.Digest != proofDigest(c) || manifest.Layers == nil || manifest.Manifests != nil || manifest.URLs != nil || manifest.Data != nil || manifest.Config.URLs != nil || manifest.Config.Data != nil || config.OS != "linux" || config.Architecture != "amd64" {
		return fmt.Errorf("invalid single-platform OCI runtime proof")
	}
	for _, layer := range manifest.Layers {
		if layer == nil || layer["urls"] != nil || layer["data"] != nil {
			return fmt.Errorf("external/embedded runtime descriptor rejected")
		}
	}
	return nil
}

func installRuntimeProofs(out, image string, m, c []byte) error {
	root := filepath.Join(out, "runtime-proofs")
	if err := os.Mkdir(root, 0o755); err != nil {
		return err
	}
	leaf := filepath.Join(root, strings.TrimPrefix(digestOf(image), "sha256:"))
	if err := os.Mkdir(leaf, 0o755); err != nil {
		return err
	}
	for _, item := range []struct {
		name string
		raw  []byte
	}{{"manifest.json", m}, {"config.json", c}} {
		f, err := os.OpenFile(filepath.Join(leaf, item.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(item.raw)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// Existing installations must reach the receipt check without mkdir, key
// generation or proof repair. Unreceipted leftovers also fail without writes.
func preflightRuntimeInstallation(out string) (bool, error) {
	info, err := os.Lstat(out)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("invalid installation directory")
	}
	if _, err := os.Lstat(filepath.Join(out, "iam-installation", "bootstrap.receipt")); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	for _, name := range append(append([]string{}, identityMaterialFiles...), "runtime-proofs") {
		if _, err := os.Lstat(filepath.Join(out, filepath.FromSlash(name))); err == nil {
			return false, fmt.Errorf("existing installation lacks complete receipt; use a fresh directory")
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	for _, name := range []string{"pki", "templates", "runtime-keys", "iam-installation", "network", "network-platform", "browser-tls"} {
		path := filepath.Join(out, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("invalid unreceipted installation directory")
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			return false, fmt.Errorf("unreceipted materials require a fresh directory")
		}
	}
	return false, nil
}
