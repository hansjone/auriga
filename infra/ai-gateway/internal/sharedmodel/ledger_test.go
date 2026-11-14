package sharedmodel

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/testpg"
	"github.com/jackc/pgx/v5"
)

func TestSharedLedgerPostgresConcurrentRestartAndIsolation(t *testing.T) {
	database := testpg.Open(t)
	ledger, err := NewPostgresSharedLedger(database)
	if err != nil {
		t.Fatal(err)
	}
	binding := sharedauth.Binding{TenantID: uuid.NewString(), MemberID: uuid.NewString(), PlacementID: uuid.NewString(), PhysicalInstanceID: uuid.NewString(), Mode: "shared", Epoch: 1, Revision: 1, SessionID: uuid.NewString(), PlatformSessionID: uuid.NewString(), AuthGeneration: 1}
	id := uuid.NewString()
	fingerprint := sha256.Sum256([]byte("request"))
	var fresh atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := ledger.Begin(context.Background(), binding, id, fingerprint)
			if err != nil {
				t.Error(err)
			}
			if ok {
				fresh.Add(1)
			}
		}()
	}
	wg.Wait()
	if fresh.Load() != 1 {
		t.Fatalf("dispatch count %d", fresh.Load())
	}
	restarted, _ := NewPostgresSharedLedger(database)
	if ok, err := restarted.Begin(context.Background(), binding, id, fingerprint); ok || err != nil {
		t.Fatalf("restart replay %v %v", ok, err)
	}
	if _, err := ledger.Begin(context.Background(), binding, id, sha256.Sum256([]byte("changed"))); !errors.Is(err, sharedauth.ErrBinding) {
		t.Fatal("fingerprint changed", err)
	}
	usage := SharedUsage{InputTokens: 2, OutputTokens: 3, Known: true}
	if err := ledger.Finish(context.Background(), binding, id, usage, nil); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Finish(context.Background(), binding, id, usage, nil); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Finish(context.Background(), binding, id, SharedUsage{}, nil); err == nil {
		t.Fatal("late unknown overwrote settlement")
	}
	if err := database.Transaction(context.Background(), uuid.NewString(), func(tx pgx.Tx) error {
		var count int
		err := tx.QueryRow(context.Background(), "SELECT count(*) FROM ai.shared_model_call WHERE tenant_id=$1", binding.TenantID).Scan(&count)
		if count != 0 {
			t.Fatal("RLS leaked usage")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	unknownID := uuid.NewString()
	if _, err := ledger.Begin(context.Background(), binding, unknownID, fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Finish(context.Background(), binding, unknownID, SharedUsage{}, errors.New("cancelled")); err != nil {
		t.Fatal(err)
	}
	if ok, err := restarted.Begin(context.Background(), binding, unknownID, fingerprint); ok || err != nil {
		t.Fatal("unknown outcome replay")
	}
}
