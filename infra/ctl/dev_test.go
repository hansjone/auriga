package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCredentialsAndPaths(t *testing.T) {
	dir := t.TempDir()
	first, err := credentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := credentials(dir)
	if err != nil || first != second || len(first) != 64 {
		t.Fatal("credentials not persisted", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.env"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials(dir); err == nil {
		t.Fatal("corrupt credentials accepted")
	}
	for _, runtime := range []string{dir, filepath.Join(dir, "pg"), filepath.Join(dir, "pg", "nested"), filepath.Join(dir, "cache")} {
		if _, err := (devOptions{DataDir: dir, RuntimeDir: runtime, PGPort: 15433, NATSPort: 14222}).normalized(); err == nil {
			t.Fatalf("unsafe runtime %s", runtime)
		}
	}
	missing := t.TempDir()
	if err := os.Mkdir(filepath.Join(missing, "pg"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missing, "pg", "PG_VERSION"), []byte("17"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials(missing); err == nil {
		t.Fatal("missing credentials accepted")
	}
}

func startForTest(t *testing.T, o devOptions) devConnections {
	t.Helper()
	services, err := startDev(context.Background(), o, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := services.Close(); err != nil {
			t.Error(err)
		}
		for _, port := range []int{int(o.PGPort), o.NATSPort} {
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
			if err != nil {
				t.Error("port left open", port, err)
			} else {
				_ = l.Close()
			}
		}
	})
	return services.Connections
}

func fmtPort(port int) string { return fmt.Sprintf("%d", port) }

func TestDevLifecycle(t *testing.T) {
	dir := t.TempDir()
	o := devOptions{DataDir: dir, PGPort: uint32(freePort(t)), NATSPort: freePort(t)}
	for _, phase := range []string{"initial", "restart"} {
		passed := t.Run(phase, func(t *testing.T) {
			c := startForTest(t, o)
			db, err := sql.Open("postgres", c.Postgres)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			var version string
			if err := db.QueryRow("SHOW server_version").Scan(&version); err != nil || !strings.HasPrefix(version, "17.") {
				t.Fatal(version, err)
			}
			var super bool
			if err := db.QueryRow("SELECT rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&super); err != nil || !super {
				t.Fatal("not a dev superuser", err)
			}
			nc, err := nats.Connect(c.NATS, nats.Timeout(5*time.Second), nats.MaxReconnects(0))
			if err != nil {
				t.Fatal(err)
			}
			defer nc.Close()
			js, err := nc.JetStream()
			if err != nil {
				t.Fatal(err)
			}
			if phase == "initial" {
				if _, err := db.Exec("CREATE TABLE lifecycle (id integer); INSERT INTO lifecycle VALUES (42)"); err != nil {
					t.Fatal(err)
				}
				if _, err := js.AddStream(&nats.StreamConfig{Name: "LIFECYCLE", Subjects: []string{"test.lifecycle"}, Storage: nats.FileStorage}); err != nil {
					t.Fatal(err)
				}
				if _, err := js.Publish("test.lifecycle", []byte("persisted")); err != nil {
					t.Fatal(err)
				}
			} else {
				var id int
				if err := db.QueryRow("SELECT id FROM lifecycle").Scan(&id); err != nil || id != 42 {
					t.Fatal("PG data lost", err)
				}
				info, err := js.StreamInfo("LIFECYCLE")
				if err != nil {
					t.Fatal(err)
				}
				if info.State.Msgs != 1 {
					t.Fatal("JetStream data lost")
				}
			}
			// Concurrent commands must fail before touching active PostgreSQL binaries.
			if err := runDev(context.Background(), o, io.Discard, slog.Default()); err == nil {
				t.Fatal("second process acquired the dev directory")
			}
		})
		if !passed {
			t.Fatal("cannot proceed with lifecycle checks after failed startup")
		}
	}
	// A NATS bind failure must unwind the already started PostgreSQL process.
	t.Run("startup-failure", func(t *testing.T) {
		occupied, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(o.NATSPort)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = occupied.Close() }()
		if err := runDev(context.Background(), o, io.Discard, slog.New(slog.NewJSONHandler(io.Discard, nil))); err == nil || !strings.Contains(err.Error(), "NATS did not become ready") {
			t.Fatal("expected NATS startup failure", err)
		}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(int(o.PGPort))))
		if err != nil {
			t.Fatal("PG left running", err)
		}
		_ = l.Close()
	})
}

func TestNATSLifecycle(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	for _, phase := range []string{"initial", "restart"} {
		t.Run(phase, func(t *testing.T) {
			srv, err := startNATS(port, dir, "auriga", "test-nats-password")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { srv.Shutdown(); srv.WaitForShutdown() }()
			nc, err := nats.Connect(authenticatedNATSURL(port, "auriga", "test-nats-password"), nats.Timeout(5*time.Second), nats.MaxReconnects(0))
			if err != nil {
				t.Fatal(err)
			}
			defer nc.Close()
			js, err := nc.JetStream()
			if err != nil {
				t.Fatal(err)
			}
			if phase == "initial" {
				if _, err := js.AddStream(&nats.StreamConfig{Name: "NATS_LIFECYCLE", Subjects: []string{"test.nats"}, Storage: nats.FileStorage}); err != nil {
					t.Fatal(err)
				}
				if _, err := js.Publish("test.nats", []byte("persisted")); err != nil {
					t.Fatal(err)
				}
			} else {
				info, err := js.StreamInfo("NATS_LIFECYCLE")
				if err != nil {
					t.Fatal(err)
				}
				if info.State.Msgs != 1 {
					t.Fatal("JetStream persistence lost")
				}
			}
		})
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal("port not released", err)
	}
	defer func() { _ = listener.Close() }()
	if _, err := startNATS(port, dir, "auriga", "test-nats-password"); err == nil {
		t.Fatal("occupied port accepted")
	}
}

func TestDevCommandCancellation(t *testing.T) {
	dir := t.TempDir()
	o := devOptions{DataDir: dir, PGPort: uint32(freePort(t)), NATSPort: freePort(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	done := make(chan error, 1)
	go func() {
		err := runDev(ctx, o, writer, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		_ = writer.CloseWithError(err)
		done <- err
	}()
	scan := bufio.NewScanner(reader)
	for range 5 {
		if !scan.Scan() {
			t.Fatalf("command failed before ready: %v", scan.Err())
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("command did not stop")
	}
	for _, port := range []int{int(o.PGPort), o.NATSPort} {
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
		if err != nil {
			t.Fatal("port left open", err)
		}
		_ = l.Close()
	}
}
