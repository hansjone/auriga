//go:build linux

package storage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Formats only a new regular file. No loop attachment, mount, existing disk or
// root privilege is involved. This exercises the actual production argv using
// the installed mke2fs parser, then checks its real superblock and feature bits.
func TestExt4FormatWithRealMke2fs(t *testing.T) {
	binary := ""
	for _, candidate := range []string{"/usr/sbin/mkfs.ext4", "/sbin/mkfs.ext4"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			binary = candidate
			break
		}
	}
	if binary == "" {
		t.Fatal("real mkfs.ext4 required; run this regression in the Agent image with e2fsprogs installed")
	}
	image := filepath.Join(t.TempDir(), "only-new-regular-file.ext4")
	file, err := os.OpenFile(image, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 << 20
	err = file.Truncate(size)
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err = formatExt4Image(t.Context(), binary, image, size); err != nil {
		t.Fatal(err)
	}
	if err = verifyExt4Image(image, size); err != nil {
		t.Fatal(err)
	}
}

func TestStorageCommandDiagnostics(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mkfs.ext4")
	// A controlled failing executable proves diagnostics/unwrap, not mkfs syntax.
	script := "#!/bin/sh\nprintf 'synthetic-format-diagnostic\\n' >&2\nexit 19\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := storageCommand(t.Context(), binary, "-E", "synthetic-safe-option", "synthetic-target")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 19 || !strings.Contains(err.Error(), "synthetic-format-diagnostic") || !strings.Contains(err.Error(), "synthetic-safe-option") {
		t.Fatalf("lost fixed-tool stderr/argv/exit cause: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = storageCommand(ctx, binary); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation cause: %v", err)
	}
	if _, err = storageCommand(t.Context(), "/usr/bin/env"); err == nil {
		t.Fatal("arbitrary executable accepted")
	}
}

func TestStorageCommandDiagnosticsBoundOutput(t *testing.T) {
	var stderr storageStderr
	input := []byte(strings.Repeat("x", 16000))
	if n, err := stderr.Write(input); err != nil || n != len(input) {
		t.Fatal("diagnostic writer interfered with command output", n, err)
	}
	if len(stderr.data) != 8192 || !stderr.truncated {
		t.Fatal("unbounded storage diagnostics")
	}
}
