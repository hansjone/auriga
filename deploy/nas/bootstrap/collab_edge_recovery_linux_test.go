package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryEdgeOwnershipAndReadOnlyMount(t *testing.T) {
	r := &nasRotation{data: "/fixture/data", recipe: "/fixture/recipe"}
	c := nasRotationContainer{ID: strings.Repeat("a", 64), Name: "/auriga-nas-test-edge"}
	c.Config.Labels = map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.service": "edge", "com.docker.compose.project.working_dir": r.recipe, "com.docker.compose.project.config_files": filepath.Join(r.recipe, "docker-compose.yml") + "," + filepath.Join(r.recipe, "managed-identity-compose.yml")}
	c.Mounts = append(c.Mounts, struct {
		Source, Destination string
		RW                  bool
	}{filepath.Join(r.data, "config/materials/edge.config.json"), "/etc/auriga/edge.config.json", false})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			t.Error("ownership check mutated engine")
		}
		if strings.Contains(req.URL.Path, "/containers/json") {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": c.ID}})
		} else {
			_ = json.NewEncoder(w).Encode(c)
		}
	}))
	defer server.Close()
	e := &nasRotationEngine{client: &http.Client{Transport: roundTripRecovery(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})}}
	for _, mode := range []string{"base", "collab", "foreign", "external", "writable"} {
		t.Run(mode, func(t *testing.T) {
			c.Config.Labels["com.docker.compose.project"] = nasRotationProject
			c.Config.Labels["com.docker.compose.project.config_files"] = filepath.Join(r.recipe, "docker-compose.yml")
			c.Mounts[0].Source = filepath.Join(r.data, "config/materials/edge.config.json")
			c.Mounts[0].RW = false
			if mode == "collab" {
				c.Mounts[0].Source = filepath.Join(r.data, "config/collab-runtime/edge/edge.config.json")
			}
			if mode == "foreign" {
				c.Config.Labels["com.docker.compose.project"] = "foreign"
			}
			if mode == "external" {
				c.Config.Labels["com.docker.compose.project.config_files"] += ",/outside/managed-identity-compose.yml"
			}
			if mode == "writable" {
				c.Mounts[0].RW = true
			}
			err := e.checkRecoveryEdge(r)
			if (err == nil) != (mode == "base" || mode == "collab") {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
		})
	}
}

type roundTripRecovery func(*http.Request) (*http.Response, error)

func (f roundTripRecovery) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
