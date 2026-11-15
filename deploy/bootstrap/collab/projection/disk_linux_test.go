package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/hansjone/auriga/infra/gen/msgnetapi"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if e := os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	return path
}

func localGeneration(t *testing.T) *Generation {
	t.Helper()
	var cases []struct {
		Input struct {
			Request  api.LocalProjectionRequest
			Manifest api.LocalProjectionManifest
			Files    map[string]string
		}
	}
	fixture(t, "msgnet-local-projection-vectors.json", &cases)
	v := cases[0].Input
	g := &Generation{Selector: v.Request.Selector, Manifest: v.Manifest, Files: map[string][]byte{}}
	for n, b := range v.Files {
		g.Files[n] = []byte(b)
	}
	return g
}
func verifyLocal(g *Generation) error {
	return CheckFiles(g.Selector, g.Manifest, g.Files, g.Selector, nil)
}
func TestPublishCrashResumeAndReaderHighWater(t *testing.T) {
	root, control, state := privateTempDir(t), privateTempDir(t), privateTempDir(t)
	g := localGeneration(t)
	defer g.Clear()
	// A process died after its durable transaction and a partial generation file.
	raw, e := Bytes(g)
	if e != nil {
		t.Fatal(e)
	}
	if e = atomicFile(filepath.Join(root, "transaction.json"), raw); e != nil {
		t.Fatal(e)
	}
	if e = Recover(root, verifyLocal); e != nil {
		t.Fatal(e)
	}
	if e = Publish(root, g, verifyLocal); e != nil {
		t.Fatal("exact retry", e)
	}
	loaded := 0
	r, e := NewReader(root, control, state, func(*Generation) (func() error, error) { return func() error { loaded++; return nil }, nil })
	if e != nil {
		t.Fatal(e)
	}
	q := api.LocalProjectionRequest{Operation: "confirm", Selector: g.Selector}
	_, phase, _, _ := ResponseFields(r.Handle(q))
	if phase != "failed" {
		t.Fatal("confirmed before load")
	}
	q.Operation = "reload"
	_, phase, _, e = ResponseFields(r.Handle(q))
	if e != nil || phase != "loaded" || loaded != 1 {
		t.Fatal(phase, e)
	}
	restarted, e := NewReader(root, control, state, func(*Generation) (func() error, error) { return func() error { return nil }, nil })
	if e != nil {
		t.Fatal(e)
	}
	q.Operation = "confirm"
	_, phase, _, _ = ResponseFields(restarted.Handle(q))
	if phase != "failed" {
		t.Fatal("restart claimed active handle")
	}
	q.Operation = "reload"
	_, phase, _, _ = ResponseFields(restarted.Handle(q))
	if phase != "loaded" {
		t.Fatal("reload after restart", phase)
	}
	other := g.Selector
	other.Nonce = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	q.Selector = other
	_, _, code, _ := ResponseFields(restarted.Handle(q))
	if code != "same_version_conflict" {
		t.Fatal(code)
	}
}
func TestDiskRejectsSymlinkAndAdditionalFile(t *testing.T) {
	root := privateTempDir(t)
	g := localGeneration(t)
	defer g.Clear()
	if e := Publish(root, g, verifyLocal); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "generations", g.Selector.ManifestDigest[7:])
	extra := filepath.Join(dir, "unlisted.key")
	if e := os.WriteFile(extra, []byte("not a key"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadGeneration(root); e == nil {
		t.Fatal("accepted unlisted file")
	}
	if e := os.Remove(extra); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "publication.json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "publication.json")
	if e = os.WriteFile(outside, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, path); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadGeneration(root); e == nil {
		t.Fatal("accepted symlink")
	}
}
func TestSocketPeerFramingAndStaleRecovery(t *testing.T) {
	if os.Geteuid() != 10001 {
		t.Skip("requires actual UID10001")
	}
	root, control := privateTempDir(t), privateTempDir(t)
	g := localGeneration(t)
	defer g.Clear()
	if e := Publish(root, g, verifyLocal); e != nil {
		t.Fatal(e)
	}
	r, e := NewReader(root, control, privateTempDir(t), func(*Generation) (func() error, error) { return func() error { return nil }, nil })
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Serve(ctx, "network") }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	timeout := time.Now().Add(3 * time.Second)
	for {
		if _, e = os.Lstat(filepath.Join(control, "reload.sock")); e == nil {
			break
		}
		if time.Now().After(timeout) {
			t.Fatal("socket not listening")
		}
		time.Sleep(time.Millisecond)
	}
	reply, e := Exchange(ctx, control, "network", api.LocalProjectionRequest{Operation: "reload", Selector: g.Selector})
	if e != nil {
		t.Fatal(e)
	}
	_, phase, _, e := ResponseFields(reply)
	if e != nil || phase != "loaded" {
		t.Fatal(phase, e)
	}
	if _, e = Exchange(ctx, control, "edge", api.LocalProjectionRequest{Operation: "confirm", Selector: g.Selector}); e == nil {
		t.Fatal("accepted wrong server UID")
	}
}
