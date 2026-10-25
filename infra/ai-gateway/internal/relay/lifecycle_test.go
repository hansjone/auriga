package relay

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
)

type trackedBody struct {
	io.Reader
	once     sync.Once
	closed   chan struct{}
	borrowed []byte
}

type observedPipe struct {
	*io.PipeReader
	entered, closed     chan struct{}
	readOnce, closeOnce sync.Once
}

func (b *observedPipe) Read(p []byte) (int, error) {
	b.readOnce.Do(func() { close(b.entered) })
	return b.PipeReader.Read(p)
}
func (b *observedPipe) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return b.PipeReader.Close()
}

func TestTransportCancellationAndStopProof(t *testing.T) {
	for _, proof := range []string{"closed", "absent", "value"} {
		t.Run(proof, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			workerStopped := make(chan struct{})
			readClosed := make(chan struct{})
			c := Core{c: Config{RecheckInterval: time.Hour, StopTimeout: 20 * time.Millisecond,
				Limits: modelprotocol.Limits{MaxBytes: 1024, MaxEvents: 10, MaxEventBytes: 512},
				Transport: func(ctx context.Context, submission Submission, _ []byte) (Exchange, error) {
					if submission.Request.Model != "fixed-upstream" || submission.PublicModel != "synthetic" {
						t.Error("unfixed upstream model")
					}
					pr, pw := io.Pipe()
					b := &observedPipe{PipeReader: pr, entered: make(chan struct{}), closed: readClosed}
					go func() { <-ctx.Done(); _ = pw.Close(); close(workerStopped) }()
					go func() { <-b.entered; cancel() }()
					var stop <-chan struct{}
					switch proof {
					case "closed":
						stop = workerStopped
					case "value":
						ch := make(chan struct{}, 1)
						ch <- struct{}{}
						stop = ch
					}
					return Exchange{Body: b, Stopped: stop}, nil
				}}}
			req, err := modelprotocol.DecodeRequest(t.Context(), strings.NewReader(plainRequest), syntheticPolicy())
			if err != nil {
				t.Fatal(err)
			}
			_, stopped, _, err := c.run(ctx, nil, admin.RelayTarget{UpstreamModel: "fixed-upstream", FirstByteTimeoutSeconds: 1, IdleTimeoutSeconds: 1}, nil, req, "unit", nil)
			if err == nil || stopped != (proof == "closed") {
				t.Fatal("cancellation inferred stop proof", err, stopped)
			}
			select {
			case <-workerStopped:
			case <-time.After(time.Second):
				t.Fatal("producer still alive")
			}
			select {
			case <-readClosed:
			default:
				t.Fatal("cancelled body alive")
			}
		})
	}
}

func (b *trackedBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

func TestBodyDeadlines(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	b := &watchedBody{body: r, idle: 20 * time.Millisecond}
	b.timer = time.AfterFunc(time.Second, cancel)
	// A received first byte replaces the first-byte bound with the idle bound.
	go func() { _, _ = w.Write([]byte("x")) }()
	buf := make([]byte, 1)
	if n, err := b.Read(buf); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("idle bound was ignored")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(buf); err == nil {
		t.Fatal("reader survived close")
	}
}

func TestProviderSlotNeedsActualStop(t *testing.T) {
	c := Core{active: map[string]int64{}, pending: map[string][]<-chan struct{}{}}
	if !c.acquire("provider", 1) {
		t.Fatal("first slot denied")
	}
	proof := make(chan struct{}, 1)
	c.awaitStop("provider", proof)
	proof <- struct{}{}
	if c.acquire("provider", 1) {
		t.Fatal("sent value fabricated stop proof")
	}
	close(proof)
	if !c.acquire("provider", 1) {
		t.Fatal("late actual stop leaked provider slot")
	}
	c.awaitStop("provider", nil)
	if c.acquire("provider", 1) {
		t.Fatal("missing proof released slot")
	}
}

func TestStreamTerminalRequiresAccounting(t *testing.T) {
	for _, failed := range []bool{false, true} {
		var dst bytes.Buffer
		s := streamOutput{limits: modelprotocol.Limits{MaxBytes: 4096, MaxEvents: 10, MaxEventBytes: 1024}, sink: func(_ context.Context, b []byte) (int, error) { return dst.Write(b) }}
		_, err := s.consume(t.Context(), []byte("data: {}\n\n"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.consume(t.Context(), []byte("data: [DONE]\n\n"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(dst.Bytes(), []byte("[DONE]")) {
			t.Fatal("DONE escaped before accounting")
		}
		var accountErr error
		if failed {
			accountErr = fault("ai.budget_unavailable")
		}
		if err = s.finish(t.Context(), "request", accountErr); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(dst.Bytes(), []byte("[DONE]")) == failed {
			t.Fatal("terminal event disagrees with accounting")
		}
	}
}
