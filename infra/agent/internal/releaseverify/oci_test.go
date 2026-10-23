package releaseverify

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/releasetrust"
)

func jsonBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The tags below are pinned to Moby v27.0.3 Image/V1Image, RootFS,
// container.Config and its vendored OCI History (not a live daemon test).
// https://github.com/moby/moby/blob/v27.0.3/image/image.go
type moby2703AliasView struct {
	OS           string   `json:"os"`
	Architecture string   `json:"architecture"`
	Variant      string   `json:"variant"`
	OSFeatures   []string `json:"os.features"`
	Config       struct {
		WorkingDir string
		Hostname   string
	} `json:"config"`
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids,omitempty"`
	} `json:"rootfs,omitempty"`
	History []struct {
		EmptyLayer bool `json:"empty_layer,omitempty"`
	} `json:"history,omitempty"`
}

func TestMoby2703StructuralAliasInterpretations(t *testing.T) {
	const base = `{"os":"linux","architecture":"amd64","config":{},"rootfs":{"type":"layers","diff_ids":[]}}`
	for _, tc := range []struct {
		name, raw string
		changed   func(moby2703AliasView) bool
	}{
		{"ASCII OS", strings.TrimSuffix(base, "}") + `,"OS":"windows"}`, func(v moby2703AliasView) bool { return v.OS == "windows" }},
		{"Unicode long s", strings.TrimSuffix(base, "}") + `,"o\u017f":"windows"}`, func(v moby2703AliasView) bool { return v.OS == "windows" }},
		{"architecture", strings.TrimSuffix(base, "}") + `,"Architecture":"arm64"}`, func(v moby2703AliasView) bool { return v.Architecture == "arm64" }},
		{"variant alone", strings.TrimSuffix(base, "}") + `,"Variant":"v8"}`, func(v moby2703AliasView) bool { return v.Variant == "v8" }},
		{"features alone", strings.TrimSuffix(base, "}") + `,"OS.Features":["win32k"]}`, func(v moby2703AliasView) bool { return len(v.OSFeatures) == 1 }},
		{"config", strings.TrimSuffix(base, "}") + `,"Config":{"Hostname":"different"}}`, func(v moby2703AliasView) bool { return v.Config.Hostname == "different" }},
		{"rootfs", strings.TrimSuffix(base, "}") + `,"RootFS":{"type":"different"}}`, func(v moby2703AliasView) bool { return v.RootFS.Type == "different" }},
		{"history", strings.TrimSuffix(base, "}") + `,"history":[{"empty_layer":true,"Empty_Layer":false}]}`, func(v moby2703AliasView) bool { return len(v.History) == 1 && !v.History[0].EmptyLayer }},
		{"Kelvin execution", strings.Replace(base, `"config":{}`, `"config":{"WorkingDir":"/safe","Wor\u212aingDir":"/changed"}`, 1), func(v moby2703AliasView) bool { return v.Config.WorkingDir == "/changed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var view moby2703AliasView
			if err := json.Unmarshal([]byte(tc.raw), &view); err != nil {
				t.Fatal(err)
			}
			if !tc.changed(view) {
				t.Fatalf("pinned Go struct did not interpret alias: %+v", view)
			}
			m, _ := imageFixture(t, false)
			m["layers"] = []any{}
			rawC := []byte(tc.raw)
			m["config"].(map[string]any)["digest"] = digest(rawC)
			m["config"].(map[string]any)["size"] = len(rawC)
			_, err := verifyRawImage(t, jsonBytes(t, m), rawC)
			rejected(t, err)
		})
	}
}

