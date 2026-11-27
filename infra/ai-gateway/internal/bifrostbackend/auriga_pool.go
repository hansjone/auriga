package bifrostbackend

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/audit"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

const defaultPoolID = "auriga"
const legacyDefaultPoolID = "auto"

func memberCatalogVisible(p api.RelayPool) bool {
	switch p.GetAudience() {
	case "external":
		return false
	default:
		return true
	}
}

func normalizePoolAudience(p *api.RelayPool) {
	switch p.GetAudience() {
	case "member", "external", "both":
		return
	default:
		p.SetAudience("both")
	}
}

func modelGranted(key api.RelayKey, poolID string) bool {
	if slices.Contains(key.Models, poolID) {
		return true
	}
	return poolID == defaultPoolID && slices.Contains(key.Models, legacyDefaultPoolID)
}

func catalogAllows(key api.RelayKey, p api.RelayPool) bool {
	if !p.Enabled {
		return false
	}
	if modelGranted(key, p.Id) {
		return true
	}
	if !slices.Contains(key.Models, "*") {
		return false
	}
	if key.Source == "member" {
		return memberCatalogVisible(p)
	}
	return true
}

func chatAllows(key api.RelayKey, p api.RelayPool) bool {
	if modelGranted(key, p.Id) {
		return true
	}
	if !slices.Contains(key.Models, "*") {
		return false
	}
	if key.Source == "member" {
		return memberCatalogVisible(p)
	}
	return true
}

func placeholderAurigaPool() api.RelayPool {
	audience := "member"
	return api.RelayPool{
		Id:              defaultPoolID,
		Enabled:         true,
		Audience:        &audience,
		Routes:          []api.RelayRoute{},
		ContextWindow:   0,
		MaxOutputTokens: 0,
		Price: api.Price{
			Currency:                     "USD",
			Version:                      "auriga-placeholder",
			EffectiveAt:                  time.Now().Add(-time.Minute),
			InputMicrosPerMillionTokens:  1,
			OutputMicrosPerMillionTokens: 1,
		},
	}
}

// EnsureAurigaPool creates the fixed default public pool when missing.
// A legacy auto pool is copied into auriga once; existing auriga rows are left alone
// except a missing audience is filled with member.
func (b *Backend) EnsureAurigaPool(ctx context.Context, tenant string) error {
	return b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var aurigaRaw []byte
		err := tx.QueryRow(ctx, `SELECT document FROM ai.relay_pool WHERE tenant_id=$1 AND id=$2`, tenant, defaultPoolID).Scan(&aurigaRaw)
		if err == nil {
			var pool api.RelayPool
			if json.Unmarshal(aurigaRaw, &pool) != nil {
				return invalid
			}
			if pool.HasAudience() {
				switch pool.GetAudience() {
				case "member", "external", "both":
					return nil
				}
			}
			pool.Id = defaultPoolID
			pool.SetAudience("member")
			raw, e := json.Marshal(pool)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE ai.relay_pool SET document=$3 WHERE tenant_id=$1 AND id=$2`, tenant, defaultPoolID, raw); e != nil {
				return e
			}
			return audit.Record(ctx, tx, tenant, "ai.pool.update", "relay_pool", defaultPoolID)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		pool := placeholderAurigaPool()
		var autoRaw []byte
		autoErr := tx.QueryRow(ctx, `SELECT document FROM ai.relay_pool WHERE tenant_id=$1 AND id=$2`, tenant, legacyDefaultPoolID).Scan(&autoRaw)
		if autoErr == nil {
			if json.Unmarshal(autoRaw, &pool) != nil {
				return invalid
			}
			pool.Id = defaultPoolID
			if !pool.HasAudience() {
				pool.SetAudience("member")
			}
			normalizePoolAudience(&pool)
		} else if !errors.Is(autoErr, pgx.ErrNoRows) {
			return autoErr
		}
		if err := normalizePoolSave(&pool); err != nil {
			return err
		}
		raw, e := json.Marshal(pool)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO ai.relay_pool(tenant_id,id,document) VALUES($1,$2,$3)`, tenant, defaultPoolID, raw); e != nil {
			return e
		}
		return audit.Record(ctx, tx, tenant, "ai.pool.create", "relay_pool", defaultPoolID)
	})
}
