package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type nasRotationFixture struct {
	r          *nasRotation
	root       ed25519.PrivateKey
	payload    nasRotationPayload
	status     nasRotationStatus
	containers map[string]nasRotationContainer
	calls      []string
	stops      int
	fail       string
}

func nasRotationTestWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func nasRotationTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	nasRotationTestWrite(t, path, b)
}
func nasRotationTestFixture(t *testing.T, mode string) *nasRotationFixture {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("NAS controller fixture requires Linux root")
	}
	_, cfg, e := loadNASAccess("../access-"+mode+".example.json", "192.0.2.138", "192.0.2.128/26", 15000)
	if e != nil {
		t.Fatal(e)
	}
	data := t.TempDir()
	r := &nasRotation{data: data, recipe: "/srv/recipe", root: filepath.Join(data, "config/collab-operations"), source: filepath.Join(data, "config/collab-source"), state: filepath.Join(data, "config/collab-runtime/state/state.json")}
	r.binding = nasRotationBinding{Base: strings.Repeat("a", 64), Source: strings.Repeat("b", 64), Image: "sha256:" + strings.Repeat("c", 64), Data: data, Recipe: r.recipe}
	for _, d := range []string{r.root, filepath.Join(r.root, "releases"), filepath.Join(r.root, "installs"), filepath.Join(r.root, "requests"), filepath.Dir(r.state)} {
		if e = os.MkdirAll(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	pub, root, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	public, e := x509.MarshalPKIXPublicKey(pub)
	if e != nil {
		t.Fatal(e)
	}
	f := &nasRotationFixture{r: r, root: root, containers: map[string]nasRotationContainer{}}
	now := time.Now().UTC().Truncate(time.Second)
	f.payload = nasRotationPayload{Schema: 1, Realm: "1", Version: "7", Issued: now.Add(-time.Minute).Format(time.RFC3339), Until: now.Add(time.Hour).Format(time.RFC3339)}
	for i, purpose := range []string{"business_delivery", "protocol_response", "management_state"} {
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		der, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		pubder, e := x509.MarshalPKIXPublicKey(pub)
		if e != nil {
			t.Fatal(e)
		}
		k := nasRotationKey{Kid: fmt.Sprintf("old-%d", i), Alg: "Ed25519", Purpose: purpose, State: "active", Public: base64.RawURLEncoding.EncodeToString(pubder), Before: f.payload.Issued, After: f.payload.Until}
		f.payload.Keys = append(f.payload.Keys, k)
		leaf := []string{"business.der", "protocol.der", "management.der"}[i]
		nasRotationTestWrite(t, filepath.Join(r.source, "collab-installation", leaf), der)
	}
	for _, name := range []string{"browser.crt", "ca.crt", "server.crt", "server.key", "client.crt", "client.key"} {
		nasRotationTestWrite(t, filepath.Join(r.source, "collab-installation", name), []byte("fixture-only"))
	}
	nasRotationTestWrite(t, filepath.Join(r.source, "collab-installation/root.der"), public)
	nasRotationTestWrite(t, filepath.Join(r.source, "collab-installation/collab.properties"), []byte("spring.datasource.url=jdbc:postgresql://postgres:5432/auriga\nauriga.security.jwt.issuer="+cfg.Issuer()+"\nauriga.collab.network.key-operations.tenant-id="+cfg.TenantID()+"\nauriga.collab.network.key-operations.realm=1\nauriga.collab.network.key-operations.root-kid=fixture-root\nauriga.collab.network.key-operations.root-fingerprint=sha256:"+nasRotationHash(public)+"\nauriga.collab.network.key-operations.private-files[old]=unchanged\n"))
	f.sign(t, filepath.Join(r.source, "collab-installation"), f.payload, false)
	nasRotationTestJSON(t, filepath.Join(r.source, "collab-installation/state.json"), map[string]string{"version": "7"})
	nasRotationTestJSON(t, r.state, map[string]string{"version": "7"})
	nasRotationTestWrite(t, r.state+".lock", nil)
	for _, p := range []string{filepath.Dir(r.state), r.state, r.state + ".lock"} {
		if e = os.Chown(p, 10002, 10002); e != nil {
			t.Fatal(e)
		}
	}
	nasRotationTestWrite(t, filepath.Join(r.source, "network/runtime.json"), []byte(`{"qos":{"limit":9007199254740993},"collab":{"Target":"collab:9092"},"protocolSigning":{}}`))
	f.status = nasRotationStatus{Version: "7", Time: now.Format(time.RFC3339Nano), First: map[string]string{}, Pending: true, Digest: "sha256:" + strings.Repeat("d", 64)}
	for _, k := range f.payload.Keys {
		f.status.First[k.Kid] = now.Add(-48 * time.Hour).Format(time.RFC3339Nano)
	}
	for i, s := range []string{"collab", "platform", "edge", "network"} {
		c := nasRotationContainer{ID: strings.Repeat(fmt.Sprint(i+1), 64), Image: r.binding.Image}
		c.Config.Labels = map[string]string{"com.docker.compose.project": nasRotationProject, "com.docker.compose.service": s, "com.docker.compose.project.working_dir": r.recipe, "com.docker.compose.project.config_files": filepath.Join(r.recipe, "docker-compose.yml")}
		c.State.Running = true
		c.State.Pid = i + 1
		f.containers[c.ID] = c
	}
	f.mounts(r.data+"/config/collab-runtime/collab", r.data+"/config/collab-runtime/network/protocol.key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/v1.41")
		if path == "/containers/json" {
			var ids []map[string]string
			for id := range f.containers {
				ids = append(ids, map[string]string{"Id": id})
			}
			_ = json.NewEncoder(w).Encode(ids)
			return
		}
		for id, c := range f.containers {
			if path == "/containers/"+id+"/json" {
				_ = json.NewEncoder(w).Encode(c)
				return
			}
			if path == "/containers/"+id+"/stop" {
				f.stops++
				c.State.Running = false
				c.State.Pid = 0
				f.containers[id] = c
				w.WriteHeader(204)
				return
			}
		}
		http.Error(w, "unexpected fixture request", 500)
	}))
	t.Cleanup(server.Close)
	r.engine = &nasRotationEngine{client: &http.Client{Transport: roundTripNASRotation{target: server.URL, base: server.Client().Transport}}}
	r.operator = func(action, work string, plan nasRotationPlan) ([]byte, error) {
		f.calls = append(f.calls, action)
		if f.fail == action {
			return nil, fmt.Errorf("fixture production rejection")
		}
		if plan.Config != filepath.Join(r.source, "collab-installation/collab.properties") || plan.Root != filepath.Join(r.source, "collab-installation/root.der") || plan.StateHash == "" {
			t.Fatal("operator plan lost immutable binding")
		}
		b, e := os.ReadFile(plan.Signed)
		if e != nil {
			return nil, e
		}
		p, e := nasRotationParsePayload(b)
		if e != nil {
			return nil, e
		}
		switch action {
		case "verify", "preflight", "preflight-recovery":
			return []byte(`{}`), nil
		case "publish", "recover-expired":
			for _, k := range p.Keys {
				if _, ok := f.status.First[k.Kid]; !ok {
					f.status.First[k.Kid] = time.Now().UTC().Format(time.RFC3339Nano)
				}
			}
			f.status.Version = p.Version
			f.payload = p
		case "install", "check-private":
			nasRotationTestJSON(t, plan.State, map[string]string{"version": p.Version})
			if action == "check-private" {
				return []byte(`{}`), nil
			}
		case "status":
		}
		return json.Marshal(f.status)
	}
	return f
}

