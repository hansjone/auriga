package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/admin"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelgate"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
)

// watchedBody owns a first-byte timer spanning dispatch and then an idle timer.
// It does not confuse closing a reader with proof that provider work stopped.
type watchedBody struct {
	body   io.ReadCloser
	mu     sync.Mutex
	timer  *time.Timer
	idle   time.Duration
	closed bool
	once   sync.Once
}

func (b *watchedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.mu.Lock()
	if n > 0 && !b.closed {
		b.timer.Reset(b.idle)
	}
	b.mu.Unlock()
	return n, err
}
func (b *watchedBody) Close() error {
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		b.timer.Stop()
		b.mu.Unlock()
		_ = b.body.Close()
	})
	return nil
}

func (c *Core) monitor(ctx context.Context, call *modelgate.Call, cancel context.CancelFunc) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(c.c.RecheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if c.check(ctx, call, true) != nil {
					cancel()
					return
				}
			}
		}
	}()
	return done
}

func (c *Core) run(ctx context.Context, call *modelgate.Call, target admin.RelayTarget, secret []byte, req *modelprotocol.Request, requestID string, sink modelprotocol.Sink) (result modelprotocol.Result, stopped bool, proof <-chan struct{}, err error) {
	ctx, cancel := context.WithCancel(ctx)
	monitorDone := c.monitor(ctx, call, cancel)
	// Timer cancellation also closes a blocking body through modelprotocol.
	var timedOut atomic.Bool
	first := time.AfterFunc(time.Duration(target.FirstByteTimeoutSeconds)*time.Second, func() { timedOut.Store(true); cancel() })
	wire := req.Value()
	publicModel := wire.Model
	wire.Model = target.UpstreamModel
	x, transportErr := c.c.Transport(ctx, Submission{Target: target, Request: wire, PublicModel: publicModel, RequestID: requestID}, secret)
	proof = x.Stopped
	if transportErr == nil && x.Body != nil && ctx.Err() == nil {
		body := &watchedBody{body: x.Body, timer: first, idle: time.Duration(target.IdleTimeoutSeconds) * time.Second}
		if streaming(req) {
			result, err = modelprotocol.Stream(ctx, body, req, c.c.Limits, requestID, sink)
		} else {
			result, err = modelprotocol.Complete(ctx, body, req, c.c.Limits)
		}
	} else {
		if x.Body != nil {
			_ = x.Body.Close()
		}
		err = &modelprotocol.Error{Code: "ai.upstream_unavailable", Message: "The model provider is unavailable"}
	}
	if ctx.Err() != nil {
		code, message := "ai.stream_interrupted", "The model response stream was interrupted"
		if timedOut.Load() || ctx.Err() == context.DeadlineExceeded {
			code, message = "ai.upstream_timeout", "The model provider timed out"
		}
		err = &modelprotocol.Error{Code: code, Message: message}
	}
	first.Stop()
	cancel() // No accounting/recovery context is ever passed to upstream work.
	<-monitorDone
	timer := time.NewTimer(c.c.StopTimeout)
	defer timer.Stop()
	select {
	case _, open := <-x.Stopped:
		stopped = !open
	case <-timer.C:
	}
	return result, stopped, proof, err
}

// Protocol terminal events are held until durable accounting has succeeded.
// This wrapper is synchronous, bounded, and forwards no unvalidated provider data.
type streamOutput struct {
	sink            modelprotocol.Sink
	limits          modelprotocol.Limits
	bytes           int64
	events          int
	started, broken bool
}

func (s *streamOutput) write(ctx context.Context, b []byte) (int, error) {
	if int64(len(b)) > s.limits.MaxBytes-s.bytes || len(b) > s.limits.MaxEventBytes || s.events >= s.limits.MaxEvents || ctx.Err() != nil {
		return 0, io.ErrShortWrite
	}
	n, err := s.sink(ctx, b)
	s.bytes += int64(n)
	s.events++
	s.started = s.started || n > 0
	s.broken = s.broken || err != nil || n != len(b)
	return n, err
}
func (s *streamOutput) consume(ctx context.Context, b []byte) (int, error) {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(bytes.TrimSuffix(bytes.TrimPrefix(b, []byte("data: ")), []byte("\n\n")), &envelope)
	if bytes.Equal(b, []byte("data: [DONE]\n\n")) || len(envelope.Error) > 0 {
		return len(b), nil
	}
	return s.write(ctx, b)
}
func (s *streamOutput) finish(ctx context.Context, requestID string, err error) error {
	if !s.started || s.broken {
		return nil
	}
	b := []byte("data: [DONE]\n\n")
	if err != nil {
		// Same generated AiError envelope as modelprotocol. Revocation, missing
		// usage and accounting failure after start cannot be reported as DONE.
		e := &modelprotocol.Error{Code: "ai.stream_interrupted", Message: "The model response stream was interrupted"}
		var protocolErr *modelprotocol.Error
		if errors.As(err, &protocolErr) {
			e = protocolErr
		}
		payload, _ := json.Marshal(e.Envelope(requestID))
		b = append(append([]byte("data: "), payload...), '\n', '\n')
	}
	// Best effort response closure only; cannot extend any upstream lifetime.
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	_, writeErr := s.write(closeCtx, b)
	return writeErr
}
