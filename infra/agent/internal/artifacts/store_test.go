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
)

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

	root := t.TempDir()
	st := artifacts.New(root)
	st.Client = srv.Client()

	path1, err := st.Ensure(context.Background(), srv.URL, digest)
	if err != nil {
		t.Fatal(err)
	}
	path2, err := st.Ensure(context.Background(), srv.URL, digest)
	if err != nil {
		t.Fatal(err)
	}
	if path1 != path2 {
		t.Fatalf("paths differ: %s vs %s", path1, path2)
	}
	if hits != 1 {
		t.Fatalf("expected 1 download, got %d", hits)
	}
	got, err := os.ReadFile(path1)
	if err != nil || string(got) != string(body) {
		t.Fatalf("content: %q %v", got, err)
	}
}

func TestEnsureRejectsDigestMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("actual-bytes"))
	}))
	t.Cleanup(srv.Close)

	st := artifacts.New(t.TempDir())
	st.Client = srv.Client()
	_, err := st.Ensure(context.Background(), srv.URL, "sha256:"+strings.Repeat("ab", 32))
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(st.Root, "sha256"))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover file after mismatch: %s", e.Name())
		}
	}
}

func TestEnsureRejectsInterruptedDownload(t *testing.T) {
	st := artifacts.New(t.TempDir())
	st.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(&errReader{data: []byte("partial"), errAfter: 7}),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	want := sha256.Sum256([]byte("partial" + strings.Repeat("x", 100)))
	_, err := st.Ensure(context.Background(), "http://example.com/artifact", "sha256:"+hex.EncodeToString(want[:]))
	if err == nil {
		t.Fatal("expected interrupted download error")
	}
	if !strings.Contains(err.Error(), "interrupted") && !strings.Contains(err.Error(), "download") {
		t.Fatalf("unexpected error: %v", err)
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

func TestReleaseAndGC(t *testing.T) {
	body := []byte("gc-me")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, strings.NewReader(string(body)))
	}))
	t.Cleanup(srv.Close)

	st := artifacts.New(t.TempDir())
	st.Client = srv.Client()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	st.Now = func() time.Time { return now }

	path, err := st.Ensure(context.Background(), srv.URL, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Release(digest); err != nil {
		t.Fatal(err)
	}
	removed, err := st.GC(false)
	if err != nil || removed != 0 {
		t.Fatalf("GC too early: removed=%d err=%v", removed, err)
	}
	st.Now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	removed, err = st.GC(false)
	if err != nil || removed != 1 {
		t.Fatalf("GC expected 1, got %d err=%v", removed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be gone: %v", err)
	}
}
