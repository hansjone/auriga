package sharedmodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/jackc/pgx/v5"
)

// PostgresSharedLedger records dispatch and attribution. The existing relay
// budget ledger remains the sole owner of monetary reservations and charging.
type PostgresSharedLedger struct{ store *store.Store }

func NewPostgresSharedLedger(s *store.Store) (*PostgresSharedLedger, error) {
	if s == nil {
		return nil, sharedauth.ErrInvalid
	}
	return &PostgresSharedLedger{store: s}, nil
}

func sharedCallRecord(binding sharedauth.Binding, callID string) ([]byte, [32]byte, error) {
	if !binding.Valid() || !sharedauth.UUID(callID) {
		return nil, [32]byte{}, sharedauth.ErrInvalid
	}
	document, err := json.Marshal(binding)
	return document, sha256.Sum256(document), err
}

func (l *PostgresSharedLedger) Begin(ctx context.Context, binding sharedauth.Binding, callID string, fingerprint [32]byte) (bool, error) {
	document, hash, err := sharedCallRecord(binding, callID)
	if err != nil {
		return false, err
	}
	fresh := false
	err = l.store.Transaction(ctx, binding.TenantID, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `INSERT INTO ai.shared_model_call
 (tenant_id,member_id,binding_hash,binding,call_id,fingerprint)
 VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, binding.TenantID, binding.MemberID, hash[:], document, callID, fingerprint[:])
		if err != nil {
			return err
		}
		fresh = result.RowsAffected() == 1
		fence, err := tx.Exec(ctx, `INSERT INTO ai.shared_model_call_fence
 (tenant_id,member_id,call_id,binding_hash) VALUES ($1,$2,$3,$4)
 ON CONFLICT DO NOTHING`, binding.TenantID, binding.MemberID, callID, hash[:])
		if err != nil {
			return err
		}
		fresh = fresh && fence.RowsAffected() == 1
		// A separate statement sees the winner of a concurrent INSERT after it
		// commits. No dispatch permission is granted on an ambiguous commit.
		var savedHash, savedFingerprint []byte
		if err := tx.QueryRow(ctx, `SELECT c.binding_hash,c.fingerprint
 FROM ai.shared_model_call_fence f JOIN ai.shared_model_call c
 ON c.tenant_id=f.tenant_id AND c.binding_hash=f.binding_hash AND c.call_id=f.call_id
 WHERE f.tenant_id=$1 AND f.member_id=$2 AND f.call_id=$3`, binding.TenantID, binding.MemberID, callID).Scan(&savedHash, &savedFingerprint); err != nil {
			return err
		}
		// A changed binding must roll back its provisional row, rather than
		// dispatching again or replacing the original accounting record.
		if !bytes.Equal(savedHash, hash[:]) || !bytes.Equal(savedFingerprint, fingerprint[:]) {
			return sharedauth.ErrBinding
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sharedauth.ErrBinding) {
			return false, err
		}
		return false, sharedauth.ErrUnavailable
	}
	return fresh, nil
}

func (l *PostgresSharedLedger) Finish(ctx context.Context, binding sharedauth.Binding, callID string, usage SharedUsage, runErr error) error {
	_, hash, err := sharedCallRecord(binding, callID)
	if err != nil {
		return err
	}
	const maxTokens = 9007199254740991
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.InputTokens > maxTokens || usage.OutputTokens > maxTokens {
		return sharedauth.ErrInvalid
	}
	state := "unknown"
	var input, output *int64
	if usage.Known {
		state = "completed"
		if runErr != nil {
			state = "failed"
		}
		input, output = &usage.InputTokens, &usage.OutputTokens
	}
	err = l.store.Transaction(ctx, binding.TenantID, func(tx pgx.Tx) error {
		var savedState string
		var savedInput, savedOutput *int64
		if err := tx.QueryRow(ctx, `SELECT c.state,c.input_tokens,c.output_tokens
 FROM ai.shared_model_call c JOIN ai.shared_model_call_fence f
 ON c.tenant_id=f.tenant_id AND c.binding_hash=f.binding_hash AND c.call_id=f.call_id
 WHERE f.tenant_id=$1 AND f.member_id=$2 AND f.call_id=$3 AND c.binding_hash=$4
 FOR UPDATE OF c`, binding.TenantID, binding.MemberID, callID, hash[:]).Scan(&savedState, &savedInput, &savedOutput); err != nil {
			return err
		}
		if savedState != "dispatching" {
			if savedState == state && equalCount(savedInput, input) && equalCount(savedOutput, output) {
				return nil
			}
			return sharedauth.ErrBinding
		}
		_, err := tx.Exec(ctx, `UPDATE ai.shared_model_call
 SET state=$4,input_tokens=$5,output_tokens=$6,finished_at=now()
 WHERE tenant_id=$1 AND binding_hash=$2 AND call_id=$3`, binding.TenantID, hash[:], callID, state, input, output)
		return err
	})
	if err != nil {
		if errors.Is(err, sharedauth.ErrBinding) {
			return err
		}
		return sharedauth.ErrUnavailable
	}
	return nil
}

func equalCount(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
