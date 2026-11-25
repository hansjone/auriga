package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func nasRotationVersionFixture(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path != "/version" {
		return false
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"ApiVersion": "1.41", "MinAPIVersion": "1.24"})
	return true
}

func TestNASRotationEngineAPINegotiation(t *testing.T) {
	for _, tc := range []struct{ name, maximum, minimum, want string }{
		{"old", "1.41", "1.24", "1.41"},
		{"modern", "1.52", "1.44", "1.52"},
		{"bounded-future", "1.99", "1.44", "1.52"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			versions, calls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/version" {
					versions++
					_ = json.NewEncoder(w).Encode(map[string]string{"ApiVersion": tc.maximum, "MinAPIVersion": tc.minimum})
					return
				}
				calls++
				if req.URL.Path != "/v"+tc.want+"/containers/json" {
					t.Errorf("unnegotiated request: %s", req.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(w, "[]")
			}))
			defer server.Close()
			e := &nasRotationEngine{client: &http.Client{Transport: roundTripNASRotation{target: server.URL, base: server.Client().Transport}}}
			for range 2 {
				if _, err := e.containers(); err != nil {
					t.Fatal(err)
				}
			}
			if versions != 1 || calls != 2 {
				t.Fatalf("negotiation not cached: version=%d calls=%d", versions, calls)
			}
		})
	}
}

func TestNASRotationEngineAPIRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"missing", `{}`, 200},
		{"missing-min", `{"ApiVersion":"1.52"}`, 200},
		{"malformed-json", `{`, 200},
		{"trailing-json", `{"ApiVersion":"1.52","MinAPIVersion":"1.44"}{}`, 200},
		{"wrong-type", `{"ApiVersion":1.52,"MinAPIVersion":"1.44"}`, 200},
		{"unsupported-major", `{"ApiVersion":"2.52","MinAPIVersion":"1.44"}`, 200},
		{"old", `{"ApiVersion":"1.40","MinAPIVersion":"1.24"}`, 200},
		{"new-min", `{"ApiVersion":"1.99","MinAPIVersion":"1.53"}`, 200},
		{"reversed", `{"ApiVersion":"1.44","MinAPIVersion":"1.52"}`, 200},
		{"noncanonical", `{"ApiVersion":"1.052","MinAPIVersion":"1.44"}`, 200},
		{"suffix", `{"ApiVersion":"1.52/containers","MinAPIVersion":"1.44"}`, 200},
		{"overflow", `{"ApiVersion":"1.999999999999999999999999","MinAPIVersion":"1.44"}`, 200},
		{"oversized", strings.Repeat(" ", 65537), 200},
		{"unavailable", `{"message":"fixture failure"}`, 500},
		{"redirect", "", 301},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/version" {
					calls++
					_, _ = io.WriteString(w, "[]")
					return
				}
				w.Header().Set("Location", "/unexpected")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			e := &nasRotationEngine{client: &http.Client{Transport: roundTripNASRotation{target: server.URL, base: server.Client().Transport}}}
			_, err := e.containers()
			if err == nil || !strings.Contains(err.Error(), "Engine API") || calls != 0 {
				t.Fatalf("invalid version did not fail closed: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestNASRotationImageErrorsPreserveEngineDiagnosis(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		versionStatus, imageStatus int
		imageBody, want            string
	}{
		{"missing-image", 200, 404, `{}`, "approved image missing locally"},
		{"protocol-error", 200, 400, `{}`, "Engine API rejected GET (status 400)"},
		{"version-error", 404, 200, `{}`, "Engine API negotiation failed"},
		{"invalid-image", 200, 200, `{}`, "approved image response or platform refused"},
		{"redirect-image", 200, 301, `{}`, "Engine API rejected GET (status 301)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/version" {
					w.WriteHeader(tc.versionStatus)
					_ = json.NewEncoder(w).Encode(map[string]string{"ApiVersion": "1.52", "MinAPIVersion": "1.44"})
					return
				}
				w.Header().Set("Location", "/version")
				w.WriteHeader(tc.imageStatus)
				_, _ = io.WriteString(w, tc.imageBody)
			}))
			defer server.Close()
			e := &nasRotationEngine{client: &http.Client{Transport: roundTripNASRotation{target: server.URL, base: server.Client().Transport}}}
			_, err := e.approvedImage("sha256:" + strings.Repeat("a", 64))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("lost image diagnosis: %v", err)
			}
			if tc.name != "missing-image" && strings.Contains(err.Error(), "missing locally") {
				t.Fatal("protocol error misreported as missing image")
			}
		})
	}
}

