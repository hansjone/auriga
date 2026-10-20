package runtime_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
)

func TestContainerEnvAllowlist(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	rendered := map[string]string{
		"AURIGA_MODE":          "managed",
		"AURIGA_INSTANCE_ID":   "i1",
		"DSH_HOME":             "/home/dsh/.dsh",
		"HTTPS_PROXY":          "http://proxy.example.com:3128",
		"TZ":                   "Asia/Shanghai",
		"LD_AUDIT":             "/tmp/evil-value.so",
		"FOO":                  "bar-secret-ish",
		"LANG":                 "C.UTF-8", // in allowlist but not declared by spec
		"AURIGA_UNKNOWN_EXTRA": "x",
	}
	specEnv := map[string]string{"TZ": "Asia/Shanghai", "LD_AUDIT": "x", "FOO": "y", "AURIGA_MODE": "evil"}
	got, err := runtime.ContainerEnv(rendered, specEnv, runtime.DefaultExtraEnvKeys, log)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"AURIGA_MODE":        "managed",
		"AURIGA_INSTANCE_ID": "i1",
		"DSH_HOME":           "/home/dsh/.dsh",
		"HTTPS_PROXY":        "http://proxy.example.com:3128",
		"TZ":                 "Asia/Shanghai",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s=%q want %q (all %v)", k, got[k], v, got)
		}
	}
	out := logs.String()
	for _, k := range []string{"LD_AUDIT", "FOO", "LANG", "AURIGA_UNKNOWN_EXTRA"} {
		if !strings.Contains(out, k) {
			t.Fatalf("dropped key %s not logged: %s", k, out)
		}
	}
	for _, v := range []string{"evil-value", "bar-secret-ish", "C.UTF-8"} {
		if strings.Contains(out, v) {
			t.Fatalf("log leaks value %q: %s", v, out)
		}
	}
}

func TestContainerEnvRejectsBadSyntax(t *testing.T) {
	for _, env := range []map[string]string{
		{"lower": "x"},
		{"A-B": "x"},
		{"": "x"},
		{"TZ": "UTC\nLD_PRELOAD=/x"},
		{"TZ": "UTC\x00"},
		{"TZ": "UTC\r"},
	} {
		if _, err := runtime.ContainerEnv(env, map[string]string{"TZ": ""}, runtime.DefaultExtraEnvKeys, nil); err == nil {
			t.Fatalf("expected reject for %q", env)
		}
	}
}

func TestContainerEnvCustomExtraKeys(t *testing.T) {
	rendered := map[string]string{"MY_FLAG": "1", "TZ": "UTC"}
	spec := map[string]string{"MY_FLAG": "1", "TZ": "UTC"}
	got, err := runtime.ContainerEnv(rendered, spec, []string{"MY_FLAG"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["MY_FLAG"] != "1" || got["TZ"] != "" {
		t.Fatalf("custom allowlist not honored: %v", got)
	}
}

func TestDockerRejectsInvalidEnvBeforeTouchingEngine(t *testing.T) {
	fake := &fakeEngine{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, _ := runtime.DialEngine(srv.URL, time.Second)
	d := runtime.NewDockerWithClient(client, "")
	d.Policy = policy.Default()
	d.Policy.AllowAnyImage = true
	for _, env := range []map[string]string{
		{"bad key": "x"},
		{"TZ": "UTC\nX=1"},
	} {
		err := d.Ensure(context.Background(), &runtime.Spec{
			InstanceID: "inst_e",
			Image:      "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32),
			HomeBind:   t.TempDir(),
			MetaBind:   t.TempDir(),
			HostUID:    200001,
			Env:        env,
		}, nil)
		if err == nil {
			t.Fatalf("expected env reject for %q", env)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.pulls) != 0 || len(fake.creates) != 0 {
		t.Fatalf("engine touched: pulls=%v creates=%d", fake.pulls, len(fake.creates))
	}
}

func TestDockerEnvIsSorted(t *testing.T) {
	fake := &fakeEngine{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client, _ := runtime.DialEngine(srv.URL, time.Second)
	d := runtime.NewDockerWithClient(client, "")
	d.Policy = policy.Default()
	d.Policy.AllowAnyImage = true
	err := d.Ensure(context.Background(), &runtime.Spec{
		InstanceID: "inst_s",
		Image:      "registry.example.com/auriga/dsh@sha256:" + strings.Repeat("ab", 32),
		HomeBind:   t.TempDir(),
		MetaBind:   t.TempDir(),
		HostUID:    200001,
		Env:        map[string]string{"TZ": "UTC", "AURIGA_MODE": "managed", "DSH_HOME": "/home/dsh/.dsh"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	env, _ := fake.creates[0]["Env"].([]any)
	var got []string
	for _, e := range env {
		got = append(got, e.(string))
	}
	if strings.Join(got, ";") != "AURIGA_MODE=managed;DSH_HOME=/home/dsh/.dsh;TZ=UTC" {
		t.Fatalf("env %v", got)
	}
}
