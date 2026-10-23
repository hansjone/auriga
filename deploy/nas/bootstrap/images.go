package main

// Image transport verification is deliberately independent of installation,
// access configuration, PKI, runtime signatures and the deployment data root.
import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
)

var imageNames = []string{"platform", "edge", "agent"}
var imageDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var imageHex = regexp.MustCompile(`^[0-9a-f]{64}$`)
var imageCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type imageLimits struct {
	tarBytes, expandedBytes  int64
	entries                  int
	jsonBytes, metadataBytes int64
}

var defaultImageLimits = imageLimits{8 << 30, 16 << 30, 32768, 2 << 20, 32 << 20}

type imageRecord struct {
	ID       string `json:"id"`
	Tar      string `json:"tar"`
	ConfigID string `json:"config_id"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

type imageProof struct {
	ConfigID  string `json:"config_id"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	diffIDs   []string
	tarSHA    string
	sourceIDs map[string]bool
}

type imageArchiveFile struct {
	digest, diffID string
	json           []byte
	size           int64
}

// All modes are read-only. Metadata is used by CI before a bundle manifest
// exists; check and resolve require the complete checksummed public bundle.
func imageCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: auriga-nas-bootstrap image-{metadata|check|resolve} DIRECTORY")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if args[0] == "image-metadata" {
		proofs, err := imageArchives(ctx, args[1], defaultImageLimits)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(proofs)
	}
	if args[0] != "image-check" && args[0] != "image-resolve" {
		return errors.New("unknown image command")
	}
	proofs, err := verifyImageBundle(ctx, args[1], defaultImageLimits)
	if err != nil {
		return fmt.Errorf("invalid image bundle (obtain a fresh nas-bundle artifact): %w", err)
	}
	if args[0] == "image-check" {
		return nil
	}
	return resolveImages(ctx, proofs, stdout, inspectDockerImage, exportDockerImage)
}

func imageArchives(ctx context.Context, dir string, limits imageLimits) (map[string]imageProof, error) {
	if err := validateImageDirectory(dir); err != nil {
		return nil, err
	}
	result := make(map[string]imageProof)
	for _, name := range imageNames {
		f, err := openImageFile(filepath.Join(dir, name+".tar"))
		if err != nil {
			return nil, err
		}
		proof, err := readImageArchive(ctx, f, "auriga/"+name+":nas", limits)
		closeErr := f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s.tar: %w", name, err)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		result[name] = proof
	}
	return result, nil
}

