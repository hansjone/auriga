package testpg

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The test binary itself is the worker: no process-local mutex can coordinate
// these independent processes, just as admin and store are separate CI binaries.
func TestPostgresConcurrentFixtures(t *testing.T) {
	DSN(t)
	probe := fixtureProbe(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	type outcome struct {
		err    error
		output string
	}
	completed := make(chan outcome, 3)
	started := 0
	for range 3 {
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPostgresFixtureWorker$", "-test.v", "-test.timeout=80s")
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err = cmd.Start(); err != nil {
			t.Error(err)
			continue
		}
		started++
		go func() { err := cmd.Wait(); completed <- outcome{err, output.String()} }()
	}
	// Keep using a live low-privilege fixture while the child processes repeatedly
	// grant and revoke other roles; its privileges and tenant rows must survive.
	for remaining := started; remaining > 0; {
		select {
		case result := <-completed:
			remaining--
			if result.err != nil {
				t.Errorf("fixture subprocess: %v\n%s", result.err, result.output)
			}
		default:
			if err = probe(ctx); err != nil {
				t.Error(err)
				cancel()
				for ; remaining > 0; remaining-- {
					result := <-completed
					if result.err != nil {
						t.Logf("cancelled subprocess: %v\n%s", result.err, result.output)
					}
				}
			}
		}
	}
	if err = probe(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Also runs as a normal test, so absence of the required CI DSN still fails.
// Parallel subtests overlap setup/use/cleanup within every worker process.
func TestPostgresFixtureWorker(t *testing.T) {
	DSN(t)
	for i := range 3 {
		t.Run(fmt.Sprintf("fixture-%d", i), func(t *testing.T) {
			t.Parallel()
			probe := fixtureProbe(t)
			for range 20 {
				if err := probe(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func fixtureProbe(t *testing.T) func(context.Context) error {
	t.Helper()
	s := Open(t)
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	tenant := fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])
	other := "00000000-0000-7000-8000-000000000099"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := s.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES ($1,$1,'{}',1)", tenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Transaction(ctx, tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM ai.provider WHERE tenant_id=$1", tenant)
			return err
		}); err != nil {
			t.Error(err)
		}
	})
	// Read isolation is checked without a tenant WHERE clause, exercising RLS.
	if err := s.Transaction(ctx, other, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM ai.provider WHERE id=$1", tenant).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("foreign tenant read returned %d rows", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := s.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO ai.provider(tenant_id,id,document,revision) VALUES ($1,$2,'{}',1)", other, tenant)
		return err
	})
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("foreign tenant write must fail RLS: %v", err)
	}
	return func(ctx context.Context) error {
		return s.Transaction(ctx, tenant, func(tx pgx.Tx) error {
			var count int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM ai.provider").Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				return fmt.Errorf("fixture RLS expected one own row, got %d", count)
			}
			updated, err := tx.Exec(ctx, "UPDATE ai.provider SET revision=revision+1 WHERE tenant_id=$1 AND id=$1", tenant)
			if err == nil && updated.RowsAffected() != 1 {
				return fmt.Errorf("fixture lost its own writable row")
			}
			return err
		})
	}
}
