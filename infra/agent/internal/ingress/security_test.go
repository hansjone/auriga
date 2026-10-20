package ingress

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"fmt"
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
	"time"
)

func TestIdentity_FailClosedAndAudience(t *testing.T) {
	priv, pub, kid := testTicketKeys(t)
	now := time.Now()
	keys := IdentityKeys{kid: pub}
	for _, aud := range []string{"i1", "i2", ""} {
		token, err := SignIdentityJWS(priv, kid, map[string]any{"iss": "auriga-edge", "aud": aud, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		got := SelectForwardIdentity([]string{token}, keys, nil, "i1")
		if (got != "") != (aud == "i1") {
			t.Fatalf("aud=%q forwarded=%v", aud, got != "")
		}
	}
	if SelectForwardIdentity([]string{"forged"}, nil, nil, "i1") != "" {
		t.Fatal("no keys forwarded forged identity")
	}
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	s := New(Config{Registry: NewMapRegistry(&Instance{ID: "i1", Upstream: u})})
	defer func() { _ = s.Close() }()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/i/i1/", nil)
	req.Header.Set(identityHeader, "forged")
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable || calls.Load() != 0 || !strings.Contains(w.Body.String(), CodeUnauthorized) {
		t.Fatalf("%d %s calls=%d", w.Code, w.Body, calls.Load())
	}
}

func TestProxy_SharedTransport(t *testing.T) {
	s := New(Config{})
	defer func() { _ = s.Close() }()
	a, b := s.newInstanceProxy(&Instance{}), s.newInstanceProxy(&Instance{})
	if a.Transport != b.Transport {
		t.Fatal("transport not shared")
	}
	if s.transport.Proxy != nil {
		t.Fatal("proxy environment must not redirect local upstreams")
	}
}

func TestTicket_CleanPrefixReplayAndLifetime(t *testing.T) {
	priv, pub, kid := testTicketKeys(t)
	now := time.Now()
	v := &TicketVerifier{Keys: map[string]ed25519.PublicKey{kid: pub}, Now: func() time.Time { return now }}
	claims := FileTicketClaims{Iss: "auriga-workspace", Aud: "auriga-agent", IID: "i1", PathPrefix: "a/./", Ops: []string{"read"}, Iat: now.Unix(), Exp: now.Add(time.Minute).Unix(), Jti: "single"}
	token, _ := SignFileTicket(priv, kid, claims)
	if _, err := v.Verify(token, "i1", OpRead, "a/../b"); err == nil || !strings.Contains(err.Error(), CodeTicketPathDenied) {
		t.Fatalf("prefix escape: %v", err)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := v.Verify(token, "i1", OpRead, "a/sub/../file"); err == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("accepted replays: %d", accepted.Load())
	}
	for _, seconds := range []int64{0, 901} {
		claims.Jti = fmt.Sprint(seconds)
		claims.Exp = claims.Iat + seconds
		token, _ = SignFileTicket(priv, kid, claims)
		if _, err := v.Verify(token, "i1", OpRead, "a/file"); err == nil || !strings.Contains(err.Error(), CodeTicketInvalid) {
			t.Fatalf("lifetime: %v", err)
		}
	}
	claims.Jti = ""
	claims.Exp = claims.Iat + 60
	token, _ = SignFileTicket(priv, kid, claims)
	if _, err := v.Verify(token, "i1", OpRead, "a/file"); err == nil {
		t.Fatal("missing jti accepted")
	}
}

func TestFiles_WriteLimitsDeleteRootAndMissing(t *testing.T) {
	root := t.TempDir()
	s := New(Config{MaxUploadBytes: 3})
	defer func() { _ = s.Close() }()
	for _, declared := range []int64{4, -1} {
		req := httptest.NewRequest("PUT", "/", strings.NewReader("1234"))
		req.ContentLength = declared
		w := httptest.NewRecorder()
		if err := s.writeFile(w, req, root, "sub/a/file"); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("upload status: %d", w.Code)
		}
		if data, err := os.ReadFile(filepath.Join(root, "sub/a/file")); err == nil && len(data) > 3 {
			t.Fatal("oversized upload written")
		}
	}
	for _, rel := range []string{"", ".", "a/.."} {
		if err := s.deletePath(httptest.NewRecorder(), root, rel); err == nil || !strings.Contains(err.Error(), CodePathInvalid) {
			t.Fatalf("root delete %q: %v", rel, err)
		}
	}
	_, err := OpenBeneath(root, "missing", os.O_RDONLY, 0)
	code, status := pathError(err)
	if status != 404 || code == CodePathEscape {
		t.Fatalf("missing: %s %d %v", code, status, err)
	}
	req := httptest.NewRequest("PUT", "/", strings.NewReader("ok"))
	if err := s.writeFile(httptest.NewRecorder(), req, root, "new/parents/file"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "new/parents/file")); err != nil || string(data) != "ok" {
		t.Fatalf("write: %q %v", data, err)
	}
}

func TestZip_UTF8AndLimits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "中文.txt"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Config{MaxZipBytes: 3, MaxZipEntries: 2})
	defer func() { _ = s.Close() }()
	w := httptest.NewRecorder()
	if err := s.zipTree(w, root, "."); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "中文.txt" || zr.File[0].Flags&0x800 == 0 {
		t.Fatalf("zip entries: %+v", zr.File)
	}
	f, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || string(data) != "abc" {
		t.Fatalf("%s %v", data, err)
	}
	s.cfg.MaxZipBytes = 2
	w = httptest.NewRecorder()
	if err := s.zipTree(w, root, "."); err == nil || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
		t.Fatalf("size limit err=%v body=%d", err, w.Body.Len())
	}
	s.cfg.MaxZipBytes = 100
	s.cfg.MaxZipEntries = 1
	if err := s.zipTree(httptest.NewRecorder(), root, "."); err == nil {
		t.Fatal("entry limit ignored")
	}
}

func TestFiles_RejectSymlinkWrite(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	s := New(Config{})
	defer func() { _ = s.Close() }()
	err := s.writeFile(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", strings.NewReader("bad")), root, "link/created/file")
	if err == nil || !strings.Contains(err.Error(), CodePathEscape) {
		t.Fatalf("write link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "created")); !os.IsNotExist(err) {
		t.Fatalf("created outside: %v", err)
	}
}