func TestStructuralMemberAliasesByContext(t *testing.T) {
	// Tag spelling matches the pinned Moby/OCI types. Reserved artifact/index
	// names are included so their aliases cannot become ignored extensions.
	manifestKeys := []string{"schemaVersion", "mediaType", "config", "layers", "annotations", "artifactType", "subject", "manifests"}
	imageKeys := []string{"created", "author", "architecture", "os", "os.version", "os.features", "variant", "config", "rootfs", "history", "id", "parent", "comment", "container", "container_config", "docker_version", "Size", "artifactType", "subject", "manifests", "mediaType"}
	containerKeys := []string{"Hostname", "Domainname", "User", "AttachStdin", "AttachStdout", "AttachStderr", "ExposedPorts", "Tty", "OpenStdin", "StdinOnce", "Env", "Cmd", "Healthcheck", "ArgsEscaped", "Image", "Volumes", "WorkingDir", "Entrypoint", "NetworkDisabled", "MacAddress", "OnBuild", "Labels", "StopSignal", "StopTimeout", "Shell"}
	healthKeys := []string{"Test", "Interval", "Timeout", "StartPeriod", "StartInterval", "Retries"}
	for _, context := range []struct {
		name         string
		keys         []string
		selectObject func(map[string]any, map[string]any) map[string]any
	}{
		{"manifest", manifestKeys, func(m, c map[string]any) map[string]any { return m }},
		{"image", imageKeys, func(m, c map[string]any) map[string]any { return c }},
		{"history", []string{"created", "created_by", "author", "comment", "empty_layer"}, func(m, c map[string]any) map[string]any { return c["history"].([]any)[0].(map[string]any) }},
		{"rootfs", []string{"type", "diff_ids"}, func(m, c map[string]any) map[string]any { return c["rootfs"].(map[string]any) }},
		{"config descriptor", []string{"mediaType", "digest", "size", "annotations"}, func(m, c map[string]any) map[string]any { return m["config"].(map[string]any) }},
		{"layer descriptor", []string{"mediaType", "digest", "size", "annotations"}, func(m, c map[string]any) map[string]any { return m["layers"].([]any)[0].(map[string]any) }},
		{"execution", containerKeys, func(m, c map[string]any) map[string]any { return c["config"].(map[string]any) }},
		{"healthcheck", healthKeys, func(m, c map[string]any) map[string]any {
			h := map[string]any{}
			c["config"].(map[string]any)["Healthcheck"] = h
			return h
		}},
		{"legacy container", containerKeys, func(m, c map[string]any) map[string]any {
			old := map[string]any{}
			c["container_config"] = old
			return old
		}},
		{"legacy healthcheck", healthKeys, func(m, c map[string]any) map[string]any {
			h := map[string]any{}
			c["container_config"] = map[string]any{"Healthcheck": h}
			return h
		}},
	} {
		t.Run(context.name, func(t *testing.T) {
			for _, key := range context.keys {
				aliases := map[string]bool{}
				for _, alias := range []string{strings.ToUpper(key), strings.ToLower(key), strings.Map(func(r rune) rune {
					switch r {
					case 's', 'S':
						return 'ſ'
					case 'k', 'K':
						return 'K'
					}
					return r
				}, key)} {
					if alias != key {
						aliases[alias] = true
					}
				}
				for alias := range aliases {
					for _, alone := range []bool{false, true} {
						mode := "alongside"
						if alone {
							mode = "alone"
						}
						t.Run(key+"/"+alias+"/"+mode, func(t *testing.T) {
							// Verify actual standard-library struct-field folding for each tag,
							// independently of the verifier's EqualFold implementation. RawMessage
							// preserves the test value; matching depends on the real JSON tag.
							typ := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[json.RawMessage](), Tag: reflect.StructTag(`json:"` + key + `"`)}})
							view := reflect.New(typ)
							rawAlias := jsonBytes(t, map[string]any{alias: "folded"})
							if err := json.Unmarshal(rawAlias, view.Interface()); err != nil {
								t.Fatal(err)
							}
							if string(view.Elem().Field(0).Bytes()) != `"folded"` {
								t.Fatalf("tag %q did not match %q", key, alias)
							}
							m, c := imageFixture(t, false)
							descriptor := m["config"].(map[string]any)
							members := context.selectObject(m, c)
							if alone {
								delete(members, key)
							} else if _, exists := members[key]; !exists {
								members[key] = "canonical"
							}
							members[alias] = "folded"
							rawC := jsonBytes(t, c)
							if context.name != "config descriptor" {
								descriptor["digest"] = digest(rawC)
								descriptor["size"] = len(rawC)
							}
							_, err := verifyRawImage(t, jsonBytes(t, m), rawC)
							rejected(t, err)
						})
					}
				}
			}
		})
	}
}

