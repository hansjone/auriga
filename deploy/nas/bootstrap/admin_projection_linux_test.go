package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAdminResourceProjectionPreservesSealedSource(t *testing.T) {
	for _, configured := range []bool{false, true} {
		upstreams := map[string]string{"iam": "https://platform:8081", "org": "https://platform:8081"}
		want := upstreams["iam"]
		if configured {
			want = "https://resource:8089"
			upstreams["resource"] = want
		}
		raw, err := json.Marshal(map[string]any{"upstreams": upstreams, "litellmOrigin": "http://litellm:4000"})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "edge.json")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		projected, err := bifrostEdgeProjection(path)
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Upstreams map[string]string }
		if err := json.Unmarshal(projected, &result); err != nil {
			t.Fatal(err)
		}
		if result.Upstreams["resource"] != want || result.Upstreams["iam"] != upstreams["iam"] {
			t.Fatal("resource must reuse the sealed IAM hop or preserve its explicit origin")
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, after) {
			t.Fatal("sealed source changed")
		}
	}
}

func TestAdminResourceProjectionRejectsMissingHop(t *testing.T) {
	for _, raw := range []string{`{}`, `{"upstreams":[]}`, `{"upstreams":{"iam":""}}`} {
		path := filepath.Join(t.TempDir(), "edge.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := bifrostEdgeProjection(path); err == nil {
			t.Fatal("projection accepted an absent protected hop")
		}
	}
}