type roundTripNASRotation struct {
	target string
	base   http.RoundTripper
}

func (t roundTripNASRotation) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	parts := strings.SplitN(strings.TrimPrefix(t.target, "http://"), "/", 2)
	clone.URL.Host = parts[0]
	return t.base.RoundTrip(clone)
}
func (f *nasRotationFixture) mounts(config, protocol string) {
	for id, c := range f.containers {
		var mounts []map[string]any
		switch c.Config.Labels["com.docker.compose.service"] {
		case "collab":
			mounts = append(mounts, map[string]any{"Source": config, "Destination": "/etc/auriga/collab", "RW": false}, map[string]any{"Source": filepath.Dir(f.r.state), "Destination": "/var/lib/auriga/collab", "RW": true})
		case "network":
			mounts = append(mounts, map[string]any{"Source": protocol, "Destination": "/etc/auriga/network/protocol.key", "RW": false}, map[string]any{"Source": filepath.Join(f.r.data, "config/network-policy/runtime.json"), "Destination": "/etc/auriga/network/runtime.json", "RW": false})
		case "platform":
			mounts = append(mounts, map[string]any{"Source": filepath.Join(f.r.data, "config/collab-runtime/platform"), "Destination": "/etc/auriga/network", "RW": false})
		case "edge":
			mounts = append(mounts, map[string]any{"Source": filepath.Join(f.r.data, "config/collab-runtime/edge/edge.config.json"), "Destination": "/etc/auriga/edge.config.json", "RW": false})
		}
		b, _ := json.Marshal(mounts)
		_ = json.Unmarshal(b, &c.Mounts)
		f.containers[id] = c
	}
}
func (f *nasRotationFixture) sign(t *testing.T, dir string, p nasRotationPayload, audit bool) {
	t.Helper()
	b, _ := json.Marshal(p)
	var generic any
	_ = json.Unmarshal(b, &generic)
	canonical, _ := json.Marshal(generic)
	sig := ed25519.Sign(f.root, append([]byte("Auriga.NetworkKeyset.v1\x00"), canonical...))
	signed := map[string]any{"keyset": generic, "root_auth": map[string]string{"alg": "Ed25519", "kid": "fixture-root", "signature": base64.RawURLEncoding.EncodeToString(sig)}}
	ann := map[string]string{"keyset_version": p.Version, "valid_until": p.Until, "manifest_digest": "sha256:" + nasRotationHash(canonical), "root_kid": "fixture-root"}
	ab, _ := json.Marshal(ann)
	ann["root_signature"] = base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.root, append([]byte("Auriga.NetworkKeysetAnnouncement.v1\x00"), ab...)))
	nasRotationTestJSON(t, filepath.Join(dir, "signed.json"), signed)
	nasRotationTestJSON(t, filepath.Join(dir, "announcement.json"), ann)
	if audit {
		sb, _ := os.ReadFile(filepath.Join(dir, "signed.json"))
		ab, _ := os.ReadFile(filepath.Join(dir, "announcement.json"))
		pb, _ := os.ReadFile(filepath.Join(dir, "payload.json"))
		nasRotationTestJSON(t, filepath.Join(dir, "approval.json"), map[string]string{"approvedBy": "alice", "reason": "expiry rotation fixture", "signedSha256": nasRotationHash(sb), "announcementSha256": nasRotationHash(ab), "payloadSha256": nasRotationHash(pb)})
	}
}
func (f *nasRotationFixture) request(t *testing.T) string {
	t.Helper()
	if e := f.r.prepare(nil, ioDiscardNASRotation{}); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(f.r.root, "requests", "8")
	b, e := os.ReadFile(filepath.Join(dir, "payload.json"))
	if e != nil {
		t.Fatal(e)
	}
	var p nasRotationPayload
	if e = nasRotationDecode(b, &p); e != nil {
		t.Fatal(e)
	}
	f.sign(t, dir, p, true)
	f.r.inputDir = dir
	return dir
}

