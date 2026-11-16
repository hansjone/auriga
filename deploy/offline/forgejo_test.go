package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureDistribution(t *testing.T, root string) distributionManifest {
	t.Helper()
	m := distributionManifest{Schema: 1, Tag: "1.0", Commit: strings.Repeat("a", 40), Locks: lockInventory{Digest: strings.Repeat("b", 64)}}
	for _, kind := range []string{"offline", "source", "devkit"} {
		name := "auriga-" + kind + "-1.0.tar"
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture tar "+kind), 0644); err != nil {
			t.Fatal(err)
		}
		a, err := describeAsset(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		m.Assets = append(m.Assets, a)
		m.Archives = append(m.Archives, a)
	}
	m.Gate = buildGate{Passed: true, Commit: m.Commit, LockDigest: m.Locks.Digest, SourceSHA: m.Archives[1].SHA256, DevkitSHA: m.Archives[2].SHA256, CatalogSHA: strings.Repeat("c", 64), OutputSHA: m.Archives[0].SHA256, Network: gateNetwork}
	return m
}

func TestForgejoUploadDraftRetryAndConflict(t *testing.T) {
	root := t.TempDir()
	key, public := testSigningKeys(t)
	manifest := fixtureDistribution(t, root)
	asset := manifest.Assets[0]
	if err := jsonFile(filepath.Join(root, "release-manifest.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if err := signSums(root, key); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	created := false
	draft := true
	mutations := 0
	requests := 0
	var base string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "token fixture-secret" {
			t.Error("missing scoped token")
		}
		endpoint := r.URL.Path
		switch {
		case endpoint == "/api/forgejo/v1/version":
			_, _ = io.WriteString(w, `{"version":"15.0.0"}`)
		case endpoint == "/api/v1/repos/owner/repo/tags/1.0":
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": manifest.Commit}})
		case strings.HasSuffix(endpoint, "/releases/tags/1.0"):
			if !created {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "draft": draft, "tag_name": "1.0"})
		case endpoint == "/api/v1/repos/owner/repo/releases" && r.Method == "POST":
			created = true
			mutations++
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "draft": true, "tag_name": "1.0"})
		case strings.HasSuffix(endpoint, "/assets") && r.Method == "GET":
			assets := []map[string]any{}
			if r.URL.Query().Get("page") == "1" {
				for name, body := range files {
					assets = append(assets, map[string]any{"name": name, "size": len(body), "browser_download_url": base + "/download/" + name})
				}
			}
			_ = json.NewEncoder(w).Encode(assets)
		case strings.HasSuffix(endpoint, "/assets") && r.Method == "POST":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if part.FormName() != "attachment" {
				t.Error("incorrect multipart field")
			}
			body, err := io.ReadAll(part)
			if err != nil {
				t.Error(err)
			}
			name := r.URL.Query().Get("name")
			files[name] = body
			mutations++
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "size": len(body), "browser_download_url": base + "/download/" + name})
		case strings.HasPrefix(endpoint, "/download/"):
			_, _ = w.Write(files[strings.TrimPrefix(endpoint, "/download/")])
		case strings.HasSuffix(endpoint, "/releases/7") && r.Method == "PATCH":
			draft = false
			mutations++
			_, _ = io.WriteString(w, `{"id":7,"draft":false,"tag_name":"1.0"}`)
		default:
			t.Error("unexpected route", r.Method, endpoint)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	base = server.URL
	t.Setenv("FORGEJO_TOKEN", "fixture-secret")
	opts := uploadOptions{Server: base, Repo: "owner/repo", Dir: root, Key: public}
	if err := uploadRelease(opts, server.Client()); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("dry-run contacted network")
	}
	opts.Execute = true
	if err := uploadRelease(opts, server.Client()); err != nil {
		t.Fatal(err)
	}
	first := mutations
	if !draft || len(files) != 6 {
		t.Fatal("incomplete draft", len(files))
	}
	if err := uploadRelease(opts, server.Client()); err != nil {
		t.Fatal(err)
	}
	if mutations != first {
		t.Fatal("retry duplicated attachment")
	}
	opts.Publish = true
	if err := uploadRelease(opts, server.Client()); err != nil {
		t.Fatal(err)
	}
	if draft {
		t.Fatal("draft not published")
	}
	files[asset.Name] = []byte("tampered remote")
	if err := uploadRelease(opts, server.Client()); err == nil {
		t.Fatal("accepted conflicting remote asset")
	}
	opts.Server = "https://api.github.com"
	opts.Execute = false
	if err := uploadRelease(opts, server.Client()); err == nil {
		t.Fatal("accepted GitHub publishing target")
	}
}

func TestForgejoRedirectAndWrongTagNeverWrite(t *testing.T) {
	root := t.TempDir()
	key, public := testSigningKeys(t)
	manifest := fixtureDistribution(t, root)
	if err := jsonFile(filepath.Join(root, "release-manifest.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if err := signSums(root, key); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGEJO_TOKEN", "fixture-secret")
	leaked := false
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(500) }))
	defer other.Close()
	for _, mode := range []string{"redirect", "wrong-tag"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					w.WriteHeader(500)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
					return
				}
				if r.URL.Path == "/api/forgejo/v1/version" {
					_, _ = io.WriteString(w, `{"version":"15.0.0"}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": strings.Repeat("b", 40)}})
			}))
			defer server.Close()
			err := uploadRelease(uploadOptions{Server: server.URL, Repo: "owner/repo", Dir: root, Key: public, Execute: true}, server.Client())
			if err == nil || writes != 0 || leaked {
				t.Fatal("unsafe publish", err, writes, leaked)
			}
		})
	}
}
