package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPoolQuotaRejectsSuccessfulExitWithBadTree(t *testing.T) {
	good := "Checking project 42 (path /data/a)...\nProcessed 1 (/etc/projects and cmdline) paths for project 42 with recursion depth infinite (-1).\n"
	if e := checkXFSProject(good, "/data/a", 42); e != nil {
		t.Fatal(e)
	}
	for _, out := range []string{"", good + "/data/a - project inheritance flag is not set\n", "Checking project 42 (path /data/a)...\n/data/a - project identifier is not set (inode=0, tree=42)\nProcessed 1 (/etc/projects and cmdline) paths for project 42 with recursion depth infinite (-1).\n"} {
		if e := checkXFSProject(out, "/data/a", 42); e == nil {
			t.Fatal("bad project tree")
		}
	}
}

func TestPoolLayoutRequiresHardQuota(t *testing.T) {
	l := &PoolLayout{Root: t.TempDir()}
	if _, e := l.Prepare(context.Background(), "pool", "tenant", "member", 1); e == nil {
		t.Fatal("soft quota fallback")
	}
	for _, part := range []string{"..", "x/y", `x\y`, "x:y", "/tmp", ""} {
		if _, e := l.Workspace("pool", "tenant", part, 1); e == nil {
			t.Fatal(part)
		}
	}
}
func TestPoolLayoutRejectsLinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "pool"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "pool", "members")); e != nil {
		t.Skipf("symlink unavailable: %v", e)
	}
	l := &PoolLayout{Root: root}
	if _, e := l.Workspace("pool", "tenant", "member", 1); e == nil {
		t.Fatal("linked parent")
	}
}
func TestPoolQuotaRejectsAbsentOrDuplicateProjects(t *testing.T) {
	q := &XFSProjectQuota{Projects: map[string]uint32{"/data/a": 1, "/data/b": 1}}
	for _, p := range []string{"/data/a", "/data/missing"} {
		if e := q.Ensure(context.Background(), p, MemberBytes, MemberInodes); e == nil {
			t.Fatal(p)
		}
	}
}
