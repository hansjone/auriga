package relayhttp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Observe the actual backend wait, not elapsed time. The holder is an independent
// connection in the same database, as in separate CI package test binaries.
func TestPostgresUsageACLFence(t *testing.T) {
	db := testpg.Open(t)
	var role string
	if err := db.Transaction(t.Context(), account, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), "SELECT current_user").Scan(&role)
	}); err != nil {
		t.Fatal(err)
	}
	connect := func(t *testing.T) *pgx.Conn {
		c, err := pgx.Connect(t.Context(), testpg.DSN(t))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = c.Close(ctx)
		})
		return c
	}
	observer, worker := connect(t), connect(t)
	quoted := pgx.Identifier{role}.Sanitize()
	for _, op := range []string{"revoke", "grant", "error", "cancel"} {
		t.Run(op, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			// Closing this connection on failure also releases the session lock.
			holder := connect(t)
			if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock(718245010)"); err != nil {
				t.Fatal(err)
			}
			statement := "REVOKE INSERT ON ai.budget_usage FROM " + quoted
			if op == "grant" {
				statement = "GRANT INSERT ON ai.budget_usage TO " + quoted
			}
			if op == "error" {
				statement = "REVOKE INSERT ON ai.usage_acl_fence_missing FROM " + quoted
			}
			done := make(chan error, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				done <- usageCatalogACL(ctx, worker, statement)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(15 * time.Second):
					t.Error("ACL worker did not stop")
				}
			})
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case err := <-done:
					t.Fatalf("ACL bypassed held fence: %v", err)
				default:
				}
				var waiting bool
				err := observer.QueryRow(ctx, `SELECT EXISTS (
                    SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory'
                    AND classid=0 AND objid=718245010 AND objsubid=1 AND NOT granted
                    AND $2::int=ANY(pg_blocking_pids(pid)))`, worker.PgConn().PID(), holder.PgConn().PID()).Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("ACL bypassed held fence: %v", err)
				case <-ctx.Done():
					t.Fatal("backend never waited on ACL fence")
				case <-ticker.C:
				}
			}
			if op == "cancel" {
				cancel()
			}
			if op != "cancel" {
				if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock(718245010)"); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("ACL helper did not finish")
			}
			var pg *pgconn.PgError
			switch op {
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel: %v", err)
				}
			case "error":
				if !errors.As(err, &pg) || pg.Code != "42P01" {
					t.Fatalf("DDL error: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				// Exercise INSERT with the same runtime role without creating
				// budget rows; existing settlement tests check real row rollback.
				err = db.Transaction(t.Context(), account, func(tx pgx.Tx) error {
					_, e := tx.Exec(t.Context(), "INSERT INTO ai.budget_usage SELECT * FROM ai.budget_usage WHERE false")
					return e
				})
				if op == "revoke" {
					if !errors.As(err, &pg) || pg.Code != "42501" {
						t.Fatalf("INSERT was not denied: %v", err)
					}
				} else if err != nil {
					t.Fatalf("same role INSERT not restored: %v", err)
				}
			}
			var locks int
			if err := observer.QueryRow(t.Context(), "SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND granted", worker.PgConn().PID()).Scan(&locks); err != nil {
				t.Fatal(err)
			}
			if locks != 0 {
				t.Fatalf("helper leaked %d advisory locks", locks)
			}
		})
	}
}
