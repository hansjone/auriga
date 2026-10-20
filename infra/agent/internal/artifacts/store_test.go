package artifacts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
)

func anyPol() policy.Policy {
	p := policy.Default()
	p.AllowAnyArtifact = true
	p.MaxArtifactBytes = 1 << 20
	p.ArtifactConnectTimeout = 5 * time.Second
	p.ArtifactTotalTimeout = 30 * time.Second
	return p
}

func TestEnsureCachesAndVerifies(t *testing.T) {
	body := []byte("hello-artifact")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = srv.Client()

	path1, err := st.Ensure(context.Background(), "inst_a", srv.URL+"/a", digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	path2, err := st.Ensure(context.Background(), "inst_a", srv.URL+"/a", digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	if path1 != path2 || hits != 1 {
		t.Fatalf("path/hits %s %d", path1, hits)
	}
}

func TestEnsureRejectsNonAllowlisted(t *testing.T) {
	p := policy.Default()
	p.AllowedArtifactURLPrefixes = []string{"https://artifacts.example.com/"}
	st := artifacts.New(t.TempDir(), p)
	_, err := st.Ensure(context.Background(), "h1", "https://evil.example.com/x", "sha256:"+strings.Repeat("ab", 32), 0)
	if err == nil {
		t.Fatal("expected policy reject")
	}
	_, err = st.Ensure(context.Background(), "h1", "http://artifacts.example.com/x", "sha256:"+strings.Repeat("ab", 32), 0)
	if err == nil {
		t.Fatal("http should fail")
	}
}

func TestEnsureRejectsDigestMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("actual-bytes"))
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = srv.Client()
	_, err := st.Ensure(context.Background(), "h1", srv.URL+"/x", "sha256:"+strings.Repeat("ab", 32), 0)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("got %v", err)
	}
}

func TestEnsureRejectsInterruptedDownload(t *testing.T) {
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(&errReader{data: []byte("partial"), errAfter: 7}),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	want := sha256.Sum256([]byte("partial" + strings.Repeat("x", 100)))
	_, err := st.Ensure(context.Background(), "h1", "https://example.com/artifact", "sha256:"+hex.EncodeToString(want[:]), 0)
	if err == nil {
		t.Fatal("expected interrupted")
	}
}

func TestEnsureRejectsTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 100))
	}))
	t.Cleanup(srv.Close)
	pol := anyPol()
	pol.MaxArtifactBytes = 10
	st := artifacts.New(t.TempDir(), pol)
	st.Client = srv.Client()
	sum := sha256.Sum256(make([]byte, 100))
	_, err := st.Ensure(context.Background(), "h1", srv.URL+"/big", "sha256:"+hex.EncodeToString(sum[:]), 0)
	if err == nil || !strings.Contains(err.Error(), "max size") {
		t.Fatalf("got %v", err)
	}
}

func TestTamperedCacheRedownloads(t *testing.T) {
	body := []byte("good-bytes")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = srv.Client()
	path, err := st.Ensure(context.Background(), "h1", srv.URL+"/a", digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(path, 0o600)
	if err := os.WriteFile(path, []byte("TAMPERED!!!!!!!!!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ensure(context.Background(), "h1", srv.URL+"/a", digest, 0); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("expected re-download, hits=%d", hits)
	}
}

func TestReleaseAndGC(t *testing.T) {
	body := []byte("gc-me")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = srv.Client()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }

	path, err := st.Ensure(context.Background(), "inst_x", srv.URL+"/a", digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Release("inst_x", digest); err != nil {
		t.Fatal(err)
	}
	removed, _ := st.GC(false)
	if removed != 0 {
		t.Fatalf("GC too early: %d", removed)
	}
	st.Now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	removed, err = st.GC(false)
	if err != nil || removed != 1 {
		t.Fatalf("GC expected 1, got %d %v", removed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("should be gone")
	}
}

func TestReleaseAll(t *testing.T) {
	body := []byte("all")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = srv.Client()
	if _, err := st.Ensure(context.Background(), "inst_y", srv.URL+"/a", digest, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.ReleaseAll("inst_y"); err != nil {
		t.Fatal(err)
	}
	st.Now = func() time.Time { return time.Now().UTC().Add(10 * 24 * time.Hour) }
	n, err := st.GC(false)
	if err != nil || n != 1 {
		t.Fatalf("gc %d %v", n, err)
	}
}

func TestRedirectOutsideAllowlistRejected(t *testing.T) {
	p := policy.Default()
	p.AllowedArtifactURLPrefixes = []string{"https://artifacts.example.com/"}
	body := []byte("redir-body")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	st := artifacts.New(t.TempDir(), p)
	st.Client = &http.Client{
		CheckRedirect: st.Client.CheckRedirect,
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host == "artifacts.example.com" {
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{"Location": []string{"https://evil.example.com/x"}},
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    req,
				}, nil
			}
			t.Fatalf("unexpected host %s", req.URL.Host)
			return nil, nil
		}),
	}
	_, err := st.Ensure(context.Background(), "h1", "https://artifacts.example.com/start", digest, 0)
	if err == nil {
		t.Fatal("expected redirect reject")
	}
}

func TestEnsureMaxBytesTakesPolicyMin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 50))
	}))
	t.Cleanup(srv.Close)
	pol := anyPol()
	pol.MaxArtifactBytes = 10
	st := artifacts.New(t.TempDir(), pol)
	st.Client = srv.Client()
	sum := sha256.Sum256(make([]byte, 50))
	// Caller asks for 1MiB but policy cap is 10 — must still reject.
	_, err := st.Ensure(context.Background(), "h1", srv.URL+"/big", "sha256:"+hex.EncodeToString(sum[:]), 1<<20)
	if err == nil || !strings.Contains(err.Error(), "max size") {
		t.Fatalf("got %v", err)
	}
}

