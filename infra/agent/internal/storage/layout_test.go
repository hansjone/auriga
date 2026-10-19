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

func TestRejectPathEscape(t *testing.T) {
	l := &storage.Layout{InstancesDir: t.TempDir()}
	if _, err := l.Ensure("../evil", 0, 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestSoftQuota(t *testing.T) {
	l := &storage.Layout{InstancesDir: t.TempDir()}
	p, err := l.Ensure("q1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 2<<20) // 2 MiB
	if err := os.WriteFile(filepath.Join(p.Home, "workspace", "blob"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	// diskGB=0 → no limit
	over, _, err := l.SoftQuotaExceeded("q1", 0)
	if err != nil || over {
		t.Fatalf("no limit: over=%v err=%v", over, err)
	}
	// 1 GB limit, 2 MiB used → not over
	over, used, err := l.SoftQuotaExceeded("q1", 1)
	if err != nil || over || used < 2 {
		t.Fatalf("under: over=%v used=%d err=%v", over, used, err)
	}
}
