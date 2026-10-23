package releaseverify

import (
	"fmt"
	"strings"

	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
)

const (
	MaxManifestBytes = 4 << 20
	MaxConfigBytes   = 4 << 20
	ociManifest      = "application/vnd.oci.image.manifest.v1+json"
	dockerManifest   = "application/vnd.docker.distribution.manifest.v2+json"
	ociConfig        = "application/vnd.oci.image.config.v1+json"
	dockerConfig     = "application/vnd.docker.container.image.v1+json"
	ociTar           = "application/vnd.oci.image.layer.v1.tar"
	dockerGzip       = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// VerifiedImage binds raw manifest/config evidence to a verified release.
// Its config ID is NOT a manifest digest or proof of local Engine contents.
type VerifiedImage struct {
	valid    bool
	release  VerifiedRelease
	configID string
	layers   int
}

func (v VerifiedImage) Valid() bool            { return v.valid }
func (v VerifiedImage) ConfigID() string       { return v.configID }
func (v VerifiedImage) ManifestDigest() string { return v.release.imageDigest }
func (v VerifiedImage) BodyDigest() string     { return v.release.bodyDigest }
func (v VerifiedImage) Partition() Partition   { return v.release.partition }
func (v VerifiedImage) LayerCount() int        { return v.layers }
func (v VerifiedImage) Recheck(ring releasetrust.Ring) error {
	if !v.valid {
		return fmt.Errorf("%w: zero verified image", ErrInvalid)
	}
	return v.release.Recheck(ring)
}

// VerifyOCI hashes the ORIGINAL M/C bytes, never reserialized JSON. It accepts
// only a single-platform image, not an index, artifact, config-as-manifest, or
// foreign/unknown layer format. It does not download or decompress layer bytes.
func VerifyOCI(release VerifiedRelease, ring releasetrust.Ring, manifest, config []byte) (VerifiedImage, error) {
	v, err := verifyOCI(release, ring, manifest, config)
	if err != nil {
		return VerifiedImage{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return v, nil
}

func verifyOCI(release VerifiedRelease, ring releasetrust.Ring, manifest, config []byte) (VerifiedImage, error) {
	if len(manifest) == 0 || len(manifest) > MaxManifestBytes || len(config) == 0 || len(config) > MaxConfigBytes {
		return VerifiedImage{}, fmt.Errorf("invalid OCI evidence size")
	}
	if err := release.Recheck(ring); err != nil {
		return VerifiedImage{}, err
	}
	if release.imageDigest == "" || digest(manifest) != release.imageDigest {
		return VerifiedImage{}, fmt.Errorf("manifest digest mismatch")
	}
	m, err := strictJSON(manifest, MaxManifestBytes)
	if err != nil {
		return VerifiedImage{}, err
	}
	if err := canonicalMembers(m, "schemaVersion", "mediaType", "config", "layers", "annotations", "artifactType", "subject", "manifests"); err != nil {
		return VerifiedImage{}, err
	}
	version, ok := integer(m, "schemaVersion")
	media := text(m, "mediaType")
	if !ok || version != 2 || (media != ociManifest && media != dockerManifest) || hasAny(m, "artifactType", "subject", "manifests") {
		return VerifiedImage{}, fmt.Errorf("unsupported image manifest")
	}
	if a, exists := m["annotations"]; exists && !stringMap(a) {
		return VerifiedImage{}, fmt.Errorf("invalid manifest annotations")
	}
	c, ok := object(m, "config")
	if !ok {
		return VerifiedImage{}, fmt.Errorf("config descriptor missing")
	}
	configMedia := ociConfig
	if media == dockerManifest {
		configMedia = dockerConfig
	}
	configDigest, _, err := descriptor(c, configMedia)
	if err != nil {
		return VerifiedImage{}, err
	}
	size, _ := integer(c, "size")
	if size != uint64(len(config)) || configDigest != digest(config) {
		return VerifiedImage{}, fmt.Errorf("config original bytes mismatch")
	}
	layers, ok := array(m, "layers")
	if !ok {
		return VerifiedImage{}, fmt.Errorf("layer descriptors missing")
	}
	layerDigests := make([]string, len(layers))
	uncompressed := make([]bool, len(layers))
	for i, raw := range layers {
		layer, ok := raw.(map[string]any)
		if !ok {
			return VerifiedImage{}, fmt.Errorf("invalid layer descriptor")
		}
		mt := text(layer, "mediaType")
		if (media == ociManifest && mt != ociTar && mt != ociTar+"+gzip" && mt != ociTar+"+zstd") || (media == dockerManifest && mt != dockerGzip) {
			return VerifiedImage{}, fmt.Errorf("unsupported layer media type or compression")
		}
		sum, _, err := descriptor(layer, mt)
		if err != nil {
			return VerifiedImage{}, err
		}
		layerDigests[i], uncompressed[i] = sum, mt == ociTar
	}
	cfg, err := strictJSON(config, MaxConfigBytes)
	if err != nil {
		return VerifiedImage{}, err
	}
	if err := imageConfig(cfg, release.partition, layerDigests, uncompressed); err != nil {
		return VerifiedImage{}, err
	}
	return VerifiedImage{valid: true, release: release, configID: configDigest, layers: len(layers)}, nil
}

func descriptor(d map[string]any, media string) (string, uint64, error) {
	size, ok := integer(d, "size")
	sum := text(d, "digest")
	if !ok || size == 0 || size > maxSafeInteger || !digestPattern.MatchString(sum) || text(d, "mediaType") != media || !only(d, "mediaType", "digest", "size", "annotations") {
		return "", 0, fmt.Errorf("invalid or unsupported descriptor")
	}
	if a, exists := d["annotations"]; exists && !stringMap(a) {
		return "", 0, fmt.Errorf("invalid descriptor annotations")
	}
	return sum, size, nil
}

func imageConfig(c map[string]any, p Partition, layerDigests []string, uncompressed []bool) error {
	// Includes the legacy image metadata tags in Moby v27.0.3 Image/V1Image.
	// Size has no explicit tag name there, so its canonical spelling is uppercase.
	if err := canonicalMembers(c, "created", "author", "architecture", "os", "os.version", "os.features", "variant", "config", "rootfs", "history", "id", "parent", "comment", "container", "container_config", "docker_version", "Size", "artifactType", "subject", "manifests", "mediaType"); err != nil {
		return err
	}
	// container_config is historical build metadata, not RunConfig. Preserve its
	// existing extension behavior but reject aliases of the actual struct fields;
	// its Labels/Volumes/ExposedPorts maps must not be folded recursively.
	if historical, ok := object(c, "container_config"); ok {
		if err := canonicalMembers(historical, "Hostname", "Domainname", "User", "AttachStdin", "AttachStdout", "AttachStderr", "ExposedPorts", "Tty", "OpenStdin", "StdinOnce", "Env", "Cmd", "Healthcheck", "ArgsEscaped", "Image", "Volumes", "WorkingDir", "Entrypoint", "NetworkDisabled", "MacAddress", "OnBuild", "Labels", "StopSignal", "StopTimeout", "Shell"); err != nil {
			return err
		}
		if health, ok := object(historical, "Healthcheck"); ok {
			if err := canonicalMembers(health, "Test", "Interval", "Timeout", "StartPeriod", "StartInterval", "Retries"); err != nil {
				return err
			}
		}
	}
	// Top-level metadata extensions are covered by raw digests and ignored.
	// Execution fields below remain explicit; artifact/index disguises are refused.
	if text(c, "os") != p.OS || text(c, "architecture") != p.Arch || hasAny(c, "artifactType", "subject", "manifests", "mediaType") {
		return fmt.Errorf("image config platform or format mismatch")
	}
	for _, key := range []string{"created", "author", "os.version", "variant"} {
		if v, exists := c[key]; exists {
			if _, ok := v.(string); !ok {
				return fmt.Errorf("invalid image config %s", key)
			}
		}
	}
	// v1 target has no variant/OS feature negotiation. Refuse extra requirements.
	if text(c, "variant") != "" {
		return fmt.Errorf("unsupported platform variant")
	}
	if v, exists := c["os.features"]; exists {
		a, ok := v.([]any)
		if !ok || len(a) != 0 {
			return fmt.Errorf("unsupported OS features")
		}
	}
	root, ok := object(c, "rootfs")
	if !ok || text(root, "type") != "layers" || !only(root, "type", "diff_ids") {
		return fmt.Errorf("normal image rootfs required")
	}
	diffs, ok := array(root, "diff_ids")
	if !ok || len(diffs) != len(layerDigests) {
		return fmt.Errorf("rootfs/layer count mismatch")
	}
	for i, v := range diffs {
		s, ok := v.(string)
		if !ok || !digestPattern.MatchString(s) || (uncompressed[i] && s != layerDigests[i]) {
			return fmt.Errorf("invalid layer diff ID")
		}
	}
	exec, ok := object(c, "config")
	if !ok {
		return fmt.Errorf("image execution config required")
	}
	if err := executionConfig(exec); err != nil {
		return err
	}
	if h, exists := c["history"]; exists {
		entries, ok := h.([]any)
		if !ok {
			return fmt.Errorf("invalid image history")
		}
		nonempty := 0
		for _, v := range entries {
			e, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("invalid history entry")
			}
			if err := canonicalMembers(e, "created", "created_by", "author", "comment", "empty_layer"); err != nil {
				return err
			}
			for _, k := range []string{"created", "created_by", "author", "comment", "empty_layer"} {
				v, exists := e[k]
				if !exists {
					continue
				}
				if k == "empty_layer" {
					if _, ok := v.(bool); !ok {
						return fmt.Errorf("invalid history empty_layer")
					}
				} else if _, ok := v.(string); !ok {
					return fmt.Errorf("invalid history text")
				}
			}
			empty, _ := e["empty_layer"].(bool)
			if !empty {
				nonempty++
			}
		}
		if nonempty != len(layerDigests) {
			return fmt.Errorf("history/layer count mismatch")
		}
	}
	return nil
}

func executionConfig(c map[string]any) error {
	// Only known OCI/Docker execution fields are supported; silently ignoring a
	// new execution field could make the verifier and Engine disagree.
	for k, v := range c {
		ok := false
		switch k {
		case "User", "WorkingDir", "StopSignal":
			_, ok = v.(string)
		case "Env", "Entrypoint", "Cmd", "OnBuild", "Shell":
			ok = v == nil || stringsArray(v)
		case "ArgsEscaped":
			_, ok = v.(bool)
		case "Labels":
			ok = v == nil || stringMap(v)
		case "ExposedPorts", "Volumes":
			m, isMap := v.(map[string]any)
			ok = v == nil || isMap
			for _, value := range m {
				o, isObject := value.(map[string]any)
				if !isObject || len(o) != 0 {
					ok = false
				}
			}
		case "Healthcheck":
			m, isMap := v.(map[string]any)
			ok = isMap && only(m, "Test", "Interval", "Timeout", "StartPeriod", "StartInterval", "Retries")
			for key, value := range m {
				if key == "Test" {
					ok = ok && stringsArray(value)
				} else {
					_, isInt := integer(m, key)
					ok = ok && isInt
				}
			}
		}
		if !ok || strings.TrimSpace(k) != k {
			return fmt.Errorf("unsupported or malformed execution config field %q", k)
		}
	}
	return nil
}
