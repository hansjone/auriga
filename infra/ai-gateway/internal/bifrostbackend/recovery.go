package bifrostbackend

import (
	"context"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"time"
)

func (b *Backend) reconcileSubject(ctx context.Context, tenant, id string) error {
	return b.ledger.ReconcileExpiredSubject(ctx, tenant, id)
}

// Scan the installed tenant under RLS, including requests left by an earlier
// process. No caller-controlled tenant list or cross-tenant discovery is used.
func (b *Backend) reconcileTenant(ctx context.Context) error {
	if !validUUID(b.tenant) {
		return invalid
	}
	after := "00000000-0000-0000-0000-000000000000"
	for {
		var subjects []string
		err := b.db.Transaction(ctx, b.tenant, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT DISTINCT subject FROM ai.budget_request WHERE tenant_id=$1 AND subject>$2 AND state IN ('reserved','dispatching','unknown') AND created_at<clock_timestamp()-interval '10 minutes' ORDER BY subject LIMIT 200`, b.tenant, after)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var subject string
				if err = rows.Scan(&subject); err != nil {
					return err
				}
				subjects = append(subjects, subject)
			}
			return rows.Err()
		})
		if err != nil {
			return err
		}
		for _, id := range subjects {
			if err = b.reconcileSubject(ctx, b.tenant, id); err != nil {
				return err
			}
			after = id
		}
		if len(subjects) < 200 {
			return nil
		}
	}
}
func (b *Backend) recoveryLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		call, cancel := context.WithTimeout(ctx, 25*time.Second)
		if err := b.reconcileTenant(call); err != nil && ctx.Err() == nil {
			slog.Warn("relay recovery incomplete")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
