package bifrostbackend

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Keep real SQL and COMMIT semantics while controlling the exact cancellation
// boundary. Other transactions and final accounting retain their real contexts.
type cancelledDispatchStore struct {
	*store.Store
	cancel    context.CancelFunc
	committed bool
	triggered atomic.Bool
}

func (s *cancelledDispatchStore) Transaction(ctx context.Context, tenant string, fn func(pgx.Tx) error) error {
	changed := false
	err := s.Store.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		return fn(&cancelledDispatchTx{Tx: tx, owner: s, changed: &changed})
	})
	if changed && s.committed && err == nil {
		s.cancel()
		return context.DeadlineExceeded // COMMIT succeeded; its response was lost.
	}
	return err
}

type cancelledDispatchTx struct {
	pgx.Tx
	owner   *cancelledDispatchStore
	changed *bool
}

func (tx *cancelledDispatchTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(sql, "UPDATE ai.budget_request SET state=") && args[3] == "dispatching" && tx.owner.triggered.CompareAndSwap(false, true) {
		*tx.changed = true
		if !tx.owner.committed {
			tx.owner.cancel()
			return pgconn.CommandTag{}, ctx.Err()
		}
	}
	return tx.Tx.Exec(ctx, sql, args...)
}

func TestChatCancellationBeforeDispatchReleasesReservation(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "dispatch-rolled-back", true: "dispatch-commit-response-lost"}[committed], func(t *testing.T) {
			b, _, _, tenant := reviewBackend(t)
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(reviewReply))
			}))
			t.Cleanup(upstream.Close)
			_, _, key := reviewPool(t, b, tenant, upstream.URL)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			controlled := &cancelledDispatchStore{Store: b.db, cancel: cancel, committed: committed}
			ledger, err := budget.New(controlled, "USD", nil)
			if err != nil {
				t.Fatal(err)
			}
			b.ledger = ledger
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reviewChat)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+key.Secret)
			response := httptest.NewRecorder()
			b.ServeHTTP(response, r)
			if response.Code != http.StatusServiceUnavailable || !controlled.triggered.Load() || calls.Load() != 0 || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("status=%d boundary=%v calls=%d cancelled=%v", response.Code, controlled.triggered.Load(), calls.Load(), ctx.Err())
			}
			assertNoHolds(t, b, tenant, key.Key.Id)
			usage, err := b.ledger.ListUsage(t.Context(), tenant, key.Key.Id, 50, "")
			if err != nil || len(usage.Items) != 1 {
				t.Fatalf("missing cancelled usage: %v", err)
			}
			row := usage.Items[0]
			if row.Outcome != "cancelled" || row.UsageQuality != "confirmed" || row.GetCostMicros() != 0 || row.GetInputTokens() != 0 || row.GetOutputTokens() != 0 || row.FrozenTokens != 0 || row.FrozenMicros != 0 {
				t.Fatal("unsubmitted request was charged or kept frozen")
			}
		})
	}
}
