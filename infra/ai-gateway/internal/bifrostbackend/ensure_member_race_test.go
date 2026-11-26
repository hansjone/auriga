package bifrostbackend

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestUniqueConstraintDetectsNameRace(t *testing.T) {
	if uniqueConstraint(errors.New("other")) || uniqueConstraint(nil) {
		t.Fatal("non-unique errors")
	}
	if !uniqueConstraint(&pgconn.PgError{Code: "23505", ConstraintName: "relay_key_tenant_id_name_key"}) {
		t.Fatal("unique_violation")
	}
	if uniqueConstraint(&pgconn.PgError{Code: "23503"}) {
		t.Fatal("foreign_key")
	}
}