func verifyImageBundle(ctx context.Context, bundle string, limits imageLimits) (map[string]imageProof, error) {
	if err := validateImageDirectory(bundle); err != nil {
		return nil, err
	}
	raw, err := readImageFile(filepath.Join(bundle, "images", "manifest.json"), limits.jsonBytes)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Commit    string                                  `json:"commit"`
		Tree      string                                  `json:"source_tree"`
		Images    map[string]imageRecord                  `json:"images"`
		Bootstrap struct{ File, SHA256 string }           `json:"bootstrap"`
		Runtime   struct{ File, SHA256, OS, Arch string } `json:"runtime_release"`
	}
	if err := imageJSON(raw, &manifest); err != nil {
		return nil, err
	}
	if !imageCommit.MatchString(manifest.Commit) || !imageCommit.MatchString(manifest.Tree) || len(manifest.Images) != 3 {
		return nil, errors.New("missing commit/source_tree or exactly three image records")
	}
	if manifest.Bootstrap.File != "deploy-nas/bin/auriga-nas-bootstrap" || !imageHex.MatchString(manifest.Bootstrap.SHA256) ||
		manifest.Runtime.File != "deploy-nas/bin/runtime-release" || !imageHex.MatchString(manifest.Runtime.SHA256) ||
		manifest.Runtime.OS != "linux" || manifest.Runtime.Arch != "amd64" {
		return nil, errors.New("invalid bundled tool metadata")
	}
	for _, name := range imageNames {
		r, ok := manifest.Images[name]
		if !ok || r.Tar != name+".tar" || !imageDigest.MatchString(r.ID) || !imageDigest.MatchString(r.ConfigID) || r.OS != "linux" || r.Arch != "amd64" {
			return nil, fmt.Errorf("missing or invalid %s config_id/platform/tar metadata", name)
		}
	}
	rootSums, err := imageChecksums(filepath.Join(bundle, "SHA256SUMS"), []string{manifest.Bootstrap.File, manifest.Runtime.File, "images/manifest.json", "versions.txt"})
	if err != nil {
		return nil, err
	}
	if rootSums[manifest.Bootstrap.File] != manifest.Bootstrap.SHA256 || rootSums[manifest.Runtime.File] != manifest.Runtime.SHA256 {
		return nil, errors.New("tool metadata/checksum mismatch")
	}
	for file, want := range rootSums {
		got, err := imageFileSHA(ctx, filepath.Join(bundle, filepath.FromSlash(file)), 128<<20)
		if err != nil {
			return nil, err
		}
		if got != want {
			return nil, fmt.Errorf("root checksum mismatch: %s", file)
		}
	}
	sums, err := imageChecksums(filepath.Join(bundle, "images", "SHA256SUMS"), []string{"platform.tar", "edge.tar", "agent.tar"})
	if err != nil {
		return nil, err
	}
	proofs, err := imageArchives(ctx, filepath.Join(bundle, "images"), limits)
	if err != nil {
		return nil, err
	}
	for _, name := range imageNames {
		p := proofs[name]
		if p.tarSHA != sums[name+".tar"] || p.ConfigID != manifest.Images[name].ConfigID || !p.sourceIDs[manifest.Images[name].ID] {
			return nil, fmt.Errorf("%s tar checksum/config_id mismatch", name)
		}
	}
	return proofs, nil
}

// filepath.Join/Abs clean dot segments before opening. Reject them first so
// validation cannot address a different directory than the loader's literal
// Docker argv when a symlink and '..' occur together.
func validateImageDirectory(dir string) error {
	if dir == "" {
		return errors.New("empty image directory")
	}
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		if part == "." || part == ".." {
			return errors.New("image directory contains unsafe dot segments")
		}
	}
	return nil
}

func openImageFile(file string) (*os.File, error) {
	absolute, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	for p := absolute; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("image path is a symlink: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	before, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular image input: %s", file)
	}
	f, err := os.Open(absolute)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, errors.New("image input changed while opening")
	}
	return f, nil
}

func readImageFile(file string, max int64) ([]byte, error) {
	f, err := openImageFile(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(raw)) > max {
		return nil, errors.New("image metadata byte budget exceeded")
	}
	return raw, err
}

func imageFileSHA(ctx context.Context, file string, max int64) (string, error) {
	f, err := openImageFile(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	r := &imageBudgetReader{ctx: ctx, r: f, left: max}
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func imageChecksums(file string, names []string) (map[string]string, error) {
	raw, err := readImageFile(file, 16<<10)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool)
	for _, name := range names {
		allowed[name] = true
	}
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if len(line) < 67 || !imageHex.MatchString(line[:64]) || line[64] != ' ' || (line[65] != ' ' && line[65] != '*') {
			return nil, errors.New("invalid image checksum record")
		}
		name := strings.TrimPrefix(line[66:], "./")
		if !allowed[name] || result[name] != "" {
			return nil, errors.New("unknown/duplicate image checksum path")
		}
		result[name] = line[:64]
	}
	if len(result) != len(allowed) {
		return nil, errors.New("missing image checksum records")
	}
	return result, nil
}

type imageBudgetReader struct {
	ctx  context.Context
	r    io.Reader
	left int64
}

func (r *imageBudgetReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.left == 0 {
		var probe [1]byte
		n, err := r.r.Read(probe[:])
		if n != 0 {
			return 0, errors.New("image byte budget exceeded")
		}
		return 0, err
	}
	if int64(len(p)) > r.left {
		p = p[:r.left]
	}
	n, err := r.r.Read(p)
	r.left -= int64(n)
	return n, err
}

