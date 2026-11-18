package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
			var body struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			fp, err := publicFingerprint(public)
			if err != nil || !strings.Contains(body.Body, fp) {
				t.Error("release body missing approved key fingerprint", err)
			}
			created = true
			mutations++
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "draft": true, "tag_name": "1.0"})
		case strings.HasSuffix(endpoint, "/assets") && r.Method == "GET":
			// The release attachment endpoint does not paginate its response.
			assets := []map[string]any{}
			for name, body := range files {
				assets = append(assets, map[string]any{"name": name, "size": len(body), "browser_download_url": base + "/download/" + name})
			}
			_ = json.NewEncoder(w).Encode(assets)
		case strings.HasSuffix(endpoint, "/assets") && r.Method == "POST":
			if r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Error("expected an unwrapped attachment stream")
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if r.ContentLength != int64(len(body)) {
				t.Error("attachment content length differs from the request body")
			}
			name := r.URL.Query().Get("name")
			files[name] = body
			mutations++
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "size": len(body), "browser_download_url": base + "/download/" + name})
		case strings.HasPrefix(endpoint, "/download/"):
			_, _ = w.Write(files[strings.TrimPrefix(endpoint, "/download/")])
		case strings.HasSuffix(endpoint, "/releases/7") && r.Method == "PATCH":
			var body struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			fp, err := publicFingerprint(public)
			if err != nil || !strings.Contains(body.Body, fp) {
				t.Error("published body missing approved key fingerprint", err)
			}
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
	publishedMutations := mutations
	if err := uploadRelease(opts, server.Client()); err != nil || mutations != publishedMutations {
		t.Fatal("retry changed an already published release", err)
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

func TestForgejoPublishRequiresConfirmedRelease(t *testing.T) {
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
	names, err := regularFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, name := range names {
		files[name], err = os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FORGEJO_TOKEN", "fixture-secret")
	const valid = `{"id":7,"tag_name":"1.0","draft":false}`
	const draft = `{"id":7,"tag_name":"1.0","draft":true}`
	for _, tc := range []struct {
		name, patch, readback, wantError string
		redirectStatus                   int
	}{
		{name: "published", patch: valid, readback: valid},
		{name: "patch-still-draft", patch: draft, wantError: "发布响应"},
		{name: "patch-missing-status", patch: `{"id":7,"tag_name":"1.0"}`, wantError: "发布响应"},
		{name: "patch-wrong-id", patch: `{"id":8,"tag_name":"1.0","draft":false}`, wantError: "发布响应"},
		{name: "readback-still-draft", patch: valid, readback: draft, wantError: "发布回读"},
		{name: "readback-unavailable", patch: valid, wantError: "HTTP 503"},
		{name: "readback-wrong-tag", patch: valid, readback: `{"id":7,"tag_name":"2.0","draft":false}`, wantError: "发布回读"},
		{name: "readback-wrong-id", patch: valid, readback: `{"id":8,"tag_name":"1.0","draft":false}`, wantError: "发布回读"},
		{name: "patch-unavailable", wantError: "HTTP 503"},
		{name: "patch-redirect-303", redirectStatus: http.StatusSeeOther, wantError: "拒绝 Forgejo 写请求重定向"},
		{name: "patch-redirect-307", redirectStatus: http.StatusTemporaryRedirect, wantError: "拒绝 Forgejo 写请求重定向"},
		{name: "create-redirect", redirectStatus: http.StatusFound, wantError: "拒绝 Forgejo 写请求重定向"},
		{name: "upload-redirect", redirectStatus: http.StatusFound, wantError: "拒绝 Forgejo 写请求重定向"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const repo = "/api/v1/repos/owner/repo"
			var lookups, writes, redirected atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				switch {
				case r.URL.Path == "/api/forgejo/v1/version":
					_, _ = io.WriteString(w, `{"version":"15.0.0"}`)
				case r.URL.Path == repo+"/tags/1.0":
					_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": manifest.Commit}})
				case r.URL.Path == repo+"/releases/tags/1.0":
					if tc.name == "create-redirect" {
						w.WriteHeader(http.StatusNotFound)
					} else {
						switch lookup := lookups.Add(1); {
						case lookup == 1:
							_, _ = io.WriteString(w, draft)
						case tc.name == "readback-unavailable" && lookup == 2:
							w.WriteHeader(http.StatusServiceUnavailable)
						case tc.name == "readback-unavailable":
							_, _ = io.WriteString(w, valid)
						default:
							_, _ = io.WriteString(w, tc.readback)
						}
					}
				case r.URL.Path == repo+"/releases/7/assets" && r.Method == http.MethodGet:
					assets := []forgejoAsset{}
					if tc.name != "upload-redirect" {
						for name, body := range files {
							assets = append(assets, forgejoAsset{Name: name, Size: int64(len(body)), URL: "https://" + r.Host + "/download/" + name})
						}
					}
					_ = json.NewEncoder(w).Encode(assets)
				case strings.HasPrefix(r.URL.Path, "/download/"):
					http.Redirect(w, r, "/bytes/"+strings.TrimPrefix(r.URL.Path, "/download/"), http.StatusFound)
				case strings.HasPrefix(r.URL.Path, "/bytes/"):
					_, _ = w.Write(files[strings.TrimPrefix(r.URL.Path, "/bytes/")])
				case r.URL.Path == repo+"/releases/7" && r.Method == http.MethodPatch:
					if tc.redirectStatus != 0 {
						http.Redirect(w, r, "/redirected", tc.redirectStatus)
					} else if tc.patch == "" {
						w.WriteHeader(http.StatusServiceUnavailable)
					} else {
						_, _ = io.WriteString(w, tc.patch)
					}
				case (r.URL.Path == repo+"/releases" || r.URL.Path == repo+"/releases/7/assets") && r.Method == http.MethodPost:
					http.Redirect(w, r, "/redirected", tc.redirectStatus)
				case r.URL.Path == "/redirected":
					redirected.Add(1)
					_, _ = io.WriteString(w, valid)
				default:
					t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			opts := uploadOptions{Server: server.URL, Repo: "owner/repo", Dir: root, Key: public, Execute: true, Publish: true}
			err := uploadRelease(opts, server.Client())
			if tc.wantError == "" {
				if err != nil || lookups.Load() != 2 {
					t.Fatalf("publication not read back: %v, lookups %d", err, lookups.Load())
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("want error containing %q, got %v", tc.wantError, err)
			}
			if tc.name == "readback-unavailable" {
				if err := uploadRelease(opts, server.Client()); err != nil || lookups.Load() != 3 {
					t.Fatalf("retry failed to recognize an already published release: %v, lookups %d", err, lookups.Load())
				}
			}
			if writes.Load() != 1 || redirected.Load() != 0 {
				t.Fatalf("unexpected writes or redirected requests: %d, %d", writes.Load(), redirected.Load())
			}
		})
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

func TestForgejoInternalHTTPRequiresExplicitPrivateRoute(t *testing.T) {
	privateIP := netip.AddrFrom4([4]byte{10, 20, 30, 40})
	for _, site := range []string{"http://127.0.0.1:3000", "http://" + privateIP.String() + ":3000"} {
		t.Run(site, func(t *testing.T) {
			err := uploadCommand([]string{"--server", "https://forgejo.example.com", "--internal-http", site, "--repo", "owner/repo"})
			if err == nil || !strings.Contains(err.Error(), "--verify-key") {
				t.Fatalf("approved internal address did not reach local validation: %v", err)
			}
		})
	}
	for _, site := range []string{"http://192.0.2.10:3000", "http://forgejo.example.com:3000", "http://user@127.0.0.1:3000", "http://127.0.0.1:3000/?token=fixture-secret"} {
		t.Run(site, func(t *testing.T) {
			err := uploadCommand([]string{"--server", "https://forgejo.example.com", "--internal-http", site, "--repo", "owner/repo"})
			if err == nil || !strings.Contains(err.Error(), "内网") || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("unsafe address not rejected without echoing credentials: %v", err)
			}
		})
	}
}

func TestForgejoInternalHTTPReadbackUsesApprovedOrigin(t *testing.T) {
	root := t.TempDir()
	key, public := testSigningKeys(t)
	m := fixtureDistribution(t, root)
	if err := jsonFile(filepath.Join(root, "release-manifest.json"), m, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeSums(root); err != nil {
		t.Fatal(err)
	}
	if err := signSums(root, key); err != nil {
		t.Fatal(err)
	}
	names, err := regularFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	const token = "fixture-secret"
	t.Setenv("FORGEJO_TOKEN", token)
	external := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("publisher contacted the external site from the internal route")
		w.WriteHeader(500)
	}))
	defer external.Close()
	t.Setenv("HTTP_PROXY", external.URL)
	canonical := external.URL + "/forge"
	for _, mode := range []string{"readback", "foreign-url", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			files := map[string][]byte{}
			var assets []forgejoAsset
			for _, name := range names {
				body, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				route := "/attachments/" + hex.EncodeToString([]byte(name))
				files[route] = body
				ad := canonical + route
				if mode == "foreign-url" {
					ad = "https://unapproved.example.com" + route
				}
				assets = append(assets, forgejoAsset{Name: name, Size: int64(len(body)), URL: ad})
			}
			const repo = "/api/v1/repos/owner/repo"
			internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "token "+token {
					t.Error("missing scoped internal token")
				}
				if r.Method != http.MethodGet {
					t.Error("complete drafts only need readback")
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/api/forgejo/v1/version":
					_, _ = io.WriteString(w, `{"version":"11.0.16"}`)
				case repo + "/tags/1.0":
					_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": m.Commit}})
				case repo + "/releases/tags/1.0":
					_, _ = io.WriteString(w, `{"id":7,"draft":true,"tag_name":"1.0"}`)
				case repo + "/releases/7/assets":
					_ = json.NewEncoder(w).Encode(assets)
				default:
					if body, ok := files[r.URL.Path]; ok {
						if mode == "redirect" {
							http.Redirect(w, r, canonical+r.URL.Path, http.StatusTemporaryRedirect)
							return
						}
						_, _ = w.Write(body)
					} else {
						t.Errorf("unexpected internal route %s", r.URL.Path)
						w.WriteHeader(404)
					}
				}
			}))
			defer internal.Close()
			err := uploadRelease(uploadOptions{Server: canonical, InternalHTTP: internal.URL, Repo: "owner/repo", Dir: root, Key: public, Execute: true}, internal.Client())
			switch mode {
			case "readback":
				if err != nil {
					t.Fatal(err)
				}
			case "foreign-url":
				if err == nil || !strings.Contains(err.Error(), "拒绝跨站点") {
					t.Fatal(err)
				}
			case "redirect":
				if err == nil || !strings.Contains(err.Error(), "拒绝跨主机或降级重定向") {
					t.Fatal(err)
				}
			}
		})
	}
}