type ioDiscardNASRotation struct{}

func (ioDiscardNASRotation) Write(b []byte) (int, error) { return len(b), nil }

func TestNASRotationPublishInstallModesPreserveSourceAndLock(t *testing.T) {
	for _, mode := range []string{"domain", "ip", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			f := nasRotationTestFixture(t, mode)
			r := f.r
			initial, _ := os.ReadFile(filepath.Join(r.source, "collab-installation/state.json"))
			lock, _ := os.Lstat(r.state + ".lock")
			dir := f.request(t)
			if e := r.publish(); e != nil {
				t.Fatal(e)
			}
			rows, e := r.releases()
			if e != nil || len(rows) != 1 {
				t.Fatalf("publication: %v", e)
			}
			if f.stops != 3 {
				t.Fatalf("wrong stop scope %d", f.stops)
			}
			if e = r.publish(); e != nil {
				t.Fatal(e)
			}
			if f.stops != 3 {
				t.Fatal("same publication reentry mutated services")
			}
			if e = r.install(); e != nil {
				t.Fatal(e)
			}
			installed, _, e := r.installed(rows, true)
			if e != nil {
				t.Fatal(e)
			}
			if e = r.install(); e != nil {
				t.Fatal(e)
			}
			after, _ := os.Lstat(r.state + ".lock")
			if !os.SameFile(lock, after) {
				t.Fatal("original state lock replaced")
			}
			old, _ := os.ReadFile(filepath.Join(r.source, "collab-installation/state.json"))
			if !reflect.DeepEqual(initial, old) {
				t.Fatal("initial state source overwritten")
			}
			snapshot, _ := os.ReadFile(filepath.Join(installed, "previous-state.json"))
			if !reflect.DeepEqual(initial, snapshot) {
				t.Fatal("previous state not retained")
			}
			if _, e = os.Lstat(filepath.Join(r.root, ".pending")); !os.IsNotExist(e) {
				t.Fatal("successful transaction left pending")
			}
			b, _ := os.ReadFile(filepath.Join(dir, "install-input.json"))
			var in nasRotationInput
			_ = json.Unmarshal(b, &in)
			if len(in.Private) != 6 {
				t.Fatal("active and next private mapping incomplete")
			}
		})
	}
}

