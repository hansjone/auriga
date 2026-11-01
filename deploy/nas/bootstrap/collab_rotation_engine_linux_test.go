package main

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNASRotationDockerOperatorScopeAndCleanup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("protected helper plans require Linux root")
	}
	for _, action := range []string{"verify", "check-private", "preflight", "publish", "install", "status", "preflight-recovery", "recover-expired"} {
		for _, exit := range []int{0, 1} {
			t.Run(action+string(rune('0'+exit)), func(t *testing.T) {
				f := nasRotationTestFixture(t, "ip")
				r := f.r
				r.recipe = t.TempDir()
				r.binding.Recipe = r.recipe
				jar := []byte("fixture-only-fixed-operator-jar")
				nasRotationTestWrite(t, filepath.Join(r.recipe, "bin/collab-key-operator.jar"), jar)
				r.binding.Jar = nasRotationHash(jar)
				work := t.TempDir()
				id := strings.Repeat("e", 64)
				removed := false
				created := false
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					p := strings.TrimPrefix(req.URL.Path, "/v1.41")
					switch {
					case p == "/containers/json":
						_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": strings.Repeat("5", 64)}})
					case p == "/containers/"+strings.Repeat("5", 64)+"/json":
						var pg nasRotationContainer
						pg.ID = strings.Repeat("5", 64)
						pg.Image = "sha256:" + strings.Repeat("f", 64)
						pg.Config.Labels = map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.service": "postgres", "com.docker.compose.project.working_dir": r.recipe, "com.docker.compose.project.config_files": filepath.Join(r.recipe, "docker-compose.yml")}
						pg.State.Running = true
						pg.State.Health.Status = "healthy"
						b, _ := json.Marshal([]map[string]any{{"Source": filepath.Join(r.data, "data/postgres"), "Destination": "/var/lib/postgresql/data", "RW": true}})
						_ = json.Unmarshal(b, &pg.Mounts)
						_ = json.NewEncoder(w).Encode(pg)
					case p == "/networks/"+nasRotationProject+"_internal":
						_ = json.NewEncoder(w).Encode(map[string]any{"Id": strings.Repeat("d", 64), "Name": nasRotationProject + "_internal", "Driver": "bridge", "Internal": true, "Labels": map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.network": "internal"}})
					case p == "/containers/create":
						var body map[string]any
						if json.NewDecoder(req.Body).Decode(&body) != nil {
							t.Error("bad helper request")
						}
						host := body["HostConfig"].(map[string]any)
						network := host["NetworkMode"].(string)
						if action == "verify" || action == "check-private" {
							if network != "none" {
								t.Error("offline verification gained a network")
							}
						} else if network != strings.Repeat("d", 64) {
							t.Error("operator did not use checked private database network")
						}
						if body["Image"] != r.binding.Image || body["User"] != "0:0" || host["ReadonlyRootfs"] != true {
							t.Error("helper scope changed")
						}
						cmd := body["Cmd"].([]any)
						if cmd[0] != "-Dloader.main=auriga.collab.network.keys.NetworkKeyOperator" || cmd[3] != "/opt/auriga/key-operator.jar" || cmd[5] != action {
							t.Error("arbitrary Java invocation")
						}
						for _, mount := range host["Mounts"].([]any) {
							m := mount.(map[string]any)
							if m["Source"] == r.data && m["ReadOnly"] != true {
								t.Error("helper gained writable installation root")
							}
							if m["Source"] == filepath.Dir(r.state) {
								want := action != "publish" && action != "install" && action != "recover-expired"
								if m["ReadOnly"] != want {
									t.Error("state write scope changed")
								}
							}
						}
						created = true
						_ = json.NewEncoder(w).Encode(map[string]string{"Id": id})
					case p == "/containers/"+id+"/start":
						w.WriteHeader(204)
					case p == "/containers/"+id+"/wait":
						_ = json.NewEncoder(w).Encode(map[string]int{"StatusCode": exit})
					case p == "/containers/"+id+"/logs":
						frame := make([]byte, 10)
						frame[0] = 1
						binary.BigEndian.PutUint32(frame[4:8], 2)
						copy(frame[8:], "{}")
						_, _ = w.Write(frame)
					case p == "/containers/"+id && req.Method == "DELETE":
						removed = true
						w.WriteHeader(204)
					default:
						http.Error(w, "unexpected helper fixture request", 500)
					}
				}))
				defer server.Close()
				engine := &nasRotationEngine{client: &http.Client{Transport: roundTripNASRotation{target: server.URL, base: server.Client().Transport}}}
				b, err := engine.operator(r, action, work, nasRotationPlan{State: r.state})
				if (err == nil) != (exit == 0) {
					t.Fatalf("wrong exit result: %v", err)
				}
				if exit == 0 && string(b) != "{}" {
					t.Fatal("stdout frame not decoded")
				}
				if !created || !removed {
					t.Fatal("owned helper cleanup missing")
				}
			})
		}
	}
}

func TestNASRotationLockAndPendingCannotBeBypassed(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root storage lock fixture")
	}
	r := &nasRotation{root: t.TempDir(), lockWait: 30 * time.Millisecond}
	if err := nasRotationLocked(r, func() error {
		if err := nasRotationLocked(r, func() error { return nil }); err == nil {
			t.Fatal("concurrent lock admitted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(r.root, ".pending"), 0700); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := nasRotationLocked(r, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("pending operation bypassed")
	}
}

func TestNASRotationLogsAreBoundedAndNeverMixStderr(t *testing.T) {
	frame := make([]byte, 10)
	frame[0] = 2
	binary.BigEndian.PutUint32(frame[4:8], 2)
	copy(frame[8:], "xx")
	if b, e := nasRotationDockerLogs(frame); e != nil || len(b) != 0 {
		t.Fatal("stderr escaped into operator result")
	}
	for _, bad := range [][]byte{{1}, make([]byte, 9)} {
		if _, e := nasRotationDockerLogs(bad); e == nil {
			t.Fatal("invalid frame accepted")
		}
	}
}

func TestNASRotationJarPinAndMetadata(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned operator fixture")
	}
	for _, bad := range []string{"none", "digest", "writable", "hardlink", "symlink", "owner"} {
		t.Run(bad, func(t *testing.T) {
			recipe := t.TempDir()
			path := filepath.Join(recipe, "bin/collab-key-operator.jar")
			b := []byte("fixture-only-operator")
			nasRotationTestWrite(t, path, b)
			pin := nasRotationHash(b)
			switch bad {
			case "digest":
				pin = strings.Repeat("a", 64)
			case "writable":
				if e := os.Chmod(path, 0660); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(path, path+".other"); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Rename(path, path+".other"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(path+".other", path); e != nil {
					t.Fatal(e)
				}
			case "owner":
				if e := os.Chown(path, 10002, 10002); e != nil {
					t.Fatal(e)
				}
			}
			if e := nasRotationJar(recipe, pin); (e == nil) != (bad == "none") {
				t.Fatalf("pin/metadata: %v", e)
			}
		})
	}
}
