package main

import (
	"archive/tar"
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
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func imageTestDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}

func imageTestArchive(t *testing.T, tag string, compressed bool, change func(map[string][]byte)) []byte {
	t.Helper()
	layer := []byte("image layer original uncompressed bytes")
	config := []byte(fmt.Sprintf(`{"os":"linux","architecture":"amd64","rootfs":{"type":"layers","diff_ids":[%q]}}`, imageTestDigest(layer)))
	manifest, err := json.Marshal([]map[string]any{{"Config": "config.json", "RepoTags": []string{tag}, "Layers": []string{"layer.tar"}}})
	if err != nil {
		t.Fatal(err)
	}
	if compressed {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		_, _ = gz.Write(layer)
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		layer = buf.Bytes()
	}
	files := map[string][]byte{"manifest.json": manifest, "config.json": config, "layer.tar": layer}
	if change != nil {
		change(files)
	}
	var result bytes.Buffer
	tw := tar.NewWriter(&result)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(raw))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func TestImageArchiveConfigAndCompressedLayerIdentity(t *testing.T) {
	var previous string
	for _, compressed := range []bool{false, true} {
		raw := imageTestArchive(t, "auriga/platform:nas", compressed, nil)
		proof, err := readImageArchive(context.Background(), bytes.NewReader(raw), "auriga/platform:nas", defaultImageLimits)
		if err != nil {
			t.Fatal(err)
		}
		if previous != "" && previous != proof.ConfigID {
			t.Fatal("compression changed config identity")
		}
		previous = proof.ConfigID
		if proof.OS != "linux" || proof.Arch != "amd64" || "sha256:"+proof.tarSHA != imageTestDigest(raw) {
			t.Fatalf("wrong proof: %+v", proof)
		}
	}
}

