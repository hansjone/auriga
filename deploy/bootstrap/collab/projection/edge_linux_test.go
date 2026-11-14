package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func TestEdgePrivateProjectionAndSocket(t *testing.T) {
	if os.Geteuid() != 65532 || os.Getegid() != 65532 {
		t.Skip("requires actual UID/GID65532")
	}
	if e := RequireProjectorUID("edge"); e != nil {
		t.Fatal(e)
	}
	if e := RequireProjectorUID("network"); e == nil {
		t.Fatal("edge accepted network process UID")
	}
	root, control := privateTempDir(t), privateTempDir(t)
	g := localGeneration(t)
	defer g.Clear()
	g.Selector.Component = "edge"
	g.Manifest.Component = "edge"
	g.Selector.ManifestDigest, _ = ObjectDigest(g.Manifest)
	if e := Publish(root, g, verifyLocal); e != nil {
		t.Fatal(e)
	}
	reader, e := NewReader(root, control, privateTempDir(t), func(*Generation) (func() error, error) { return func() error { return nil }, nil })
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reader.Serve(ctx, "edge") }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Lstat(filepath.Join(control, "reload.sock")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("edge socket not listening")
		}
		time.Sleep(time.Millisecond)
	}
	st, e := os.Lstat(filepath.Join(control, "reload.sock"))
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("socket not private", e)
	}
	q := api.LocalProjectionRequest{Operation: "reload", Selector: g.Selector}
	if _, e = Exchange(ctx, control, "edge", q); e != nil {
		t.Fatal(e)
	}
	q.Operation = "confirm"
	if _, e = Exchange(ctx, control, "edge", q); e != nil {
		t.Fatal(e)
	}
	if _, e = Exchange(ctx, control, "network", q); e == nil {
		t.Fatal("accepted incorrect peer expectation")
	}
}
