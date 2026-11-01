package main

import (
	"archive/tar"
	"bytes"
	"context"
	encodingbinary "encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hansjone/auriga/deploy/bootstrap/collab/testfixture"
	"golang.org/x/sys/unix"
)

// Run the public stop/start scripts and the real bootstrap against an isolated
// Engine protocol fixture. In particular history/status/check are NOT scripts
// or fake executables: production database() sees missing/stopped/unready PG.
// A private mount namespace replaces only the child's fixed Docker socket;
// neither the real daemon, NAS, host services nor any live database is touched.
func TestNASColdStartWithRealBootstrap(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root and mount namespaces")
	}
	root, err := os.MkdirTemp("", "auriga-cold-start-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	recipe := filepath.Join(root, "recipe")
	nas, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	baseline := os.Getenv("AURIGA_TEST_COLD_BASELINE")
	if baseline != "" {
		nas = baseline
	}
	if err = filepath.WalkDir(nas, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(nas, path)
		if e != nil {
			return e
		}
		if entry.IsDir() {
			if rel == "bin" || rel == "bootstrap" || strings.Contains(rel, "__pycache__") {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(recipe, rel), 0755)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(recipe, rel), b, 0644)
	}); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bootstrap")
	if baseline != "" {
		b, e := os.ReadFile(filepath.Join(baseline, "bin/auriga-nas-bootstrap"))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(binary, b, 0755); e != nil {
			t.Fatal(e)
		}
	} else {
		if b, e := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); e != nil {
			t.Fatalf("build %v: %s", e, b)
		}
	}
	_, cfg, err := loadNASAccess("../access-ip.example.json", "192.0.2.138", "192.0.2.128/26", 15000)
	if err != nil {
		t.Fatal(err)
	}
	input := testfixture.Write(t, cfg.TenantID())
	// Existing lifecycle creates real sealed materials, root signatures, fixed
	// UID projections and receipts before exercising any restart.
	init := exec.Command("sh", filepath.Join(recipe, "test/collab.sh"), binary, root, input, "ip")
	if b, e := init.CombinedOutput(); e != nil {
		t.Fatalf("real initialization %v: %s", e, b)
	}
	data := filepath.Join(root, "srv/container/auriga-collab-ip")
	stateLock := filepath.Join(data, "config/collab-runtime/state/state.json.lock")
	if err = os.WriteFile(stateLock, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chown(stateLock, 10002, 10002); err != nil {
		t.Fatal(err)
	}
	image := "sha256:" + strings.Repeat("a", 64)
	stack := filepath.Join(data, "stack.env")
	f, err := os.OpenFile(stack, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"PLATFORM", "EDGE", "AGENT", "POSTGRES", "NATS", "REGISTRY", "LITELLM"} {
		if _, err = fmt.Fprintf(f, "AURIGA_IMAGE_%s=%s\n", service, image); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.WriteString("AURIGA_HOST_PUBLISH_NODECONTROL=17443\nAURIGA_HOST_PUBLISH_REGISTRY=15000\nAURIGA_HOST_PUBLISH_PLATFORM_HTTP=18081\n"); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	baseCmd := exec.Command(binary, "collab-base", filepath.Join(data, "config/materials"))
	baseCmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001, Groups: []uint32{10001}}}
	baseHash, err := baseCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(data, "config/collab-source/activation.receipt"))
	if err != nil {
		t.Fatal(err)
	}
	operations := filepath.Join(data, "config/collab-operations")
	for _, name := range []string{"", "releases", "requests", "installs"} {
		if err = os.Mkdir(filepath.Join(operations, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	nasRotationTestJSON(t, filepath.Join(operations, "binding.json"), nasRotationBinding{Base: string(baseHash), Source: nasRotationHash(source), Image: image, Data: data, Recipe: recipe})
	// Only unrelated LAN/UID mount probing and interactive emergency delivery are
	// omitted. Every prerequisite, material render and rotation command is real.
	for _, name := range []string{"network-preflight.sh", "mount-access.sh", "emergency.sh"} {
		if err = os.WriteFile(filepath.Join(recipe, "scripts", name), []byte("#!/bin/sh\nexit 0\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(root, "cli")
	if err = os.Mkdir(bin, 0755); err != nil {
		t.Fatal(err)
	}
	cli := `#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$COLD_CLI_LOG"
case "$1 ${2:-}" in
  'image inspect') printf 'linux/amd64\n' ;;
  'compose version'|'info '|'ps -q') exit 0 ;;
  'compose -f') case " $* " in
    *' up '*) case " $* " in
      *' collab network '*) touch "$COLD_MESSAGE_UP" ;;
      *) touch "$COLD_CORE_UP" ;;
    esac ;;
  esac ;;
  *) echo 'unexpected fixture CLI operation' >&2; exit 91 ;;
esac
`
	if err = os.WriteFile(filepath.Join(bin, "docker"), []byte(cli), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bin, "ss"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"stop-start", "pg-stopped", "pg-slow", "renew-lock", "lock-clears"} {
		t.Run(scenario, func(t *testing.T) {
			positive := scenario == "stop-start" || scenario == "pg-slow" || scenario == "lock-clears"
			if scenario == "renew-lock" || scenario == "lock-clears" {
				if err := os.RemoveAll(filepath.Join(data, "config/network-policy")); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(filepath.Join(data, "collab-tokens")); err != nil {
					t.Fatal(err)
				}
			}
			coreUp := filepath.Join(root, scenario+".core-up")
			messageUp := filepath.Join(root, scenario+".message-up")
			socket := filepath.Join(root, scenario+".sock")
			listener, e := net.Listen("unix", socket)
			if e != nil {
				t.Fatal(e)
			}
			var pgReads atomic.Int32
			var earlyReads atomic.Int32
			var healthReads atomic.Int32
			var statusCalls atomic.Int32
			var helperOutput atomic.Value
			helperOutput.Store([]byte("{}"))
			helperID := strings.Repeat("f", 64)
			networkID := strings.Repeat("c", 64)
			pg := nasRotationContainer{ID: strings.Repeat("b", 64), Image: image}
			pg.Config.Labels = map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.service": "postgres", "com.docker.compose.project.working_dir": recipe, "com.docker.compose.project.config_files": filepath.Join(recipe, "docker-compose.yml")}
			pg.State.Running = positive
			pg.State.Health.Status = "starting"
			pg.Mounts = append(pg.Mounts, struct {
				Source, Destination string
				RW                  bool
			}{filepath.Join(data, "data/postgres"), "/var/lib/postgresql/data", true})
			server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				path := strings.TrimPrefix(request.URL.Path, "/v1.41")
				switch {
				case strings.HasPrefix(path, "/images/"):
					_ = json.NewEncoder(w).Encode(map[string]string{"Id": image, "Os": "linux", "Architecture": "amd64"})
				case path == "/containers/json":
					pgReads.Add(1)
					if _, err := os.Stat(coreUp); err != nil {
						earlyReads.Add(1)
					}
					if !positive && scenario != "pg-stopped" {
						_, _ = w.Write([]byte("[]"))
					} else {
						_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": pg.ID}})
					}
				case path == "/containers/"+pg.ID+"/json":
					current := pg
					inspections := healthReads.Add(1)
					if positive && (scenario != "pg-slow" || inspections >= 4) {
						current.State.Health.Status = "healthy"
					}
					_ = json.NewEncoder(w).Encode(current)
				case path == "/networks/"+nasRotationProject+"_internal":
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": networkID, "Name": nasRotationProject + "_internal", "Driver": "bridge", "Internal": true, "Labels": map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.network": "internal"}})
				case path == "/containers/create" && request.Method == "POST":
					var body struct {
						Cmd        []string
						HostConfig struct {
							NetworkMode    string
							ReadonlyRootfs bool
						}
					}
					if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Cmd) != 7 || body.Cmd[5] != "status" || body.HostConfig.NetworkMode != networkID || !body.HostConfig.ReadonlyRootfs {
						http.Error(w, "only a read-only production status helper is allowed", 500)
						return
					}
					planRaw, err := os.ReadFile(body.Cmd[6])
					var plan nasRotationPlan
					if err != nil || json.Unmarshal(planRaw, &plan) != nil {
						http.Error(w, "missing real observer plan", 500)
						return
					}
					pair, err := os.ReadFile(plan.Expected)
					if err != nil {
						http.Error(w, "missing expected pair", 500)
						return
					}
					payload, err := nasRotationParsePayload(pair)
					if err != nil {
						http.Error(w, "invalid expected pair", 500)
						return
					}
					first := map[string]string{}
					for _, key := range payload.Keys {
						first[key.Kid] = payload.Issued
					}
					reply, err := json.Marshal(nasRotationStatus{Version: payload.Version, Time: time.Now().UTC().Format(time.RFC3339Nano), First: first})
					if err != nil {
						http.Error(w, "status encoding", 500)
						return
					}
					helperOutput.Store(reply)
					statusCalls.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]string{"Id": helperID})
				case path == "/containers/"+helperID+"/start":
					w.WriteHeader(204)
				case path == "/containers/"+helperID+"/wait":
					_ = json.NewEncoder(w).Encode(map[string]int{"StatusCode": 0})
				case path == "/containers/"+helperID+"/logs":
					data := helperOutput.Load().([]byte)
					frame := make([]byte, 8+len(data))
					frame[0] = 1
					encodingbinary.BigEndian.PutUint32(frame[4:8], uint32(len(data)))
					copy(frame[8:], data)
					_, _ = w.Write(frame)
				case path == "/containers/"+helperID && request.Method == "DELETE":
					w.WriteHeader(204)
				default:
					http.Error(w, "fixture refuses unexpected Engine operation", 500)
				}
			})}
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close() })
			if scenario == "renew-lock" || scenario == "lock-clears" {
				lock, e := os.OpenFile(filepath.Join(operations, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
				if e != nil {
					t.Fatal(e)
				}
				if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { _ = lock.Close() })
				if scenario == "lock-clears" {
					releaseCtx, releaseCancel := context.WithCancel(context.Background())
					t.Cleanup(releaseCancel)
					go func() {
						for releaseCtx.Err() == nil {
							if _, err := os.Stat(coreUp); err == nil {
								break
							}
							time.Sleep(10 * time.Millisecond)
						}
						timer := time.NewTimer(500 * time.Millisecond)
						defer timer.Stop()
						select {
						case <-releaseCtx.Done():
							return
						case <-timer.C:
							_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
						}
					}()
				}
			}
			logPath := filepath.Join(root, scenario+".cli")
			program := `mount --make-rprivate /; mount -t tmpfs tmpfs /run; touch /run/docker.sock; mount --bind "$1" /run/docker.sock; shift; exec sh -ec "$1"`
			actions := `sh "$NAS_ROOT/scripts/start.sh"`
			if scenario == "stop-start" {
				actions = `sh "$NAS_ROOT/scripts/stop.sh"; ` + actions
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "unshare", "-m", "--", "sh", "-ec", program, "sh", socket, actions)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			cmd.WaitDelay = time.Second
			cmd.Env = append(os.Environ(), "NAS_ROOT="+recipe, "AURIGA_DATA_ROOT="+data, "AURIGA_NAS_BOOTSTRAP_BINARY="+binary, "PATH="+bin+":"+os.Getenv("PATH"), "COLD_CLI_LOG="+logPath, "COLD_CORE_UP="+coreUp, "COLD_MESSAGE_UP="+messageUp, "AURIGA_TEST_MIN_MEMORY_KB=0", "AURIGA_TEST_MIN_DISK_KB=0")
			began := time.Now()
			output, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("public startup %v (%s): %s", e, time.Since(began), output)
			}
			if _, e = os.Stat(coreUp); e != nil {
				t.Fatalf("core up was not reached: %s", output)
			}
			if positive {
				_, messageErr := os.Stat(messageUp)
				if messageErr != nil || !strings.Contains(string(output), "message_network=READY") || (scenario != "lock-clears" && strings.Contains(string(output), "message_network=degraded")) {
					t.Fatalf("message network did not become READY: %s", output)
				}
				if statusCalls.Load() == 0 {
					t.Fatal("production PG observer status helper was bypassed")
				}
				if scenario == "pg-slow" && healthReads.Load() < 4 {
					t.Fatal("PG health wait was bypassed")
				}
			} else if !strings.Contains(string(output), "message_network=degraded") {
				t.Fatalf("missing isolated message degradation: %s", output)
			}
			if scenario != "renew-lock" && pgReads.Load() == 0 {
				t.Fatalf("production PG observer was bypassed: %s", output)
			}
			if earlyReads.Load() != 0 {
				t.Fatal("PG was observed before core startup")
			}
			for _, generated := range []string{"config/network-policy/runtime.json", "config/network-policy/broker.conf", "collab-tokens"} {
				if _, err := os.Stat(filepath.Join(data, generated)); err != nil {
					t.Fatalf("static projection skipped: %s: %v", generated, err)
				}
			}
			if scenario == "renew-lock" {
				// Container installation uses the same real privileged export,
				cliLog, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				for _, line := range strings.Split(string(cliLog), "\n") {
					if strings.Contains(line, " stop collab ") && !strings.Contains(line, "collab-compose.yml") {
						t.Fatal("busy stop lost message service definitions")
					}
				}
				// rather than host Compose's direct path lookup.
				helperCtx, helperCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer helperCancel()
				helper := exec.CommandContext(helperCtx, "unshare", "-m", "--", "sh", "-ec", program, "sh", socket, `NAS_INSTALLER_ACTIVE=1 sh "$NAS_ROOT/scripts/installer-entry.sh" export-env`)
				helper.Env = cmd.Env
				helper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				helper.Cancel = func() error { return syscall.Kill(-helper.Process.Pid, syscall.SIGKILL) }
				helper.WaitDelay = time.Second
				raw, err := helper.Output()
				if err != nil {
					t.Fatalf("real privileged export blocked by renewal: %v", err)
				}
				archive := tar.NewReader(bytes.NewReader(raw))
				found := false
				for {
					entry, err := archive.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if entry.Name == "collab.rotation.available" {
						value, err := io.ReadAll(archive)
						if err != nil || strings.TrimSpace(string(value)) != "0" {
							t.Fatal("busy export did not refuse unchecked message startup")
						}
						found = true
					}
				}
				if !found {
					t.Fatal("missing privileged message availability decision")
				}
			}
			t.Logf("real public startup completed in %s; PG observer reads=%d", time.Since(began), pgReads.Load())
		})
	}
}