func safeImageTarPath(name string) bool {
	if name == "" || len(name) > 240 || name != path.Clean(name) || strings.HasPrefix(name, "/") || name == "." {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("/._-", c)) {
			return false
		}
	}
	return true
}

func readImageArchive(ctx context.Context, input io.Reader, tag string, limits imageLimits) (imageProof, error) {
	var proof imageProof
	hash := sha256.New()
	raw := &imageBudgetReader{ctx: ctx, r: input, left: limits.tarBytes}
	stream := io.TeeReader(raw, hash)
	tr := tar.NewReader(stream)
	files := make(map[string]imageArchiveFile)
	seen := make(map[string]bool)
	var expanded, metadata int64
	var padding int64
	for count := 0; ; count++ {
		before := raw.left
		header, err := tr.Next()
		if err == io.EOF {
			if before-raw.left < padding+1024 {
				return proof, errors.New("truncated image tar end marker")
			}
			break
		}
		if err != nil {
			return proof, err
		}
		if count >= limits.entries {
			return proof, errors.New("image tar entry budget exceeded")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safeImageTarPath(name) || seen[name] {
			return proof, errors.New("unsafe/duplicate image tar path")
		}
		seen[name] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return proof, errors.New("nonempty image tar directory")
			}
			padding = 0
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Linkname != "" {
			return proof, errors.New("image tar links/special entries forbidden")
		}
		if header.Size < 0 || header.Size > limits.tarBytes {
			return proof, errors.New("image tar member byte budget exceeded")
		}
		padding = (512 - header.Size%512) % 512
		fileHash := sha256.New()
		reader := bufio.NewReader(io.TeeReader(tr, fileHash))
		magic, _ := reader.Peek(2)
		var content io.Reader = reader
		var gz *gzip.Reader
		if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
			gz, err = gzip.NewReader(reader)
			if err != nil {
				return proof, err
			}
			content = gz
		}
		diff := sha256.New()
		var captured bytes.Buffer
		// Docker-save metadata is either named JSON or a JSON OCI blob. Keep
		// bounded raw bytes only for these; layers are always streamed.
		prefix, _ := reader.Peek(64)
		trimmed := bytes.TrimSpace(prefix)
		capture := gz == nil && (name == "manifest.json" || strings.HasSuffix(name, ".json") || len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '['))
		var sink io.Writer = diff
		if capture {
			if header.Size > limits.jsonBytes || metadata+header.Size > limits.metadataBytes {
				return proof, errors.New("image JSON metadata budget exceeded")
			}
			metadata += header.Size
			sink = io.MultiWriter(diff, &captured)
		}
		n, err := io.Copy(sink, &imageBudgetReader{ctx: ctx, r: content, left: limits.expandedBytes - expanded})
		expanded += n
		if gz != nil {
			_ = gz.Close()
		}
		if err != nil {
			return proof, err
		}
		if capture {
			var value any
			if err := imageJSON(captured.Bytes(), &value); err != nil {
				return proof, fmt.Errorf("%s: %w", name, err)
			}
		}
		files[name] = imageArchiveFile{"sha256:" + hex.EncodeToString(fileHash.Sum(nil)), "sha256:" + hex.EncodeToString(diff.Sum(nil)), captured.Bytes(), header.Size}
	}
	// tar.Reader stops at the end marker. Consume and hash the complete stream,
	// refusing hidden concatenated archives or nonzero trailing payloads.
	buffer := make([]byte, 32<<10)
	for {
		n, err := stream.Read(buffer)
		for _, b := range buffer[:n] {
			if b != 0 {
				return proof, errors.New("nonzero data after image tar end")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return proof, err
		}
	}
	var manifest []struct {
		Config           string
		RepoTags, Layers []string
	}
	if err := imageJSON(files["manifest.json"].json, &manifest); err != nil {
		return proof, fmt.Errorf("docker-save manifest: %w", err)
	}
	if len(manifest) != 1 || len(manifest[0].Layers) == 0 || len(manifest[0].Layers) > 256 {
		return proof, errors.New("ambiguous image manifest/tag/layers")
	}
	// Saving an immutable ID can omit RepoTags. Source archives require exactly
	// the fixed application tag. Exported archives are selected by immutable ID,
	// so any tag aliases in that record have no selection authority.
	if tag != "" && (len(manifest[0].RepoTags) != 1 || manifest[0].RepoTags[0] != tag) {
		return proof, errors.New("wrong fixed image tag")
	}
	configName := manifest[0].Config
	config, ok := files[configName]
	if !safeImageTarPath(configName) || !ok {
		return proof, errors.New("missing/unsafe image config")
	}
	var cfg struct {
		OS      string `json:"os"`
		Arch    string `json:"architecture"`
		Variant string `json:"variant"`
		RootFS  struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err := imageJSON(config.json, &cfg); err != nil {
		return proof, fmt.Errorf("image config: %w", err)
	}
	if cfg.OS != "linux" || cfg.Arch != "amd64" || cfg.Variant != "" {
		return proof, errors.New("image must be unique linux/amd64")
	}
	if cfg.RootFS.Type != "layers" || len(cfg.RootFS.DiffIDs) != len(manifest[0].Layers) {
		return proof, errors.New("image rootfs/layer count mismatch")
	}
	for i, name := range manifest[0].Layers {
		f, ok := files[name]
		if !safeImageTarPath(name) || !ok || name == configName || name == "manifest.json" {
			return proof, errors.New("missing/ambiguous image layer")
		}
		if !imageDigest.MatchString(cfg.RootFS.DiffIDs[i]) || f.diffID != cfg.RootFS.DiffIDs[i] {
			return proof, errors.New("image layer diffID mismatch")
		}
	}
	ids, err := imageOCIIdentity(files, config.digest, manifest[0].Layers, tag)
	if err != nil {
		return proof, err
	}
	return imageProof{config.digest, cfg.OS, cfg.Arch, cfg.RootFS.DiffIDs, hex.EncodeToString(hash.Sum(nil)), ids}, nil
}

type imageDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS      string `json:"os"`
		Arch    string `json:"architecture"`
		Variant string `json:"variant"`
	} `json:"platform"`
	Annotations map[string]string `json:"annotations"`
	URLs        []string          `json:"urls"`
}