func TestImageArchiveRejectsUnsafeAndAmbiguousInputs(t *testing.T) {
	cases := map[string]struct {
		change func(map[string][]byte)
		want   string
	}{
		"wrong tag": {func(f map[string][]byte) {
			f["manifest.json"] = bytes.ReplaceAll(f["manifest.json"], []byte("auriga/platform:nas"), []byte("auriga/other:nas"))
		}, "wrong fixed image tag"},
		"wrong arch": {func(f map[string][]byte) {
			f["config.json"] = bytes.ReplaceAll(f["config.json"], []byte("amd64"), []byte("arm64"))
		}, "linux/amd64"},
		"wrong os": {func(f map[string][]byte) {
			f["config.json"] = bytes.ReplaceAll(f["config.json"], []byte("linux"), []byte("windows"))
		}, "linux/amd64"},
		"variant": {func(f map[string][]byte) {
			f["config.json"] = bytes.Replace(f["config.json"], []byte(`"os":`), []byte(`"variant":"v8","os":`), 1)
		}, "linux/amd64"},
		"duplicate config": {func(f map[string][]byte) {
			f["config.json"] = bytes.Replace(f["config.json"], []byte(`"os":`), []byte(`"os":"linux","os":`), 1)
		}, "duplicate/ambiguous"},
		"case alias": {func(f map[string][]byte) {
			f["config.json"] = bytes.Replace(f["config.json"], []byte(`"os":`), []byte(`"OS":"linux","os":`), 1)
		}, "duplicate/ambiguous"},
		"duplicate manifest": {func(f map[string][]byte) {
			f["manifest.json"] = bytes.Replace(f["manifest.json"], []byte(`"Config":`), []byte(`"Config":"config.json","Config":`), 1)
		}, "duplicate/ambiguous"},
		"multiple candidates": {func(f map[string][]byte) {
			raw := f["manifest.json"]
			out := append([]byte(nil), raw[:len(raw)-1]...)
			f["manifest.json"] = append(append(out, ','), raw[1:]...)
		}, "ambiguous image manifest"},
		"missing config": {func(f map[string][]byte) { delete(f, "config.json") }, "missing/unsafe image config"},
		"missing layer":  {func(f map[string][]byte) { delete(f, "layer.tar") }, "missing/ambiguous image layer"},
		"changed layer":  {func(f map[string][]byte) { f["layer.tar"] = []byte("different layer") }, "diffID mismatch"},
		"layer count": {func(f map[string][]byte) {
			f["manifest.json"] = bytes.Replace(f["manifest.json"], []byte(`"Layers":["layer.tar"]`), []byte(`"Layers":[]`), 1)
		}, "ambiguous image manifest/tag/layers"},
		"traversal":    {func(f map[string][]byte) { f["../escape"] = []byte("bad") }, "unsafe/duplicate image tar path"},
		"absolute":     {func(f map[string][]byte) { f["/escape"] = []byte("bad") }, "unsafe/duplicate image tar path"},
		"windows path": {func(f map[string][]byte) { f[`C:\escape`] = []byte("bad") }, "unsafe/duplicate image tar path"},
		"unsafe reference": {func(f map[string][]byte) {
			f["manifest.json"] = bytes.ReplaceAll(f["manifest.json"], []byte("config.json"), []byte("../config.json"))
		}, "missing/unsafe image config"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readImageArchive(context.Background(), bytes.NewReader(imageTestArchive(t, "auriga/platform:nas", false, tc.change)), "auriga/platform:nas", defaultImageLimits)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestImageArchiveBudgetsAndTruncation(t *testing.T) {
	raw := imageTestArchive(t, "auriga/platform:nas", true, nil)
	for _, tc := range []struct {
		name   string
		raw    []byte
		limits imageLimits
		want   string
	}{
		{"raw budget", raw, imageLimits{1024, 16 << 30, 32768, 2 << 20, 32 << 20}, "budget"},
		{"expanded budget", raw, imageLimits{8 << 30, 10, 32768, 2 << 20, 32 << 20}, "budget"},
		{"entry budget", raw, imageLimits{8 << 30, 16 << 30, 1, 2 << 20, 32 << 20}, "entry budget"},
		{"JSON budget", raw, imageLimits{8 << 30, 16 << 30, 32768, 8, 32 << 20}, "metadata budget"},
		{"metadata budget", raw, imageLimits{8 << 30, 16 << 30, 32768, 2 << 20, 8}, "metadata budget"},
		{"missing trailer", raw[:len(raw)-1024], defaultImageLimits, "truncated"},
		{"half trailer", raw[:len(raw)-512], defaultImageLimits, "truncated"},
		{"truncated member", raw[:600], defaultImageLimits, "unexpected EOF"},
		{"hidden payload", append(append([]byte(nil), raw...), []byte("bad")...), defaultImageLimits, "nonzero data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readImageArchive(context.Background(), bytes.NewReader(tc.raw), "auriga/platform:nas", tc.limits)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readImageArchive(ctx, bytes.NewReader(raw), "auriga/platform:nas", defaultImageLimits); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestImageArchiveRejectsLinksAndDuplicateMembers(t *testing.T) {
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeFifo, tar.TypeReg} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			if err := tw.WriteHeader(&tar.Header{Name: "bad", Typeflag: kind, Linkname: "target", Mode: 0644}); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := readImageArchive(context.Background(), &buf, "", defaultImageLimits); err == nil || !strings.Contains(err.Error(), "links/special") {
				t.Fatalf("link accepted: %v", err)
			}
		})
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for range 2 {
		if err := tw.WriteHeader(&tar.Header{Name: "duplicate", Mode: 0644}); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	if _, err := readImageArchive(context.Background(), &buf, "", defaultImageLimits); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate accepted: %v", err)
	}
}

func TestImageResolveAcceptsEngineSpecificImmutableIDs(t *testing.T) {
	for _, store := range []string{"legacy-C", "modern-M", "modern-I"} {
		t.Run(store, func(t *testing.T) {
			archives := make(map[string][]byte)
			proofs := make(map[string]imageProof)
			for _, name := range imageNames {
				raw := imageTestArchive(t, "auriga/"+name+":nas", true, nil)
				archives[name] = raw
				p, err := readImageArchive(context.Background(), bytes.NewReader(raw), "auriga/"+name+":nas", defaultImageLimits)
				if err != nil {
					t.Fatal(err)
				}
				proofs[name] = p
			}
			selectors := make(map[string]string)
			inspect := func(_ context.Context, tag string) (imageInspection, error) {
				name := strings.TrimSuffix(strings.TrimPrefix(tag, "auriga/"), ":nas")
				id := imageTestDigest([]byte(store + name))
				if store == "legacy-C" {
					id = proofs[name].ConfigID
				}
				selectors[id] = name
				return imageInspection{id, "linux", "amd64"}, nil
			}
			export := func(_ context.Context, id string, verify func(io.Reader) error) error {
				name, ok := selectors[id]
				if !ok {
					t.Fatalf("save did not use inspected immutable ID: %s", id)
				}
				return verify(bytes.NewReader(archives[name]))
			}
			var out bytes.Buffer
			if err := resolveImages(context.Background(), proofs, &out, inspect, export); err != nil {
				t.Fatal(err)
			}
			if strings.Count(out.String(), "AURIGA_IMAGE_") != 3 {
				t.Fatal(out.String())
			}
			for id, name := range selectors {
				if !strings.Contains(out.String(), "AURIGA_IMAGE_"+strings.ToUpper(name)+"="+id+"\n") {
					t.Fatal(out.String())
				}
			}
		})
	}
}

func TestImageResolveRejectsWrongLoadedImageWithoutPartialOutput(t *testing.T) {
	for _, failure := range []string{"wrong-config", "wrong-platform", "inspect-failure", "export-failure", "truncated-export", "wrong-layer", "invalid-id"} {
		t.Run(failure, func(t *testing.T) {
			proofs := make(map[string]imageProof)
			archives := make(map[string][]byte)
			for _, name := range imageNames {
				raw := imageTestArchive(t, "auriga/"+name+":nas", false, nil)
				p, err := readImageArchive(context.Background(), bytes.NewReader(raw), "auriga/"+name+":nas", defaultImageLimits)
				if err != nil {
					t.Fatal(err)
				}
				proofs[name] = p
				archives[name] = raw
			}
			current := ""
			inspect := func(_ context.Context, tag string) (imageInspection, error) {
				current = strings.TrimSuffix(strings.TrimPrefix(tag, "auriga/"), ":nas")
				r := imageInspection{imageTestDigest([]byte(current)), "linux", "amd64"}
				if current == "agent" {
					switch failure {
					case "wrong-platform":
						r.Arch = "arm64"
					case "inspect-failure":
						return r, errors.New("inspect failed")
					case "invalid-id":
						r.ID = "auriga/agent:nas"
					}
				}
				return r, nil
			}
			export := func(_ context.Context, _ string, verify func(io.Reader) error) error {
				raw := archives[current]
				if current == "agent" {
					switch failure {
					case "export-failure":
						return errors.New("save failed")
					case "truncated-export":
						raw = raw[:600]
					case "wrong-config":
						raw = imageTestArchive(t, "auriga/agent:nas", false, func(f map[string][]byte) { f["config.json"] = append([]byte(" "), f["config.json"]...) })
					case "wrong-layer":
						raw = imageTestArchive(t, "auriga/agent:nas", false, func(f map[string][]byte) { f["layer.tar"] = []byte("changed") })
					}
				}
				return verify(bytes.NewReader(raw))
			}
			var out bytes.Buffer
			if err := resolveImages(context.Background(), proofs, &out, inspect, export); err == nil {
				t.Fatal("bad loaded image accepted")
			}
			if out.Len() != 0 {
				t.Fatalf("partial success output: %s", out.String())
			}
		})
	}
}

func imageTestBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, raw []byte) {
		file := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	images := make(map[string]imageRecord)
	var sums strings.Builder
	for _, name := range imageNames {
		raw := imageTestArchive(t, "auriga/"+name+":nas", true, nil)
		write("images/"+name+".tar", raw)
		p, err := readImageArchive(context.Background(), bytes.NewReader(raw), "auriga/"+name+":nas", defaultImageLimits)
		if err != nil {
			t.Fatal(err)
		}
		images[name] = imageRecord{p.ConfigID, name + ".tar", p.ConfigID, "linux", "amd64"}
		fmt.Fprintf(&sums, "%s  ./%s.tar\n", strings.TrimPrefix(imageTestDigest(raw), "sha256:"), name)
	}
	write("images/SHA256SUMS", []byte(sums.String()))
	tool := []byte("prebuilt tool fixture")
	write("deploy-nas/bin/auriga-nas-bootstrap", tool)
	write("deploy-nas/bin/runtime-release", tool)
	write("versions.txt", []byte("fixture"))
	manifest := map[string]any{"commit": strings.Repeat("a", 40), "source_tree": strings.Repeat("b", 40), "images": images, "bootstrap": map[string]string{"file": "deploy-nas/bin/auriga-nas-bootstrap", "sha256": strings.TrimPrefix(imageTestDigest(tool), "sha256:")}, "runtime_release": map[string]string{"file": "deploy-nas/bin/runtime-release", "sha256": strings.TrimPrefix(imageTestDigest(tool), "sha256:"), "os": "linux", "arch": "amd64"}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	write("images/manifest.json", raw)
	imageTestRootSums(t, dir)
	return dir
}

func imageTestRootSums(t *testing.T, dir string) {
	t.Helper()
	var sums strings.Builder
	for _, name := range []string{"deploy-nas/bin/auriga-nas-bootstrap", "deploy-nas/bin/runtime-release", "images/manifest.json", "versions.txt"} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%s  %s\n", strings.TrimPrefix(imageTestDigest(raw), "sha256:"), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestImageBundleRejectsMissingChecksumsMetadataAndTampering(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		change func(string)
		want   string
	}{
		{"valid", func(string) {}, ""},
		{"missing root sums", func(d string) { _ = os.Remove(filepath.Join(d, "SHA256SUMS")) }, "SHA256SUMS"},
		{"missing archive sums", func(d string) { _ = os.Remove(filepath.Join(d, "images/SHA256SUMS")) }, "SHA256SUMS"},
		{"wrong tar", func(d string) {
			f, e := os.OpenFile(filepath.Join(d, "images/agent.tar"), os.O_APPEND|os.O_WRONLY, 0)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = f.Write([]byte{0})
			_ = f.Close()
		}, "checksum"},
		{"missing config id", func(d string) {
			file := filepath.Join(d, "images/manifest.json")
			raw, _ := os.ReadFile(file)
			raw = bytes.ReplaceAll(raw, []byte(`"config_id"`), []byte(`"old_config_id"`))
			_ = os.WriteFile(file, raw, 0644)
			imageTestRootSums(t, d)
		}, "config_id"},
		{"wrong config id", func(d string) {
			file := filepath.Join(d, "images/manifest.json")
			raw, _ := os.ReadFile(file)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			m["images"].(map[string]any)["agent"].(map[string]any)["config_id"] = imageTestDigest([]byte("wrong"))
			raw, _ = json.Marshal(m)
			_ = os.WriteFile(file, raw, 0644)
			imageTestRootSums(t, d)
		}, "config_id mismatch"},
		{"duplicate root sum", func(d string) {
			file := filepath.Join(d, "SHA256SUMS")
			raw, _ := os.ReadFile(file)
			_ = os.WriteFile(file, append(raw, raw...), 0644)
		}, "duplicate"},
		{"unknown sum path", func(d string) {
			file := filepath.Join(d, "images/SHA256SUMS")
			raw, _ := os.ReadFile(file)
			raw = bytes.ReplaceAll(raw, []byte("./agent.tar"), []byte("../agent.tar"))
			_ = os.WriteFile(file, raw, 0644)
		}, "checksum path"},
		{"missing tar", func(d string) { _ = os.Remove(filepath.Join(d, "images/agent.tar")) }, "agent.tar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := imageTestBundle(t)
			tc.change(dir)
			_, err := verifyImageBundle(ctx, dir, defaultImageLimits)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestImageJSONBudgetsAndDuplicateNestedKeys(t *testing.T) {
	for _, raw := range []string{`{"x":{"a":1,"a":2}}`, `{"x":1} {"y":2}`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66)} {
		var v any
		if err := imageJSON([]byte(raw), &v); err == nil {
			t.Fatalf("ambiguous JSON accepted: %s", raw)
		}
	}
	// Dynamic Docker label maps are case-sensitive. Only typed schema aliases
	// are ambiguous to encoding/json; ordinary A/a labels remain distinct.
	var record struct {
		Labels map[string]string `json:"labels"`
	}
	if err := imageJSON([]byte(`{"labels":{"A":"one","a":"two"}}`), &record); err != nil || len(record.Labels) != 2 {
		t.Fatalf("distinct labels rejected: %v", err)
	}
}

func TestImageInputPathsRejectLinksAndDotSegmentCleaning(t *testing.T) {
	dir := imageTestBundle(t)
	for _, path := range []string{filepath.Join(dir, "images") + string(os.PathSeparator) + "..", dir + string(os.PathSeparator) + "."} {
		if _, err := verifyImageBundle(context.Background(), path, defaultImageLimits); err == nil || !strings.Contains(err.Error(), "unsafe dot segments") {
			t.Fatalf("unsafe source directory accepted: %v", err)
		}
	}
	link := filepath.Join(t.TempDir(), "linked-bundle")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := verifyImageBundle(context.Background(), link, defaultImageLimits); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("linked source directory accepted: %v", err)
	}
	file := filepath.Join(dir, "images", "agent.tar")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "images", "edge.tar"), file); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyImageBundle(context.Background(), dir, defaultImageLimits); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("linked source tar accepted: %v", err)
	}
}

