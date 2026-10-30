// Package testpg shares only the explicit PostgreSQL test connection input.
package testpg

import (
	"os"
	"testing"
)

func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AURIGA_AI_TEST_PG_DSN")
	if dsn == "" {
		if os.Getenv("CI") == "true" {
			t.Fatal("CI requires AURIGA_AI_TEST_PG_DSN")
		}
		t.Skip("AURIGA_AI_TEST_PG_DSN absent; real PostgreSQL integration not run")
	}
	return dsn
}
