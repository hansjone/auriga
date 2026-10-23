package runtimeimport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The approved fake executable uses this test binary as its HTTP client.
// It deliberately follows the same response-controlled URLs that exposed M1.
func init() {
	if len(os.Args) > 4 && os.Args[1] == "transport-helper" {
		os.Exit(transportHelper(os.Args[2], os.Args[3], os.Args[4], os.Args[5:]))
	}
}

func transportHelper(mode, other, probeDigest string, args []string) int {
	if len(args) < 2 || args[0] != "cp" {
		return 2
	}
	target := args[len(args)-1]
	tag := strings.LastIndexByte(target, ':')
	if tag < 0 {
		return 3
	}
	target = target[:tag]
	slash := strings.IndexByte(target, '/')
	if slash < 0 {
		return 4
	}
	base := "http://" + target[:slash] + "/v2/" + target[slash+1:]
	client := &http.Client{Timeout: 3 * time.Second}
	if mode == "redirect" || mode == "bearer" {
		req, err := http.NewRequest(http.MethodHead, base+"/blobs/"+probeDigest, nil)
		if err != nil {
			return 5
		}
		res, err := client.Do(req)
		if err != nil {
			return 6
		}
		_ = res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized {
			challenge := res.Header.Get("WWW-Authenticate")
			start := strings.Index(challenge, `realm="`)
			if start >= 0 {
				realm := strings.SplitN(challenge[start+7:], `"`, 2)[0]
				got, getErr := client.Get(realm)
				if getErr == nil {
					_ = got.Body.Close()
				}
			}
		}
		return 7
	}
	res, err := client.Post(base+"/blobs/uploads/", "application/octet-stream", nil)
	if err != nil {
		return 8
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusAccepted || res.Header.Get("Location") == "" {
		return 9
	}
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		return 10
	}
	query := location.Query()
	query.Set("digest", probeDigest)
	location.RawQuery = query.Encode()
	req, err := http.NewRequest(http.MethodPut, location.String(), strings.NewReader("payload"))
	if err != nil {
		return 11
	}
	got, err := client.Do(req)
	if err != nil {
		return 12
	}
	_, _ = io.Copy(io.Discard, got.Body)
	_ = got.Body.Close()
	if got.StatusCode != http.StatusCreated {
		return 13
	}
	_ = other // passed only as the unapproved receiver in test setup
	return 0
}

func testLoopbackServer(t *testing.T, handler http.HandlerFunc) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return "http://" + listener.Addr().String(), func() { _ = server.Close(); _ = listener.Close() }
}

func TestTransportBoundaryTwoLoopbacks(t *testing.T) {
	for _, mode := range []string{"redirect", "bearer", "cross-location", "cross-repo", "encoded-location", "dot-location", "authority-location", "same-relative", "same-absolute"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			var otherRequests, otherWrites, approvedPuts atomic.Int64
			other, closeOther := testLoopbackServer(t, func(w http.ResponseWriter, r *http.Request) {
				otherRequests.Add(1)
				if r.Method == http.MethodPut || r.Method == http.MethodPost || r.Method == http.MethodPatch {
					otherWrites.Add(1)
				}
				w.WriteHeader(http.StatusCreated)
			})
			defer closeOther()
			approved := strings.SplitN(f.opts.Stage.Verify.Policy.AllowedImage, "/", 2)[0]
			upload := "/v2/auriga/dsh/blobs/uploads/upload-id"
			location := upload
			switch mode {
			case "cross-location":
				location = other + "/upload"
			case "cross-repo":
				location = "http://" + approved + "/v2/auriga/other/blobs/uploads/upload-id"
			case "encoded-location":
				location = "http://" + approved + "/v2/auriga/dsh/blobs/uploads/%2e%2e/upload-id"
			case "dot-location":
				location = "../uploads/upload-id"
			case "authority-location":
				location = "//" + strings.TrimPrefix(other, "http://") + "/upload"
			case "same-absolute":
				location = "http://" + approved + upload
			}
			f.mu.Lock()
			f.custom = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodHead && strings.Contains(r.URL.Path, "/blobs/") {
					switch mode {
					case "redirect":
						w.Header().Set("Location", other+"/redirect")
						w.WriteHeader(http.StatusTemporaryRedirect)
					case "bearer":
						w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm=%q`, other+"/token"))
						w.WriteHeader(http.StatusUnauthorized)
					default:
						w.WriteHeader(http.StatusNotFound)
					}
					return true
				}
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/blobs/uploads/") {
					w.Header().Set("Location", location)
					w.WriteHeader(http.StatusAccepted)
					return true
				}
				if r.Method == http.MethodPut && r.URL.Path == upload {
					approvedPuts.Add(1)
					w.Header().Set("Location", "http://"+approved+"/v2/auriga/dsh/blobs/"+strings.Split(f.opts.Stage.Verify.Policy.AllowedImage, "@")[1])
					w.WriteHeader(http.StatusCreated)
					return true
				}
				return false
			}
			f.mu.Unlock()
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			probe := strings.Split(f.opts.Stage.Verify.Policy.AllowedImage, "@")[1]
			script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf 'Version: 1.3.0\\n'; exit 0; fi\nexec '" + self + "' transport-helper '" + mode + "' '" + other + "' '" + probe + "' \"$@\"\n"
			if err := os.WriteFile(f.opts.ORAS, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			_, err = Import(context.Background(), f.opts)
			wantSuccess := mode == "same-relative" || mode == "same-absolute"
			if (err == nil) != wantSuccess {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
			if otherRequests.Load() != 0 || otherWrites.Load() != 0 {
				t.Fatalf("unapproved receiver got %d requests, %d writes", otherRequests.Load(), otherWrites.Load())
			}
			if wantSuccess && approvedPuts.Load() != 1 {
				t.Fatalf("approved upload PUTs=%d", approvedPuts.Load())
			}
			if !wantSuccess && approvedPuts.Load() != 0 {
				t.Fatalf("blocked case wrote approved upload: %d", approvedPuts.Load())
			}
		})
	}
}

func TestRunORASKillsDescendantsAfterParentExit(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "descendant-alive")
	script := filepath.Join(dir, "fake-oras")
	body := "#!/bin/sh\n/bin/sh -c '/bin/sleep 2; printf alive > \"" + marker + "\"' &\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := runORAS(ctx, script, nil, "cp")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("parent exit handling: %v", err)
	}
	time.Sleep(2300 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("descendant survived group cleanup: %v", err)
	}
}