func TestSemanticMapCaseAndMetadataRemainDistinct(t *testing.T) {
	for _, docker := range []bool{false, true} {
		m, c := imageFixture(t, docker)
		distinct := map[string]string{"x": "one", "X": "two", "os": "linux", "OS": "windows", "oſ": "other", "k": "one", "K": "two"}
		m["annotations"] = distinct
		m["config"].(map[string]any)["annotations"] = distinct
		m["layers"].([]any)[0].(map[string]any)["annotations"] = distinct
		exec := c["config"].(map[string]any)
		exec["Labels"] = distinct
		exec["ExposedPorts"] = map[string]any{"x": map[string]any{}, "X": map[string]any{}}
		exec["Volumes"] = map[string]any{"/x": map[string]any{}, "/X": map[string]any{}}
		c["container_config"] = map[string]any{"Labels": distinct, "Healthcheck": map[string]any{"Test": []string{"NONE"}}}
		c["Size"] = 123
		c["docker_version"] = "example"
		c["x-extension"] = map[string]any{"config": "metadata", "Config": "different metadata"}
		c["history"].([]any)[0].(map[string]any)["x-extension"] = distinct
		m["x-extension"] = map[string]any{"os": "metadata", "OS": "different metadata"}
		rawC := jsonBytes(t, c)
		m["config"].(map[string]any)["digest"] = digest(rawC)
		m["config"].(map[string]any)["size"] = len(rawC)
		if _, err := verifyRawImage(t, jsonBytes(t, m), rawC); err != nil {
			t.Fatal(err)
		}
	}
}
func imageFixture(t *testing.T, docker bool) (map[string]any, map[string]any) {
	t.Helper()
	mt, cm, layer := ociManifest, ociConfig, ociTar+"+gzip"
	if docker {
		mt, cm, layer = dockerManifest, dockerConfig, dockerGzip
	}
	config := map[string]any{"os": "linux", "architecture": "amd64", "config": map[string]any{"Env": []string{"PATH=/usr/bin"}, "Entrypoint": []string{"/bin/dsh"}, "Labels": map[string]string{"example": "<>&😀"}}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + strings.Repeat("c", 64)}}, "history": []any{map[string]any{"created_by": "ADD rootfs"}, map[string]any{"created_by": "ENV PATH", "empty_layer": true}}}
	manifest := map[string]any{"schemaVersion": 2, "mediaType": mt, "config": map[string]any{"mediaType": cm, "digest": digest(jsonBytes(t, config)), "size": len(jsonBytes(t, config))}, "layers": []any{map[string]any{"mediaType": layer, "digest": "sha256:" + strings.Repeat("d", 64), "size": 123}}, "annotations": map[string]string{"org.opencontainers.image.title": "dsh"}}
	return manifest, config
}
func verifyRawImage(t *testing.T, m, c []byte) (VerifiedImage, error) {
	t.Helper()
	d, ring, key, target := fixture(t)
	d.ReleaseEnvelope.ImageDigest = digest(m)
	d.Image = "registry.example.com/auriga/dsh@" + d.ReleaseEnvelope.ImageDigest
	sign(t, d, key)
	v, err := Verify(d, ring, target)
	if err != nil {
		t.Fatal(err)
	}
	return VerifyOCI(v, ring, m, c)
}
func TestValidOCIAndDockerEvidence(t *testing.T) {
	for _, docker := range []bool{false, true} {
		name := "oci"
		if docker {
			name = "docker"
		}
		t.Run(name, func(t *testing.T) {
			m, c := imageFixture(t, docker)
			m["x-build-metadata"] = map[string]any{"producer": "example"}
			c["docker_version"] = "example"
			c["history"].([]any)[0].(map[string]any)["x-build-metadata"] = "example"
			m["config"].(map[string]any)["digest"] = digest(jsonBytes(t, c))
			m["config"].(map[string]any)["size"] = len(jsonBytes(t, c))
			rawC := jsonBytes(t, c)
			rawM := jsonBytes(t, m)
			v, err := verifyRawImage(t, rawM, rawC)
			if err != nil {
				t.Fatal(err)
			}
			if !v.Valid() || v.ConfigID() != digest(rawC) || v.ManifestDigest() != digest(rawM) || v.ManifestDigest() == v.ConfigID() || v.BodyDigest() == "" || v.LayerCount() != 1 || v.Partition().OS != "linux" {
				t.Fatal("bad verified evidence")
			}
			// Digest covers original bytes, including insignificant JSON whitespace.
			rawM = append(rawM, '\n')
			v, err = verifyRawImage(t, rawM, rawC)
			if err != nil || v.ManifestDigest() != digest(rawM) {
				t.Fatalf("raw manifest bytes: %v", err)
			}
		})
	}
	m, c := imageFixture(t, false)
	m["layers"].([]any)[0].(map[string]any)["mediaType"] = ociTar
	m["layers"].([]any)[0].(map[string]any)["digest"] = "sha256:" + strings.Repeat("c", 64)
	if _, err := verifyRawImage(t, jsonBytes(t, m), jsonBytes(t, c)); err != nil {
		t.Fatal(err)
	}
	m, c = imageFixture(t, false)
	m["layers"].([]any)[0].(map[string]any)["mediaType"] = ociTar + "+zstd"
	if _, err := verifyRawImage(t, jsonBytes(t, m), jsonBytes(t, c)); err != nil {
		t.Fatal(err)
	}
}