// New Docker-save archives also carry an OCI graph. Validate its descriptor
// bytes and require exactly one runnable manifest bound to the legacy save
// record. BuildKit's unknown/unknown attestations are auxiliary only, and must
// point back to that exact runnable manifest.
func imageOCIIdentity(files map[string]imageArchiveFile, configID string, layers []string, tag string) (map[string]bool, error) {
	ids := map[string]bool{configID: true}
	index, hasIndex := files["index.json"]
	layout, hasLayout := files["oci-layout"]
	if !hasIndex && !hasLayout {
		return ids, nil
	}
	if !hasIndex || !hasLayout {
		return nil, errors.New("incomplete OCI image layout")
	}
	var version struct {
		Version string `json:"imageLayoutVersion"`
	}
	if err := imageJSON(layout.json, &version); err != nil || version.Version != "1.0.0" {
		return nil, errors.New("invalid OCI image layout version")
	}
	for name, file := range files {
		if strings.HasPrefix(name, "blobs/") && name != "blobs/sha256/"+strings.TrimPrefix(file.digest, "sha256:") {
			return nil, errors.New("OCI blob path/hash mismatch")
		}
	}
	readDescriptor := func(d imageDescriptor) (imageArchiveFile, error) {
		file, ok := files["blobs/sha256/"+strings.TrimPrefix(d.Digest, "sha256:")]
		if !imageDigest.MatchString(d.Digest) || !ok || file.digest != d.Digest || d.Size != file.size || len(d.URLs) != 0 {
			return file, errors.New("OCI descriptor digest/size/blob mismatch")
		}
		return file, nil
	}
	var selected string
	attestations := []imageDescriptor{}
	visited := map[string]bool{}
	var walk func(imageDescriptor, int) error
	walk = func(d imageDescriptor, depth int) error {
		if depth > 4 || visited[d.Digest] {
			return errors.New("ambiguous/cyclic OCI image graph")
		}
		visited[d.Digest] = true
		file, err := readDescriptor(d)
		if err != nil {
			return err
		}
		if d.Platform != nil && d.Platform.OS == "unknown" && d.Platform.Arch == "unknown" && d.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
			if d.MediaType != "application/vnd.oci.image.manifest.v1+json" {
				return errors.New("invalid OCI attestation descriptor")
			}
			attestations = append(attestations, d)
			return nil
		}
		if d.Platform != nil && (d.Platform.OS != "linux" || d.Platform.Arch != "amd64" || d.Platform.Variant != "") {
			return errors.New("additional/wrong runnable OCI platform")
		}
		ids[d.Digest] = true
		switch d.MediaType {
		case "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json":
			var idx struct {
				Schema    int               `json:"schemaVersion"`
				Manifests []imageDescriptor `json:"manifests"`
			}
			if err := imageJSON(file.json, &idx); err != nil {
				return err
			}
			if idx.Schema != 2 || len(idx.Manifests) == 0 || len(idx.Manifests) > 16 {
				return errors.New("invalid OCI index candidates")
			}
			for _, child := range idx.Manifests {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json":
			if selected != "" {
				return errors.New("multiple runnable OCI image candidates")
			}
			selected = d.Digest
			var m struct {
				Schema int               `json:"schemaVersion"`
				Config imageDescriptor   `json:"config"`
				Layers []imageDescriptor `json:"layers"`
			}
			if err := imageJSON(file.json, &m); err != nil {
				return err
			}
			if m.Schema != 2 || m.Config.Digest != configID || len(m.Layers) != len(layers) {
				return errors.New("OCI manifest/config/layer binding mismatch")
			}
			if _, err := readDescriptor(m.Config); err != nil {
				return err
			}
			for i, layer := range m.Layers {
				if _, err := readDescriptor(layer); err != nil {
					return err
				}
				if layer.Digest != files[layers[i]].digest {
					return errors.New("OCI ordered layer binding mismatch")
				}
			}
		default:
			return errors.New("unsupported runnable OCI media type")
		}
		return nil
	}
	var root struct {
		Schema    int               `json:"schemaVersion"`
		Manifests []imageDescriptor `json:"manifests"`
	}
	if err := imageJSON(index.json, &root); err != nil {
		return nil, err
	}
	if root.Schema != 2 || len(root.Manifests) != 1 {
		return nil, errors.New("ambiguous OCI archive root")
	}
	if tag != "" {
		if name := root.Manifests[0].Annotations["io.containerd.image.name"]; name != "" && name != "docker.io/"+tag && name != tag {
			return nil, errors.New("OCI archive tag mismatch")
		}
	}
	if err := walk(root.Manifests[0], 0); err != nil {
		return nil, err
	}
	if selected == "" {
		return nil, errors.New("missing runnable OCI image")
	}
	for _, d := range attestations {
		if d.Annotations["vnd.docker.reference.digest"] != selected {
			return nil, errors.New("OCI attestation bound to another image")
		}
		file, _ := readDescriptor(d)
		var m struct {
			Schema int               `json:"schemaVersion"`
			Config imageDescriptor   `json:"config"`
			Layers []imageDescriptor `json:"layers"`
		}
		if err := imageJSON(file.json, &m); err != nil {
			return nil, err
		}
		if m.Schema != 2 || len(m.Layers) > 16 {
			return nil, errors.New("invalid OCI attestation manifest")
		}
		if _, err := readDescriptor(m.Config); err != nil {
			return nil, err
		}
		for _, layer := range m.Layers {
			if _, err := readDescriptor(layer); err != nil {
				return nil, err
			}
		}
	}
	return ids, nil
}

