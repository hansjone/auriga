package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtraImageRejectsBadArgumentsAndArchiveBeforeDocker(t *testing.T) {
	for _, args := range [][]string{nil, {"a"}, {"a", ""}, {"a", "-bad"}, {"a", "tag", "mutable:latest"}} {
		if err := extraImageCommand(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "extra.tar")
	if err := os.WriteFile(file, imageTestArchive(t, "auriga/offline-postgres:test", false, nil), 0600); err != nil {
		t.Fatal(err)
	}
	err := extraImageCommand(context.Background(), []string{file, "wrong:tag"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "wrong fixed image tag") {
		t.Fatalf("%v", err)
	}
}
