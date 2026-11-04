package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNASRotationR5PrepareSelectsTenBeforeNineAndReplacesStale(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	r := f.r
	dir := f.request(t)
	b, e := nasRotationRead(filepath.Join(dir, "payload.json"), 0)
	if e != nil {
		t.Fatal(e)
	}
	var p nasRotationPayload
	if e = nasRotationDecode(b, &p); e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"9", "10"} {
		p.Version = v
		nasRotationTestJSON(t, filepath.Join(r.root, "requests", v, "payload.json"), p)
	}
	var out bytes.Buffer
	if e = r.prepare(nil, &out); e != nil {
		t.Fatal(e)
	}
	var result map[string]any
	if e = json.Unmarshal(out.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if filepath.Base(result["requestDirectory"].(string)) != "10" {
		t.Fatal("lexicographic selection", out.String())
	}
	p.Until = time.Now().UTC().Add(17 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	for _, v := range []string{"8", "9", "10"} {
		p.Version = v
		nasRotationTestJSON(t, filepath.Join(r.root, "requests", v, "payload.json"), p)
	}
	out.Reset()
	if e = r.prepare(nil, &out); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(out.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if filepath.Base(result["requestDirectory"].(string)) != "11" {
		t.Fatal("stale request reused", out.String())
	}
}

func TestNASRotationR5RequestFreshnessAndNumericSelection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	p := nasRotationPayload{Issued: now.Format(time.RFC3339), Until: now.Add(18 * time.Hour).Format(time.RFC3339)}
	if !nasRotationReusable(p, now, false) {
		t.Fatal("18h reserve refused")
	}
	p.Until = now.Add(18*time.Hour - time.Second).Format(time.RFC3339)
	if nasRotationReusable(p, now, false) {
		t.Fatal("stale request reused")
	}
	p.Until = now.Add(24 * time.Hour).Format(time.RFC3339)
	p.Keys = []nasRotationKey{{State: "next"}}
	if nasRotationReusable(p, now, true) {
		t.Fatal("mature activation masked")
	}
}

func TestNASRotationR5BindingSurvivesPlatformUpgradeOnlyWithPinnedJar(t *testing.T) {
	old := nasRotationBinding{Base: "base", Source: "source", Image: "old-image", Jar: "fixed-jar", Data: "data", Recipe: "recipe"}
	next := old
	next.Image = "new-platform"
	got, e := nasRotationHistoricalBinding(old, next)
	if e != nil || got != old {
		t.Fatal("platform upgrade changed immutable history", e)
	}
	next.Jar = "other-jar"
	if _, e = nasRotationHistoricalBinding(old, next); e == nil {
		t.Fatal("unapproved jar admitted")
	}
	old.Jar = ""
	next = old
	next.Image = "new-platform"
	if _, e = nasRotationHistoricalBinding(old, next); e == nil {
		t.Fatal("unbound operator changed")
	}
}

func TestNASRotationR5LockWaitsAndReadOnlyPending(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root lock fixture")
	}
	r := &nasRotation{root: t.TempDir(), lockWait: time.Second}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- nasRotationLocked(r, func() error { close(ready); time.Sleep(60 * time.Millisecond); return nil })
	}()
	<-ready
	called := false
	if e := nasRotationLocked(r, func() error { called = true; return nil }); e != nil || !called {
		t.Fatal("contended operation did not wait", e)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(r.root, ".pending"), 0700); e != nil {
		t.Fatal(e)
	}
	r.allowPending = true
	if e := nasRotationLocked(r, func() error { return nil }); e != nil {
		t.Fatal("read-only operation blocked by pending", e)
	}
}

func TestNASRotationR5StoppedDatabaseNeverStartedByObserver(t *testing.T) {
	r := &nasRotation{data: "/srv/data", recipe: "/srv/recipe"}
	id := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Method != "GET" {
			t.Error("observer attempted database mutation")
			http.Error(w, "denied", 500)
			return
		}
		if strings.HasSuffix(q.URL.Path, "/containers/json") {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"Id": id}})
			return
		}
		c := nasRotationContainer{ID: id}
		c.Config.Labels = map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.service": "postgres", "com.docker.compose.project.working_dir": r.recipe, "com.docker.compose.project.config_files": r.recipe + "/docker-compose.yml"}
		b, _ := json.Marshal([]map[string]any{{"Source": r.data + "/data/postgres", "Destination": "/var/lib/postgresql/data", "RW": true}})
		_ = json.Unmarshal(b, &c.Mounts)
		_ = json.NewEncoder(w).Encode(c)
	}))
	defer server.Close()
	e := &nasRotationEngine{client: &http.Client{Transport: roundTripNASRotation{target: server.URL, base: server.Client().Transport}}}
	if _, err := e.database(r); err == nil {
		t.Fatal("stopped database accepted")
	}
}