func TestSyncHoldersReleasesOldDigest(t *testing.T) {
	oldBody := []byte("old-art")
	newBody := []byte("new-art")
	oldSum := sha256.Sum256(oldBody)
	newSum := sha256.Sum256(newBody)
	oldDig := "sha256:" + hex.EncodeToString(oldSum[:])
	newDig := "sha256:" + hex.EncodeToString(newSum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "new") {
			_, _ = w.Write(newBody)
			return
		}
		_, _ = w.Write(oldBody)
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = srv.Client()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }

	if _, err := st.Ensure(context.Background(), "inst_u", srv.URL+"/old", oldDig, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ensure(context.Background(), "inst_u", srv.URL+"/new", newDig, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SyncHolders("inst_u", []string{newDig}); err != nil {
		t.Fatal(err)
	}
	st.Now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	n, err := st.GC(false)
	if err != nil || n != 1 {
		t.Fatalf("GC after sync: %d %v", n, err)
	}
}

func TestCorruptRefsRebuild(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "refs.json"), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := []byte("ok")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(root, anyPol())
	st.Client = srv.Client()
	if _, err := st.Ensure(context.Background(), "h1", srv.URL+"/a", digest, 0); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type errReader struct {
	data     []byte
	off      int
	errAfter int
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.off >= e.errAfter {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, e.data[e.off:])
	e.off += n
	if e.off >= e.errAfter {
		return n, io.ErrUnexpectedEOF
	}
	if e.off >= len(e.data) {
		return n, io.EOF
	}
	return n, nil
}

func TestEnsureRejectsUncleanURLPath(t *testing.T) {
	p := policy.Default()
	p.AllowedArtifactURLPrefixes = []string{"https://artifacts.example.com/team/"}
	st := artifacts.New(t.TempDir(), p)
	st.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("must not fetch %s", r.URL)
		return nil, nil
	})}
	digest := "sha256:" + strings.Repeat("ab", 32)
	for _, u := range []string{
		"https://artifacts.example.com/team/../other/x.tgz",
		"https://artifacts.example.com/team/./x.tgz",
		"https://artifacts.example.com/team//x.tgz",
		"https://artifacts.example.com/team/%2e%2e/other/x.tgz",
		"https://artifacts.example.com/team/a%2Fb.tgz",
		"https://artifacts.example.com/team/a%5c..%5cb.tgz",
	} {
		if _, err := st.Ensure(context.Background(), "h1", u, digest, 0); err == nil {
			t.Fatalf("%s should be rejected", u)
		}
	}
}

func TestRedirectToUncleanPathRejected(t *testing.T) {
	body := []byte("x")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/a//b")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client.Transport = srv.Client().Transport
	if _, err := st.Ensure(context.Background(), "h1", srv.URL+"/start", digest, 0); err == nil {
		t.Fatal("redirect to unclean path should be rejected")
	}
}

func TestEnsureRejectsSymlinkedCacheEntry(t *testing.T) {
	root := t.TempDir()
	st := artifacts.New(root, anyPol())
	digest := "sha256:" + strings.Repeat("cd", 32)
	if err := os.MkdirAll(filepath.Join(root, "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "sha256", strings.Repeat("cd", 32))); err != nil {
		t.Skipf("symlink not permitted here: %v", err)
	}
	st.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("must not download")
		return nil, nil
	})}
	if _, err := st.Ensure(context.Background(), "h1", "https://artifacts.example.com/x", digest, 0); err == nil {
		t.Fatal("symlinked cache entry must be rejected")
	}
	if _, err := st.Path(digest); err == nil {
		t.Fatal("Path must reject symlinked entry")
	}
	if raw, _ := os.ReadFile(outside); string(raw) != "secret" {
		t.Fatal("outside file touched")
	}
}

func TestGCKeepProtectsLiveDigests(t *testing.T) {
	body := []byte("live")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	st := artifacts.New(t.TempDir(), anyPol())
	st.Client = srv.Client()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }
	path, err := st.Ensure(context.Background(), "inst_l", srv.URL+"/a", digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Refs lose track of the holder (e.g. a bug or a manual edit) ...
	if err := st.ReleaseAll("inst_l"); err != nil {
		t.Fatal(err)
	}
	st.Now = func() time.Time { return now.Add(30 * 24 * time.Hour) }
	// ... but the live set still names it, so GC keeps it.
	n, err := st.GCKeep(true, []string{digest})
	if err != nil || n != 0 {
		t.Fatalf("GCKeep removed live digest: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("live artifact deleted:", err)
	}
	if _, err := st.GCKeep(false, []string{"not-a-digest"}); err == nil {
		t.Fatal("bad keep digest must fail")
	}
}

func TestGCSkipsRoundWhenRefsCorrupt(t *testing.T) {
	root := t.TempDir()
	hexDig := strings.Repeat("ef", 32)
	if err := os.MkdirAll(filepath.Join(root, "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "sha256", hexDig)
	if err := os.WriteFile(p, []byte("held-by-someone"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "refs.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := artifacts.New(root, anyPol())
	if n, err := st.GC(true); err == nil || n != 0 {
		t.Fatalf("GC must skip after rebuilding corrupt refs: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("artifact removed despite corrupt refs:", err)
	}
	if _, err := os.Stat(filepath.Join(root, "refs.json.corrupt")); err != nil {
		t.Fatal("corrupt refs not preserved:", err)
	}
}
