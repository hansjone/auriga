package relayhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/store"
	"github.com/jackc/pgx/v5"
)

type pendingHTTPStore struct {
	*store.Store
	mode      string
	ref       budget.Ref
	committed chan struct{}
}

func (s *pendingHTTPStore) Transaction(ctx context.Context, tenant string, fn func(pgx.Tx) error) error {
	cause := &budget.Error{Code: "common.conflict", OriginalRequestID: device, Outcome: "private SQL cause"}
	if s.mode == "before" {
		return errors.New("private SQL before INSERT")
	}
	err := s.Store.Transaction(ctx, tenant, func(tx pgx.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT tenant_id::text,subject::text,request_id::text FROM ai.budget_request WHERE tenant_id=$1`, tenant).Scan(&s.ref.TenantID, &s.ref.AccountID, &s.ref.RequestID); err != nil {
			return err
		}
		if s.mode == "rollback" {
			return cause
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.mode == "cancel" {
		close(s.committed)
		<-ctx.Done()
	}
	return fmt.Errorf("lost reply: %w", cause)
}

func TestHTTPAdmissionPendingRef(t *testing.T) {
	for _, key := range []string{"", "client-key"} {
		for _, mode := range []string{"commit", "rollback", "before", "cancel"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/key=%s/stream=%v", mode, key, stream), func(t *testing.T) {
					f := fixtureNew(t)
					db := &pendingHTTPStore{Store: f.db, mode: mode, committed: make(chan struct{})}
					l, err := budget.New(db, "USD", nil)
					if err != nil {
						t.Fatal(err)
					}
					f.cfg.Ledger = l
					pending := make(chan Pending, 1)
					released := make(chan struct{}, 1)
					f.http.ReserveRecovery = func() (func(*Pending), bool) {
						return func(p *Pending) {
							if p != nil {
								pending <- *p
							} else {
								released <- struct{}{}
							}
						}, true
					}
					var calls atomic.Int32
					s := f.server(t, finite(completion(textChoice), &calls))
					var logs bytes.Buffer
					s.Config.ErrorLog = log.New(&logs, "", 0)
					body := plainRequest
					if stream {
						body = strings.Replace(body, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
					}
					var public string
					if mode == "cancel" {
						c := socket(t, s)
						if _, err := fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nIdempotency-Key: %s\r\nX-Request-ID: forged-client-request\r\nContent-Length: %d\r\n\r\n%s", chatPath, f.raw, key, len(body), body); err != nil {
							t.Fatal(err)
						}
						select {
						case <-db.committed:
						case <-time.After(5 * time.Second):
							t.Fatal("admission did not commit before disconnect")
						}
						_ = c.Close()
					} else {
						r, err := http.NewRequestWithContext(t.Context(), "POST", s.URL+chatPath, strings.NewReader(body))
						if err != nil {
							t.Fatal(err)
						}
						r.Header.Set("Authorization", "Bearer "+f.raw)
						r.Header.Set("Content-Type", "application/json")
						r.Header.Set("X-Request-ID", "forged-client-request")
						r.Header.Set("X-Tenant-ID", "forged-tenant")
						r.Header.Set("X-Account-ID", "forged-account")
						if key != "" {
							r.Header.Set("Idempotency-Key", key)
						}
						res, err := s.Client().Do(r)
						if err != nil {
							t.Fatal(err)
						}
						b := readPendingBody(t, res)
						public = fmt.Sprint(res.Header) + b
						if res.StatusCode != 503 || res.Header.Get("Content-Type") != "application/json" || !strings.Contains(b, "ai.budget_unavailable") || strings.Contains(b, "data:") || strings.Contains(b, "[DONE]") {
							t.Fatal("ambiguous admission escaped as success/stream", res.StatusCode, b)
						}
					}
					var p Pending
					if mode == "before" {
						select {
						case <-released:
						case <-time.After(5 * time.Second):
							t.Fatal("uncommitted empty Ref did not release capacity")
						}
					} else {
						select {
						case p = <-pending:
						case <-time.After(5 * time.Second):
							t.Fatal("lost recovery ownership after admission error/cancellation")
						}
					}
					if calls.Load() != 0 || p.StopProof != nil {
						t.Fatal("ambiguous admission executed or fabricated stop proof")
					}
					if mode == "before" {
						if p.Ref != (budget.Ref{}) {
							t.Fatal("client metadata fabricated pending hint")
						}
					} else if p.Ref != db.ref || p.Ref.TenantID != f.control.tenant || p.Ref.AccountID != account || p.Ref.RequestID == "" {
						t.Fatal("Recover lost exact internal hint")
					}
					b, err := json.Marshal(p)
					if err != nil {
						t.Fatal(err)
					}
					public += fmt.Sprintf("%v %+v %#v", p, p, p) + string(b)
					// Wait for the real handler to finish before inspecting its log.
					s.Close()
					public += logs.String()
					for _, secret := range []string{db.ref.RequestID, f.control.tenant, account, device, "private", "SQL", "forged-client-request", "forged-tenant", "forged-account", "common.conflict"} {
						if secret != "" && strings.Contains(public, secret) {
							t.Fatal("internal hint/cause or client identity leaked to public output/log/formatting")
						}
					}
					select {
					case <-pending:
						t.Fatal("duplicate recovery handoff")
					default:
					}
				})
			}
		}
	}
}

func readPendingBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPendingHTTPFormatting(t *testing.T) {
	p := Pending{Ref: budget.Ref{TenantID: "private-tenant", AccountID: "private-account", RequestID: "private-request"}, StopProof: make(chan struct{})}
	b, err := json.Marshal(p)
	if err != nil || string(b) != "{}" {
		t.Fatal("internal handoff entered JSON", err)
	}
	for _, v := range []string{fmt.Sprint(p), fmt.Sprintf("%+v %#v", p, p), fmt.Sprintf("%v %+v %#v", &p, &p, &p)} {
		if strings.Contains(v, "private") {
			t.Fatal("internal handoff formatting leaked")
		}
	}
}
