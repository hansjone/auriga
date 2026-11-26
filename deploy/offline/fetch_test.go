package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClassifyArtifactLocation(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		isWindows bool
		wantKind  artifactLocationKind
		wantErr   string
	}{
		// Windows native absolute drive paths
		{name: "win backslash drive path", raw: `C:\work\inputs\desktop.tar`, isWindows: true, wantKind: artifactLocationLocal},
		{name: "win forward slash drive path", raw: `C:/work/inputs/desktop.tar`, isWindows: true, wantKind: artifactLocationLocal},
		{name: "win lowercase drive with spaces and unicode", raw: `d:\测试 目录\100% ok\desktop.tar`, isWindows: true, wantKind: artifactLocationLocal},

		// Linux absolute and relative paths
		{name: "linux abs path on linux", raw: `/tmp/inputs/desktop.tar`, isWindows: false, wantKind: artifactLocationLocal},
		{name: "relative path on linux", raw: `inputs/desktop.tar`, isWindows: false, wantKind: artifactLocationLocal},
		{name: "relative path on windows", raw: `inputs\desktop.tar`, isWindows: true, wantKind: artifactLocationLocal},
		{name: "relative filename", raw: `nas.tar`, isWindows: true, wantKind: artifactLocationLocal},

		// Windows drive-relative paths explicitly rejected on Windows
		{name: "win drive-relative backslash rejected", raw: `C:relative\desktop.tar`, isWindows: true, wantErr: "盘符相对路径"},
		{name: "win drive-relative bare rejected", raw: `C:`, isWindows: true, wantErr: "盘符相对路径"},
		{name: "win drive-relative disguised url rejected", raw: `c:https://example.com/a.tar`, isWindows: true, wantErr: "盘符相对路径"},

		// Windows drive path on non-Windows must not be treated as local
		{name: "win drive path on linux rejected by https gate", raw: `C:\work\inputs\desktop.tar`, isWindows: false, wantErr: "HTTPS"},

		// UNC and device paths rejected on both platforms
		{name: "win UNC backslash rejected", raw: `\\server\share\desktop.tar`, isWindows: true, wantErr: "UNC"},
		{name: "win device verbatim rejected", raw: `\\?\C:\work\desktop.tar`, isWindows: true, wantErr: "UNC"},
		{name: "win device namespace rejected", raw: `\\.\pipe\auriga`, isWindows: true, wantErr: "UNC"},
		{name: "slash UNC rejected on windows", raw: `//server/share/desktop.tar`, isWindows: true, wantErr: "UNC"},
		{name: "slash device rejected on windows", raw: `//?/C:/work/desktop.tar`, isWindows: true, wantErr: "UNC"},
		{name: "slash UNC rejected on linux", raw: `//server/share/desktop.tar`, isWindows: false, wantErr: "UNC"},

		// file: scheme explicitly rejected
		{name: "file uri windows rejected", raw: `file:///C:/work/desktop.tar`, isWindows: true, wantErr: "HTTPS"},
		{name: "file uri linux rejected", raw: `file:///tmp/desktop.tar`, isWindows: false, wantErr: "HTTPS"},

		// Network URL policy
		{name: "valid https url", raw: `https://releases.example.test/auriga/desktop.tar`, isWindows: true, wantKind: artifactLocationHTTPS},
		{name: "valid https url linux", raw: `https://releases.example.test/auriga/desktop.tar`, isWindows: false, wantKind: artifactLocationHTTPS},
		{name: "http rejected", raw: `http://releases.example.test/auriga/desktop.tar`, isWindows: true, wantErr: "HTTPS"},
		{name: "https with credentials rejected", raw: `https://user:pass@releases.example.test/desktop.tar`, isWindows: true, wantErr: "HTTPS"},
		{name: "https without host rejected", raw: `https:///desktop.tar`, isWindows: true, wantErr: "HTTPS"},
		{name: "unknown scheme rejected", raw: `ftp://releases.example.test/desktop.tar`, isWindows: true, wantErr: "HTTPS"},
		{name: "custom scheme rejected", raw: `s3://bucket/desktop.tar`, isWindows: false, wantErr: "HTTPS"},
		{name: "empty path rejected", raw: ``, isWindows: true, wantErr: "不能为空"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, u, err := classifyArtifactLocation(tc.raw, tc.isWindows)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("classifyArtifactLocation(%q, win=%v) err = %v, want substring %q", tc.raw, tc.isWindows, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyArtifactLocation(%q, win=%v) unexpected err: %v", tc.raw, tc.isWindows, err)
			}
			if kind != tc.wantKind {
				t.Fatalf("classifyArtifactLocation(%q, win=%v) kind = %v, want %v", tc.raw, tc.isWindows, kind, tc.wantKind)
			}
			if kind == artifactLocationHTTPS && u == nil {
				t.Fatalf("expected non-nil URL for HTTPS location %q", tc.raw)
			}
		})
	}
}

