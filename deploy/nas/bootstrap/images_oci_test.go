package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func imageTestModernArchive(t *testing.T, problem string) []byte {
	t.Helper()
	return imageTestArchive(t, "auriga/platform:nas", true, func(f map[string][]byte) {
		encode := func(value any) []byte {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}
		blob := func(media string, raw []byte) imageDescriptor {
			digest := imageTestDigest(raw)
			f["blobs/sha256/"+strings.TrimPrefix(digest, "sha256:")] = raw
			return imageDescriptor{MediaType: media, Digest: digest, Size: int64(len(raw))}
		}
		config := blob("application/vnd.oci.image.config.v1+json", f["config.json"])
		layer := blob("application/vnd.oci.image.layer.v1.tar+gzip", f["layer.tar"])
		attConfig := blob("application/vnd.oci.image.config.v1+json", []byte(`{"os":"unknown","architecture":"unknown"}`))
		attLayer := blob("application/vnd.in-toto+json", []byte(`{"predicate":"unit test only"}`))
		manifestConfig := config
		if problem == "wrong config" {
			manifestConfig = attConfig
		}
		manifestLayers := []imageDescriptor{layer}
		if problem == "wrong layer" {
			manifestLayers = []imageDescriptor{attLayer}
		}
		manifest := blob("application/vnd.oci.image.manifest.v1+json", encode(map[string]any{"schemaVersion": 2, "config": manifestConfig, "layers": manifestLayers}))
		platform := func(d imageDescriptor, os, arch string) imageDescriptor {
			raw := encode(map[string]any{"mediaType": d.MediaType, "digest": d.Digest, "size": d.Size, "platform": map[string]string{"os": os, "architecture": arch}})
			if err := json.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			return d
		}
		manifest = platform(manifest, "linux", "amd64")
		if problem == "wrong platform" {
			manifest = platform(manifest, "linux", "arm64")
		}
		att := blob("application/vnd.oci.image.manifest.v1+json", encode(map[string]any{"schemaVersion": 2, "config": attConfig, "layers": []imageDescriptor{attLayer}}))
		att = platform(att, "unknown", "unknown")
		att.Annotations = map[string]string{"vnd.docker.reference.type": "attestation-manifest", "vnd.docker.reference.digest": manifest.Digest}
		if problem == "wrong attestation binding" {
			att.Annotations["vnd.docker.reference.digest"] = imageTestDigest([]byte("other manifest"))
		}
		children := []imageDescriptor{manifest, att}
		if problem == "multiple runnable" {
			other := blob(manifest.MediaType, encode(map[string]any{"schemaVersion": 2, "config": config, "layers": []imageDescriptor{layer}, "annotations": map[string]string{"test": "second candidate"}}))
			children = append(children, platform(other, "linux", "amd64"))
		}
		index := blob("application/vnd.oci.image.index.v1+json", encode(map[string]any{"schemaVersion": 2, "manifests": children}))
		index.Annotations = map[string]string{"io.containerd.image.name": "docker.io/auriga/platform:nas"}
		if problem == "wrong tag" {
			index.Annotations["io.containerd.image.name"] = "docker.io/auriga/other:nas"
		}
		if problem == "wrong descriptor size" {
			index.Size++
		}
		f["index.json"] = encode(map[string]any{"schemaVersion": 2, "manifests": []imageDescriptor{index}})
		f["oci-layout"] = []byte(`{"imageLayoutVersion":"1.0.0"}`)
		f["manifest.json"] = encode([]map[string]any{{"Config": "blobs/sha256/" + strings.TrimPrefix(config.Digest, "sha256:"), "RepoTags": []string{"auriga/platform:nas"}, "Layers": []string{"blobs/sha256/" + strings.TrimPrefix(layer.Digest, "sha256:")}}})
		delete(f, "config.json")
		delete(f, "layer.tar")
		if problem == "wrong blob hash" {
			f["blobs/sha256/"+strings.TrimPrefix(layer.Digest, "sha256:")] = []byte("bad blob")
		}
		if problem == "missing layout" {
			delete(f, "oci-layout")
		}
	})
}

func TestImageOCIUniqueRunnableAndBoundAttestation(t *testing.T) {
	for _, tc := range []struct{ problem, want string }{
		{"", ""},
		{"wrong config", "manifest/config/layer binding"},
		{"wrong layer", "ordered layer binding"},
		{"wrong platform", "runnable OCI platform"},
		{"multiple runnable", "multiple runnable OCI"},
		{"wrong attestation binding", "attestation bound to another"},
		{"wrong tag", "OCI archive tag mismatch"},
		{"wrong descriptor size", "descriptor digest/size/blob"},
		{"wrong blob hash", "diffID mismatch"},
		{"missing layout", "incomplete OCI"},
	} {
		t.Run(fmt.Sprint("case-", tc.problem), func(t *testing.T) {
			p, err := readImageArchive(context.Background(), bytes.NewReader(imageTestModernArchive(t, tc.problem)), "auriga/platform:nas", defaultImageLimits)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want %q, got %v", tc.want, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(p.sourceIDs) != 3 || !p.sourceIDs[p.ConfigID] {
				t.Fatalf("source IDs did not retain C/M/I: %+v", p.sourceIDs)
			}
		})
	}
}

func TestImageRepeatedOrderedLayerIsValid(t *testing.T) {
	raw := imageTestArchive(t, "auriga/platform:nas", true, func(f map[string][]byte) {
		var cfg map[string]any
		if err := json.Unmarshal(f["config.json"], &cfg); err != nil {
			t.Fatal(err)
		}
		root := cfg["rootfs"].(map[string]any)
		diff := root["diff_ids"].([]any)[0]
		root["diff_ids"] = []any{diff, diff}
		var err error
		f["config.json"], err = json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		f["manifest.json"] = []byte(`[{"Config":"config.json","RepoTags":["auriga/platform:nas"],"Layers":["layer.tar","layer.tar"]}]`)
	})
	p, err := readImageArchive(context.Background(), bytes.NewReader(raw), "auriga/platform:nas", defaultImageLimits)
	if err != nil || len(p.diffIDs) != 2 || p.diffIDs[0] != p.diffIDs[1] {
		t.Fatalf("valid repeated layer rejected: %+v %v", p, err)
	}
}
