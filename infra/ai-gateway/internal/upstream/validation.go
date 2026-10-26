package upstream

import (
	"context"
	"io"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func validationRequest(ctx context.Context, s relay.Submission) (*modelprotocol.Request, error) {
	v := s.Request
	v.Model = s.PublicModel
	const safe = int64(9007199254740991)
	return modelprotocol.ValidateRequest(ctx, v, modelprotocol.Policy{Model: v.Model, ContextWindow: safe, MaxOutputTokens: safe, Tools: true, Temperature: true, TopP: true, Stop: true, MeasureInput: func(context.Context, api.ChatCompletionRequest) (int64, error) { return safe - v.MaxTokens, nil }, VerifyUsage: func(context.Context, api.TokenUsage) bool { return true }})
}

// Validate normalized frames incrementally with backpressure. The pipe has no queue:
// validation keeps pace with consumption, and terminal work never replays the
// history. Core still owns the actual usage policy, deadlines and settlement.
type streamValidation struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	done   chan struct{}
	valid  bool // Published by closing done.
}

func newStreamValidation(ctx context.Context, s relay.Submission, l modelprotocol.Limits) *streamValidation {
	r, err := validationRequest(ctx, s)
	if err != nil {
		return nil
	}
	reader, writer := io.Pipe()
	v := &streamValidation{reader: reader, writer: writer, done: make(chan struct{})}
	go func() {
		defer close(v.done)
		result, err := modelprotocol.Stream(ctx, reader, r, l, s.RequestID, func(_ context.Context, p []byte) (int, error) { return len(p), nil })
		v.valid = err == nil && result.Completed && ctx.Err() == nil
	}()
	return v
}

func (v *streamValidation) feed(p []byte, terminal bool) bool {
	if v == nil {
		return false
	}
	if _, err := v.writer.Write(p); err != nil {
		return false
	}
	if !terminal {
		return true
	}
	_ = v.writer.Close()
	<-v.done // Only the final event remains; no history or network read is pending.
	return v.valid
}

func (v *streamValidation) close() {
	if v == nil {
		return
	}
	_ = v.writer.CloseWithError(io.ErrClosedPipe)
	_ = v.reader.CloseWithError(io.ErrClosedPipe)
}
