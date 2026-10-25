package modelprotocol

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

type eventReader struct {
	reader       *bufio.Reader
	limits       Limits
	total        int64
	events, size int
}

func (e *eventReader) byte() (byte, error) {
	b, err := e.reader.ReadByte()
	if err != nil {
		return 0, err
	}
	e.total++
	e.size++
	if e.total > e.limits.MaxBytes || e.size > e.limits.MaxEventBytes {
		return 0, io.ErrUnexpectedEOF
	}
	return b, nil
}
func (e *eventReader) line() (string, error) {
	var b []byte
	for {
		c, err := e.byte()
		if err != nil {
			return "", err
		}
		if c == '\r' || c == '\n' {
			if c == '\r' {
				if next, _ := e.reader.Peek(1); len(next) > 0 && next[0] == '\n' {
					if _, err := e.byte(); err != nil {
						return "", err
					}
				}
			}
			if !utf8.Valid(b) {
				return "", io.ErrUnexpectedEOF
			}
			return string(b), nil
		}
		b = append(b, c)
	}
}
func (e *eventReader) next() (string, error) {
	var data []string
	for {
		line, err := e.line()
		if err != nil {
			return "", err
		}
		if line == "" {
			e.events++
			e.size = 0
			if e.events > e.limits.MaxEvents {
				return "", io.ErrUnexpectedEOF
			}
			if len(data) > 0 {
				return strings.Join(data, "\n"), nil
			}
			continue
		}
		if line == "data" {
			data = append(data, "")
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(line[5:], " "))
		} else if !strings.HasPrefix(line, ":") && !strings.HasPrefix(line, "id:") && !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "retry:") {
			return "", io.ErrUnexpectedEOF
		}
	}
}

// Read errors returned alongside bytes must not be hidden by buffered [DONE].
type observedReader struct {
	io.Reader
	err error
}

func (r *observedReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

type eventWriter struct {
	sink            Sink
	limits          Limits
	total           int64
	events          int
	started, broken bool
}

func (w *eventWriter) write(ctx context.Context, payload []byte) bool {
	b := append([]byte("data: "), payload...)
	b = append(b, '\n', '\n')
	if len(b) > w.limits.MaxEventBytes || int64(len(b)) > w.limits.MaxBytes-w.total || w.events >= w.limits.MaxEvents || ctx.Err() != nil {
		return false
	}
	w.total += int64(len(b))
	w.events++
	n, err := w.sink(ctx, b)
	w.started = w.started || n > 0
	if err != nil || n != len(b) || ctx.Err() != nil {
		w.broken = true
		return false
	}
	return true
}

// Stream consumes an owned D2 body once. It never dispatches/retries. The caller
// supplies a deadline context, cancellation for expiry/revocation, and a bounded
// context-aware sink. The only internal concurrency closes body on cancellation.
func Stream(ctx context.Context, body io.ReadCloser, r *Request, l Limits, requestID string, sink Sink) (result Result, err error) {
	defer ownBody(ctx, body)()
	if r == nil || !l.valid() || !r.streaming() || sink == nil {
		return result, failure("common.validation_failed")
	}
	w := &eventWriter{sink: sink, limits: l}
	s := newState(r)
	defer func() {
		if err != nil && w.started && !w.broken && ctx.Err() == nil {
			if e, ok := err.(*Error); ok {
				w.write(ctx, encode(e.Envelope(requestID)))
			}
		}
		result.Started = w.started
		result.Usage = s.usage
		result.UsageKnown = s.usage != nil
	}()
	observed := &observedReader{Reader: io.LimitReader(body, l.MaxBytes+1)}
	e := &eventReader{reader: bufio.NewReaderSize(observed, 4096), limits: l}
	for {
		if ctx.Err() != nil {
			return result, ioFailure(ctx, true)
		}
		payload, readErr := e.next()
		if readErr != nil || observed.err != nil || ctx.Err() != nil {
			return result, ioFailure(ctx, true)
		}
		if payload == "[DONE]" {
			if !s.complete() {
				return result, failure("ai.stream_interrupted")
			}
			if !w.write(ctx, []byte("[DONE]")) {
				return result, ioFailure(ctx, true)
			}
			result.Completed = true
			return result, nil
		}
		var c api.ChatCompletionChunk
		if !parse([]byte(payload), chunkRule, &c) {
			return result, upstreamError([]byte(payload), "ai.stream_interrupted")
		}
		if !s.accept(ctx, &c) {
			return result, failure("ai.stream_interrupted")
		}
		// Generated types contain only D2 fields after strict validation. Re-encode
		// instead of forwarding raw provider data; unverified usage has been removed.
		b, marshalErr := json.Marshal(c)
		if marshalErr != nil || !w.write(ctx, b) {
			return result, ioFailure(ctx, true)
		}
	}
}
