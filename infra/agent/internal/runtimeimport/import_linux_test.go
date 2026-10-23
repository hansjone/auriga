package runtimeimport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/runtimebundle"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func digest(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}

type fixture struct {
	opts    Options
	objects map[string][]byte
	mu      sync.Mutex
	mode    string
	custom  func(http.ResponseWriter, *http.Request) bool
	log     string
	server  *http.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{objects: map[string][]byte{}, log: filepath.Join(dir, "oras-args")}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		mode := f.mode
		custom := f.custom
		raw, ok := f.objects[r.URL.Path]
		f.mu.Unlock()
		if custom != nil && custom(w, r) {
			return
		}
		if mode == "redirect" {
			http.Redirect(w, r, "http://example.com/escape", http.StatusFound)
			return
		}
		if mode == "compressed" {
			w.Header().Set("Content-Encoding", "gzip")
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	})}
	go func() { _ = f.server.Serve(listener) }()
	t.Cleanup(func() { _ = f.server.Close(); _ = listener.Close() })
	put := func(name string, raw []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	key := put("private.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	rootRaw, err := proto.MarshalOptions{Deterministic: true}.Marshal(&nodev1.ArtifactSigningKey{Kid: "import-test", PublicKey: pub, NotAfter: timestamppb.New(time.Now().Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	root := put("root.pb", rootRaw)
	layer := []byte("one-test-layer")
	config := []byte(fmt.Sprintf(`{"os":"linux","architecture":"amd64","config":{},"rootfs":{"type":"layers","diff_ids":[%q]}}`, digest(layer)))
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","size":%d,"digest":%q},"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar","size":%d,"digest":%q}]}`, len(config), digest(config), len(layer), digest(layer)))
	image := listener.Addr().String() + "/auriga/dsh@" + digest(manifest)
	bundle := filepath.Join(dir, "bundle")
	_, err = runtimebundle.Sign(runtimebundle.SignOptions{Image: image, ArtifactID: "dsh-test", Version: "1.0.0", OS: "linux", Arch: "amd64", Sequence: 1, IssuedAt: timestamppb.New(time.Now().Add(-time.Minute)), RootPath: root, PrivateKeyPath: key, ManifestPath: put("manifest", manifest), ConfigPath: put("config", config), OutputDir: bundle})
	if err != nil {
		t.Fatal(err)
	}
	layout := filepath.Join(dir, "layout")
	if err := os.MkdirAll(filepath.Join(layout, "blobs", "sha256"), 0o700); err != nil {
		t.Fatal(err)
	}
	put("layout/oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	put("layout/index.json", []byte(fmt.Sprintf(`{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d}]}`, digest(manifest), len(manifest))))
	for _, b := range [][]byte{manifest, config, layer} {
		put("layout/blobs/sha256/"+strings.TrimPrefix(digest(b), "sha256:"), b)
	}
	base := "/v2/auriga/dsh/"
	f.objects[base+"manifests/"+digest(manifest)] = manifest
	f.objects[base+"blobs/"+digest(config)] = config
	f.objects[base+"blobs/"+digest(layer)] = layer
	oras := put("fake-oras", []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf 'Version: 1.3.0\\n'; exit 0; fi\n[ \"$5\" = \"$7\" ] || exit 3\n[ \"$HOME\" = \"$DOCKER_CONFIG\" ] || exit 4\n[ \"$HOME\" = \"$XDG_CONFIG_HOME\" ] || exit 5\n[ -z \"${HTTP_PROXY+x}\" ] || exit 6\nIFS= read -r auth < \"$5\"\n[ \"$auth\" = '{}' ] || exit 7\nprintf '%s\\n' \"$@\" > '"+f.log+"'\nexit 0\n"))
	if err := os.Chmod(oras, 0o700); err != nil {
		t.Fatal(err)
	}
	f.opts = Options{Stage: runtimebundle.StageLayoutOptions{Verify: runtimebundle.VerifyOptions{Dir: bundle, TrustedRootPath: root, Policy: runtimebundle.Policy{OS: "linux", Arch: "amd64", AllowedImage: image}}, LayoutDir: layout, OutputDir: filepath.Join(dir, "staged"), MaxBytes: int64(len(manifest) + len(config) + len(layer))}, ORAS: oras}
	return f
}

func TestImportReadbackAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "missing", "bad-layer", "wrong-size", "redirect", "compressed", "wrong-manifest"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.mu.Lock()
			f.mode = mode
			for path, raw := range f.objects {
				switch mode {
				case "missing":
					if strings.Contains(path, "/blobs/") && bytes.Equal(raw, []byte("one-test-layer")) {
						delete(f.objects, path)
					}
				case "bad-layer":
					if bytes.Equal(raw, []byte("one-test-layer")) {
						f.objects[path] = []byte("two-test-layer")
					}
				case "wrong-size":
					if bytes.Equal(raw, []byte("one-test-layer")) {
						f.objects[path] = append(bytes.Clone(raw), 'x')
					}
				case "wrong-manifest":
					if strings.Contains(path, "/manifests/") {
						f.objects[path] = bytes.Repeat([]byte("x"), len(raw))
					}
				}
			}
			f.mu.Unlock()
			s, err := Import(context.Background(), f.opts)
			if (err == nil) != (mode == "success") {
				t.Fatalf("mode=%s snapshot=%+v err=%v", mode, s, err)
			}
			args, readErr := os.ReadFile(f.log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Contains(args, []byte("--from-oci-layout")) || !bytes.Contains(args, []byte("--to-plain-http")) || !bytes.Contains(args, []byte("@"+s.ManifestDigest)) && mode == "success" {
				t.Fatalf("unexpected ORAS arguments: %s", args)
			}
		})
	}
}

