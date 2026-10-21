package runtime_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

type fakeEngine struct {
	mu           sync.Mutex
	creates      []map[string]any
	networks     []map[string]any
	pulls        []string
	started      []string
	pullErr      string
	inspectWrong bool // RepoDigests omit the requested digest
	lastInspect  string
	pulledOK     bool // after a successful pull stream, inspect finds the image
	healthPort   string
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
		errMsg := f.pullErr
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		if errMsg != "" {
			_, _ = w.Write([]byte(`{"error":"` + errMsg + `"}` + "\n"))
			return
		}
		f.mu.Lock()
		f.pulledOK = true
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"status":"Pull complete"}` + "\n"))
	})
	mux.HandleFunc("/images/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/json") {
			http.NotFound(w, r)
			return
		}
		// Path is /images/<escaped-ref>/json — ref is repo@sha256:… (manifest digest).
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/images/"), "/json")
		if unesc, err := url.PathUnescape(name); err == nil {
			name = unesc
		}
		f.mu.Lock()
		f.lastInspect = name
		wrong := f.inspectWrong
		have := f.pulledOK || f.inspectWrong // digest-mismatch test inspects after pull
		f.mu.Unlock()
		if !have {
			http.NotFound(w, r)
			return
		}

		manifestDig := "sha256:" + strings.Repeat("ab", 32)
		if _, dig, ok := strings.Cut(name, "@"); ok {
			manifestDig = dig
		}
		// Real Engine: Id is config digest, distinct from manifest digest in RepoDigests.
		configID := "sha256:" + strings.Repeat("cd", 32)
		repoDigests := `["registry.example.com/auriga/dsh@` + manifestDig + `"]`
		if wrong {
			other := "sha256:" + strings.Repeat("ef", 32)
			repoDigests = `["registry.example.com/auriga/dsh@` + other + `"]`
		}
		_, _ = w.Write([]byte(`{"Id":"` + configID + `","RepoDigests":` + repoDigests + `}`))
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
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/json") && r.Method == http.MethodGet:
			port := f.healthPort
			if port == "" {
				port = "41999"
			}
			_, _ = w.Write([]byte(`{"State":{"Running":true},"Config":{"Labels":{"auriga.instance":"inst_i1","auriga.generation":"3"}},"NetworkSettings":{"Ports":{"7860/tcp":[{"HostIp":"127.0.0.1","HostPort":"` + port + `"}]}}}`))
		case strings.HasSuffix(path, "/stats"):
			_, _ = w.Write([]byte(`{"memory_stats":{"usage":67108864},"pids_stats":{"current":4}}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && path == "/containers/json":
			_, _ = w.Write([]byte(`[{"Labels":{"auriga.managed":"true","auriga.instance":"inst_i1","auriga.generation":"3"},"State":"running"}]`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	return mux
}

func TestDockerDriverSecurityParams(t *testing.T) {
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, readyBody) }))
	t.Cleanup(health.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeEngine{healthPort: port}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	client, err := runtime.DialEngine(srv.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d := runtime.NewDockerWithClient(client, "")
	d.Policy = policy.Default()
	d.Policy.AllowAnyImage = true
	d.Policy.MaxMemoryMB = 4096
	d.Policy.MaxCPUMillis = 4000
	d.Policy.MaxPIDs = 512

	home := t.TempDir()
	meta := t.TempDir()
	spec := &runtime.Spec{
		InstanceID: "inst_i1",
		Generation: 3,
		Image:      "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32),
		CPUMillis:  99999,
		MemoryMB:   99999,
		PIDs:       99999,
		HostUID:    200042,
		HomeBind:   home,
		MetaBind:   meta,
		ListenPort: 7860,
		Env:        map[string]string{"AURIGA_MODE": "managed"},
		Labels:     map[string]string{"tier": "std"},
	}
	ctx := context.Background()
	if err := d.Ensure(ctx, spec, &runtime.Rendered{Digest: "sha256:x"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Start(ctx, "inst_i1"); err != nil {
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
	mounts, _ := hc["Mounts"].([]any)
	if len(mounts) != 2 {
		t.Fatalf("Mounts=%v", mounts)
	}
	foundMeta := false
	for _, m := range mounts {
		mm, _ := m.(map[string]any)
		if mm["Target"] == renderer.ContainerMetaRoot {
			foundMeta = true
			if mm["ReadOnly"] != true && mm["RW"] != false {
				t.Fatalf("meta mount not readonly: %v", mm)
			}
		}
	}
	if !foundMeta {
		t.Fatal("missing meta mount")
	}
	if _, ok := hc["Binds"]; ok {
		t.Fatal("should use Mounts not Binds")
	}
	if int64(hc["Memory"].(float64)) != 4096*1024*1024 {
		t.Fatalf("Memory clamped=%v", hc["Memory"])
	}
	if int64(hc["NanoCpus"].(float64)) != 4000*1_000_000 {
		t.Fatalf("NanoCpus clamped=%v", hc["NanoCpus"])
	}
	if int64(hc["PidsLimit"].(float64)) != 512 {
		t.Fatalf("PidsLimit=%v", hc["PidsLimit"])
	}
}

func TestDockerRejectsBadImageAndID(t *testing.T) {
	fake := &fakeEngine{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, _ := runtime.DialEngine(srv.URL, time.Second)
	d := runtime.NewDockerWithClient(client, "")
	d.Policy.AllowAnyImage = false
	d.Policy.AllowedImageRepos = []string{"registry.example.com/auriga/"}

	err := d.Ensure(context.Background(), &runtime.Spec{
		InstanceID: "inst_x",
		Image:      "evil.example.com/x@sha256:" + strings.Repeat("ab", 32),
		HomeBind:   t.TempDir(),
		MetaBind:   t.TempDir(),
		HostUID:    200001,
	}, nil)
	if err == nil {
		t.Fatal("expected image reject")
	}
	err = d.Ensure(context.Background(), &runtime.Spec{
		InstanceID: ".",
		Image:      "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32),
		HomeBind:   t.TempDir(),
		MetaBind:   t.TempDir(),
		HostUID:    200001,
	}, nil)
	if err == nil {
		t.Fatal("expected id reject")
	}
}

func TestPullStreamError(t *testing.T) {
	fake := &fakeEngine{pullErr: "denied"}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, _ := runtime.DialEngine(srv.URL, time.Second)
	err := client.PullImage(context.Background(), "registry.example.com/auriga/dsh@sha256:"+strings.Repeat("ab", 32))
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("got %v", err)
	}
}

func TestPullRejectsDigestMismatch(t *testing.T) {
	fake := &fakeEngine{inspectWrong: true}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, _ := runtime.DialEngine(srv.URL, time.Second)
	img := "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32)
	err := client.PullImage(context.Background(), img)
	if err == nil || !strings.Contains(err.Error(), "missing digest") {
		t.Fatalf("got %v", err)
	}
	fake.mu.Lock()
	got := fake.lastInspect
	fake.mu.Unlock()
	if got != img {
		t.Fatalf("inspect target=%q want full ref %q", got, img)
	}
}

func TestNewDockerZeroPolicyDeniesImages(t *testing.T) {
	fake := &fakeEngine{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	d, err := runtime.NewDocker(runtime.DockerConfig{Host: srv.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if d.Policy.AllowAnyImage {
		t.Fatal("zero policy must not AllowAnyImage")
	}
	err = d.Ensure(context.Background(), &runtime.Spec{
		InstanceID: "inst_z",
		Image:      "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32),
		HomeBind:   t.TempDir(),
		MetaBind:   t.TempDir(),
		HostUID:    200001,
	}, nil)
	if err == nil {
		t.Fatal("expected deny without allowlist")
	}
}

func TestDockerUnixSocketFake(t *testing.T) {
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
	d, err := runtime.SelectDriver("fake", runtime.DockerConfig{})
	if err != nil || d.Name() != "fake" {
		t.Fatalf("%v %v", d, err)
	}
}
