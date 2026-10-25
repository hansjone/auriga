package modelprotocol

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"unicode/utf8"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// Limits are mandatory positive deployment bounds, applied separately to input
// and output. Stream has no queue: one event is synchronously consumed/written.
type Limits struct {
	MaxBytes                 int64
	MaxEvents, MaxEventBytes int
}

func (l Limits) valid() bool {
	return l.MaxBytes > 0 && l.MaxBytes < safeInteger && l.MaxEvents > 0 && l.MaxEventBytes > 0 && l.MaxEventBytes <= EventLimit
}

type Result struct {
	Completion                     *api.ChatCompletion
	Usage                          *api.TokenUsage
	UsageKnown, Started, Completed bool
}

func (r *Request) streaming() bool { v := r.Value(); return v.GetStream() }

// Sink must return promptly on ctx cancellation. n counts bytes actually sent,
// including partial writes. A blocking sink must implement its own write deadline.
type Sink func(context.Context, []byte) (int, error)

// Own body until exit. Close must interrupt concurrent Read and return promptly,
// as with an HTTP response body. Wait for the cancellation callback before exit.
func ownBody(ctx context.Context, body io.ReadCloser) func() {
	var once sync.Once
	closeBody := func() { once.Do(func() { _ = body.Close() }) }
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { closeBody(); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		closeBody()
	}
}
func ioFailure(ctx context.Context, stream bool) *Error {
	if ctx.Err() == context.DeadlineExceeded {
		return failure("ai.upstream_timeout")
	}
	if stream {
		return failure("ai.stream_interrupted")
	}
	return failure("ai.upstream_unavailable")
}

// Complete returns generated D2 data for the handler to encode. Started remains
// false: this function never writes the public response. No usage => no JSON success.
func Complete(ctx context.Context, body io.ReadCloser, r *Request, l Limits) (Result, error) {
	defer ownBody(ctx, body)()
	result := Result{}
	if r == nil || !l.valid() || r.streaming() {
		return result, failure("common.validation_failed")
	}
	if ctx.Err() != nil {
		return result, ioFailure(ctx, false)
	}
	b, err := boundedRead(body, l.MaxBytes)
	if err != nil || ctx.Err() != nil {
		return result, ioFailure(ctx, false)
	}
	var c api.ChatCompletion
	if !parse(b, completionRule, &c) {
		return result, upstreamError(b, "ai.upstream_rejected")
	}
	if int64(len(encode(&c))) > l.MaxBytes {
		return result, failure("ai.upstream_rejected")
	}
	m := c.Choices[0].Message
	if m.Content.Get() == nil && len(m.ToolCalls) == 0 {
		return result, failure("ai.upstream_rejected")
	}
	d := api.ChatCompletionChunkChoicesInnerDelta{Role: &m.Role, Content: m.Content}
	for i, t := range m.ToolCalls {
		name, args := t.Function.Name, t.Function.Arguments
		d.ToolCalls = append(d.ToolCalls, api.ToolCallDelta{Index: int64(i), Id: &t.Id, Type: &t.Type, Function: &api.ToolCallDeltaFunction{Name: &name, Arguments: &args}})
	}
	f := api.NullableString{}
	f.Set(&c.Choices[0].FinishReason)
	s := newState(r)
	chunk := api.ChatCompletionChunk{Id: c.Id, Object: "chat.completion.chunk", Created: c.Created, Model: c.Model, Usage: &c.Usage, Choices: []api.ChatCompletionChunkChoicesInner{{Index: 0, Delta: d, FinishReason: f}}}
	accepted := s.accept(ctx, &chunk)
	if ctx.Err() != nil {
		return result, ioFailure(ctx, false)
	}
	if !accepted || !s.complete() || s.usage == nil {
		return result, failure("ai.upstream_rejected")
	}
	result.Completion = &c
	result.Usage = s.usage
	result.UsageKnown = true
	result.Completed = true
	return result, nil
}

type toolState struct{ id, name, args string }
type state struct {
	request    *Request
	value      api.ChatCompletionRequest
	id, finish string
	created    int64
	tools      map[int64]*toolState
	textRunes  int
	sawUsage   bool
	usage      *api.TokenUsage
}

