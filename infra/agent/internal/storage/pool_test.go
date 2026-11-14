package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
func TestPoolQuotaRejectsMissingLedger(t *testing.T) {
	q := &XFSProjectQuota{}
	if e := q.Ensure(context.Background(), filepath.Join(t.TempDir(), "workspace"), MemberBytes, MemberInodes); e == nil {
		t.Fatal("missing ledger")
	}
}

func TestPoolQuotaRecycleRequiresKernelReadback(t *testing.T) {
	root := t.TempDir()
	ids := &ProjectIDs{File: filepath.Join(root, "ids.db"), First: 100, Last: 100}
	old, next := filepath.Join(root, "archive"), filepath.Join(root, "next")
	if e := ids.Use(old, func(uint32) error { return nil }); e != nil {
		t.Fatal(e)
	}
	q := &XFSProjectQuota{IDs: ids, run: func(_ context.Context, command string) (string, error) {
		if strings.HasPrefix(command, "limit") {
			return "", nil
		}
		return "#100 1 0 0 0 0\n", nil
	}}
	if e := q.Release(context.Background(), old); e == nil {
		t.Fatal("open unlinked inode released")
	}
	if e := ids.Use(next, func(uint32) error { return nil }); e == nil {
		t.Fatal("unconfirmed id recycled")
	}
	q.run = func(_ context.Context, command string) (string, error) {
		if strings.HasPrefix(command, "limit") {
			return "", nil
		}
		return "#100 0 0 0 0 0\n", nil
	}
	if e := q.Release(context.Background(), old); e != nil {
		t.Fatal(e)
	}
	if e := ids.Use(next, func(id uint32) error {
		if id != 100 {
			t.Fatal(id)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
