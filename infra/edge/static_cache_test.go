package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStaticSharedBudgetConcurrentMisses(t *testing.T) {
	g, _, _ := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) {
		c.Static.PortalRoot = t.TempDir()
		c.Static.AdminRoot = t.TempDir()
		c.Static.GzipCacheBytes = 2048
	})
	cache := g.litellmStaticCache
	if g.portalStatic.(*spaFileServer).gzipCache != cache || g.adminStatic.(*spaFileServer).gzipCache != cache {
		t.Fatal("static services do not share one budget")
	}
	raw := make([]byte, 512)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := range 32 {
		workers.Go(func() {
			if _, err := cache.encode(staticCacheKey{strconv.Itoa(i % 8), 512, "mtime"}, raw); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	accounted := 0
	for _, entry := range cache.items {
		accounted += len(entry.Value.(staticCacheItem).data)
	}
	if accounted != cache.bytes || accounted > 2048 || len(cache.items) != cache.lru.Len() {
		t.Fatal("concurrent misses broke global accounting")
	}
	large := make([]byte, 4096)
	if _, err := rand.Read(large); err != nil {
		t.Fatal(err)
	}
	key := staticCacheKey{"oversized", 4096, "mtime"}
	if _, err := cache.encode(key, large); err != nil {
		t.Fatal(err)
	}
	if cache.get(key) != nil || cache.bytes > 2048 {
		t.Fatal("single oversized entry exceeded cache budget")
	}
}

func TestStaticCacheMetadataHeadAndCanonicalPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "assets", "app.js")
	if err := os.WriteFile(file, []byte(strings.Repeat("first;", 1000)), 0600); err != nil {
		t.Fatal(err)
	}
	h := newSPAFileServer(root)
	request := func(method, path, modified string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Accept-Encoding", "gzip")
		r.Header.Set("If-Modified-Since", modified)
		w := httptest.NewRecorder()
		w.Header().Add("Vary", "Origin")
		h.ServeHTTP(w, r)
		return w
	}
	get := request("GET", "/assets/app.js", "")
	if get.Header().Get("Last-Modified") == "" || !strings.Contains(strings.Join(get.Header().Values("Vary"), ","), "Origin") {
		t.Fatal("static metadata or Vary lost")
	}
	head := request("HEAD", "/assets/app.js", "")
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") != get.Header().Get("Content-Length") || head.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("HEAD differs from GET")
	}
	if got := request("GET", "/assets/app.js", get.Header().Get("Last-Modified")); got.Code != http.StatusNotModified || got.Body.Len() != 0 {
		t.Fatal("conditional GET lost")
	}
	canonical := request("GET", "/elsewhere/../assets/app.js", "")
	if canonical.Header().Get("Content-Encoding") != get.Header().Get("Content-Encoding") {
		t.Fatal("compression and file lookup use different paths")
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("other;", 1000)), 0600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(file, future, future); err != nil {
		t.Fatal(err)
	}
	changed := request("GET", "/assets/app.js", "")
	if changed.Body.String() == get.Body.String() {
		t.Fatal("mtime change reused stale cache")
	}
}

func TestLiteLLMUppercaseLayoutDirectory(t *testing.T) {
	for _, name := range []string{"TeamSettings", "ABC_12-a"} {
		r := httptest.NewRequest("GET", "/ai/ui/"+name+"/__next.%21KGRhc2hib2FyZCk.__PAGE__.txt", nil)
		if !liteLLMPathAllowed(r) {
			t.Fatal("valid case-sensitive Next.js directory refused")
		}
	}
}

func TestStaticCacheReuseAndBoundedEviction(t *testing.T) {
	cache := &staticGzipCache{}
	key := staticCacheKey{"asset.js", 100, "mtime"}
	first, err := cache.encode(key, []byte(strings.Repeat("first", 20)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.encode(key, []byte(strings.Repeat("other", 20)))
	if err != nil || &first[0] != &second[0] {
		t.Fatal("same metadata recompressed")
	}
	for i := range 160 {
		_, err := cache.encode(staticCacheKey{strconv.Itoa(i), 100, "mtime"}, []byte(strings.Repeat("text", 25)))
		if err != nil {
			t.Fatal(err)
		}
	}
	if cache.get(key) != nil || len(cache.items) > 128 || cache.bytes > 32<<20 {
		t.Fatal("cache limit/eviction failed")
	}
}

type unreadStaticBody struct{ closed bool }

func (*unreadStaticBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (b *unreadStaticBody) Close() error           { b.closed = true; return nil }

func TestStaticUpstreamCacheStillChecksMetadata(t *testing.T) {
	cache := &staticGzipCache{}
	r := httptest.NewRequest("GET", "http://litellm.example.com/ai/_next/static/app.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	response := func(body io.ReadCloser, mtime string) *http.Response {
		return &http.Response{Request: r, StatusCode: 200, ContentLength: 100, Header: http.Header{"Content-Type": {"text/javascript"}, "Last-Modified": {mtime}}, Body: body}
	}
	modified := time.Now().UTC().Format(http.TimeFormat)
	first := response(io.NopCloser(strings.NewReader(strings.Repeat("hello", 20))), modified)
	if err := gzipStaticResponseCached(first, cache); err != nil {
		t.Fatal(err)
	}
	if err := first.Body.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		modified string
		hit      bool
	}{
		{modified, true}, {time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), false}, {"", false},
	} {
		body := &unreadStaticBody{}
		err := gzipStaticResponseCached(response(body, tc.modified), cache)
		if tc.hit && (err != nil || !body.closed) || !tc.hit && err == nil {
			t.Fatal("upstream metadata cache boundary failed")
		}
	}
}

func TestLiteLLMStaticBodyTimeout(t *testing.T) {
	finished := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(finished)
	}))
	defer upstream.Close()
	g, _, store := accessFixtureConfigured(t, false, nil, func(c *Config, _ map[string]any) { c.LiteLLMOrigin = upstream.URL })
	backend, _ := browserRPCFixture(t, g, store)
	sess := putBrowserSession(g, backend, "admin", "admin")
	claims, _ := json.Marshal(map[string]any{"ctx": "admin", "tid": sess.TenantID, "sub": sess.AccountID, "perms": []string{"ai.providers.write"}})
	sess.AccessToken = "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".fixture"
	store.Put("admin", sess)
	r := httptest.NewRequest("GET", "/ai/_next/static/stalled.js", nil)
	r.Header.Set("Cookie", "auriga_litellm_session=admin")
	r.Header.Set("Accept-Encoding", "gzip")
	r = r.WithContext(context.WithValue(r.Context(), accessEntryKey{}, accessEntry{role: "relayAdmin", scheme: "http"}))
	w := httptest.NewRecorder()
	started := time.Now()
	g.proxyLiteLLM(w, r, "relayAdmin", "static-timeout")
	if w.Code != http.StatusBadGateway || time.Since(started) > staticBodyTimeout+3*time.Second {
		t.Fatal("static body read escaped its deadline")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("upstream request was not canceled")
	}
}