func TestNASExpiredRecoveryExplicitAuditAndModes(t *testing.T) {
	for _, mode := range []string{"domain", "ip", "proxy"} {
		t.Run(mode, func(t *testing.T) {
			f := nasRotationTestFixture(t, mode)
			r := f.r
			r.binding.Jar = strings.Repeat("e", 64)
			r.recovery = true
			now := time.Now().UTC().Truncate(time.Second)
			f.payload.Until = now.Add(-time.Hour).Format(time.RFC3339)
			f.payload.Issued = now.Add(-25 * time.Hour).Format(time.RFC3339)
			for i := range f.payload.Keys {
				f.payload.Keys[i].Before = f.payload.Issued
				f.payload.Keys[i].After = f.payload.Until
			}
			f.sign(t, filepath.Join(r.source, "collab-installation"), f.payload, false)
			dir := f.request(t)
			if e := r.publish(); e == nil {
				t.Fatal("missing client refresh approval accepted")
			}
			b, e := os.ReadFile(filepath.Join(dir, "approval.json"))
			if e != nil {
				t.Fatal(e)
			}
			var audit map[string]any
			if e = json.Unmarshal(b, &audit); e != nil {
				t.Fatal(e)
			}
			audit["requireClientRefresh"] = true
			nasRotationTestJSON(t, filepath.Join(dir, "approval.json"), audit)
			r.recovery = false
			if e = r.publish(); e == nil {
				t.Fatal("normal command accepted recovery input")
			}
			r.recovery = true
			if e = r.publish(); e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(strings.Join(f.calls, ","), "preflight-recovery,recover-expired") {
				t.Fatal("recovery bypassed fixed production preflight/operator")
			}
			if e = r.install(); e != nil {
				t.Fatal(e)
			}
			rows, e := r.releases()
			if e != nil || len(rows) != 1 || rows[0].Mode != "expired_recovery" {
				t.Fatalf("recovery audit missing: %v", e)
			}
		})
	}
}

