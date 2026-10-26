package relayhttp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
)

type watched struct {
	io.Reader
	once   sync.Once
	closed chan struct{}
}

func (b *watched) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

const firstChunk = "data: {\"id\":\"completion\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"synthetic\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"

func TestHTTPPostgresCancellation(t *testing.T) {
	for _, mode := range []string{"disconnect", "expiry", "revocation", "first-byte", "idle", "total", "slow-writer", "missing-usage", "no-stop-proof"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtureNew(t)
			pending := make(chan Pending, 1)
			f.http.Recover = func(p Pending) { pending <- p }
			upstreamCanceled := make(chan struct{})
			stopped := make(chan struct{})
			closed := make(chan struct{})
			request := plainRequest
			if mode != "first-byte" && mode != "total" && mode != "no-stop-proof" && mode != "missing-usage" {
				request = strings.Replace(request, `"max_tokens":`, `"stream":true,"max_tokens":`, 1)
			}
			if mode == "expiry" {
				f.control.mu.Lock()
				f.control.expires = time.Now().Add(time.Second)
				f.control.mu.Unlock()
				f.exec(t, `UPDATE ai.provider SET document=jsonb_set(document,'{firstByteTimeoutSeconds}','2')`)
			}
			if mode == "total" {
				f.exec(t, `UPDATE ai.provider SET document=jsonb_set(jsonb_set(jsonb_set(document,'{totalTimeoutSeconds}','1'),'{firstByteTimeoutSeconds}','2'),'{idleTimeoutSeconds}','2')`)
			}
			if mode == "slow-writer" {
				f.cfg.Limits.MaxBytes = 32 << 20
				f.cfg.Limits.MaxEvents = 256
				f.cfg.Limits.MaxEventBytes = 256 << 10
				f.http.MaxResponseBytes = 32 << 20
			}
			transport := func(ctx context.Context, _ relay.Submission, _ []byte) (relay.Exchange, error) {
				context.AfterFunc(ctx, func() { close(upstreamCanceled) })
				if mode == "no-stop-proof" || mode == "missing-usage" || mode == "slow-writer" {
					body := completion(textChoice)
					if mode == "missing-usage" {
						body = strings.Replace(body, `,"usage":`+usageJSON, "", 1)
					}
					if mode == "slow-writer" {
						chunk := strings.Replace(firstChunk, `"content":"ok"`, `"content":"`+strings.Repeat("x", 128<<10)+`"`, 1)
						body = strings.Repeat(chunk, 100) // Exceeds kernel buffers; client never reads.
					}
					if mode != "no-stop-proof" {
						close(stopped)
					}
					return relay.Exchange{Body: &watched{Reader: strings.NewReader(body), closed: closed}, Stopped: stopped}, nil
				}
				pr, pw := io.Pipe()
				go func() {
					if mode != "first-byte" && mode != "total" {
						_, _ = io.WriteString(pw, firstChunk)
					}
					<-ctx.Done()
					_ = pw.Close()
					close(stopped)
				}()
				if mode == "revocation" {
					f.control.mu.Lock()
					f.control.active = false
					f.control.mu.Unlock()
				}
				return relay.Exchange{Body: &watched{Reader: pr, closed: closed}, Stopped: stopped}, nil
			}
			// Pipe Close must interrupt a read; watched alone cannot implement that.
			// wrappedPipe below closes both the real reader and the observation signal.
			wrapped := func(ctx context.Context, s relay.Submission, key []byte) (relay.Exchange, error) {
				x, e := transport(ctx, s, key)
				if b, ok := x.Body.(*watched); ok {
					if pr, ok := b.Reader.(*io.PipeReader); ok {
						x.Body = &wrappedPipe{watched: b, pipe: pr}
					}
				}
				return x, e
			}
			s := f.server(t, wrapped)
			if mode == "disconnect" || mode == "slow-writer" {
				c := socket(t, s)
				if _, err := fmt.Fprintf(c, "POST %s HTTP/1.1\r\nHost: example.com\r\nAuthorization: Bearer %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", chatPath, f.raw, len(request), request); err != nil {
					t.Fatal(err)
				}
				if mode == "disconnect" {
					r, err := http.ReadResponse(bufio.NewReader(c), nil)
					if err != nil {
						t.Fatal(err)
					}
					if r.StatusCode != 200 {
						t.Fatal(r.StatusCode)
					}
					_ = c.Close()
					_ = r.Body.Close()
				}
			} else {
				status, _, result := f.request(t, s, "POST", chatPath, request, "")
				if strings.Contains(result, "[DONE]") || strings.Contains(result, `"object":"chat.completion"`) {
					t.Fatalf("unknown succeeded %d %s", status, result)
				}
				if mode == "first-byte" || mode == "total" {
					if status != 504 {
						t.Fatalf("%d %s", status, result)
					}
				}
			}
			var p Pending
			select {
			case p = <-pending:
			case <-time.After(5 * time.Second):
				t.Fatal("lost durable recovery handoff")
			}
			if p.Ref.TenantID != f.control.tenant || p.Ref.AccountID != account || p.Ref.RequestID == "" || p.StopProof != stopped {
				t.Fatal("handoff changed identity or stop proof")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("upstream body not closed")
			}
			select {
			case <-upstreamCanceled:
			case <-time.After(time.Second):
				t.Fatal("upstream context alive")
			}
			active := int64(0)
			if mode == "no-stop-proof" {
				active = 1
			}
			f.assertState(t, "unknown", active, 48)
			f.control.mu.Lock()
			f.control.active = true
			f.control.expires = time.Now().Add(time.Minute)
			f.control.mu.Unlock()
			status, _, body := f.request(t, s, "GET", usagePath, "", "")
			if status != 200 {
				t.Fatal(status, body)
			}
			for _, field := range []string{`"inputTokens":null`, `"outputTokens":null`, `"costMicros":null`, `"usageQuality":"unknown"`, `"frozenTokens":48`} {
				if !strings.Contains(body, field) {
					t.Fatalf("missing %s: %s", field, body)
				}
			}
		})
	}
}