func TestOCIRawEvidenceRejections(t *testing.T) {
	manifestCases := map[string]func(map[string]any){
		"schema1":            func(m map[string]any) { m["schemaVersion"] = 1 },
		"schema exponent":    func(m map[string]any) { m["schemaVersion"] = json.Number("2e0") },
		"index":              func(m map[string]any) { m["mediaType"] = "application/vnd.oci.image.index.v1+json" },
		"manifest list":      func(m map[string]any) { m["mediaType"] = "application/vnd.docker.distribution.manifest.list.v2+json" },
		"artifact":           func(m map[string]any) { m["artifactType"] = "application/example" },
		"artifact subject":   func(m map[string]any) { m["subject"] = map[string]any{} },
		"unknown manifest":   func(m map[string]any) { m["mediaType"] = "application/octet-stream" },
		"config as manifest": func(m map[string]any) { m["mediaType"] = ociConfig },
		"empty config type": func(m map[string]any) {
			m["config"].(map[string]any)["mediaType"] = "application/vnd.oci.empty.v1+json"
		},
		"wrong config type":   func(m map[string]any) { m["config"].(map[string]any)["mediaType"] = dockerConfig },
		"wrong config digest": func(m map[string]any) { m["config"].(map[string]any)["digest"] = "sha256:" + strings.Repeat("e", 64) },
		"wrong config size":   func(m map[string]any) { m["config"].(map[string]any)["size"] = 1 },
		"null config":         func(m map[string]any) { m["config"] = nil },
		"descriptor URLs": func(m map[string]any) {
			m["config"].(map[string]any)["urls"] = []string{"https://download.example.com/blob"}
		},
		"descriptor data":     func(m map[string]any) { m["config"].(map[string]any)["data"] = "e30=" },
		"negative size":       func(m map[string]any) { m["config"].(map[string]any)["size"] = -1 },
		"zero size":           func(m map[string]any) { m["config"].(map[string]any)["size"] = 0 },
		"unsafe size":         func(m map[string]any) { m["config"].(map[string]any)["size"] = maxSafeInteger + 1 },
		"unknown compression": func(m map[string]any) { m["layers"].([]any)[0].(map[string]any)["mediaType"] = ociTar + "+brotli" },
		"foreign layer": func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["mediaType"] = "application/vnd.docker.image.rootfs.foreign.diff.tar.gzip"
		},
		"unknown layer": func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["mediaType"] = "application/octet-stream"
		},
		"upper digest": func(m map[string]any) {
			m["layers"].([]any)[0].(map[string]any)["digest"] = "sha256:" + strings.Repeat("D", 64)
		},
		"null layers":            func(m map[string]any) { m["layers"] = nil },
		"null layer":             func(m map[string]any) { m["layers"] = []any{nil} },
		"uncompressed mismatch":  func(m map[string]any) { m["layers"].([]any)[0].(map[string]any)["mediaType"] = ociTar },
		"bad annotations":        func(m map[string]any) { m["annotations"] = map[string]any{"bad": 1} },
		"descriptor annotations": func(m map[string]any) { m["layers"].([]any)[0].(map[string]any)["annotations"] = false },
	}
	for name, mutate := range manifestCases {
		t.Run(name, func(t *testing.T) {
			m, c := imageFixture(t, false)
			mutate(m)
			_, err := verifyRawImage(t, jsonBytes(t, m), jsonBytes(t, c))
			rejected(t, err)
		})
	}
	configCases := map[string]func(map[string]any){
		"wrong OS":              func(c map[string]any) { c["os"] = "windows" },
		"wrong arch":            func(c map[string]any) { c["architecture"] = "arm64" },
		"unknown config format": func(c map[string]any) { c["mediaType"] = "application/example" },
		"no rootfs":             func(c map[string]any) { delete(c, "rootfs") },
		"artifact rootfs":       func(c map[string]any) { c["rootfs"].(map[string]any)["type"] = "artifact" },
		"no diffids":            func(c map[string]any) { delete(c["rootfs"].(map[string]any), "diff_ids") },
		"diffid mismatch":       func(c map[string]any) { c["rootfs"].(map[string]any)["diff_ids"] = []any{} },
		"bad diffid":            func(c map[string]any) { c["rootfs"].(map[string]any)["diff_ids"] = []string{"sha512:123"} },
		"null execution":        func(c map[string]any) { c["config"] = nil },
		"unknown executable":    func(c map[string]any) { c["config"].(map[string]any)["NewExecMode"] = "arbitrary" },
		"wrong entrypoint":      func(c map[string]any) { c["config"].(map[string]any)["Entrypoint"] = "/bin/dsh" },
		"history count":         func(c map[string]any) { c["history"] = []any{} },
		"history type":          func(c map[string]any) { c["history"] = false },
		"empty layer string":    func(c map[string]any) { c["history"].([]any)[0].(map[string]any)["empty_layer"] = "true" },
		"variant":               func(c map[string]any) { c["variant"] = "v8" },
		"OS features":           func(c map[string]any) { c["os.features"] = []string{"win32k"} },
	}
	for name, mutate := range configCases {
		t.Run(name, func(t *testing.T) {
			m, c := imageFixture(t, false)
			mutate(c)
			rawC := jsonBytes(t, c)
			m["config"].(map[string]any)["digest"] = digest(rawC)
			m["config"].(map[string]any)["size"] = len(rawC)
			_, err := verifyRawImage(t, jsonBytes(t, m), rawC)
			rejected(t, err)
		})
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"trailing object": func(b []byte) []byte { return append(b, []byte(`{}`)...) },
		"duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersion":2`), 1)
		},
		"escaped duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersi\u006fn":2`), 1)
		},
		"invalid UTF8": func(b []byte) []byte { return bytes.Replace(b, []byte(`"dsh"`), []byte{'"', 0xff, '"'}, 1) },
		"surrogate":    func(b []byte) []byte { return bytes.Replace(b, []byte(`"dsh"`), []byte(`"\ud800"`), 1) },
	} {
		t.Run(name, func(t *testing.T) {
			m, c := imageFixture(t, false)
			_, err := verifyRawImage(t, mutate(jsonBytes(t, m)), jsonBytes(t, c))
			rejected(t, err)
		})
	}
}

