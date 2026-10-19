package runtime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/agent/internal/artifacts"
	"github.com/hansjone/auriga/infra/agent/internal/identity"
	"github.com/hansjone/auriga/infra/agent/internal/policy"
	"github.com/hansjone/auriga/infra/agent/internal/renderer"
	"github.com/hansjone/auriga/infra/agent/internal/runtime"
	"github.com/hansjone/auriga/infra/agent/internal/storage"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
)

type memSpecs map[string]*nodev1.InstanceSpec

func (m memSpecs) GetDesired(id string) (*nodev1.InstanceSpec, error) { return m[id], nil }

func TestPreparingFetchesAndRenders(t *testing.T) {
	body := []byte("plugin-bytes")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := renderer.SealForTest(pub, "prep1", "instance_token", 1, []byte("tok"))
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	pol := policy.Default()
	pol.AllowAnyArtifact = true
	art := artifacts.New(filepath.Join(root, "artifacts"), pol)
	art.Client = srv.Client()
	layout := &storage.Layout{InstancesDir: filepath.Join(root, "instances")}
	uids := storage.NewUIDTable(filepath.Join(root, "agent"))
	inner := runtime.NewFake()
	imgDig := "sha256:" + hex.EncodeToString(sum[:])
	specs := memSpecs{
		"prep1": &nodev1.InstanceSpec{
			InstanceId: "prep1",
			Generation: 1,
			OwnerId:    "u1",
			TenantId:   "t1",
			Dsh: &nodev1.DshRuntime{
				Image: "registry.example.com/auriga/dsh@" + imgDig,
			},
			Plugins: []*nodev1.PluginRef{{
				Name: "auriga-dsh", Version: "1.0.0", Url: srv.URL + "/p", Digest: digest, Enabled: true,
			}},
			Resources: &nodev1.Resources{CpuMillis: 1000, MemoryMb: 512, Pids: 128},
			Profile:   &nodev1.Profile{ListenPort: 7860},
			Storage:   &nodev1.Storage{SchemaVersion: 1},
			Secrets:   &nodev1.Secrets{InstanceToken: sealed},
			Desired:   nodev1.DesiredState_DESIRED_STATE_RUNNING,
		},
	}
	var chowned int
	prep := &runtime.Preparing{
		Inner:          inner,
		Specs:          specs,
		Artifacts:      art,
		Layout:         layout,
		UIDs:           uids,
		Policy:         policy.Default(),
		SealingPrivate: priv,
		PlatformURL:    "https://edge.example.com",
		Chown: func(path string, uid, gid int) error {
			chowned++
			if uid < 200000 {
				t.Fatalf("uid %d", uid)
			}
			return nil // CI runners are not root; production uses storage.Lchown
		},
	}
	if err := prep.Ensure(context.Background(), &runtime.Spec{InstanceID: "prep1", Generation: 1}, &runtime.Rendered{}); err != nil {
		t.Fatal(err)
	}
	if chowned == 0 {
		t.Fatal("expected Chown calls")
	}
	tokenPath := filepath.Join(root, "instances", "prep1", "meta", "instance.token")
	got, err := os.ReadFile(tokenPath)
	if err != nil || string(got) != "tok" {
		t.Fatalf("token %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(art.Root, "sha256", hex.EncodeToString(sum[:]))); err != nil {
		t.Fatal("artifact missing:", err)
	}
	if err := prep.Remove(context.Background(), "prep1"); err != nil {
		t.Fatal(err)
	}
}

func TestPreparingUpgradeReleasesOldArtifact(t *testing.T) {
	oldBody := []byte("plugin-v1")
	newBody := []byte("plugin-v2-bytes")
	oldSum := sha256.Sum256(oldBody)
	newSum := sha256.Sum256(newBody)
	oldDig := "sha256:" + hex.EncodeToString(oldSum[:])
	newDig := "sha256:" + hex.EncodeToString(newSum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "v2") {
			_, _ = w.Write(newBody)
			return
		}
		_, _ = w.Write(oldBody)
	}))
	t.Cleanup(srv.Close)

	priv, pub, err := identity.GenerateSealingKey()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := renderer.SealForTest(pub, "prep2", "instance_token", 1, []byte("tok"))
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	pol := policy.Default()
	pol.AllowAnyArtifact = true
	art := artifacts.New(filepath.Join(root, "artifacts"), pol)
	art.Client = srv.Client()
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	art.Now = func() time.Time { return now }

	spec := &nodev1.InstanceSpec{
		InstanceId: "prep2",
		Generation: 1,
		OwnerId:    "u1",
		TenantId:   "t1",
		Dsh: &nodev1.DshRuntime{
			Image: "registry.example.com/auriga/dsh@sha256:" + hex.EncodeToString(oldSum[:]),
		},
		Plugins: []*nodev1.PluginRef{{
			Name: "auriga-dsh", Version: "1.0.0", Url: srv.URL + "/v1", Digest: oldDig, Enabled: true,
		}},
		Resources: &nodev1.Resources{CpuMillis: 1000, MemoryMb: 512, Pids: 128},
		Profile:   &nodev1.Profile{ListenPort: 7860},
		Secrets:   &nodev1.Secrets{InstanceToken: sealed},
		Desired:   nodev1.DesiredState_DESIRED_STATE_RUNNING,
	}
	specs := memSpecs{"prep2": spec}
	prep := &runtime.Preparing{
		Inner:          runtime.NewFake(),
		Specs:          specs,
		Artifacts:      art,
		Layout:         &storage.Layout{InstancesDir: filepath.Join(root, "instances")},
		UIDs:           storage.NewUIDTable(filepath.Join(root, "agent")),
		Policy:         policy.Default(),
		SealingPrivate: priv,
		PlatformURL:    "https://edge.example.com",
		Chown:          func(string, int, int) error { return nil },
	}
	if err := prep.Ensure(context.Background(), &runtime.Spec{InstanceID: "prep2"}, &runtime.Rendered{}); err != nil {
		t.Fatal(err)
	}
	sealed2, err := renderer.SealForTest(pub, "prep2", "instance_token", 2, []byte("tok"))
	if err != nil {
		t.Fatal(err)
	}
	spec.Plugins[0].Url = srv.URL + "/v2"
	spec.Plugins[0].Digest = newDig
	spec.Generation = 2
	spec.Secrets = &nodev1.Secrets{InstanceToken: sealed2}
	if err := prep.Ensure(context.Background(), &runtime.Spec{InstanceID: "prep2"}, &runtime.Rendered{}); err != nil {
		t.Fatal(err)
	}
	art.Now = func() time.Time { return now.Add(8 * 24 * time.Hour) }
	n, err := art.GC(false)
	if err != nil || n != 1 {
		t.Fatalf("old digest should be GC'd after upgrade sync: n=%d err=%v", n, err)
	}
}
