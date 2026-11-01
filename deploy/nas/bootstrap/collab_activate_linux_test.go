package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nasActivationTransport struct {
	base             http.RoundTripper
	fixture          *nasRotationFixture
	t                *testing.T
	created, removed int
}

func (x *nasActivationTransport) RoundTrip(q *http.Request) (*http.Response, error) {
	f := x.fixture
	reply := func(v any) (*http.Response, error) {
		b, e := json.Marshal(v)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Header: make(http.Header)}, e
	}
	path := strings.TrimPrefix(q.URL.Path, "/v1.41")
	if strings.HasPrefix(path, "/networks/") {
		return reply(map[string]any{"Id": strings.Repeat("f", 64), "Name": nasRotationProject + "_internal", "Driver": "bridge", "Internal": true, "Labels": map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.network": "internal"}})
	}
	if path == "/containers/create" {
		var body map[string]any
		if e := json.NewDecoder(q.Body).Decode(&body); e != nil {
			return nil, e
		}
		s := body["Labels"].(map[string]any)["com.docker.compose.service"].(string)
		if s != "collab" && s != "network" {
			x.t.Fatal("activation attempted core service")
		}
		host := body["HostConfig"].(map[string]any)
		if host["ReadonlyRootfs"] != true || body["Image"] != f.r.binding.Image {
			x.t.Fatal("activation changed image/read-only policy")
		}
		c := nasRotationContainer{ID: strings.Repeat(fmt.Sprint(6+x.created), 64), Image: f.r.binding.Image}
		raw, _ := json.Marshal(body)
		_ = json.Unmarshal(raw, &c.Config)
		var mounts []map[string]any
		for _, v := range host["Binds"].([]any) {
			parts := strings.Split(v.(string), ":")
			mounts = append(mounts, map[string]any{"Source": parts[0], "Destination": parts[1], "RW": parts[2] != "ro"})
		}
		b, _ := json.Marshal(mounts)
		_ = json.Unmarshal(b, &c.Mounts)
		c.State.Health.Status = "healthy"
		f.containers[c.ID] = c
		x.created++
		return reply(map[string]string{"Id": c.ID})
	}
	for id, c := range f.containers {
		if path == "/containers/"+id && q.Method == "DELETE" {
			if c.State.Running {
				x.t.Fatal("removed live signer")
			}
			delete(f.containers, id)
			x.removed++
			return reply(nil)
		}
		if path == "/containers/"+id+"/start" {
			c.State.Running = true
			c.State.Pid = 123
			f.containers[id] = c
			return reply(nil)
		}
		if path == "/containers/"+id+"/json" {
			b, _ := json.Marshal(c)
			var raw map[string]any
			_ = json.Unmarshal(b, &raw)
			s := c.Config.Labels["com.docker.compose.service"]
			raw["Name"] = "/" + nasRotationProject + "-" + s + "-1"
			binds := []string{}
			for _, m := range c.Mounts {
				mode := "ro"
				if m.RW {
					mode = "rw"
				}
				binds = append(binds, m.Source+":"+m.Destination+":"+mode)
			}
			raw["HostConfig"] = map[string]any{"Binds": binds, "ReadonlyRootfs": true, "CapDrop": []string{"ALL"}}
			raw["NetworkSettings"] = map[string]any{"Networks": map[string]any{nasRotationProject + "_internal": map[string]any{"Aliases": []string{s}}}}
			return reply(raw)
		}
	}
	return x.base.RoundTrip(q)
}

func TestNASQueueActivationRecreatesOnlyStoppedSignersAndPreservesMounts(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	r := f.r
	f.request(t)
	if e := r.publish(); e != nil {
		t.Fatal(e)
	}
	if e := r.install(); e != nil {
		t.Fatal(e)
	}
	x := nasEnableActivationFixture(t, f)
	if e := r.engine.activate(r); e != nil {
		t.Fatal(e)
	}
	if x.created != 2 || x.removed != 2 {
		t.Fatal("wrong recreation scope")
	}
	if e := r.engine.activate(r); e != nil {
		t.Fatal(e)
	}
	if x.created != 2 || x.removed != 2 {
		t.Fatal("idempotent activation recreated services")
	}
	for _, c := range f.containers {
		s := c.Config.Labels["com.docker.compose.service"]
		if (s == "platform" || s == "edge" || s == "postgres") && !c.State.Running {
			t.Fatal("core service interrupted")
		}
	}
	// Private crash definitions exist; no root/business key bytes are exported.
	entries, e := os.ReadDir(r.root)
	if e != nil {
		t.Fatal(e)
	}
	backups := 0
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), "activation-") {
			backups++
		}
	}
	if backups != 2 {
		t.Fatal("missing recreation evidence")
	}
}

func nasEnableActivationFixture(t *testing.T, f *nasRotationFixture) *nasActivationTransport {
	t.Helper()
	r := f.r
	pg := nasRotationContainer{ID: strings.Repeat("5", 64), Image: r.binding.Image}
	pg.Config.Labels = map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.service": "postgres", "com.docker.compose.project.working_dir": r.recipe, "com.docker.compose.project.config_files": r.recipe + "/docker-compose.yml"}
	pg.State.Running = true
	pg.State.Health.Status = "healthy"
	b, _ := json.Marshal([]map[string]any{{"Source": filepath.Join(r.data, "data/postgres"), "Destination": "/var/lib/postgresql/data", "RW": true}})
	_ = json.Unmarshal(b, &pg.Mounts)
	f.containers[pg.ID] = pg
	for id, c := range f.containers {
		c.Config.Image = c.Image
		if s := c.Config.Labels["com.docker.compose.service"]; s == "collab" || s == "network" {
			c.Config.Image = "fixture-mutable-tag"
		}
		if c.Config.Labels["com.docker.compose.service"] == "platform" {
			c.State.Health.Status = "healthy"
		}
		f.containers[id] = c
	}
	x := &nasActivationTransport{base: r.engine.client.Transport, fixture: f, t: t}
	r.engine.client.Transport = x
	return x
}