func newState(r *Request) *state {
	return &state{request: r, value: r.Value(), tools: map[int64]*toolState{}}
}
func (s *state) accept(ctx context.Context, c *api.ChatCompletionChunk) bool {
	if c.Model != s.value.Model || (s.id != "" && (s.id != c.Id || s.created != c.Created)) {
		return false
	}
	s.id = c.Id
	s.created = c.Created
	if len(c.Choices) > 0 {
		if s.finish != "" {
			return false
		}
		a := c.Choices[0]
		d := a.Delta
		if text := d.Content.Get(); text != nil {
			s.textRunes += utf8.RuneCountInString(*text)
			if s.textRunes > RequestLimit {
				return false
			}
		}
		for _, part := range d.ToolCalls {
			if !s.request.policy.Tools || len(s.value.Tools) == 0 || (s.value.ToolChoice != nil && *s.value.ToolChoice == "none") {
				return false
			}
			t := s.tools[part.Index]
			if t == nil {
				if part.Id == nil {
					return false
				}
				for _, other := range s.tools {
					if other.id == *part.Id {
						return false
					}
				}
				t = &toolState{id: *part.Id}
				s.tools[part.Index] = t
			} else if part.Id != nil && *part.Id != t.id {
				return false
			}
			if part.Function != nil {
				if part.Function.Name != nil {
					t.name += *part.Function.Name
				}
				if part.Function.Arguments != nil {
					t.args += *part.Function.Arguments
				}
			}
			if len(t.name) > 64 || utf8.RuneCountInString(t.args) > 262144 {
				return false
			}
		}
		if f := a.FinishReason.Get(); f != nil {
			s.finish = *f
			if !s.complete() {
				return false
			}
		}
	}
	if c.Usage != nil {
		if s.finish == "" || s.sawUsage {
			return false
		}
		s.sawUsage = true
		u := c.Usage
		if u.PromptTokens > s.request.inputBound || u.CompletionTokens > s.value.MaxTokens || u.PromptTokens > safeInteger-u.CompletionTokens || u.TotalTokens != u.PromptTokens+u.CompletionTokens {
			return false
		}
		if verify := s.request.policy.VerifyUsage; verify != nil && verify(ctx, *u) {
			copy := *u
			s.usage = &copy
		} else {
			c.Usage = nil
		}
	}
	return true
}
func (s *state) complete() bool {
	switch s.finish {
	case "content_filter":
		// Filtering terminates generation, with or without complete tool data.
		// It does not authorize execution or require the model to produce a tool.
	case "tool_calls":
		if len(s.tools) == 0 {
			return false
		}
	case "stop", "length":
		if len(s.tools) > 0 || (s.value.ToolChoice != nil && *s.value.ToolChoice == "required") {
			return false
		}
	default:
		return false
	}
	for _, t := range s.tools {
		if !namePattern.MatchString(t.name) || !parse([]byte(t.args), func(v any) bool { _, ok := v.(map[string]any); return ok }, nil) {
			return false
		}
		found := false
		for _, declared := range s.value.Tools {
			if declared.Function.Name == t.name {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

var errorRule = object("error requestId", map[string]rule{
	"requestId": stringRule, "originalRequestId": stringRule, "outcome": enum("completed", "failed", "cancelled", "unknown"),
	"error": object("code message type", map[string]rule{"code": stringRule, "message": stringRule, "type": enum("authentication_error", "permission_error", "invalid_request_error", "rate_limit_error", "upstream_error", "conflict_error", "internal_error")}),
})

func upstreamError(b []byte, fallback string) *Error {
	var e api.AiError
	if parse(b, errorRule, &e) {
		switch e.Error.Code {
		case "ai.upstream_timeout", "ai.upstream_rejected", "ai.upstream_unavailable", "ai.stream_interrupted":
			return failure(e.Error.Code)
		}
		return failure("ai.upstream_rejected")
	}
	return failure(fallback)
}

func encode(v any) []byte { b, _ := json.Marshal(v); return b } // Only validated generated values.
