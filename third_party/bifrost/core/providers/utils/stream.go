package utils

import (
	"context"
	"errors"
	"fmt"

	"sync/atomic"
	"time"

	schemas "github.com/maximhq/bifrost/core/schemas"
)

type StreamStartupState struct {
	isPreamble    func(*schemas.BifrostStreamChunk) bool
	maxBytes      int
	underDeadline bool
	count         int
	bytes         int
	committed     bool
	ended         bool
}
type AttemptAbort struct {
	timeout time.Duration
	state   atomic.Int32
	done    chan struct{}
	stopped chan struct{}
	timer   *time.Timer
}

var ErrStreamFirstTokenTimeout = errors.New(schemas.ErrStreamFirstTokenTimeout)

const (
	attemptAbortArmed int32 = iota
	attemptAbortFired
	attemptAbortDisarmed
)

func NewAttemptAbort(d time.Duration) *AttemptAbort {
	if d <= 0 {
		return nil
	}
	a := &AttemptAbort{
		timeout: d,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	a.timer = time.AfterFunc(d, a.fire)
	return a
}

func (a *AttemptAbort) fire() {
	if a.state.CompareAndSwap(attemptAbortArmed, attemptAbortFired) {
		close(a.done)
	}
}

func (a *AttemptAbort) Done() <-chan struct{} {
	if a == nil {
		return nil
	}
	return a.done
}

func (a *AttemptAbort) Stopped() <-chan struct{} {
	if a == nil {
		return nil
	}
	return a.stopped
}

func (a *AttemptAbort) Disarm() bool {
	if a == nil {
		return true
	}
	if a.state.CompareAndSwap(attemptAbortArmed, attemptAbortDisarmed) {
		a.timer.Stop()
		close(a.stopped)
		return true
	}
	return a.state.Load() == attemptAbortDisarmed
}

func (a *AttemptAbort) Fired() bool {
	return a != nil && a.state.Load() == attemptAbortFired
}

func (a *AttemptAbort) Timeout() time.Duration {
	if a == nil {
		return 0
	}
	return a.timeout
}

func AttemptAbortFromContext(ctx context.Context) *AttemptAbort {
	if ctx == nil {
		return nil
	}
	abort, _ := ctx.Value(schemas.BifrostContextKeyStreamAttemptAbort).(*AttemptAbort)
	return abort
}

func NewFirstTokenTimeoutError(d time.Duration) *schemas.BifrostError {
	err := NewBifrostTimeoutError(fmt.Sprintf("%s of %s", schemas.ErrStreamFirstTokenTimeout, d), ErrStreamFirstTokenTimeout)
	err.Error.Code = new(schemas.FirstTokenTimeoutErrorCode)
	return err
}

const (
	maxStreamPreambleChunks = 64
	maxStreamPreambleBytes  = 256 * 1024

	maxStreamPreambleBytesUnderDeadline = 4 * 1024 * 1024
)

func fitStreamPreamble(count, bytes int, chunk *schemas.BifrostStreamChunk, maxBytes int) (int, bool) {
	if count+1 >= maxStreamPreambleChunks {
		return 0, false
	}
	encoded, err := MarshalSorted(chunk)
	if err != nil || len(encoded) >= maxBytes-bytes {
		return 0, false
	}
	return len(encoded), true
}

func isStreamStartupError(err *schemas.BifrostError) bool {
	return err != nil && err.Error != nil &&
		(err.Error.Message != "" || err.Error.Code != nil || err.Error.Type != nil)
}

func NewStreamStartupState(isPreamble func(*schemas.BifrostStreamChunk) bool, underDeadline bool) *StreamStartupState {
	maxBytes := maxStreamPreambleBytes
	if underDeadline {
		maxBytes = maxStreamPreambleBytesUnderDeadline
	}
	return &StreamStartupState{isPreamble: isPreamble, maxBytes: maxBytes, underDeadline: underDeadline}
}

func (c *StreamStartupState) IsStartupError(err *schemas.BifrostError) bool {
	return !c.committed && isStreamStartupError(err)
}

func (c *StreamStartupState) ObserveResult(ctx context.Context, resp *schemas.BifrostResponse, err *schemas.BifrostError) bool {
	if c.committed || c.ended || isStreamControlSkip(err) {
		return false
	}
	chunk := BuildClientStreamChunk(ctx, resp, err)
	if isStreamStartupError(chunk.BifrostError) {
		return false
	}
	if c.isPreamble != nil && c.isPreamble(chunk) {
		if size, ok := fitStreamPreamble(c.count, c.bytes, chunk, c.maxBytes); ok {
			c.count++
			c.bytes += size
			return false
		}
		if c.underDeadline {

			c.ended = true
			return false
		}
	}
	c.committed = true
	return true
}

func newStreamContextError(ctx context.Context) *schemas.BifrostError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return NewBifrostTimeoutError(schemas.ErrProviderRequestTimedOut, ctx.Err())
	}
	err := NewBifrostOperationError(schemas.ErrRequestCancelled, ctx.Err())
	err.StatusCode = new(499)
	err.Error.Type = new(schemas.RequestCancelled)
	err.AllowFallbacks = new(false)
	return err
}

func drainInBackground(stream chan *schemas.BifrostStreamChunk) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream {
		}
	}()
	return done
}

func CheckFirstStreamChunkForError(
	ctx context.Context,
	stream chan *schemas.BifrostStreamChunk,
) (chan *schemas.BifrostStreamChunk, <-chan struct{}, *schemas.BifrostError) {
	if stream == nil {
		done := make(chan struct{})
		close(done)
		return nil, done, nil
	}
	abort := AttemptAbortFromContext(ctx)
	var firstChunk *schemas.BifrostStreamChunk
	var ok bool
	select {
	case firstChunk, ok = <-stream:

	default:
		select {
		case firstChunk, ok = <-stream:
		case <-ctx.Done():

			return nil, drainInBackground(stream), newStreamContextError(ctx)
		case <-abort.Done():
			return nil, drainInBackground(stream), NewFirstTokenTimeoutError(abort.Timeout())
		}
	}
	if !ok {
		if !abort.Disarm() {

			return nil, drainInBackground(stream), NewFirstTokenTimeoutError(abort.Timeout())
		}

		done := make(chan struct{})
		close(done)
		return nil, done, nil
	}

	if isStreamStartupError(firstChunk.BifrostError) {

		return nil, drainInBackground(stream), firstChunk.BifrostError
	}

	if !abort.Disarm() {

		return nil, drainInBackground(stream), NewFirstTokenTimeoutError(abort.Timeout())
	}

	done := make(chan struct{})
	wrapped := make(chan *schemas.BifrostStreamChunk, max(cap(stream), 1))
	if !schemas.IsStreamCommitMarker(firstChunk) {
		wrapped <- firstChunk
	}
	go func() {
		defer close(done)
		defer close(wrapped)
		for chunk := range stream {
			if schemas.IsStreamCommitMarker(chunk) {
				continue
			}
			select {
			case wrapped <- chunk:
			case <-ctx.Done():

				for range stream {
				}
				return
			}
		}
	}()
	return wrapped, done, nil
}
