package runtimebundle

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/releaseverify"
)

func stageFixture(t *testing.T, duplicate bool) (StageLayoutOptions, []byte, *Material) {
	t.Helper()
	f := fresh(t)
	layer := bytes.Repeat([]byte("layer-data"), 5)
	diffs := fmt.Sprintf("%q", hash(layer))
	layers := fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":%q,"size":%d}`, hash(layer), len(layer))
	if duplicate {
		diffs += "," + diffs
		layers += "," + layers
	}
	config := []byte(fmt.Sprintf(`{"os":"linux","architecture":"amd64","config":{"Cmd":["/dsh"]},"rootfs":{"type":"layers","diff_ids":[%s]}}`, diffs))
	f.setConfig(t, config)
	f.setManifest(t, []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[%s]}`, hash(config), len(config), layers)))
	m := signOK(t, f)
	layout := filepath.Join(filepath.Dir(f.o.OutputDir), "input-layout")
	if err := os.MkdirAll(filepath.Join(layout, "blobs", "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(layout, "oci-layout"), []byte(releaseverify.LayoutVersionJSON))
	index := []byte(fmt.Sprintf(`{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d,"annotations":{"org.opencontainers.image.ref.name":"source-tag"}}]}`, hash(f.manifest), len(f.manifest)))
	put(t, filepath.Join(layout, "index.json"), index)
	for _, b := range [][]byte{f.manifest, f.conf, layer, []byte("unreferenced")} {
		put(t, filepath.Join(layout, "blobs", "sha256", strings.TrimPrefix(hash(b), "sha256:")), b)
	}
	return StageLayoutOptions{Verify: f.verify(), LayoutDir: layout, OutputDir: filepath.Join(filepath.Dir(layout), "staged"), MaxBytes: int64(len(f.manifest) + len(f.conf) + len(layer))}, layer, m
}

func TestStageLayoutCompleteAndBudget(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			o, layer, m := stageFixture(t, duplicate)
			o.MaxBytes--
			if _, err := StageLayout(o); err == nil {
				t.Fatal("under budget accepted")
			}
			if _, err := os.Stat(o.OutputDir); !os.IsNotExist(err) {
				t.Fatal("output created before budget check")
			}
			o.MaxBytes++
			s, err := StageLayout(o)
			if err != nil {
				t.Fatal(err)
			}
			if s.BlobCount != 3 || s.TotalBytes != o.MaxBytes || s.ManifestDigest != hash(m.Manifest()) || s.ConfigDigest != hash(m.Config()) {
				t.Fatalf("wrong snapshot: %+v", s)
			}
			for _, b := range [][]byte{m.Manifest(), m.Config(), layer} {
				got, err := os.ReadFile(filepath.Join(o.OutputDir, "blobs", "sha256", strings.TrimPrefix(hash(b), "sha256:")))
				if err != nil || !bytes.Equal(got, b) {
					t.Fatal("staged blob differs")
				}
			}
			entries, err := os.ReadDir(filepath.Join(o.OutputDir, "blobs", "sha256"))
			if err != nil || len(entries) != 3 {
				t.Fatal("unselected blob copied")
			}
			index, err := os.ReadFile(filepath.Join(o.OutputDir, "index.json"))
			if err != nil || bytes.Contains(index, []byte("source-tag")) {
				t.Fatal("untrusted tag retained")
			}
			if _, err := StageLayout(o); err == nil {
				t.Fatal("existing output accepted")
			}
		})
	}
}

func TestStageLayoutRejectsMissingCorruptAndOverlap(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*testing.T, StageLayoutOptions, []byte, *Material)
	}{
		{"missing layer", func(t *testing.T, o StageLayoutOptions, layer []byte, _ *Material) {
			t.Helper()
			if err := os.Remove(filepath.Join(o.LayoutDir, "blobs", "sha256", strings.TrimPrefix(hash(layer), "sha256:"))); err != nil {
				t.Fatal(err)
			}
		}},
		{"same-size corrupt layer", func(t *testing.T, o StageLayoutOptions, layer []byte, _ *Material) {
			t.Helper()
			put(t, filepath.Join(o.LayoutDir, "blobs", "sha256", strings.TrimPrefix(hash(layer), "sha256:")), bytes.Repeat([]byte{'x'}, len(layer)))
		}},
		{"wrong descriptor size", func(t *testing.T, o StageLayoutOptions, _ []byte, _ *Material) {
			t.Helper()
			p := filepath.Join(o.LayoutDir, "index.json")
			b, _ := os.ReadFile(p)
			put(t, p, bytes.Replace(b, []byte(`"size":`), []byte(`"size":999`), 1))
		}},
		{"unapproved manifest", func(t *testing.T, o StageLayoutOptions, _ []byte, _ *Material) {
			t.Helper()
			p := filepath.Join(o.LayoutDir, "index.json")
			b, _ := os.ReadFile(p)
			put(t, p, bytes.Replace(b, []byte(`"digest":"sha256:`), []byte(`"digest":"sha256:1`), 1))
		}},
		{"bad config", func(t *testing.T, o StageLayoutOptions, _ []byte, m *Material) {
			t.Helper()
			p := filepath.Join(o.LayoutDir, "blobs", "sha256", strings.TrimPrefix(hash(m.Config()), "sha256:"))
			b, _ := os.ReadFile(p)
			b[0] ^= 1
			put(t, p, b)
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			o, layer, m := stageFixture(t, false)
			change.edit(t, o, layer, m)
			if _, err := StageLayout(o); err == nil {
				t.Fatal("invalid input accepted")
			}
			if _, err := os.Stat(o.OutputDir); !os.IsNotExist(err) {
				t.Fatal("output created on input failure")
			}
		})
	}
	o, _, _ := stageFixture(t, false)
	o.MaxBytes = 0
	if _, err := StageLayout(o); err == nil {
		t.Fatal("zero budget accepted")
	}
	o.MaxBytes = 1 << 62
	for _, out := range []string{filepath.Join(o.LayoutDir, "inside"), filepath.Join(o.Verify.Dir, "inside"), o.LayoutDir, o.Verify.Dir} {
		o.OutputDir = out
		if _, err := StageLayout(o); err == nil {
			t.Fatal("overlap accepted")
		}
	}
}

func TestCopyRechecksChangedBlob(t *testing.T) {
	o, layer, _ := stageFixture(t, false)
	r, err := openDirectory(o.LayoutDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	d := releaseverify.BlobDescriptor{Digest: hash(layer), Size: uint64(len(layer)), MediaType: "application/vnd.oci.image.layer.v1.tar"}
	if err := checkBlob(r, d, nil); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(o.LayoutDir, "blobs", "sha256", strings.TrimPrefix(hash(layer), "sha256:")), bytes.Repeat([]byte{'x'}, len(layer)))
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	dest, err := openDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dest.Close() }()
	if err := copyBlob(r, dest, d); err == nil {
		t.Fatal("copy succeeded after source mutation")
	}
}

func TestDescriptorBudgetOverflowAndConflict(t *testing.T) {
	a := releaseverify.BlobDescriptor{Digest: "sha256:" + strings.Repeat("a", 64), Size: uint64(math.MaxInt64) - 1, MediaType: "x"}
	b := releaseverify.BlobDescriptor{Digest: "sha256:" + strings.Repeat("b", 64), Size: 2, MediaType: "x"}
	if _, _, err := planDescriptors([]releaseverify.BlobDescriptor{a, b}, math.MaxInt64); err == nil {
		t.Fatal("overflow budget accepted")
	}
	if _, _, err := planDescriptors([]releaseverify.BlobDescriptor{b, b}, 2); err != nil {
		t.Fatal(err)
	}
	conflict := b
	conflict.MediaType = "other"
	if _, _, err := planDescriptors([]releaseverify.BlobDescriptor{b, conflict}, 10); err == nil {
		t.Fatal("conflicting digest accepted")
	}
	conflict = b
	conflict.Size = 3
	if _, _, err := planDescriptors([]releaseverify.BlobDescriptor{b, conflict}, 10); err == nil {
		t.Fatal("conflicting size accepted")
	}
}
