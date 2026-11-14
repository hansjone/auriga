package storage

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestPoolProjectsAllocationRestartExhaustionAndRecycle(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "control", "project-ids.db")
	a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	ids := &ProjectIDs{File: file, First: 100, Last: 101}
	var first uint32
	if e := ids.Use(a, func(id uint32) error { first = id; return errors.New("kernel assignment interrupted") }); e == nil {
		t.Fatal("missing failure")
	}
	// New allocator object recovers the committed reservation after failed work.
	ids = &ProjectIDs{File: file, First: 100, Last: 101}
	if e := ids.Use(a, func(id uint32) error {
		if id != first {
			t.Fatal("reservation changed")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := ids.Use(b, func(id uint32) error {
		if id == first {
			t.Fatal("duplicate id")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := ids.Use(c, func(uint32) error { return nil }); e == nil {
		t.Fatal("range overallocated")
	}
	if e := os.MkdirAll(a, 0700); e != nil {
		t.Fatal(e)
	}
	if e := ids.Release(a, func(uint32) error { return nil }); e == nil {
		t.Fatal("recycled live path")
	}
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	if e := ids.Release(a, func(uint32) error { return errors.New("kernel cleanup unconfirmed") }); e == nil {
		t.Fatal("cleanup ignored")
	}
	if e := ids.Use(c, func(uint32) error { return nil }); e == nil {
		t.Fatal("recycled unconfirmed project")
	}
	if e := ids.Release(a, func(uint32) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e := ids.Use(c, func(id uint32) error {
		if id != first {
			t.Fatal("id not recycled")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	changed := &ProjectIDs{File: file, First: 100, Last: 102}
	if e := changed.Use(c, func(uint32) error { return nil }); e == nil {
		t.Fatal("range changed over ledger")
	}
}

func TestPoolProjectsConcurrentObjectsDoNotDuplicate(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "project-ids.db")
	results := make(chan uint32, 8)
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids := &ProjectIDs{File: file, First: 100, Last: 107}
			failures <- ids.Use(filepath.Join(root, string(rune('a'+i))), func(id uint32) error { results <- id; return nil })
		}(i)
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	seen := map[uint32]bool{}
	for id := range results {
		if seen[id] {
			t.Fatal("duplicate", id)
		}
		seen[id] = true
	}
	if len(seen) != 8 {
		t.Fatal(seen)
	}
}

func TestPoolProjectsCrashChild(t *testing.T) {
	file := os.Getenv("AURIGA_POOL_PROJECT_CRASH_LEDGER")
	if file == "" {
		return
	}
	ids := &ProjectIDs{File: file, First: 200, Last: 201}
	if e := ids.Use(filepath.Join(filepath.Dir(file), "workspace"), func(uint32) error { os.Exit(23); return nil }); e != nil {
		t.Fatal(e)
	}
}

func TestPoolProjectsCrashKeepsReservation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "project-ids.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPoolProjectsCrashChild$")
	cmd.Env = append(os.Environ(), "AURIGA_POOL_PROJECT_CRASH_LEDGER="+file)
	out, e := cmd.CombinedOutput()
	var exited *exec.ExitError
	if !errors.As(e, &exited) || exited.ExitCode() != 23 {
		t.Fatalf("crash helper: %v %s", e, out)
	}
	ids := &ProjectIDs{File: file, First: 200, Last: 201}
	if e = ids.Use(filepath.Join(filepath.Dir(file), "workspace"), func(id uint32) error {
		if id != 200 {
			t.Fatal(id)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = ids.Use(filepath.Join(filepath.Dir(file), "other"), func(id uint32) error {
		if id != 201 {
			t.Fatal(id)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
