package relayhttp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	"github.com/jackc/pgx/v5"
)

func TestHTTPStreamErrorsAndAccountingRollback(t *testing.T) {
	for _, mode := range []string{"before-first-event", "missing-usage", "cut-stream", "accounting-before-json", "accounting-after-sse"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtureNew(t)
			var calls atomic.Int32
			request := strings.Replace(plainRequest, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
			body := sse(textChoice)
			if mode == "before-first-event" {
				body = `private provider body SQL secret https://upstream.example.com/key`
			}
			if mode == "missing-usage" {
				body = strings.Replace(body, `,"usage":`+usageJSON, "", 1)
			}
			if mode == "cut-stream" {
				body = strings.Replace(body, "data: [DONE]\n\n", "", 1)
			}
			if mode == "accounting-before-json" {
				body = completion(textChoice)
				request = plainRequest
			}
			pending := make(chan Pending, 1)
			f.http.Recover = func(p Pending) { pending <- p }
			s := f.server(t, func(ctx context.Context, s relay.Submission, key []byte) (relay.Exchange, error) {
				if strings.HasPrefix(mode, "accounting-") {
					f.denyUsageWrites(t)
				}
				return finite(body, &calls)(ctx, s, key)
			})
			status, headers, result := f.request(t, s, "POST", chatPath, request, "")
			if calls.Load() != 1 || strings.Contains(result, "[DONE]") || strings.Contains(result, "SQL") || strings.Contains(result, "secret") || strings.Contains(result, "upstream.example.com") {
				t.Fatal(status, result, calls.Load())
			}
			switch mode {
			case "before-first-event":
				if status != 502 || headers.Get("Content-Type") != "application/json" {
					t.Fatal(status, result)
				}
			case "accounting-before-json":
				if status != 503 || !strings.Contains(result, "ai.budget_unavailable") {
					t.Fatal(status, result)
				}
			default:
				if status != 200 || headers.Get("Content-Type") != "text/event-stream" || strings.Count(result, `"error":`) != 1 || !strings.Contains(result, "ai.stream_interrupted") {
					t.Fatal(status, result)
				}
			}
			var p Pending
			select {
			case p = <-pending:
			case <-time.After(time.Second):
				t.Fatal("no recovery ownership transfer")
			}
			if p.Ref.RequestID == "" {
				t.Fatal("lost durable request")
			}
			if strings.HasPrefix(mode, "accounting-") {
				err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
					var state string
					var active, held, usage, outbox int64
					if err := tx.QueryRow(t.Context(), `SELECT state FROM ai.budget_request WHERE tenant_id=$1`, f.control.tenant).Scan(&state); err != nil {
						return err
					}
					if err := tx.QueryRow(t.Context(), `SELECT active,held_tokens FROM ai.budget_subject WHERE tenant_id=$1`, f.control.tenant).Scan(&active, &held); err != nil {
						return err
					}
					if err := tx.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM ai.budget_usage WHERE tenant_id=$1),(SELECT count(*) FROM ai.budget_outbox WHERE tenant_id=$1)`, f.control.tenant).Scan(&usage, &outbox); err != nil {
						return err
					}
					if state != "dispatching" || active != 1 || held != 48 || usage != 0 || outbox != 0 {
						t.Errorf("rollback lost holds: %s %d %d %d %d", state, active, held, usage, outbox)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				f.assertState(t, "unknown", 0, 48)
			}
		})
	}
}
