package materials

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipePublicFileLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int
		limit int64
		valid bool
	}{
		{"default boundary", 1 << 20, 0, true}, {"default rejects larger", (1 << 20) + 1, 0, false},
		{"proof boundary", 4 << 20, 4 << 20, true}, {"proof rejects larger", (4 << 20) + 1, 4 << 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			set := MaterialSet{PublicFiles: []string{"proof.json"}}
			if tc.limit != 0 {
				set.PublicFileLimits = map[string]int64{"proof.json": tc.limit}
			}
			path := filepath.Join(out, "proof.json")
			if err := os.WriteFile(path, bytes.Repeat([]byte("a"), tc.size), 0o644); err != nil {
				t.Fatal(err)
			}
			err := SealReceipt(out, "input", set)
			if (err == nil) != tc.valid {
				t.Fatalf("seal valid=%t: %v", tc.valid, err)
			}
			if !tc.valid {
				return
			}
			if reuse, err := CheckReceipt(out, "input", set); err != nil || !reuse {
				t.Fatalf("reuse: %v", err)
			}
			if err := os.WriteFile(path, bytes.Repeat([]byte("b"), tc.size), 0o644); err != nil {
				t.Fatal(err)
			}
			if reuse, err := CheckReceipt(out, "input", set); err == nil || reuse {
				t.Fatal("tampered proof reused")
			}
		})
	}
	for _, limits := range []map[string]int64{{"proof": 0}, {"proof": -1}, {"proof": (4 << 20) + 1}, {"other": 1}, {"managed": 1}} {
		set := MaterialSet{PublicFiles: []string{"proof"}, ManagedDirectories: []string{"managed"}, PublicFileLimits: limits}
		if set.validate() == nil {
			t.Fatalf("invalid limits accepted: %v", limits)
		}
	}
}
