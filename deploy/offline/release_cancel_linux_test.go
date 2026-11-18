package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestReleaseCancellationDuringExternalWork(t *testing.T) {
	for _, phase := range []string{"prepare-download", "bundle-download", "bundle-docker"} {
		for _, interrupted := range []os.Signal{os.Interrupt, syscall.SIGTERM, nil} {
			name := "ordinary-failure"
			if interrupted != nil {
				name = interrupted.String()
			}
			t.Run(phase+"/"+name, func(t *testing.T) {
				root := t.TempDir()
				source := filepath.Join(root, "source")
				bin := filepath.Join(root, "bin")
				scratch := filepath.Join(root, "scratch")
				for _, dir := range []string{source, bin, scratch} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module fixture\n"), 0644); err != nil {
					t.Fatal(err)
				}
				for _, args := range [][]string{{"init"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.com"}, {"add", "."}, {"commit", "-m", "fixture"}, {"tag", "1.0"}} {
					if _, err := gitAt(source, args...); err != nil {
						t.Fatal(err)
					}
				}
				single := fixtureSingleKit(t, "1.0", "a")
				kit, err := readDevkit(single)
				if err != nil {
					t.Fatal(err)
				}
				t.Chdir(source)
				t.Setenv("TMPDIR", scratch)
				t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
				t.Setenv("DOCKER_HOST", "unix:///fixture.sock")
				t.Setenv("DOCKER_CONTEXT", "")
				t.Setenv("RELEASE_CANCEL_ROOT", root)
				t.Setenv("RELEASE_CANCEL_PHASE", phase)
				writeGateProgram(t, filepath.Join(bin, "docker"), `if test "$1" = info && test "$RELEASE_CANCEL_PHASE" = bundle-docker && test -f "$RELEASE_CANCEL_ROOT/gate-passed"; then
  printf '%s\n' "$$" >"$RELEASE_CANCEL_ROOT/docker.pid"
  exec sleep 30
fi
case "$1" in
info) printf '29.0\n' ;;
compose) printf '2.29.2\n' ;;
context) printf 'unix:///fixture.sock\n' ;;
*) exit 71 ;;
esac
`)
				blocked := make(chan struct{})
				release := make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				started := sync.OnceFunc(func() { close(blocked) })
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					started()
					select {
					case <-request.Context().Done():
					case <-release:
					}
					http.Error(w, "fixture released", http.StatusServiceUnavailable)
				}))
				defer server.Close()
				defer unblock()
				previousTransport := http.DefaultTransport
				http.DefaultTransport = server.Client().Transport
				defer func() { http.DefaultTransport = previousTransport }()
				key, _ := testSigningKeys(t)
				r := kit.Release
				r.NAS.SHA256, err = digest(filepath.Join(single, "cache", "nas.tar"))
				if err != nil {
					t.Fatal(err)
				}
				kit.Release.NAS.SHA256 = r.NAS.SHA256
				r.NAS.URL = server.URL + "/nas"
				if phase == "prepare-download" {
					r.Build.Inputs.URL = server.URL + "/inputs"
					r.Build.Recipes.URL = server.URL + "/recipes"
				}
				index := filepath.Join(root, "release.json")
				if err := jsonFile(index, r, 0644); err != nil {
					t.Fatal(err)
				}
				builders := releaseBuilders{}
				if phase != "prepare-download" {
					builders.Devkit = func(args []string) error {
						locks, err := dependencyLocks(source, r)
						if err != nil {
							return err
						}
						kit.Locks = &locks
						if err := jsonFile(filepath.Join(single, "devkit.json"), kit, 0644); err != nil {
							return err
						}
						if err := signingPublic(key, filepath.Join(single, "release-public.pem")); err != nil {
							return err
						}
						if err := writeSums(single); err != nil {
							return err
						}
						if err := signSums(single, key); err != nil {
							return err
						}
						return pack(single, flagValue(args, "--out"))
					}
				}
				builders.Gate = func(request gateRequest) (buildGate, error) {
					if err := os.WriteFile(filepath.Join(root, "gate-passed"), nil, 0600); err != nil {
						return buildGate{}, err
					}
					return buildGate{Passed: true, Commit: request.Commit, LockDigest: request.LockDigest, SourceSHA: request.SourceSHA, CatalogSHA: request.CatalogSHA, DevkitSHA: request.DevkitSHA, OutputSHA: strings.Repeat("a", 64), Network: gateNetwork}, nil
				}
				builders.Executable = filepath.Join(root, "cli")
				if err := os.WriteFile(builders.Executable, []byte("fixture"), 0755); err != nil {
					t.Fatal(err)
				}
				opts := releaseOptions{Source: source, Tag: "1.0", Index: index, Out: filepath.Join(root, "out"), Cache: filepath.Join(root, "cache"), Key: key, Limit: 1 << 20, NewBaseline: true}
				observed := make(chan os.Signal, 1)
				if interrupted != nil {
					signal.Notify(observed, interrupted)
				}
				defer signal.Stop(observed)
				done := make(chan error, 1)
				go func() { done <- generateRelease(opts, builders) }()
				if phase == "bundle-docker" {
					deadline := time.After(5 * time.Second)
					for {
						if _, err := os.Stat(filepath.Join(root, "docker.pid")); err == nil {
							break
						}
						select {
						case err := <-done:
							t.Fatalf("release exited before Docker: %v", err)
						case <-deadline:
							t.Fatal("Docker fixture did not start")
						case <-time.After(10 * time.Millisecond):
						}
					}
				} else {
					select {
					case <-blocked:
					case err := <-done:
						t.Fatalf("release exited before download: %v", err)
					case <-time.After(5 * time.Second):
						t.Fatal("download did not start")
					}
				}
				if interrupted != nil {
					if err := syscall.Kill(os.Getpid(), interrupted.(syscall.Signal)); err != nil {
						t.Fatal(err)
					}
					<-observed
				} else {
					unblock()
					if phase == "bundle-docker" {
						pid := waitGatePID(t, filepath.Join(root, "docker.pid"))
						if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
							t.Fatal(err)
						}
					}
				}
				select {
				case err := <-done:
					if interrupted == nil {
						if err == nil || errors.Is(err, context.Canceled) {
							t.Errorf("ordinary failure was misclassified: %v", err)
						}
						expected := "HTTP 503"
						if phase == "bundle-docker" {
							expected = "本机 Docker Engine 不可用"
						}
						if err != nil && !strings.Contains(err.Error(), expected) {
							t.Errorf("ordinary failure diagnostic changed: %v", err)
						}
					} else if !errors.Is(err, context.Canceled) {
						t.Errorf("cancellation lost: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Error("release kept waiting after cancellation")
					unblock()
					if phase == "bundle-docker" {
						pid := waitGatePID(t, filepath.Join(root, "docker.pid"))
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
					<-done
				}
				if _, err := os.Stat(opts.Out); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("signed output appeared: %v", err)
				}
				entries, err := os.ReadDir(scratch)
				if err != nil || len(entries) != 0 {
					t.Errorf("release left scratch data %v %v", entries, err)
				}
				for directory, prefix := range map[string]string{root: ".auriga-release-", opts.Cache: ".devkit-"} {
					entries, err := os.ReadDir(directory)
					if err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), prefix) {
							t.Errorf("release left incomplete staging data: %s", entry.Name())
						}
					}
				}
				if phase == "bundle-docker" {
					pid := waitGatePID(t, filepath.Join(root, "docker.pid"))
					if gateProcessRunning(pid) {
						t.Error("release left its Docker process running")
					}
				}
			})
		}
	}
}