func TestNASRotationNegativeBoundariesRetainFailure(t *testing.T) {
	for _, mode := range []string{"domain", "ip", "proxy"} {
		for _, bad := range []string{"preflight", "foreign-writer", "wrong-image", "wrong-mount", "changed-state", "changed-audit", "hardlinked-key"} {
			t.Run(mode+"/"+bad, func(t *testing.T) {
				f := nasRotationTestFixture(t, mode)
				r := f.r
				dir := f.request(t)
				switch bad {
				case "preflight":
					f.fail = "preflight"
				case "foreign-writer":
					c := f.containers[strings.Repeat("1", 64)]
					c.ID = strings.Repeat("9", 64)
					c.Config.Labels = map[string]string{}
					b, _ := json.Marshal([]map[string]any{{"Source": r.data, "Destination": "/foreign", "RW": true}})
					_ = json.Unmarshal(b, &c.Mounts)
					f.containers[c.ID] = c
				case "wrong-image":
					c := f.containers[strings.Repeat("1", 64)]
					c.Image = "sha256:" + strings.Repeat("f", 64)
					f.containers[c.ID] = c
				case "wrong-mount":
					c := f.containers[strings.Repeat("1", 64)]
					c.Mounts[0].Source = "/foreign"
					f.containers[c.ID] = c
				case "changed-state":
					nasRotationTestJSON(t, r.state, map[string]string{"version": "99"})
				case "changed-audit":
					nasRotationTestJSON(t, filepath.Join(dir, "approval.json"), map[string]string{})
				case "hardlinked-key":
					if e := os.Link(filepath.Join(dir, "input.json"), filepath.Join(dir, "linked-input")); e != nil {
						t.Fatal(e)
					}
				}
				if e := r.publish(); e == nil {
					t.Fatal("unsafe publication accepted")
				}
				for _, call := range f.calls {
					if call == "publish" {
						t.Fatal("unsafe operation committed PG")
					}
				}
				rows, e := r.releases()
				if e != nil || len(rows) != 0 {
					t.Fatal("failed operation archived success")
				}
				if bad == "preflight" {
					if _, e = os.Lstat(filepath.Join(r.root, ".pending/first-failure.json")); e != nil {
						t.Fatal("failure evidence lost")
					}
					if e = r.publish(); e == nil {
						t.Fatal("pending bypassed on retry")
					}
				}
			})
		}
	}
}

func TestNASRotationReadRejectsLinksAndGroupAccess(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root metadata fixture")
	}
	for _, kind := range []string{"symlink", "hardlink", "group-readable"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "secret")
			nasRotationTestWrite(t, p, []byte("generated fixture"))
			switch kind {
			case "symlink":
				if e := os.Symlink(p, filepath.Join(dir, "link")); e != nil {
					t.Fatal(e)
				}
				p = filepath.Join(dir, "link")
			case "hardlink":
				if e := os.Link(p, filepath.Join(dir, "link")); e != nil {
					t.Fatal(e)
				}
			case "group-readable":
				if e := os.Chmod(p, 0640); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := nasRotationRead(p, 0); e == nil {
				t.Fatal("unsafe metadata accepted")
			}
		})
	}
}

func TestNASRotationStopEscapeDoesNotRequireRepairedState(t *testing.T) {
	f := nasRotationTestFixture(t, "proxy")
	r := f.r
	f.request(t)
	if e := r.publish(); e != nil {
		t.Fatal(e)
	}
	if e := r.install(); e != nil {
		t.Fatal(e)
	}
	nasRotationTestJSON(t, r.state, map[string]string{"version": "unknown-interrupted-state"})
	for id, c := range f.containers {
		c.State.Running = true
		c.State.Pid = 1
		f.containers[id] = c
	}
	if e := r.engine.stop(r); e == nil {
		t.Fatal("normal mutation ignored changed state")
	}
	r.stopOnly = true
	if e := r.engine.stop(r); e != nil {
		t.Fatal(e)
	}
	for _, c := range f.containers {
		if c.Config.Labels["com.docker.compose.service"] != "platform" && c.State.Running {
			t.Fatal("escape did not stop fixed signer service")
		}
	}
	if _, e := r.stateCheckpoint(nil); e == nil {
		t.Fatal("escape repaired changed state")
	}
}