// encoding/json accepts duplicate object keys and case-insensitive field names.
// Reject both ambiguity forms before decoding into a typed record.
func imageJSON(raw []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var walk func(int, reflect.Type) error
	tokens := 0
	walk = func(depth int, schema reflect.Type) error {
		for schema != nil && schema.Kind() == reflect.Pointer {
			schema = schema.Elem()
		}
		if depth > 64 || tokens > 100000 {
			return errors.New("image JSON nesting/token budget exceeded")
		}
		tokens++
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			fields := make(map[string]bool)
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return errors.New("duplicate/ambiguous image JSON key")
				}
				seen[s] = true
				var child reflect.Type
				if schema != nil && schema.Kind() == reflect.Struct {
					for i := 0; i < schema.NumField(); i++ {
						field := schema.Field(i)
						name := strings.Split(field.Tag.Get("json"), ",")[0]
						if name == "" {
							name = field.Name
						}
						if strings.EqualFold(name, s) {
							if fields[name] {
								return errors.New("duplicate/ambiguous image JSON field")
							}
							fields[name] = true
							child = field.Type
							break
						}
					}
				} else if schema != nil && schema.Kind() == reflect.Map {
					child = schema.Elem()
				}
				if err := walk(depth+1, child); err != nil {
					return err
				}
			}
		case '[':
			var child reflect.Type
			if schema != nil && (schema.Kind() == reflect.Slice || schema.Kind() == reflect.Array) {
				child = schema.Elem()
			}
			for decoder.More() {
				if err := walk(depth+1, child); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected image JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(0, reflect.TypeOf(output)); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing image JSON data")
	}
	return json.Unmarshal(raw, output)
}

