package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizedMaterializeErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secret-options.json")
	for _, tc := range []struct {
		name, input, category string
		args                  []string
		code                  int
	}{
		{"usage", "", "usage", nil, 2},
		{"read", "", "options_read_failed", []string{dir, filepath.Join(dir, "missing-secret")}, 1},
		{"json", `{"secret":"private-value"`, "options_invalid_json", []string{dir, file}, 1},
		{"options", `{}`, "invalid_options", []string{dir, file}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(file, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if code := run(tc.args, &out); code != tc.code {
				t.Fatalf("code=%d", code)
			}
			if !strings.Contains(out.String(), "category="+tc.category) || strings.Contains(out.String(), dir) || strings.Contains(out.String(), "private-value") {
				t.Fatalf("unsafe or absent category: %s", out.String())
			}
		})
	}
}
