package runtimebundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
)

// StageLayoutOptions provides independent trust, an untrusted OCI layout and
// an explicit maximum for all unique referenced CAS bytes including M and C.
type StageLayoutOptions struct {
	Verify               VerifyOptions
	LayoutDir, OutputDir string
	MaxBytes             int64
}

// LayoutSnapshot describes only the completed copy at the time of return.
type LayoutSnapshot struct {
	ManifestDigest, ConfigDigest string
	BlobCount                    int
	TotalBytes                   int64
}

// StageLayout verifies the public bundle with production Trust/V/OCI, then
// fully checks and copies one image into a new minimal OCI image layout.
func StageLayout(o StageLayoutOptions) (LayoutSnapshot, error) {
	s, err := stageLayout(o)
	if err != nil {
		return LayoutSnapshot{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return s, nil
}

func stageLayout(o StageLayoutOptions) (LayoutSnapshot, error) {
	if o.MaxBytes <= 0 {
		return LayoutSnapshot{}, fmt.Errorf("positive explicit byte budget required")
	}
	bundlePath, err := absolutePath(o.Verify.Dir)
	if err != nil {
		return LayoutSnapshot{}, err
	}
	layoutPath, err := absolutePath(o.LayoutDir)
	if err != nil {
		return LayoutSnapshot{}, err
	}
	outPath, err := absolutePath(o.OutputDir)
	if err != nil {
		return LayoutSnapshot{}, err
	}
	if pathsOverlap(outPath, bundlePath) || pathsOverlap(outPath, layoutPath) {
		return LayoutSnapshot{}, fmt.Errorf("output overlaps an input tree")
	}
	m, err := Verify(o.Verify)
	if err != nil {
		return LayoutSnapshot{}, err
	}
	r, err := openDirectory(layoutPath)
	if err != nil {
		return LayoutSnapshot{}, fmt.Errorf("cannot open OCI layout")
	}
	defer func() { _ = r.Close() }()
	layout, err := readMember(r, "oci-layout", releaseverify.MaxLayoutMetadataBytes)
	if err != nil {
		return LayoutSnapshot{}, fmt.Errorf("cannot read OCI layout metadata")
	}
	index, err := readMember(r, "index.json", releaseverify.MaxLayoutMetadataBytes)
	if err != nil {
		return LayoutSnapshot{}, fmt.Errorf("cannot read OCI index")
	}
	if err := releaseverify.VerifyLayoutMetadata(layout, index, m.image); err != nil {
		return LayoutSnapshot{}, fmt.Errorf("invalid OCI layout metadata: %w", err)
	}
	manifest, config, layers := m.image.Descriptors()
	all := append([]releaseverify.BlobDescriptor{manifest, config}, layers...)
	unique, total, err := planDescriptors(all, o.MaxBytes)
	if err != nil {
		return LayoutSnapshot{}, err
	}
	// Complete the input pass, including every layer, before creating output.
	for _, d := range unique {
		if err := checkBlob(r, d, nil); err != nil {
			return LayoutSnapshot{}, err
		}
	}
	if err := checkBlob(r, manifest, m.manifest); err != nil {
		return LayoutSnapshot{}, fmt.Errorf("layout manifest differs from signed bytes")
	}
	if err := checkBlob(r, config, m.config); err != nil {
		return LayoutSnapshot{}, fmt.Errorf("layout config differs from signed bytes")
	}
	bundle, err := openDirectory(bundlePath)
	if err != nil {
		return LayoutSnapshot{}, fmt.Errorf("cannot reopen bundle directory")
	}
	defer func() { _ = bundle.Close() }()
	return writeStageLayout(r, bundle, outPath, manifest, config, unique, total)
}

func planDescriptors(all []releaseverify.BlobDescriptor, maxBytes int64) ([]releaseverify.BlobDescriptor, int64, error) {
	unique := make([]releaseverify.BlobDescriptor, 0, len(all))
	seen := make(map[string]releaseverify.BlobDescriptor, len(all))
	var total int64
	for _, d := range all {
		if old, ok := seen[d.Digest]; ok {
			if old.Size != d.Size || old.MediaType != d.MediaType {
				return nil, 0, fmt.Errorf("conflicting descriptor for repeated digest")
			}
			continue
		}
		if maxBytes <= 0 || d.Size == 0 || d.Size > uint64(maxBytes-total) {
			return nil, 0, fmt.Errorf("OCI byte budget exceeded")
		}
		total += int64(d.Size)
		seen[d.Digest] = d
		unique = append(unique, d)
	}
	return unique, total, nil
}

func writeStageLayout(r, bundle *os.Root, outPath string, manifest, config releaseverify.BlobDescriptor, unique []releaseverify.BlobDescriptor, total int64) (LayoutSnapshot, error) {
	parent, err := openDirectory(filepath.Dir(outPath))
	if err != nil {
		return LayoutSnapshot{}, fmt.Errorf("cannot open output parent")
	}
	defer func() { _ = parent.Close() }()
	// A lexical Rel misses Windows 8.3 aliases. Compare actual directory
	// identities all the way up from the pinned output parent before mkdir.
	if err := rejectInputAncestor(filepath.Dir(outPath), parent, r, bundle); err != nil {
		return LayoutSnapshot{}, err
	}
	name := filepath.Base(outPath)
	if err := parent.Mkdir(name, 0o700); err != nil {
		return LayoutSnapshot{}, fmt.Errorf("output must be a new directory")
	}
	created, err := parent.Lstat(name)
	if err != nil || !created.IsDir() || created.Mode()&os.ModeSymlink != 0 {
		return LayoutSnapshot{}, fmt.Errorf("new output directory changed")
	}
	dest, err := parent.OpenRoot(name)
	if err != nil {
		return LayoutSnapshot{}, fmt.Errorf("cannot open new output")
	}
	defer func() { _ = dest.Close() }()
	opened, err := dest.Stat(".")
	if err != nil || !os.SameFile(created, opened) {
		return LayoutSnapshot{}, fmt.Errorf("new output directory changed")
	}
	if err := dest.Mkdir("blobs", 0o755); err != nil {
		return LayoutSnapshot{}, err
	}
	if err := dest.Mkdir("blobs/sha256", 0o755); err != nil {
		return LayoutSnapshot{}, err
	}
	for _, d := range unique {
		if err := copyBlob(r, dest, d); err != nil {
			return LayoutSnapshot{}, err
		}
	}
	if err := writeLayoutFile(dest, "oci-layout", []byte(releaseverify.LayoutVersionJSON)); err != nil {
		return LayoutSnapshot{}, err
	}
	// The index is written last, after every referenced blob has been copied and
	// rechecked. It has no tag; consumers must select the signed digest.
	minimalIndex := []byte(fmt.Sprintf("{\"schemaVersion\":2,\"mediaType\":\"application/vnd.oci.image.index.v1+json\",\"manifests\":[{\"mediaType\":%q,\"digest\":%q,\"size\":%d}]}\n", manifest.MediaType, manifest.Digest, manifest.Size))
	if err := writeLayoutFile(dest, "index.json", minimalIndex); err != nil {
		return LayoutSnapshot{}, err
	}
	return LayoutSnapshot{ManifestDigest: manifest.Digest, ConfigDigest: config.Digest, BlobCount: len(unique), TotalBytes: total}, nil
}

func rejectInputAncestor(parentPath string, pinnedParent *os.Root, inputs ...*os.Root) error {
	identities := make([]os.FileInfo, len(inputs))
	for i, input := range inputs {
		st, err := input.Stat(".")
		if err != nil {
			return fmt.Errorf("cannot verify input directory identity")
		}
		identities[i] = st
	}
	pinned, err := pinnedParent.Stat(".")
	if err != nil {
		return fmt.Errorf("cannot verify output parent identity")
	}
	for _, input := range identities {
		if os.SameFile(pinned, input) {
			return fmt.Errorf("output overlaps an input tree")
		}
	}
	for path := filepath.Dir(parentPath); ; path = filepath.Dir(path) {
		ancestor, err := openDirectory(path)
		if err != nil {
			return fmt.Errorf("cannot verify output ancestor")
		}
		st, statErr := ancestor.Stat(".")
		closeErr := ancestor.Close()
		if statErr != nil || closeErr != nil {
			return fmt.Errorf("cannot verify output ancestor")
		}
		for _, input := range identities {
			if os.SameFile(st, input) {
				return fmt.Errorf("output overlaps an input tree")
			}
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	return nil
}

func pathsOverlap(a, b string) bool {
	inside := func(x, y string) bool {
		rel, err := filepath.Rel(y, x)
		if err != nil {
			return false
		}
		if runtime.GOOS == "windows" {
			rel = strings.ToLower(rel)
		}
		return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return inside(a, b) || inside(b, a)
}

func blobName(d releaseverify.BlobDescriptor) string {
	return "blobs/sha256/" + strings.TrimPrefix(d.Digest, "sha256:")
}

func checkBlob(r *os.Root, d releaseverify.BlobDescriptor, expected []byte) error {
	f, err := openMember(r, blobName(d), false)
	if err != nil {
		return fmt.Errorf("missing or unsafe OCI blob")
	}
	defer func() { _ = f.Close() }()
	return verifyBlobStream(f, d, expected, nil)
}

func verifyBlobStream(f *os.File, d releaseverify.BlobDescriptor, expected []byte, dest io.Writer) error {
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || uint64(st.Size()) != d.Size || singleLink(f) != nil {
		return fmt.Errorf("unsafe or incorrectly sized OCI blob")
	}
	h := sha256.New()
	w := io.Writer(h)
	if dest != nil {
		w = io.MultiWriter(h, dest)
	}
	if expected != nil {
		w = io.MultiWriter(w, &compareWriter{expected: expected})
	}
	n, err := io.CopyN(w, f, int64(d.Size))
	if err != nil || uint64(n) != d.Size {
		return fmt.Errorf("OCI blob changed during read")
	}
	var extra [1]byte
	got, readErr := f.Read(extra[:])
	if got != 0 || readErr != io.EOF || "sha256:"+hex.EncodeToString(h.Sum(nil)) != d.Digest {
		return fmt.Errorf("OCI blob size or digest mismatch")
	}
	return nil
}

type compareWriter struct {
	expected []byte
	offset   int
}

func (w *compareWriter) Write(p []byte) (int, error) {
	if len(p) > len(w.expected)-w.offset || !bytes.Equal(p, w.expected[w.offset:w.offset+len(p)]) {
		return 0, fmt.Errorf("original evidence bytes mismatch")
	}
	w.offset += len(p)
	return len(p), nil
}

func copyBlob(src, dest *os.Root, d releaseverify.BlobDescriptor) error {
	in, err := openMember(src, blobName(d), false)
	if err != nil {
		return fmt.Errorf("OCI blob changed before copy")
	}
	defer func() { _ = in.Close() }()
	out, err := dest.OpenFile(blobName(d), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("cannot create staged blob")
	}
	defer func() { _ = out.Close() }()
	if err := singleLink(out); err != nil {
		return fmt.Errorf("unsafe staged blob")
	}
	if err := verifyBlobStream(in, d, nil, out); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

func writeLayoutFile(r *os.Root, name string, raw []byte) error {
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("cannot create OCI metadata")
	}
	defer func() { _ = f.Close() }()
	if err := singleLink(f); err != nil {
		return err
	}
	n, err := f.Write(raw)
	if err != nil || n != len(raw) {
		return fmt.Errorf("cannot write OCI metadata")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
