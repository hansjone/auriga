package storage_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

func TestEnsureLayout(t *testing.T) {
	l := &storage.Layout{InstancesDir: t.TempDir()}
	p, err := l.Ensure("inst_01", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		p.Home, p.Meta, p.Trash,
		filepath.Join(p.Home, "workspace"),
		filepath.Join(p.Home, ".dsh"),
	} {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			t.Fatalf("%s: %v", dir, err)
		}
	}
	v, err := l.SchemaVersion("inst_01")
	if err != nil || v != 1 {
		t.Fatalf("schema %d %v", v, err)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	l := &storage.Layout{InstancesDir: t.TempDir()}
	if err := l.Migrate("inst_m", 1); err != nil {
		t.Fatal(err)
	}
	if err := l.Migrate("inst_m", 1); err != nil {
		t.Fatal(err)
	}
	v, _ := l.SchemaVersion("inst_m")
	if v != 1 {
		t.Fatalf("got %d", v)
	}
}

func TestRejectPathEscapeAndDot(t *testing.T) {
	l := &storage.Layout{InstancesDir: t.TempDir()}
	for _, id := range []string{"../evil", ".", "..", "", "a/b", "a\\b", "a:b", " a", ".hidden"} {
		if _, err := l.Ensure(id, 0, 0); err == nil {
			t.Fatalf("expected reject for %q", id)
		}
		if err := l.Remove(id); err == nil {
			t.Fatalf("Remove(%q) should fail", id)
		}
	}
}

func TestChownRecursiveHome(t *testing.T) {
	l := &storage.Layout{InstancesDir: t.TempDir()}
	seen := map[string]bool{}
	l.Chown = func(path string, uid, gid int) error {
		seen[filepath.Base(path)] = true
		if uid != 200001 || gid != 200001 {
			t.Fatalf("uid/gid %d/%d", uid, gid)
		}
		return nil
	}
	if _, err := l.Ensure("inst_ch", 200001, 200001); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "meta", "workspace", ".dsh"} {
		if !seen[name] {
			t.Fatalf("missing chown of %s: %v", name, seen)
		}
	}
}

func TestSoftQuota(t *testing.T) {
	l := &storage.Layout{InstancesDir: t.TempDir()}
	p, err := l.Ensure("q1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 2<<20)
	if err := os.WriteFile(filepath.Join(p.Home, "workspace", "blob"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	over, _, err := l.SoftQuotaExceeded("q1", 0)
	if err != nil || over {
		t.Fatalf("no limit: over=%v err=%v", over, err)
	}
	over, used, err := l.SoftQuotaExceeded("q1", 1)
	if err != nil || over || used < 2 {
		t.Fatalf("under: over=%v used=%d err=%v", over, used, err)
	}
}