func TestImportRejectsBeforeORAS(t *testing.T) {
	for _, mode := range []string{"origin", "repo", "port", "budget", "signature"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			switch mode {
			case "origin":
				f.opts.Stage.Verify.Policy.AllowedImage = strings.Replace(f.opts.Stage.Verify.Policy.AllowedImage, "127.0.0.1", "localhost", 1)
			case "repo":
				f.opts.Stage.Verify.Policy.AllowedImage = strings.Replace(f.opts.Stage.Verify.Policy.AllowedImage, "/auriga/dsh@", "/other/dsh@", 1)
			case "port":
				f.opts.Stage.Verify.Policy.AllowedImage = strings.Replace(f.opts.Stage.Verify.Policy.AllowedImage, "127.0.0.1:", "127.0.0.1:0", 1)
			case "budget":
				f.opts.Stage.MaxBytes--
			case "signature":
				p := filepath.Join(f.opts.Stage.Verify.Dir, "runtime.pb")
				raw, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				raw[len(raw)-1] ^= 1
				if err := os.WriteFile(p, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Import(context.Background(), f.opts); err == nil {
				t.Fatal("accepted invalid input")
			}
			if _, err := os.Stat(f.log); !os.IsNotExist(err) {
				t.Fatalf("ORAS executed: %v", err)
			}
		})
	}
}

func TestImportORASFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"fail", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf 'Version: 1.3.0\\n'; exit 0; fi\n"
			if mode == "fail" {
				script += "exit 7\n"
			} else {
				script += "/bin/sleep 30 &\nwait\n"
			}
			if err := os.WriteFile(f.opts.ORAS, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if mode == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			start := time.Now()
			if _, err := Import(ctx, f.opts); err == nil {
				t.Fatal("ORAS failure accepted")
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("cancellation failed to collect child")
			}
		})
	}
}

func TestBoundedOutputBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		chunks   []int
		exceeded bool
	}{
		{"single-3000", []int{3000}, false},
		{"exact-4096", []int{4096}, false},
		{"single-4097", []int{4097}, true},
		{"split-exact", []int{3000, 1096}, false},
		{"split-over", []int{3000, 1097}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &boundedOutput{limit: 4096}
			for _, n := range tc.chunks {
				if wrote, err := out.Write(bytes.Repeat([]byte{'x'}, n)); err != nil || wrote != n {
					t.Fatalf("write=%d err=%v", wrote, err)
				}
			}
			if out.exceeded != tc.exceeded || out.buf.Len() != min(sum(tc.chunks), 4096) {
				t.Fatalf("len=%d exceeded=%t", out.buf.Len(), out.exceeded)
			}
		})
	}
}

func sum(v []int) int {
	n := 0
	for _, x := range v {
		n += x
	}
	return n
}

func TestImportCLISignalCollectsORAS(t *testing.T) {
	f := newFixture(t)
	bin := filepath.Join(t.TempDir(), "runtime-release")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/runtime-release")
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, raw)
	}
	marker := filepath.Join(t.TempDir(), "oras-started")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then printf 'Version: 1.3.0\\n'; exit 0; fi\nprintf ready > '" + marker + "'\n/bin/sleep 30 &\nwait\n"
	if err := os.WriteFile(f.opts.ORAS, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	o := f.opts.Stage
	args := []string{"import-layout", "--dir", o.Verify.Dir, "--root", o.Verify.TrustedRootPath, "--os", o.Verify.Policy.OS, "--arch", o.Verify.Policy.Arch, "--allow-image", o.Verify.Policy.AllowedImage, "--layout", o.LayoutDir, "--out", o.OutputDir, "--max-bytes", fmt.Sprint(o.MaxBytes), "--oras", f.opts.ORAS}
	cmd := exec.Command(bin, args...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatal("ORAS did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil || bytes.Contains(output.Bytes(), []byte("imported and read back")) {
			t.Fatalf("signal incorrectly succeeded: %v %s", err, output.Bytes())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("CLI did not collect ORAS after SIGTERM")
	}
}
