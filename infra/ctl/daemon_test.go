package main

import (
	"bytes"
	"context"
	"database/sql"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func buildCtl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	name := "auriga-ctl"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)
	if testing.Short() {
		t.Skip("building ctl binary")
	}
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	return out
}

func TestStartStopStatusLifecycle(t *testing.T) {
	bin := buildCtl(t)
	dir := t.TempDir()
	pgPort := freePort(t)
	natsPort := freePort(t)

	runCtl := func(args ...string) (string, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		err := cmd.Run()
		return out.String(), errBuf.String(), err
	}

	t.Cleanup(func() {
		_, _, _ = runCtl("stop", "--data-dir", dir)
	})
	out, errBuf, err := runCtl("start",
		"--data-dir", dir,
		"--pg-port", fmtPort(pgPort),
		"--nats-port", fmtPort(natsPort),
	)
	if err != nil {
		t.Fatalf("start: %v\nout=%s\nerr=%s", err, out, errBuf)
	}
	if !strings.Contains(out, "started pid=") || strings.Contains(out, "PGPASSWORD") {
		t.Fatalf("unexpected start output: %q", out)
	}

	out, errBuf, err = runCtl("status", "--data-dir", dir)
	if err != nil {
		t.Fatalf("status running: %v\nout=%s\nerr=%s", err, out, errBuf)
	}
	if !strings.Contains(out, "Status: running") || strings.Contains(strings.ToLower(out), "password=") {
		t.Fatalf("status leaked secrets or wrong state: %q", out)
	}

	// Idempotent second start.
	out, errBuf, err = runCtl("start",
		"--data-dir", dir,
		"--pg-port", fmtPort(pgPort),
		"--nats-port", fmtPort(natsPort),
	)
	if err != nil {
		t.Fatalf("second start: %v\nout=%s\nerr=%s", err, out, errBuf)
	}
	if !strings.Contains(out, "already running") {
		t.Fatalf("expected idempotent start, got %q", out)
	}

	body, err := os.ReadFile(filepath.Join(dir, "credentials.env"))
	if err != nil {
		t.Fatal(err)
	}
	password := strings.TrimSpace(strings.TrimPrefix(string(body), "PGPASSWORD="))
	db, err := sql.Open("postgres",
		"postgres://auriga_dev:"+password+"@127.0.0.1:"+fmtPort(pgPort)+"/auriga?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, schema := range serviceSchemas {
		var exists bool
		if err := db.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema,
		).Scan(&exists); err != nil || !exists {
			t.Fatalf("schema %s missing: %v", schema, err)
		}
		var roleExists bool
		if err := db.QueryRow(
			`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, roleName(schema),
		).Scan(&roleExists); err != nil || !roleExists {
			t.Fatalf("role %s missing: %v", roleName(schema), err)
		}
	}
	_ = db.Close()

	out, errBuf, err = runCtl("stop", "--data-dir", dir)
	if err != nil {
		t.Fatalf("stop: %v\nout=%s\nerr=%s", err, out, errBuf)
	}
	if !strings.Contains(out, "stopped pid=") {
		t.Fatalf("unexpected stop output: %q", out)
	}

	cmd := exec.Command(bin, "status", "--data-dir", dir)
	var statusOut bytes.Buffer
	cmd.Stdout = &statusOut
	err = cmd.Run()
	if err == nil {
		t.Fatal("status should exit non-zero when stopped")
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != statusExitStopped {
		t.Fatalf("want exit %d, got %v (out=%q)", statusExitStopped, err, statusOut.String())
	}
	if !strings.Contains(statusOut.String(), "Status: stopped") {
		t.Fatalf("status stopped output: %q", statusOut.String())
	}

	for _, port := range []int{pgPort, natsPort} {
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
		if err != nil {
			t.Fatal("port left open", port, err)
		}
		_ = l.Close()
	}

	out, errBuf, err = runCtl("stop", "--data-dir", dir)
	if err != nil {
		t.Fatalf("second stop: %v\nout=%s\nerr=%s", err, out, errBuf)
	}
	if !strings.Contains(out, "already stopped") {
		t.Fatalf("expected already stopped, got %q", out)
	}
}

func TestStatusAndStopViaRun(t *testing.T) {
	dir := t.TempDir()
	var out, logs bytes.Buffer
	err := run(context.Background(), []string{"status", "--data-dir", dir}, &out, &logs)
	if !isStatusStopped(err) {
		t.Fatalf("want stopped, got %v out=%q", err, out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"stop", "--data-dir", dir}, &out, &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already stopped") {
		t.Fatal(out.String())
	}
}

func TestDevAndStartShareLock(t *testing.T) {
	dir := t.TempDir()
	o := devOptions{DataDir: dir, PGPort: uint32(freePort(t)), NATSPort: freePort(t)}
	_ = startForTest(t, o)
	var out, logs bytes.Buffer
	err := run(context.Background(), []string{
		"start",
		"--data-dir", dir,
		"--pg-port", fmtPort(int(o.PGPort)),
		"--nats-port", fmtPort(o.NATSPort),
	}, &out, &logs)
	if err == nil {
		t.Fatalf("start overlapped with foreground dev: %s", out.String())
	}
	if !strings.Contains(err.Error(), "locked by pid") {
		t.Fatalf("want lock error, got %v", err)
	}
}
