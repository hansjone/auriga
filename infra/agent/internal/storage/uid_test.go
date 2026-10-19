package storage_test

import (
	"path/filepath"
	"testing"

	"github.com/hansjone/auriga/infra/agent/internal/storage"
)

func TestUIDTableUnique(t *testing.T) {
	tab := storage.NewUIDTable(t.TempDir())
	u1, err := tab.Allocate("inst_a")
	if err != nil || u1 < 200000 {
		t.Fatalf("%d %v", u1, err)
	}
	u1b, err := tab.Allocate("inst_a")
	if err != nil || u1b != u1 {
		t.Fatalf("stable %d %d", u1, u1b)
	}
	u2, err := tab.Allocate("inst_b")
	if err != nil || u2 == u1 {
		t.Fatalf("unique %d %d", u1, u2)
	}
	if err := tab.Release("inst_a"); err != nil {
		t.Fatal(err)
	}
	// table file exists
	if _, err := filepath.Glob(filepath.Join(filepath.Dir(tab.Path), "*")); err != nil {
		t.Fatal(err)
	}
}
