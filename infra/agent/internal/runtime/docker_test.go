package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

type fakeEngine struct {
	mu       sync.Mutex
	creates  []map[string]any
	networks []map[string]any
	pulls    []string
	started  []string
	stopped  []string
	removed  []string
}

func (f *fakeEngine) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/images/create", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.pulls = append(f.pulls, r.URL.Query().Get("fromImage")+"@"+r.URL.Query().Get("tag"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/networks/create", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.networks = append(f.networks, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"net1"}`))
	})
	mux.HandleFunc("/networks/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/containers/create", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.creates = append(f.creates, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"ctr1"}`))
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/start") && r.Method == http.MethodPost:
			f.mu.Lock()
			f.started = append(f.started, path)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/stop") && r.Method == http.MethodPost:
			f.mu.Lock()
			f.stopped = append(f.stopped, path)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/json") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"State":{"Running":true},"Config":{"Labels":{"auriga.instance":"i1","auriga.generation":"3"}},"NetworkSettings":{"Ports":{"7860/tcp":[{"HostIp":"127.0.0.1","HostPort":"41999"}]}}}`))
		case strings.HasSuffix(path, "/stats"):
			_, _ = w.Write([]byte(`{"memory_stats":{"usage":67108864},"pids_stats":{"current":4}}`))
		case r.Method == http.MethodDelete:
			f.mu.Lock()
			f.removed = append(f.removed, path)
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && path == "/containers/json":
			_, _ = w.Write([]byte(`[{"Labels":{"auriga.managed":"true","auriga.instance":"i1","auriga.generation":"3"},"State":"running"}]`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	return mux
}

func TestDockerDriverSecurityParams(t *testing.T) {
	fake := &fakeEngine{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	client, err := runtime.DialEngine(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d := runtime.NewDockerWithClient(client, "")

	home := t.TempDir()
	spec := &runtime.Spec{
		InstanceID: "i1",
		Generation: 3,
		Image:      "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32),
		CPUMillis:  2000,
		MemoryMB:   2048,
		PIDs:       256,
		HostUID:    200042,
		HomeBind:   home,
		ListenPort: 7860,
		Env:        map[string]string{"AURIGA_MODE": "managed"},
		Labels:     map[string]string{"tier": "std"},
	}
	ctx := context.Background()
	if err := d.Ensure(ctx, spec, &runtime.Rendered{Digest: "sha256:x"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Start(ctx, "i1"); err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.creates) != 1 {
		t.Fatalf("creates=%d", len(fake.creates))
	}
	body := fake.creates[0]
	if body["User"] != "200042:200042" {
		t.Fatalf("User=%v", body["User"])
	}
	hc, _ := body["HostConfig"].(map[string]any)
	if hc["ReadonlyRootfs"] != true {
		t.Fatalf("ReadonlyRootfs=%v", hc["ReadonlyRootfs"])
	}
	if hc["Privileged"] != false {
		t.Fatalf("Privileged=%v", hc["Privileged"])
	}
	caps, _ := hc["CapDrop"].([]any)
	if len(caps) != 1 || caps[0] != "ALL" {
		t.Fatalf("CapDrop=%v", hc["CapDrop"])
	}
	sec, _ := hc["SecurityOpt"].([]any)
	found := false
	for _, s := range sec {
		if s == "no-new-privileges:true" {
			found = true
		}
	}
	if !found {
		t.Fatalf("SecurityOpt=%v", sec)
	}
	if int64(hc["Memory"].(float64)) != 2048*1024*1024 {
		t.Fatalf("Memory=%v", hc["Memory"])
	}
	if int64(hc["NanoCpus"].(float64)) != 2000*1_000_000 {
		t.Fatalf("NanoCpus=%v", hc["NanoCpus"])
	}
	if int64(hc["PidsLimit"].(float64)) != 256 {
		t.Fatalf("PidsLimit=%v", hc["PidsLimit"])
	}
	tmpfs, _ := hc["Tmpfs"].(map[string]any)
	if _, ok := tmpfs["/tmp"]; !ok {
		t.Fatalf("Tmpfs=%v", tmpfs)
	}
	labels, _ := body["Labels"].(map[string]any)
	if labels["auriga.instance"] != "i1" || labels["auriga.generation"] != "3" {
		t.Fatalf("labels=%v", labels)
	}
	if len(fake.networks) != 1 || fake.networks[0]["Internal"] != true {
		t.Fatalf("network=%v", fake.networks)
	}
	if len(fake.pulls) != 1 || !strings.Contains(fake.pulls[0], "@sha256:") {
		t.Fatalf("pulls=%v", fake.pulls)
	}

	st, err := d.Inspect(ctx, "i1")
	if err != nil || !st.Running || st.Generation != 3 {
		t.Fatalf("inspect %+v %v", st, err)
	}
	ep, err := d.Endpoint("i1")
	if err != nil || !strings.Contains(ep.URL, "127.0.0.1") {
		t.Fatalf("endpoint %v %v", ep, err)
	}
}

func TestDockerRejectsUnpinnedImage(t *testing.T) {
	fake := &fakeEngine{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, _ := runtime.DialEngine(srv.URL, time.Second)
	d := runtime.NewDockerWithClient(client, "")
	err := d.Ensure(context.Background(), &runtime.Spec{
		InstanceID: "x",
		Image:      "registry.example.com/auriga/dsh:latest",
		HomeBind:   t.TempDir(),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("got %v", err)
	}
}

func TestDockerUnixSocketFake(t *testing.T) {
	// Prefer TCP on all platforms; also exercise DialEngine over a listener when unix works.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeEngine{}
	srv := &http.Server{Handler: fake.handler()}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	client, err := runtime.DialEngine("tcp://"+ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSelectDriverDefaultDocker(t *testing.T) {
	if runtime.PrimaryDriverName(nil) != "docker" {
		t.Fatal(runtime.PrimaryDriverName(nil))
	}
	if runtime.PrimaryDriverName([]string{"fake"}) != "fake" {
		t.Fatal("want fake")
	}
	d, err := runtime.SelectDriver("fake", runtime.DockerConfig{})
	if err != nil || d.Name() != "fake" {
		t.Fatalf("%v %v", d, err)
	}
}