func TestOCIOriginalBytesAndCurrentTrust(t *testing.T) {
	m, c := imageFixture(t, false)
	rawM, rawC := jsonBytes(t, m), jsonBytes(t, c)
	d, ring, key, target := fixture(t)
	d.ReleaseEnvelope.ImageDigest = digest(rawM)
	d.Image = "registry.example.com/auriga/dsh@" + d.ReleaseEnvelope.ImageDigest
	sign(t, d, key)
	release, err := Verify(d, ring, target)
	if err != nil {
		t.Fatal(err)
	}
	v, err := VerifyOCI(release, ring, rawM, rawC)
	if err != nil {
		t.Fatal(err)
	}
	_, err = VerifyOCI(release, ring, append(bytes.Clone(rawM), ' '), rawC)
	rejected(t, err)
	_, err = VerifyOCI(release, ring, rawM, append(bytes.Clone(rawC), ' '))
	rejected(t, err)
	_, err = VerifyOCI(release, ring, rawM, []byte(`{}`))
	rejected(t, err)
	_, err = VerifyOCI(release, ring, rawC, rawC)
	rejected(t, err)
	_, err = VerifyOCI(VerifiedRelease{}, ring, rawM, rawC)
	rejected(t, err)
	expired := ring.Clone()
	expired.Records[0].Deadline = releasetrust.FromTime(time.Now().Add(-time.Second))
	_, err = VerifyOCI(release, expired, rawM, rawC)
	rejected(t, err)
	rejected(t, v.Recheck(expired))
	rejected(t, (VerifiedImage{}).Recheck(ring))
	// Even a correctly signed config digest cannot stand in for a manifest digest.
	d.ReleaseEnvelope.ImageDigest = digest(rawC)
	d.Image = "registry.example.com/auriga/dsh@" + d.ReleaseEnvelope.ImageDigest
	sign(t, d, key)
	release, err = Verify(d, ring, target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = VerifyOCI(release, ring, rawC, rawC)
	rejected(t, err)
	// Changed inputs do not mutate an already checked config ID.
	before := v.ConfigID()
	rawM[0] = 'x'
	rawC[0] = 'x'
	if v.ConfigID() != before {
		t.Fatal("evidence was mutable")
	}
}

func TestStrictJSONBoundariesAndAmbiguity(t *testing.T) {
	for _, raw := range []string{`{"x":"\ud800"}`, `{"x":"\udc00"}`, `{"x":"\ud800\u0041"}`, `{"x":"\ud800\ud800"}`, `{"x":1,"x":2}`, `{"a":{"x":1,"\u0078":2}}`, `{}null`, `[]`, `null`, `{"x":NaN}`, `{"x":"\u00"}`, `{"x":1,}`} {
		if _, err := strictJSON([]byte(raw), MaxManifestBytes); err == nil {
			t.Fatalf("bad JSON accepted: %s", raw)
		}
	}
	for _, raw := range []string{`{"x":"\ud83d\ude00"}`, `{"x":"\\ud800"}`, `{"x":"\ufffd"}`} {
		if _, err := strictJSON([]byte(raw), MaxManifestBytes); err != nil {
			t.Fatalf("valid Unicode %s: %v", raw, err)
		}
	}
	for _, limit := range []int{MaxManifestBytes, MaxConfigBytes} {
		body := append([]byte(`{}`), bytes.Repeat([]byte{' '}, limit-2)...)
		if _, err := strictJSON(body, limit); err != nil {
			t.Fatal(err)
		}
		if _, err := strictJSON(append(body, ' '), limit); err == nil {
			t.Fatal("size limit exceeded")
		}
	}
	for _, depth := range []int{MaxJSONDepth, MaxJSONDepth + 1} {
		raw := []byte(`{"x":` + strings.Repeat("[", depth-1) + "0" + strings.Repeat("]", depth-1) + "}")
		_, err := strictJSON(raw, MaxManifestBytes)
		if (err == nil) != (depth == MaxJSONDepth) {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
}

func TestOCIConfigAmbiguityAndEvidenceLimits(t *testing.T) {
	for name, mutate := range map[string]func([]byte) []byte{
		"duplicate platform": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"os":"linux"`), []byte(`"os":"linux","os":"linux"`), 1)
		},
		"nested executable duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"Entrypoint":["/bin/dsh"]`), []byte(`"Entrypoint":["/bin/dsh"],"Entrypoint":["/bin/other"]`), 1)
		},
		"trailing config":       func(b []byte) []byte { return append(b, []byte(` {}`)...) },
		"config invalid UTF8":   func(b []byte) []byte { return append(b, 0xff) },
		"config lone surrogate": func(b []byte) []byte { return bytes.Replace(b, []byte(`"linux"`), []byte(`"\ud800"`), 1) },
	} {
		t.Run(name, func(t *testing.T) {
			m, c := imageFixture(t, false)
			rawC := mutate(jsonBytes(t, c))
			m["config"].(map[string]any)["digest"] = digest(rawC)
			m["config"].(map[string]any)["size"] = len(rawC)
			_, err := verifyRawImage(t, jsonBytes(t, m), rawC)
			rejected(t, err)
		})
	}
	m, c := imageFixture(t, false)
	rawC := jsonBytes(t, c)
	rawC = append(rawC, bytes.Repeat([]byte{' '}, MaxConfigBytes-len(rawC))...)
	m["config"].(map[string]any)["digest"] = digest(rawC)
	m["config"].(map[string]any)["size"] = len(rawC)
	rawM := jsonBytes(t, m)
	rawM = append(rawM, bytes.Repeat([]byte{' '}, MaxManifestBytes-len(rawM))...)
	if _, err := verifyRawImage(t, rawM, rawC); err != nil {
		t.Fatal(err)
	}
	_, err := verifyRawImage(t, append(bytes.Clone(rawM), ' '), rawC)
	rejected(t, err)
	_, err = verifyRawImage(t, rawM, append(bytes.Clone(rawC), ' '))
	rejected(t, err)
	for _, depth := range []int{MaxJSONDepth, MaxJSONDepth + 1} {
		for _, isConfig := range []bool{false, true} {
			m, c := imageFixture(t, false)
			var nested any = 0
			for i := 1; i < depth; i++ {
				nested = []any{nested}
			}
			if isConfig {
				c["x-metadata"] = nested
				m["config"].(map[string]any)["digest"] = digest(jsonBytes(t, c))
				m["config"].(map[string]any)["size"] = len(jsonBytes(t, c))
			} else {
				m["x-metadata"] = nested
			}
			_, err := verifyRawImage(t, jsonBytes(t, m), jsonBytes(t, c))
			if depth == MaxJSONDepth {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				rejected(t, err)
			}
		}
	}
}