func TestImageCommandDoesNotRequireInstallationInputs(t *testing.T) {
	dir := imageTestBundle(t)
	t.Setenv("AURIGA_DATA_ROOT", filepath.Join(dir, "must-not-create"))
	for _, args := range [][]string{{"image-check", dir}, {"image-metadata", filepath.Join(dir, "images")}} {
		var out bytes.Buffer
		if err := imageCommand(context.Background(), args, &out); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(os.Getenv("AURIGA_DATA_ROOT")); !os.IsNotExist(err) {
			t.Fatalf("data root created: %v", err)
		}
	}
	var out bytes.Buffer
	if err := imageCommand(context.Background(), []string{"image-resolve", t.TempDir()}, &out); err == nil || out.Len() != 0 {
		t.Fatalf("bad source should fail before docker/output: %v %s", err, out.String())
	}
}

func TestImageDockerSubprocessUsesDeadlineAndClosesFailedExport(t *testing.T) {
	// A shell fixture is only a subprocess unit test, never real Engine evidence.
	if os.PathSeparator == '\\' {
		t.Skip("POSIX executable fixture")
	}
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\nprintf 'partial'\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := exportDockerImage(ctx, imageTestDigest([]byte("id")), func(io.Reader) error { return errors.New("reject immediately") })
	if err == nil || !strings.Contains(err.Error(), "reject immediately") || time.Since(start) > 3*time.Second {
		t.Fatalf("export process not cancelled/reaped: %v", err)
	}
	ctx, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	start = time.Now()
	if _, err := inspectDockerImage(ctx, "auriga/platform:nas"); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("inspect deadline not enforced: %v", err)
	}
}
