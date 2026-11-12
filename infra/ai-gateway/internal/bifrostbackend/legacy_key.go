package bifrostbackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/audit"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
	"github.com/jackc/pgx/v5"
)

type legacyKeyBinding struct {
	KeyID     string     `json:"keyId"`
	SHA256    string     `json:"sha256"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

func (b *Backend) bindLegacyKey(ctx context.Context, tenant string, input legacyKeyBinding) error {
	digest, err := hex.DecodeString(input.SHA256)
	if err != nil || len(digest) != sha256.Size || input.SHA256 != strings.ToLower(input.SHA256) || !validUUID(input.KeyID) || b.tenant != tenant {
		return invalid
	}
	return b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		var document []byte
		if err := tx.QueryRow(ctx, `SELECT document FROM ai.relay_key WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, input.KeyID).Scan(&document); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return missing
			}
			return err
		}
		var key api.RelayKey
		if json.Unmarshal(document, &key) != nil || key.Source != "external" {
			return denied
		}
		result, err := tx.Exec(ctx, `INSERT INTO ai.relay_legacy_key(tenant_id,digest,key_id,expires_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, input.SHA256, input.KeyID, input.ExpiresAt)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			var previous string
			var expires *time.Time
			if err := tx.QueryRow(ctx, `SELECT key_id::text,expires_at FROM ai.relay_legacy_key WHERE tenant_id=$1 AND digest=$2`, tenant, input.SHA256).Scan(&previous, &expires); err != nil {
				return err
			}
			if previous != input.KeyID || (expires == nil) != (input.ExpiresAt == nil) || expires != nil && !expires.Equal(*input.ExpiresAt) {
				return fault("common.conflict")
			}
			return nil
		}
		return audit.Record(ctx, tx, tenant, "ai.relay_key.update", "relay_key", input.KeyID)
	})
}

func (b *Backend) legacyToken(raw string) (string, string, error) {
	if b.tenant == "" || len(raw) < 16 || len(raw) > 512 || strings.ContainsAny(raw, "\r\n\t ") {
		return "", "", fault("common.unauthenticated")
	}
	digest := sha256.Sum256([]byte(raw))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var id string
	err := b.db.Transaction(ctx, b.tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT key_id::text FROM ai.relay_legacy_key WHERE tenant_id=$1 AND digest=$2 AND (expires_at IS NULL OR expires_at>now())`, b.tenant, hex.EncodeToString(digest[:])).Scan(&id)
	})
	if err != nil {
		return "", "", fault("common.unauthenticated")
	}
	return b.tenant, id, nil
}
