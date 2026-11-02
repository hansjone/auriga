package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nasAssertGatewayPair(t *testing.T, f *nasRotationFixture) (string, string) {
	t.Helper()
	runtimePath, privatePath := "", ""
	for _, c := range f.containers {
		if c.Config.Labels["com.docker.compose.service"] != "network" {
			continue
		}
		for _, m := range c.Mounts {
			if m.Destination == "/etc/auriga/network/runtime.json" {
				runtimePath = m.Source
			}
			if m.Destination == "/etc/auriga/network/protocol.key" {
				privatePath = m.Source
			}
			if (m.Destination == "/etc/auriga/network/runtime.json" || m.Destination == "/etc/auriga/network/protocol.key") && m.RW {
				t.Fatal("writable gateway signing material")
			}
		}
	}
	raw, e := os.ReadFile(runtimePath)
	if e != nil {
		t.Fatal(e)
	}
	var cfg struct {
		Operation int `json:"operationTimeoutMillis"`
		Signing   struct {
			Kid     string `json:"keyId"`
			Version string `json:"keysetVersion"`
			Before  string `json:"notBefore"`
			After   string `json:"notAfter"`
		} `json:"protocolSigning"`
	}
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	if cfg.Signing.Version != f.status.Version {
		t.Fatalf("consumer runtime version=%s, installed=%s", cfg.Signing.Version, f.status.Version)
	}
	if cfg.Operation != 60000 {
		t.Fatal("activation lost the installed operation budget")
	}
	keyRaw, e := os.ReadFile(privatePath)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(keyRaw)
	block, _ := pem.Decode(keyRaw)
	if block == nil {
		t.Fatal("protocol PEM missing")
	}
	key, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	clear(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	private := key.(ed25519.PrivateKey)
	defer clear(private)
	for _, k := range f.payload.Keys {
		if k.Kid != cfg.Signing.Kid {
			continue
		}
		pub, e := base64.RawURLEncoding.DecodeString(k.Public)
		if e != nil {
			t.Fatal(e)
		}
		actual, e := x509.MarshalPKIXPublicKey(private.Public())
		if e != nil || string(pub) != string(actual) || k.State != "active" || k.Before != cfg.Signing.Before || k.After != cfg.Signing.After {
			t.Fatal("consumer private key and signed active metadata differ")
		}
		return runtimePath, privatePath
	}
	t.Fatal("consumer kid is absent from approved manifest")
	return "", ""
}

func TestNASActivatedGatewayConsumesSameVersionPair(t *testing.T) {
	f := nasRotationTestFixture(t, "ip")
	f.request(t)
	if e := f.r.publish(); e != nil {
		t.Fatal(e)
	}
	if e := f.r.install(); e != nil {
		t.Fatal(e)
	}
	nasEnableActivationFixture(t, f)
	if e := f.r.engine.activate(f.r); e != nil {
		t.Fatal(e)
	}
	nasAssertGatewayPair(t, f)
}

type nasActivationTransport struct {
	base             http.RoundTripper
	fixture          *nasRotationFixture
	t                *testing.T
	created, removed int
	failNetworkStart bool
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
		c := nasRotationContainer{ID: fmt.Sprintf("%064x", 6+x.created), Image: f.r.binding.Image}
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
			if c.Config.Labels["com.docker.compose.service"] == "network" && x.failNetworkStart {
				x.failNetworkStart = false
				return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("injected crash before network start")), Header: make(http.Header)}, nil
			}
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
