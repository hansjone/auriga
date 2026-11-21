package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func fixtureDownloadURL(base, route string) string {
	u, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	segments := strings.Split(route, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.TrimSuffix(u.String(), "/") + strings.Join(segments, "/")
}

func TestForgejoCanonicalDownloadAddress(t *testing.T) {
	for _, release := range []struct{ repo, tag string }{{"owner/repo", "1.0"}, {"another-owner/another_repo", "v9.4-beta.3"}} {
		for _, internal := range []bool{false, true} {
			public, _ := url.Parse("https://forgejo.example.com/forge")
			origin := public
			a := forgejoAPI{base: public.String(), origin: origin, downloadPrefix: "/" + release.repo + "/releases/download/" + release.tag + "/"}
			if internal {
				origin, _ = url.Parse("http://127.0.0.1:3000/inside")
				a.origin, a.public, a.base = origin, public, origin.String()
			}
			origins := []*url.URL{public}
			if internal {
				origins = append(origins, origin)
			}
			for _, from := range origins {
				for _, name := range []string{"bundle.tar", "file with +?#%.tar", "资料.tar", "percent%2f.txt", "name;with,segments.tar"} {
					t.Run(release.repo+"/"+release.tag+"/"+from.Scheme+"/"+name, func(t *testing.T) {
						route := a.downloadPrefix + name
						address := fixtureDownloadURL(from.String(), route)
						actual, err := a.attachmentAddress(address, name)
						want := address
						if internal {
							want = fixtureDownloadURL(origin.String(), route)
						}
						if err != nil || actual != want {
							t.Fatalf("approved canonical asset: got %q, %v; want %q", actual, err, want)
						}
					})
				}
				const legacy = "/attachments/12345678-1234-abcd-1234-123456789abc"
				address := from.String() + legacy
				actual, err := a.attachmentAddress(address, "bundle.tar")
				want := address
				if internal {
					want = origin.String() + legacy
				}
				if err != nil || actual != want {
					t.Fatalf("legacy UUID: got %q, %v; want %q", actual, err, want)
				}
			}
		}
	}
}

func TestForgejoDownloadAddressRejectsUnapprovedRoutes(t *testing.T) {
	public, _ := url.Parse("https://forgejo.example.com/forge")
	internal, _ := url.Parse("http://127.0.0.1:3000/inside")
	const prefix = "/owner/repo/releases/download/1.0/"
	const canonical = "https://forgejo.example.com/forge" + prefix + "bundle.tar"
	for _, remap := range []bool{false, true} {
		a := forgejoAPI{base: public.String(), origin: public, downloadPrefix: prefix}
		if remap {
			a.base, a.origin, a.public = internal.String(), internal, public
		}
		for _, address := range []string{
			"https://other.example.com/forge" + prefix + "bundle.tar",
			"https://forgejo.example.com.other.example/forge" + prefix + "bundle.tar",
			"http://forgejo.example.com/forge" + prefix + "bundle.tar",
			"https://user@forgejo.example.com/forge" + prefix + "bundle.tar",
			"https://forgejo.example.com:443/forge" + prefix + "bundle.tar",
			"https://forgejo.example.com/forge/owner/other/releases/download/1.0/bundle.tar",
			"https://forgejo.example.com/forge/other/repo/releases/download/1.0/bundle.tar",
			"https://forgejo.example.com/forge/owner/repo/releases/download/2.0/bundle.tar",
			"https://forgejo.example.com/forge" + prefix + "other-local-asset.tar",
			"https://forgejo.example.com/forge/owner%2frepo/releases/download/1.0/bundle.tar",
			"https://forgejo.example.com/forge/owner/repo/releases/download/%31.0/bundle.tar",
			"https://forgejo.example.com/forge" + prefix + "%62undle.tar",
			"https://forgejo.example.com/forge" + prefix + "sub/bundle.tar",
			"https://forgejo.example.com/forge" + prefix + "../bundle.tar",
			"https://forgejo.example.com/forge" + prefix + "..%2fbundle.tar",
			"https://forgejo.example.com/forge" + prefix + "/bundle.tar",
			canonical + "/",
			canonical + "?download=1",
			canonical + "?",
			canonical + "#fragment",
			canonical + "#",
			canonical + "%00",
			canonical + "%zz",
			"https://forgejo.example.com/forge/download/bundle.tar",
			"https://forgejo.example.com/forge/attachments/1234?x=1",
			"https://forgejo.example.com/forge/attachments/%31abc",
			"http://127.0.0.1:3001/inside" + prefix + "bundle.tar",
			"https://127.0.0.1:3000/inside" + prefix + "bundle.tar",
			"//forgejo.example.com/forge" + prefix + "bundle.tar",
			"/forge" + prefix + "bundle.tar",
		} {
			t.Run(address, func(t *testing.T) {
				actual, err := a.attachmentAddress(address, "bundle.tar")
				if err == nil || actual != "" {
					t.Fatalf("unapproved route accepted: %q, %v", actual, err)
				}
			})
		}
		for _, name := range []string{"", ".", "..", "sub/bundle.tar", "../bundle.tar", "C:\\bundle.tar", "bundle\n.tar"} {
			if actual, err := a.attachmentAddress(canonical, name); err == nil || actual != "" {
				t.Fatalf("invalid local signed asset name accepted: %q, %v", actual, err)
			}
		}
		for _, name := range []string{"raw space.tar", "资料.tar"} {
			if actual, err := a.attachmentAddress(public.String()+prefix+name, name); err == nil || actual != "" {
				t.Fatalf("unescaped noncanonical name accepted: %q, %v", actual, err)
			}
		}
		a.downloadPrefix = ""
		if actual, err := a.attachmentAddress(canonical, "bundle.tar"); err == nil || actual != "" {
			t.Fatalf("canonical route accepted without approved release context: %q, %v", actual, err)
		}
	}
	// A private HTTP URL cannot become a new origin without explicit configuration.
	a := forgejoAPI{base: public.String(), origin: public, downloadPrefix: prefix}
	if actual, err := a.attachmentAddress(internal.String()+prefix+"bundle.tar", "bundle.tar"); err == nil || actual != "" {
		t.Fatalf("implicit internal remapping accepted: %q, %v", actual, err)
	}
}

func TestForgejoCanonicalUploadAndReadback(t *testing.T) {
	for _, internal := range []bool{false, true} {
		for _, routeKind := range []string{"canonical", "uuid"} {
			for _, mode := range []string{"new", "missing", "existing", "unknown", "corrupt"} {
				t.Run(routeKind+"/"+mode+"/"+map[bool]string{false: "https", true: "internal"}[internal], func(t *testing.T) {
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
					local := map[string][]byte{}
					remote := map[string][]byte{}
					for _, name := range names {
						body, err := os.ReadFile(filepath.Join(root, name))
						if err != nil {
							t.Fatal(err)
						}
						local[name] = body
						if mode != "new" {
							remote[name] = append([]byte(nil), body...)
						}
					}
					if mode == "missing" {
						delete(remote, names[0])
					}
					if mode == "unknown" {
						remote["unexpected.tar"] = []byte("unapproved")
					}
					if mode == "corrupt" {
						remote[names[0]][0] ^= 1 // Same size: must fail actual SHA, not only metadata.
					}
					created, draft := mode != "new", true
					var mu sync.Mutex
					var uploads, publishes, lookups int
					reads := map[string]int{}
					var external atomic.Int32
					var canonicalBase, pathBase string
					const api = "/api/v1/repos/owner/repo"
					routeFor := func(name string) string {
						if routeKind == "uuid" {
							return "/attachments/" + hex.EncodeToString([]byte(name))
						}
						return fixtureDownloadPrefix + name
					}
					const token = "fixture-scoped-token"
					handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						defer mu.Unlock()
						if r.Header.Get("Authorization") != "token "+token {
							t.Error("missing scoped token")
						}
						path := strings.TrimPrefix(r.URL.Path, pathBase)
						switch {
						case path == "/api/forgejo/v1/version":
							_, _ = io.WriteString(w, `{"version":"11.0.16"}`)
						case path == api+"/tags/1.0":
							_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": m.Commit}})
						case path == api+"/releases/tags/1.0":
							lookups++
							if !created {
								w.WriteHeader(http.StatusNotFound)
								return
							}
							_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "tag_name": "1.0", "draft": draft})
						case path == api+"/releases" && r.Method == http.MethodPost:
							created = true
							w.WriteHeader(http.StatusCreated)
							_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "tag_name": "1.0", "draft": true})
						case path == api+"/releases/7/assets" && r.Method == http.MethodGet:
							var assets []forgejoAsset
							for name, body := range remote {
								assets = append(assets, forgejoAsset{Name: name, Size: int64(len(body)), URL: canonicalBase + routeFor(name)})
							}
							_ = json.NewEncoder(w).Encode(assets)
						case path == api+"/releases/7/assets" && r.Method == http.MethodPost:
							name := r.URL.Query().Get("name")
							body, err := io.ReadAll(r.Body)
							if err != nil || r.ContentLength != int64(len(body)) || r.Header.Get("Content-Type") != "application/octet-stream" || string(body) != string(local[name]) || remote[name] != nil {
								t.Error("unexpected attachment overwrite or upload bytes")
							}
							remote[name] = body
							uploads++
							w.WriteHeader(http.StatusCreated)
							_ = json.NewEncoder(w).Encode(forgejoAsset{Name: name, Size: int64(len(body)), URL: canonicalBase + routeFor(name)})
						case path == api+"/releases/7" && r.Method == http.MethodPatch:
							for name := range local {
								if reads[name] == 0 {
									t.Errorf("publication preceded complete attachment readback: %s", name)
								}
							}
							publishes++
							draft = false
							_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "tag_name": "1.0", "draft": false})
						default:
							for name, body := range remote {
								if path == routeFor(name) {
									reads[name]++
									_, _ = w.Write(body)
									return
								}
							}
							t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
							w.WriteHeader(http.StatusNotFound)
						}
					})
					var server *httptest.Server
					var internalURL string
					if internal {
						outside := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							external.Add(1)
							w.WriteHeader(http.StatusInternalServerError)
						}))
						defer outside.Close()
						server = httptest.NewServer(handler)
						canonicalBase, pathBase = outside.URL+"/forge", "/inside"
						internalURL = server.URL + pathBase
						t.Setenv("HTTP_PROXY", outside.URL)
					} else {
						server = httptest.NewTLSServer(handler)
						canonicalBase, pathBase = server.URL+"/forge", "/forge"
					}
					defer server.Close()
					t.Setenv("FORGEJO_TOKEN", token)
					err = uploadRelease(uploadOptions{Server: canonicalBase, InternalHTTP: internalURL, Repo: "owner/repo", Dir: root, Key: public, Execute: true, Publish: true}, server.Client())
					mu.Lock()
					defer mu.Unlock()
					if external.Load() != 0 {
						t.Fatal("explicit internal transport contacted public or proxy origin")
					}
					if mode == "unknown" || mode == "corrupt" {
						wantError := map[string]string{"unknown": "未批准附件", "corrupt": "摘要冲突"}[mode]
						if err == nil || !strings.Contains(err.Error(), wantError) || publishes != 0 || uploads != 0 || !draft {
							t.Fatalf("conflicting remote assets changed or published: %v, uploads=%d publishes=%d draft=%t", err, uploads, publishes, draft)
						}
						return
					}
					wantUploads := map[string]int{"new": len(local), "missing": 1, "existing": 0}[mode]
					if err != nil || uploads != wantUploads || publishes != 1 || lookups != 2 || draft || len(remote) != len(local) {
						t.Fatalf("normal upload/readback not preserved: %v, uploads=%d/%d publishes=%d lookups=%d draft=%t", err, uploads, wantUploads, publishes, lookups, draft)
					}
					for name, body := range local {
						if string(remote[name]) != string(body) || reads[name] == 0 {
							t.Errorf("asset not preserved and fully read back: %s", name)
						}
					}
				})
			}
		}
	}
}