type imageInspection struct{ ID, OS, Arch string }
type imageInspectFunc func(context.Context, string) (imageInspection, error)
type imageExportFunc func(context.Context, string, func(io.Reader) error) error

func resolveImages(ctx context.Context, proofs map[string]imageProof, stdout io.Writer, inspect imageInspectFunc, export imageExportFunc) error {
	var result bytes.Buffer
	for _, name := range imageNames {
		actual, err := inspect(ctx, "auriga/"+name+":nas")
		if err != nil {
			return err
		}
		if !imageDigest.MatchString(actual.ID) || actual.OS != "linux" || actual.Arch != "amd64" {
			return fmt.Errorf("%s: invalid local immutable ID/platform", name)
		}
		err = export(ctx, actual.ID, func(r io.Reader) error {
			got, err := readImageArchive(ctx, r, "", defaultImageLimits)
			if err != nil {
				return err
			}
			want := proofs[name]
			if got.ConfigID != want.ConfigID || !equalImageLayers(got.diffIDs, want.diffIDs) {
				return errors.New("loaded image differs from source config/layer chain")
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("%s (%s): %w", name, actual.ID, err)
		}
		fmt.Fprintf(&result, "AURIGA_IMAGE_%s=%s\n", strings.ToUpper(name), actual.ID)
	}
	_, err := io.Copy(stdout, &result)
	return err
}

func equalImageLayers(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Docker receives literal argv only. The immutable ID returned by inspect is
// the sole save selector: a concurrent mutable tag change cannot redirect it.
func inspectDockerImage(ctx context.Context, tag string) (imageInspection, error) {
	var actual imageInspection
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", `{"Id":{{json .Id}},"Os":{{json .Os}},"Architecture":{{json .Architecture}}}`, tag)
	var out imageLimitedBuffer
	out.max = 16 << 10
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return actual, fmt.Errorf("docker image inspect failed: %w", err)
	}
	var record struct {
		ID   string `json:"Id"`
		OS   string `json:"Os"`
		Arch string `json:"Architecture"`
	}
	if err := imageJSON(out.Bytes(), &record); err != nil {
		return actual, err
	}
	return imageInspection{record.ID, record.OS, record.Arch}, nil
}

type imageLimitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *imageLimitedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, errors.New("Docker metadata byte budget exceeded")
	}
	return b.Buffer.Write(p)
}

func exportDockerImage(ctx context.Context, id string, verify func(io.Reader) error) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "save", id)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = pipe.Close()
		return err
	}
	err = verify(pipe)
	if err != nil {
		cancel()
	}
	_ = pipe.Close()
	waitErr := cmd.Wait()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return fmt.Errorf("docker image save failed: %w", waitErr)
	}
	return nil
}
