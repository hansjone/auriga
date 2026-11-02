package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The real base verifier deliberately copies its running executable and drops
// to the fixed installer UID. In these in-process Engine tests that executable
// is the test binary, so preserve the production subcommand dispatch as well.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "collab-base" {
		if err := nasCollabCommand(os.Args[1:]); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Called by the real fixed-UID installer/CLI lifecycle after an actual sealed
// legacy upgrade and collab activation. Only the Docker Engine is a fake.
func testNASBifrostRotationControlScope(t *testing.T, config string) {
	t.Helper()
	// A read-only scheduler must be able to validate an activation while another
	// reader holds its lock, and a missing lock must never be implicitly repaired.
	lockPath := filepath.Join(config, "materials-upgrades/.lock")
	fd, err := unix.Open(lockPath, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(fd, unix.LOCK_SH); err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	checked := make(chan error, 1)
	go func() { checked <- checkNASBifrostActivation(config) }()
	var checkErr error
	timedOut := false
	select {
	case checkErr = <-checked:
	case <-time.After(5 * time.Second):
		timedOut = true
	}
	if err := unix.Flock(fd, unix.LOCK_UN); err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	if err := unix.Close(fd); err != nil {
		t.Fatal(err)
	}
	if timedOut {
		checkErr = <-checked
		t.Fatalf("read-only Bifrost validation required an exclusive write lock: %v", checkErr)
	}
	if checkErr != nil {
		t.Fatal(checkErr)
	}
	savedLock := lockPath + ".fixture-saved"
	if err := os.Rename(lockPath, savedLock); err != nil {
		t.Fatal(err)
	}
	checkErr = checkNASBifrostActivation(config)
	_, lockErr := os.Lstat(lockPath)
	if err := os.Rename(savedLock, lockPath); err != nil {
		t.Fatal(err)
	}
	if checkErr == nil || !os.IsNotExist(lockErr) {
		t.Fatal("read-only Bifrost validation recreated a missing lock")
	}
	r := &nasRotation{data: filepath.Dir(config), recipe: "/srv/recipe"}
	r.root = filepath.Join(config, "collab-operations")
	r.state = filepath.Join(config, "collab-runtime/state/state.json")
	r.binding.Image = "sha256:" + strings.Repeat("c", 64)
	containers := map[string]nasRotationContainer{}
	overlay := filepath.Join(filepath.Dir(r.data), ".auriga-compose.fixture123", "bifrost.yml")
	files := filepath.Join(r.recipe, "docker-compose.yml") + "," + overlay
	for i, service := range []string{"collab", "platform", "edge", "network", "postgres"} {
		c := nasRotationContainer{ID: strings.Repeat(fmt.Sprint(i+1), 64), Image: r.binding.Image}
		c.Config.Labels = map[string]string{
			"com.docker.compose.project": nasRotationProject, "com.docker.compose.service": service,
			"com.docker.compose.project.working_dir": r.recipe, "com.docker.compose.project.config_files": files,
		}
		c.State.Running, c.State.Pid, c.State.Health.Status = true, i+1, "healthy"
		mounts := []map[string]any{}
		add := func(source, target string, rw bool) {
			mounts = append(mounts, map[string]any{"Source": source, "Destination": target, "RW": rw})
		}
		switch service {
		case "collab":
			add(filepath.Join(config, "collab-runtime/collab"), "/etc/auriga/collab", false)
			add(filepath.Dir(r.state), "/var/lib/auriga/collab", true)
		case "platform":
			add(filepath.Join(config, "collab-runtime/platform"), "/etc/auriga/network", false)
		case "edge":
			c.Name = "/auriga-nas-test-edge"
			add(filepath.Join(config, "bifrost/edge.config.json"), "/etc/auriga/edge.config.json", false)
		case "network":
			add(filepath.Join(config, "collab-runtime/network/protocol.key"), "/etc/auriga/network/protocol.key", false)
			add(filepath.Join(config, "network-policy/runtime.json"), "/etc/auriga/network/runtime.json", false)
		case "postgres":
			add(filepath.Join(r.data, "data/postgres"), "/var/lib/postgresql/data", true)
		}
		b, err := json.Marshal(mounts)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &c.Mounts); err != nil {
			t.Fatal(err)
		}
		containers[c.ID] = c
	}
	stops := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/v1.41")
		switch {
		case path == "/containers/json":
			ids := []map[string]string{}
			for id := range containers {
				ids = append(ids, map[string]string{"Id": id})
			}
			_ = json.NewEncoder(w).Encode(ids)
		case strings.HasPrefix(path, "/containers/"):
			parts := strings.Split(strings.TrimPrefix(path, "/containers/"), "/")
			c, exists := containers[parts[0]]
			if !exists || len(parts) != 2 {
				http.Error(w, "unknown fixture container", 404)
				return
			}
			if req.Method == "GET" && parts[1] == "json" {
				_ = json.NewEncoder(w).Encode(c)
			} else if req.Method == "POST" && parts[1] == "stop" {
				stops++
				c.State.Running, c.State.Pid = false, 0
				containers[c.ID] = c
				w.WriteHeader(204)
			} else {
				http.Error(w, "unexpected fixture mutation", 500)
			}
		case path == "/networks/"+nasRotationProject+"_internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": strings.Repeat("d", 64), "Name": nasRotationProject + "_internal", "Driver": "bridge", "Internal": true, "Labels": map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.network": "internal"}})
		default:
			http.Error(w, "unexpected fixture request", 500)
		}
	}))
	defer server.Close()
	e := &nasRotationEngine{client: &http.Client{Transport: roundTripNASRotation{target: server.URL, base: server.Client().Transport}}}
	if _, err := e.database(r); err != nil {
		t.Fatalf("public Bifrost upgrade rejected its own database: %v", err)
	}
	if err := e.checkRecoveryEdge(r); err != nil {
		t.Fatalf("public Bifrost upgrade rejected its own recovery edge: %v", err)
	}
	for _, bad := range []string{
		"/outside/bifrost.yml",
		filepath.Join(filepath.Dir(r.data), ".auriga-compose.fixture123", "sub", "bifrost.yml"),
		filepath.Join(filepath.Dir(r.data), ".auriga-compose.", "bifrost.yml"),
		filepath.Dir(overlay) + "/../bifrost.yml",
		filepath.Join(filepath.Dir(overlay), "foreign.yml"),
	} {
		c := containers[strings.Repeat("5", 64)]
		c.Config.Labels["com.docker.compose.project.config_files"] = filepath.Join(r.recipe, "docker-compose.yml") + "," + bad
		if err := e.owned(r, c); err == nil {
			t.Fatalf("foreign Bifrost overlay accepted: %s", bad)
		}
		c.Config.Labels["com.docker.compose.project.config_files"] = files
	}
	projection := filepath.Join(config, "bifrost/edge.config.json")
	original, err := os.ReadFile(projection)
	if err != nil {
		t.Fatal(err)
	}
	rejectEdge := func(name string) {
		t.Helper()
		if err := e.checkRecoveryEdge(r); err == nil {
			t.Fatalf("recovery accepted %s", name)
		}
		if err := e.stop(r); err == nil {
			t.Fatalf("rotation accepted %s", name)
		}
		if stops != 0 {
			t.Fatalf("rejected %s changed a signer process", name)
		}
	}
	edgeID := strings.Repeat("3", 64)
	c := containers[edgeID]
	c.Mounts[0].RW = true
	containers[edgeID] = c
	rejectEdge("writable Bifrost config")
	c.Mounts[0].RW = false
	c.Mounts[0].Source = filepath.Join(config, "elsewhere/edge.config.json")
	containers[edgeID] = c
	rejectEdge("foreign config mount")
	c.Mounts[0].Source = projection
	containers[edgeID] = c
	if err := os.WriteFile(projection, []byte(`{"relay":{"origin":"http://foreign:8080"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	rejectEdge("unsealed projection contents")
	if err := os.WriteFile(projection, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(projection, 0664); err != nil {
		t.Fatal(err)
	}
	rejectEdge("writable projection metadata")
	if err := os.Chmod(projection, 0644); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(config, "materials-upgrades", bifrostGeneration, "bifrost/member_password")
	sealed, err := os.ReadFile(leaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leaf, append(append([]byte{}, sealed...), 'x'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.database(r); err == nil {
		t.Fatal("overlay accepted an invalid materials generation")
	}
	if err := os.WriteFile(leaf, sealed, 0600); err != nil {
		t.Fatal(err)
	}
	// A genuine core-only projection is valid for message recovery, but normal
	// rotation must not stop active consumers using a missing message projection.
	base := filepath.Join(config, "materials")
	generation := filepath.Join(config, "materials-upgrades", bifrostGeneration)
	if err := projectBifrostEdge(base, generation, true); err != nil {
		t.Fatal(err)
	}
	if err := e.checkRecoveryEdge(r); err != nil {
		t.Fatalf("recovery rejected a sealed core-only Bifrost edge: %v", err)
	}
	if err := e.stop(r); err == nil || stops != 0 {
		t.Fatal("normal rotation accepted a core-only projection or changed a signer")
	}
	if err := projectBifrostEdge(base, generation, false); err != nil {
		t.Fatal(err)
	}
	if err := e.stop(r); err != nil {
		t.Fatalf("public Bifrost upgrade rejected renewal stop: %v", err)
	}
	if stops != 2 {
		t.Fatal("renewal did not stop exactly the checked message consumers")
	}
}