func TestFetchContextLocalPaths(t *testing.T) {
	payload := []byte("approved local desktop payload")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	wrongSum := fmt.Sprintf("%x", sha256.Sum256([]byte("tampered")))

	baseDir := t.TempDir()
	spacedDir := filepath.Join(baseDir, "sub dir 中文")
	if err := os.MkdirAll(spacedDir, 0755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(spacedDir, "desktop 附件.tar")
	if err := os.WriteFile(src, payload, 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("native absolute path with spaces and unicode", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "out.tar")
		if err := fetchContext(t.Context(), artifact{URL: src, SHA256: sum}, dst); err != nil {
			t.Fatalf("fetchContext native path failed: %v", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil || string(got) != string(payload) {
			t.Fatalf("copied content mismatch: err=%v got=%q", err, got)
		}
	})

	if runtime.GOOS == "windows" {
		t.Run("windows forward-slash absolute drive path", func(t *testing.T) {
			slashSrc := filepath.ToSlash(src)
			dst := filepath.Join(t.TempDir(), "out-slash.tar")
			if err := fetchContext(t.Context(), artifact{URL: slashSrc, SHA256: sum}, dst); err != nil {
				t.Fatalf("fetchContext forward-slash drive path %q failed: %v", slashSrc, err)
			}
			got, err := os.ReadFile(dst)
			if err != nil || string(got) != string(payload) {
				t.Fatalf("copied content mismatch: err=%v got=%q", err, got)
			}
		})
	}

	t.Run("relative path preserves existing policy", func(t *testing.T) {
		prevWD, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(baseDir); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(prevWD) })

		rel := filepath.Join("sub dir 中文", "desktop 附件.tar")
		dst := filepath.Join(t.TempDir(), "out-rel.tar")
		if err := fetchContext(t.Context(), artifact{URL: rel, SHA256: sum}, dst); err != nil {
			t.Fatalf("fetchContext relative path %q failed: %v", rel, err)
		}
	})

	t.Run("missing local file returns fs error instead of https error", func(t *testing.T) {
		missing := filepath.Join(baseDir, "does-not-exist.tar")
		dst := filepath.Join(t.TempDir(), "missing.tar")
		err := fetchContext(t.Context(), artifact{URL: missing, SHA256: sum}, dst)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist, got: %v", err)
		}
		if strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("missing local file must not fail with HTTPS error: %v", err)
		}
	})

	t.Run("wrong sha256 rejected on local copy", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "wrong-sha.tar")
		err := fetchContext(t.Context(), artifact{URL: src, SHA256: wrongSum}, dst)
		if err == nil || !strings.Contains(err.Error(), "摘要不匹配") {
			t.Fatalf("expected digest mismatch error, got: %v", err)
		}
	})

	t.Run("canceled context aborts local copy", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		dst := filepath.Join(t.TempDir(), "canceled.tar")
		err := fetchContext(ctx, artifact{URL: src, SHA256: sum}, dst)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})
}

func TestFetchHTTPSContextAndRedirects(t *testing.T) {
	payload := []byte("signed https release artifact")
	sum := fmt.Sprintf("%x", sha256.Sum256(payload))
	wrongSum := fmt.Sprintf("%x", sha256.Sum256([]byte("other")))

	otherHost := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer otherHost.Close()

	plainHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer plainHTTP.Close()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.tar":
			_, _ = w.Write(payload)
		case "/same-host-redirect":
			http.Redirect(w, r, "/ok.tar", http.StatusFound)
		case "/cross-host-redirect":
			http.Redirect(w, r, otherHost.URL+"/ok.tar", http.StatusFound)
		case "/downgrade-redirect":
			http.Redirect(w, r, plainHTTP.URL+"/ok.tar", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	newClient := func() *http.Client {
		c := srv.Client()
		c.CheckRedirect = fetchRedirectPolicy
		return c
	}

	t.Run("valid https download and sha256 verified", func(t *testing.T) {
		raw := srv.URL + "/ok.tar"
		u, _ := url.Parse(raw)
		dst := filepath.Join(t.TempDir(), "ok.tar")
		if err := fetchHTTPSContext(t.Context(), newClient(), artifact{URL: raw, SHA256: sum}, u, dst); err != nil {
			t.Fatalf("valid HTTPS download failed: %v", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil || string(got) != string(payload) {
			t.Fatalf("downloaded content mismatch: err=%v got=%q", err, got)
		}
	})

	t.Run("same-host https redirect allowed", func(t *testing.T) {
		raw := srv.URL + "/same-host-redirect"
		u, _ := url.Parse(raw)
		dst := filepath.Join(t.TempDir(), "same.tar")
		if err := fetchHTTPSContext(t.Context(), newClient(), artifact{URL: raw, SHA256: sum}, u, dst); err != nil {
			t.Fatalf("same-host HTTPS redirect failed: %v", err)
		}
	})

	t.Run("https wrong sha256 rejected", func(t *testing.T) {
		raw := srv.URL + "/ok.tar"
		u, _ := url.Parse(raw)
		dst := filepath.Join(t.TempDir(), "bad-sha.tar")
		err := fetchHTTPSContext(t.Context(), newClient(), artifact{URL: raw, SHA256: wrongSum}, u, dst)
		if err == nil || !strings.Contains(err.Error(), "摘要不匹配") {
			t.Fatalf("expected digest mismatch, got: %v", err)
		}
	})

	t.Run("cross-host redirect rejected", func(t *testing.T) {
		raw := srv.URL + "/cross-host-redirect"
		u, _ := url.Parse(raw)
		dst := filepath.Join(t.TempDir(), "cross.tar")
		err := fetchHTTPSContext(t.Context(), newClient(), artifact{URL: raw, SHA256: sum}, u, dst)
		if err == nil || !strings.Contains(err.Error(), "拒绝跨主机或非 HTTPS 重定向") {
			t.Fatalf("expected cross-host redirect rejection, got: %v", err)
		}
	})

	t.Run("https to http downgrade redirect rejected", func(t *testing.T) {
		raw := srv.URL + "/downgrade-redirect"
		u, _ := url.Parse(raw)
		dst := filepath.Join(t.TempDir(), "downgrade.tar")
		err := fetchHTTPSContext(t.Context(), newClient(), artifact{URL: raw, SHA256: sum}, u, dst)
		if err == nil || !strings.Contains(err.Error(), "拒绝跨主机或非 HTTPS 重定向") {
			t.Fatalf("expected non-HTTPS redirect rejection, got: %v", err)
		}
	})
}
