package relayhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// One callback per synchronous I/O, joined before resetting its deadline. No
// detached writer goroutine or output queue can outlive the owning operation.
func interrupt(ctx context.Context, f func()) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(done); f() })
	return func() {
		if !stop() {
			<-done
		}
	}
}

type response struct {
	w                    http.ResponseWriter
	rc                   *http.ResponseController
	timeout              time.Duration
	max, sent            int64
	committed, errorSent bool
	problem              bool
	language             string
}

func (s *response) write(ctx context.Context, status int, typ string, b []byte, flush bool) (int, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if int64(len(b)) > s.max-s.sent {
		return 0, io.ErrShortWrite
	}
	end := time.Now().Add(s.timeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(end) {
		end = deadline
	}
	if err := s.rc.SetWriteDeadline(end); err != nil {
		return 0, err
	}
	stop := interrupt(ctx, func() { _ = s.rc.SetWriteDeadline(time.Now()) })
	defer stop()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if !s.committed {
		s.w.Header().Set("Content-Type", typ)
		s.committed = true // WriteHeader commits even when the following Write returns 0.
		s.w.WriteHeader(status)
	}
	n, err := s.w.Write(b)
	s.sent += int64(n)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil && flush {
		err = s.rc.Flush()
	}
	return n, err
}

func (s *response) sink(ctx context.Context, b []byte) (int, error) {
	if len(b) > 256<<10 {
		return 0, io.ErrShortWrite
	}
	n, err := s.write(ctx, 200, "text/event-stream", b, true)
	if n > 0 {
		// Core emits generated AiError events only, never provider error bodies.
		var event struct {
			Error json.RawMessage `json:"error"`
		}
		if len(b) > 8 && json.Unmarshal(b[6:len(b)-2], &event) == nil && len(event.Error) > 0 {
			s.errorSent = true
		}
	}
	return n, err
}

func (s *response) json(ctx context.Context, status int, typ string, v any) {
	b, err := json.Marshal(v)
	if err != nil || int64(len(b)) > s.max-s.sent {
		if !s.committed {
			s.failure(ctx, s.w.Header().Get("X-Request-Id"), s.problem, fault("common.internal"))
		}
		return
	}
	if _, err = s.write(ctx, status, typ, b, false); err != nil && !s.committed {
		s.failure(ctx, s.w.Header().Get("X-Request-Id"), s.problem, err)
	}
}
