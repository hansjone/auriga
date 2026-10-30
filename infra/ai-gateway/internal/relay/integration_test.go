package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/jackc/pgx/v5"
)

const usageJSON = `{"prompt_tokens":32,"completion_tokens":2,"total_tokens":34}`
const textChoice = `{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}`
const toolChoice = `{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}`
const filterChoice = `{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}`
const toolsRequest = `"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":"required",`

func completion(choice string) string {
	return `{"id":"completion","object":"chat.completion","created":1,"model":"synthetic","choices":[` + choice + `],"usage":` + usageJSON + `}`
}
func sse(choice string) string {
	choice = strings.Replace(choice, `"message":`, `"delta":`, 1)
	choice = strings.Replace(choice, `"tool_calls":[{`, `"tool_calls":[{"index":0,`, 1)
	return `data: {"id":"completion","object":"chat.completion.chunk","created":1,"model":"synthetic","choices":[` + choice + `],"usage":` + usageJSON + "}\n\ndata: [DONE]\n\n"
}

func (f *fixture) reply(t *testing.T, body string, calls *atomic.Int32) *trackedBody {
	t.Helper()
	b := &trackedBody{Reader: strings.NewReader(body), closed: make(chan struct{})}
	f.core.c.Transport = func(ctx context.Context, submission Submission, secret []byte) (Exchange, error) {
		calls.Add(1)
		b.borrowed = secret // Test-only reference proves WithCredential clears its loan.
		if submission.Target.BaseURL != "https://upstream.example.com/v1" || submission.Target.UpstreamModel != "fixed-synthetic-v1" || submission.Target.ConnectTimeoutSeconds != 1 || submission.Target.FirstByteTimeoutSeconds != 1 || submission.Target.IdleTimeoutSeconds != 1 || submission.Target.TotalTimeoutSeconds != 3 || submission.Target.MaxConcurrency != 2 || !bytes.Equal(secret, f.secret) || submission.Request.Model != "fixed-synthetic-v1" || submission.PublicModel != "synthetic" || ctx.Err() != nil {
			t.Error("dispatch changed trusted target, credential, request or limits")
		}
		stopped := make(chan struct{})
		close(stopped) // Finite fixture is fully produced; no upstream worker exists.
		return Exchange{Body: b, Stopped: stopped}, nil
	}
	return b
}
func codeOf(err error) string {
	var b *budget.Error
	var p *modelprotocol.Error
	if errors.As(err, &b) {
		return b.Code
	}
	if errors.As(err, &p) {
		return p.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
func expectCode(t *testing.T, err error, want string) {
	t.Helper()
	if codeOf(err) != want {
		t.Fatalf("error=%v want=%s", err, want)
	}
}

// All assertions inspect the actual tenant-scoped database, never mirrored
// accounting logic. The fixture prices make 32 input + 2 output cost 36 micros.
func (f *fixture) facts(t *testing.T, out Output, state string, slot bool, unknown bool) {
	t.Helper()
	err := f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		var got string
		var held bool
		var active, tokens, micros int64
		var body []byte
		if err := tx.QueryRow(t.Context(), `SELECT state,slot_held FROM ai.budget_request WHERE request_id=$1`, out.Ref.RequestID).Scan(&got, &held); err != nil {
			return err
		}
		if got != state || held != slot {
			t.Errorf("durable state=%s slot=%v", got, held)
		}
		if err := tx.QueryRow(t.Context(), `SELECT active,held_tokens,held_micros FROM ai.budget_subject WHERE subject=$1`, account).Scan(&active, &tokens, &micros); err != nil {
			return err
		}
		wantActive := int64(0)
		if slot {
			wantActive = 1
		}
		if active != wantActive {
			t.Errorf("active=%d", active)
		}
		wantTokens, wantMicros := int64(0), int64(0)
		if unknown || state == "dispatching" {
			wantTokens, wantMicros = 48, 64
		}
		if tokens != wantTokens || micros != wantMicros {
			t.Errorf("held tokens/micros=%d/%d want=%d/%d", tokens, micros, wantTokens, wantMicros)
		}
		if state == "dispatching" {
			return nil
		}
		if err := tx.QueryRow(t.Context(), `SELECT document FROM ai.budget_usage WHERE request_id=$1`, out.Ref.RequestID).Scan(&body); err != nil {
			return err
		}
		for _, field := range []string{"inputTokens", "outputTokens", "costMicros"} {
			if unknown && !bytes.Contains(body, []byte(`"`+field+`": null`)) {
				t.Errorf("unknown %s not null: %s", field, body)
			}
		}
		var count int
		if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM ai.budget_outbox WHERE request_id=$1`, out.Ref.RequestID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("outbox count=%d", count)
		}
		if state == "completed" {
			var input, output, cost int64
			if err := tx.QueryRow(t.Context(), `SELECT (document->>'inputTokens')::bigint,(document->>'outputTokens')::bigint,(document->>'costMicros')::bigint FROM ai.budget_usage WHERE request_id=$1`, out.Ref.RequestID).Scan(&input, &output, &cost); err != nil {
				return err
			}
			if input != 32 || output != 2 || cost != 36 {
				t.Errorf("settled usage=%d/%d/%d", input, output, cost)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeviceExecutionIntegration(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for name, choice := range map[string]string{"text": textChoice, "tool": toolChoice, "filter": filterChoice} {
			t.Run(name+map[bool]string{false: "-json", true: "-sse"}[stream], func(t *testing.T) {
				f := fixtureNew(t)
				var calls atomic.Int32
				body := completion(choice)
				request := plainRequest
				if name != "text" {
					request = strings.Replace(request, `"max_tokens":`, toolsRequest+`"max_tokens":`, 1)
				}
				if stream {
					body = sse(choice)
					request = strings.Replace(request, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
				}
				b := f.reply(t, body, &calls)
				var sink bytes.Buffer
				out, err := f.core.Execute(t.Context(), f.control.raw, strings.NewReader(request), "single", func(_ context.Context, b []byte) (int, error) { return sink.Write(b) })
				if err != nil || out.Pending || !out.Result.Completed || calls.Load() != 1 {
					t.Fatalf("execute: %v pending=%v calls=%d", err, out.Pending, calls.Load())
				}
				if !bytes.Equal(b.borrowed, make([]byte, len(f.secret))) {
					t.Fatal("borrowed secret survived callback")
				}
				a := out.Reservation.Admission
				if a.GetDeviceId() != device || a.GetInstanceId() != "" || a.AccountId != account || a.TenantId != f.control.tenant || a.ProviderId != provider || a.CredentialId != credential || a.PriceVersion != "fixed-price" || a.InputTokenUpperBound != 32 || a.MaxOutputTokens != 16 {
					t.Fatal("budget admission lost pinned identity/model/price/origin")
				}
				select {
				case <-b.closed:
				default:
					t.Fatal("body not closed")
				}
				if stream && strings.Count(sink.String(), "[DONE]") != 1 {
					t.Fatal("missing unique committed DONE")
				}
				f.facts(t, out, "completed", false, false)
				for _, scope := range [][2]string{{f.control.tenant, device}, {device, account}} {
					page, e := f.core.c.Ledger.ListUsage(t.Context(), scope[0], scope[1], 10, "")
					if e != nil || len(page.Items) != 0 {
						t.Fatal("owner/tenant usage isolation failed", e)
					}
				}
			})
		}
	}
}

func TestConcurrentIdempotencyIntegration(t *testing.T) {
	f := fixtureNew(t)
	var calls atomic.Int32
	b := f.reply(t, completion(textChoice), &calls)
	original := f.core.c.Transport
	entered, release := make(chan struct{}), make(chan struct{})
	f.core.c.Transport = func(ctx context.Context, submission Submission, secret []byte) (Exchange, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return original(ctx, submission, secret)
	}
	type result struct {
		out Output
		err error
	}
	first := make(chan result, 1)
	go func() {
		out, err := f.core.Execute(t.Context(), f.control.raw, strings.NewReader(plainRequest), "same-key", nil)
		first <- result{out, err}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("no dispatcher")
	}
	ref := budget.Ref{TenantID: f.control.tenant, AccountID: account}
	if err := f.db.Transaction(t.Context(), ref.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), `SELECT request_id::text FROM ai.budget_request`).Scan(&ref.RequestID)
	}); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []int64{1, 2} {
		_, err := f.core.c.Ledger.MarkDispatching(t.Context(), ref, revision)
		expectCode(t, err, "common.precondition_failed")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, err := f.core.Execute(t.Context(), f.control.raw, strings.NewReader(" \n"+plainRequest), "same-key", nil)
			expectCode(t, err, "ai.request_in_progress")
		})
	}
	wg.Wait()
	_, err := f.core.Execute(t.Context(), f.control.raw, strings.NewReader(strings.Replace(plainRequest, "private prompt", "different", 1)), "same-key", nil)
	expectCode(t, err, "common.conflict")
	close(release)
	got := <-first
	if got.err != nil {
		t.Fatal(got.err)
	}
	_, err = f.core.Execute(t.Context(), f.control.raw, strings.NewReader(plainRequest), "same-key", nil)
	expectCode(t, err, "ai.replay_unavailable")
	var replay *budget.Error
	if !errors.As(err, &replay) || replay.OriginalRequestID != got.out.Ref.RequestID || replay.Outcome != "completed" {
		t.Fatal("terminal idempotency lost original outcome")
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate transport")
	}
	select {
	case <-b.closed:
	default:
		t.Fatal("body alive")
	}
	f.facts(t, got.out, "completed", false, false)
	err = f.db.Transaction(t.Context(), f.control.tenant, func(tx pgx.Tx) error {
		var count int
		var raw string
		if err := tx.QueryRow(t.Context(), `SELECT count(*),string_agg(to_jsonb(r)::text,'') FROM ai.budget_request r`).Scan(&count, &raw); err != nil {
			return err
		}
		if count != 1 || strings.Contains(raw, "private prompt") || strings.Contains(raw, "same-key") || strings.Contains(raw, f.control.raw) {
			t.Error("duplicate admission or persisted plaintext")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionDenialsIntegration(t *testing.T) {
	for _, name := range []string{"missing-policy", "capacity", "zero-quota", "acl", "before-cas", "after-cas"} {
		t.Run(name, func(t *testing.T) {
			f := fixtureNew(t)
			var calls atomic.Int32
			f.reply(t, completion(textChoice), &calls)
			want := "ai.budget_unavailable"
			switch name {
			case "missing-policy":
				f.core.c.Policy = func(context.Context, string, string, admin.RelayVersions) (modelprotocol.Policy, error) {
					return modelprotocol.Policy{}, nil
				}
			case "capacity":
				f.core.c.Policy = func(context.Context, string, string, admin.RelayVersions) (modelprotocol.Policy, error) {
					p := syntheticPolicy()
					p.ContextWindow++
					return p, nil
				}
			case "zero-quota":
				f.control.quota = 0
				want = "ai.budget_exceeded"
			case "acl":
				f.exec(t, `UPDATE ai.model SET grants='{"items":[]}',revision=revision+1`)
				want = "ai.model_not_allowed"
			case "before-cas", "after-cas":
				n := 3
				if name == "after-cas" {
					n = 4
				}
				f.control.hook = func(check int) {
					if check == n {
						f.control.active = false
					}
				}
				want = "common.unauthenticated"
			}
			out, err := f.core.Execute(t.Context(), f.control.raw, strings.NewReader(plainRequest), "denied", nil)
			expectCode(t, err, want)
			if calls.Load() != 0 {
				t.Fatal("denial submitted upstream")
			}
			if name == "before-cas" {
				f.facts(t, out, "cancelled", false, false)
			}
			if name == "after-cas" {
				f.facts(t, out, "unknown", false, true)
			}
		})
	}
}

func TestUnknownAndAccountingFailureIntegration(t *testing.T) {
	for _, name := range []string{"missing-usage", "bad-usage", "stream-missing", "stream-cut", "cancel", "request-deadline", "provider-total", "first-byte", "idle", "revoked", "acl-revoked", "key-revoked", "dependency", "no-stop-proof", "accounting-failure"} {
		t.Run(name, func(t *testing.T) {
			f := fixtureNew(t)
			var calls atomic.Int32
			request := plainRequest
			body := completion(textChoice)
			switch name {
			case "missing-usage":
				body = strings.Replace(body, `,"usage":`+usageJSON, "", 1)
			case "bad-usage":
				body = strings.Replace(body, `"prompt_tokens":32`, `"prompt_tokens":33`, 1)
			case "stream-missing":
				body = strings.Replace(sse(textChoice), `,"usage":`+usageJSON, "", 1)
				request = strings.Replace(request, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
			case "stream-cut":
				body = strings.Replace(sse(textChoice), "data: [DONE]\n\n", "", 1)
				request = strings.Replace(request, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
			}
			tracked := f.reply(t, body, &calls)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "no-stop-proof" {
				old := f.core.c.Transport
				f.core.c.Transport = func(ctx context.Context, submission Submission, secret []byte) (Exchange, error) {
					x, e := old(ctx, submission, secret)
					x.Stopped = make(chan struct{})
					return x, e
				}
			}
			if name == "accounting-failure" {
				old := f.core.c.Transport
				f.core.c.Transport = func(ctx context.Context, submission Submission, secret []byte) (Exchange, error) {
					f.denyUsageWrites(t)
					return old(ctx, submission, secret)
				}
			}
			if name == "cancel" || name == "request-deadline" || name == "provider-total" || name == "first-byte" || name == "idle" || name == "revoked" || name == "acl-revoked" || name == "key-revoked" || name == "dependency" {
				if name == "provider-total" {
					f.exec(t, `UPDATE ai.provider SET document=jsonb_set(jsonb_set(jsonb_set(document,'{totalTimeoutSeconds}','1'),'{firstByteTimeoutSeconds}','2'),'{idleTimeoutSeconds}','2')`)
				}
				f.core.c.Transport = func(ctx context.Context, submission Submission, secret []byte) (Exchange, error) {
					calls.Add(1)
					pr, pw := io.Pipe()
					stopped := make(chan struct{})
					// An actual fake producer lives until cancellation; only its exit
					// closes Stopped. Closing the read body cannot manufacture proof.
					go func() {
						if name == "idle" {
							_, _ = pw.Write([]byte("{"))
						}
						<-ctx.Done()
						_ = pw.Close()
						close(stopped)
					}()
					if name == "acl-revoked" {
						f.exec(t, `UPDATE ai.model SET grants='{"items":[]}',revision=revision+1`)
					}
					if name == "key-revoked" {
						f.exec(t, `UPDATE ai.credential SET state='revoked',revision=revision+1`)
					}
					f.control.mu.Lock()
					if name == "revoked" {
						f.control.active = false
					}
					if name == "dependency" {
						f.control.fail = true
					}
					f.control.mu.Unlock()
					if name == "cancel" {
						cancel()
					}
					return Exchange{Body: pr, Stopped: stopped}, nil
				}
				if name == "request-deadline" {
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithTimeout(ctx, 2*time.Second)
					defer deadlineCancel()
				}
			}
			var dst bytes.Buffer
			out, err := f.core.Execute(ctx, f.control.raw, strings.NewReader(request), "unknown", func(_ context.Context, b []byte) (int, error) { return dst.Write(b) })
			if err == nil || calls.Load() != 1 {
				t.Fatalf("unsafe success: err=%v calls=%d", err, calls.Load())
			}
			state, slot := "unknown", name == "no-stop-proof"
			if name == "accounting-failure" {
				state, slot = "dispatching", true
				if !out.Pending {
					t.Fatal("lost accounting recovery handoff")
				}
			}
			f.facts(t, out, state, slot, true)
			if strings.Contains(dst.String(), "[DONE]") {
				t.Fatal("unknown emitted successful DONE")
			}
			if name == "stream-cut" || name == "stream-missing" {
				if !strings.Contains(dst.String(), `"code":"ai.stream_interrupted"`) {
					t.Fatal("missing stream error")
				}
			}
			if name == "missing-usage" || name == "bad-usage" || name == "stream-missing" || name == "stream-cut" || name == "no-stop-proof" || name == "accounting-failure" {
				select {
				case <-tracked.closed:
				default:
					t.Fatal("body left alive")
				}
			}
		})
	}
}