type wrappedPipe struct {
	*watched
	pipe *io.PipeReader
}

func (b *wrappedPipe) Close() error { _ = b.pipe.Close(); return b.watched.Close() }

func TestHTTPIdempotency(t *testing.T) {
	f := fixtureNew(t)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s := f.server(t, func(ctx context.Context, s relay.Submission, key []byte) (relay.Exchange, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return relay.Exchange{}, ctx.Err()
		}
		return finite(completion(textChoice), &calls)(ctx, s, key)
	})
	first := make(chan string, 1)
	go func() { _, _, b := f.request(t, s, "POST", chatPath, plainRequest, "once"); first <- b }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("submission not reached")
	}
	status, _, body := f.request(t, s, "POST", chatPath, plainRequest, "once")
	if status != 409 || !strings.Contains(body, "ai.request_in_progress") {
		t.Fatal(status, body)
	}
	status, _, body = f.request(t, s, "POST", chatPath, strings.Replace(plainRequest, "private prompt", "different prompt", 1), "once")
	if status != 409 || !strings.Contains(body, "common.conflict") {
		t.Fatal(status, body)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case body = <-first:
		if !strings.Contains(body, "chat.completion") {
			t.Fatal(body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial response blocked")
	}
	status, _, body = f.request(t, s, "POST", chatPath, plainRequest, "once")
	if status != 409 || !strings.Contains(body, "ai.replay_unavailable") || !strings.Contains(body, `"outcome":"completed"`) || !strings.Contains(body, `"originalRequestId":`) {
		t.Fatal(status, body)
	}
	if calls.Load() != 1 {
		t.Fatal("automatic re-execution", calls.Load())
	}
	f.assertState(t, "completed", 0, 0)
}