func TestNASRotationOfficialComposeCombination(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	nasEnableActivationFixture(t, f)
	r := f.r
	parent := filepath.Dir(r.data)
	for _, prefix := range []string{".auriga-compose.", ".auriga-runtime-compose."} {
		t.Run(prefix, func(t *testing.T) {
			dir := filepath.Join(parent, prefix+"ABC123")
			files := []string{
				filepath.Join(r.recipe, "docker-compose.yml"), filepath.Join(dir, "override.yml"),
				filepath.Join(r.recipe, "collab-compose.yml"), filepath.Join(dir, "runtime.yml"),
				filepath.Join(r.recipe, "shared-runtime-compose.yml"), filepath.Join(dir, "shared.yml"),
				filepath.Join(r.recipe, "oneclick-compose.yml"), filepath.Join(dir, "rotation.yml"),
				filepath.Join(r.recipe, "managed-identity-compose.yml"), filepath.Join(r.recipe, "resource-downgrade-compose.yml"),
			}
			for id, c := range f.containers {
				c.Config.Labels["com.docker.compose.project.config_files"] = strings.Join(files, ",")
				f.containers[id] = c
			}
			if _, err := r.engine.database(r); err != nil {
				t.Fatalf("official shared/oneclick PG rejected: %v", err)
			}
			if err := r.engine.checkRecoveryEdge(r); err != nil {
				t.Fatalf("official shared/oneclick recovery edge rejected: %v", err)
			}
			var c nasRotationContainer
			for _, item := range f.containers {
				if item.Config.Labels["com.docker.compose.service"] == "platform" {
					c = item
					break
				}
			}
			original := c.Config.Labels["com.docker.compose.project.config_files"]
			for _, bad := range []string{
				filepath.Join(parent, "foreign", "shared-runtime-compose.yml"),
				filepath.Join(r.recipe, "shared-runtime-compose.yml.bak"), filepath.Join(r.recipe, "oneclick-compose.yml.evil"),
				r.recipe + "/../recipe/oneclick-compose.yml", filepath.Join(dir, "shared.yml.bak"),
				filepath.Join(dir, "nested", "shared.yml"), dir + "/../foreign/shared.yml",
				filepath.Join(parent, ".auriga-compose.", "shared.yml"), filepath.Join(parent, "foreign", prefix+"ABC123", "shared.yml"),
				filepath.Join(dir, "arbitrary.yml"), dir + "/./shared.yml",
			} {
				c.Config.Labels["com.docker.compose.project.config_files"] = original + "," + bad
				err := r.engine.owned(r, c)
				if err == nil || !strings.Contains(err.Error(), "foreign Compose file refused") || !strings.Contains(err.Error(), bad) {
					t.Fatalf("foreign file not diagnosed: file=%q err=%v", bad, err)
				}
			}
			c.Config.Labels["com.docker.compose.project.config_files"] = original
			c.Image = "sha256:" + strings.Repeat("0", 64)
			if err := r.engine.owned(r, c); err == nil {
				t.Fatal("foreign image accepted")
			}
			c.Image = r.binding.Image
			c.Config.Labels["com.docker.compose.project"] = "foreign"
			if err := r.engine.owned(r, c); err == nil {
				t.Fatal("foreign project accepted")
			}
			c.Config.Labels["com.docker.compose.project"] = nasRotationProject
			if err := r.engine.stop(r); err != nil {
				t.Fatalf("official combined rotation stop: %v", err)
			}
		})
	}
}
