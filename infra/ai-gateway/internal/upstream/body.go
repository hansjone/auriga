package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relay"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// Normalization is lazy and synchronous: no response pump, unbounded read or
// queue. Close directly interrupts the network Read. Raw and normalized data
// both obey deployment bounds, before Core applies its own protocol limits.
type body struct {
	ctx        context.Context
	raw        io.ReadCloser
	reader     *bufio.Reader
	s          relay.Submission
	limits     modelprotocol.Limits
	proof      chan struct{}
	stopOnce   sync.Once
	pending    []byte
	transcript []byte
	rawBytes   int64
	finished   bool
	skipLF     bool
	jsonRaw    []byte
	partial    []byte
	data       []string
	frameStart int64
	comment    bool
	emitted    int64
}

var errProgress = errors.New("upstream read progress")

func newBody(ctx context.Context, raw io.ReadCloser, s relay.Submission, l modelprotocol.Limits, proof chan struct{}) *body {
	return &body{ctx: ctx, raw: raw, reader: bufio.NewReaderSize(io.LimitReader(raw, l.MaxBytes+1), 4096), s: s, limits: l, proof: proof}
}
func (b *body) Close() error { return b.raw.Close() }

func (b *body) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(b.pending) == 0 {
		if b.finished {
			return 0, io.EOF
		}
		if b.ctx.Err() != nil {
			return 0, fault("ai.upstream_unavailable")
		}
		var err error
		if b.s.Request.GetStream() {
			err = b.frame()
		} else {
			err = b.complete()
		}
		if err == errProgress {
			// Expose real network progress while buffering a JSON value/SSE
			// frame. Legal whitespace/comments let Core's existing first-byte
			// and idle monitor observe each read, without an adapter timer.
			b.pending = []byte(" ")
			if b.s.Request.GetStream() {
				if b.comment {
					b.pending = []byte(".")
				} else {
					b.pending = []byte(":.")
					b.comment = true
				}
			}
		} else if err != nil {
			b.finished = true
			return 0, fault("ai.upstream_unavailable")
		}
	}
	if int64(len(b.pending)) > b.limits.MaxBytes-b.emitted {
		b.finished = true
		return 0, fault("ai.upstream_unavailable")
	}
	n := copy(p, b.pending)
	b.emitted += int64(n)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *body) complete() error {
	var chunk [4096]byte
	n, err := b.reader.Read(chunk[:])
	b.jsonRaw = append(b.jsonRaw, chunk[:n]...)
	if int64(len(b.jsonRaw)) > b.limits.MaxBytes || (err != nil && err != io.EOF) {
		return fault("ai.upstream_unavailable")
	}
	if n > 0 {
		return errProgress
	}
	if err != io.EOF {
		return io.ErrNoProgress
	}
	normalized, err := normalize(b.jsonRaw, b.s.PublicModel, b.s.Target.UpstreamModel)
	b.jsonRaw = nil
	if err != nil || int64(len(normalized)) > b.limits.MaxBytes {
		return fault("ai.upstream_rejected")
	}
	b.pending, b.finished = normalized, true
	b.prove(normalized, false)
	return nil
}

func (b *body) line() (string, error) {
	for {
		if b.reader.Buffered() == 0 {
			if _, err := b.reader.Peek(1); err != nil {
				return "", err
			}
			return "", errProgress
		}
		v, err := b.reader.ReadByte()
		if err != nil {
			return "", err
		}
		b.rawBytes++
		if b.skipLF {
			b.skipLF = false
			if v == '\n' {
				continue
			}
		}
		if b.rawBytes > b.limits.MaxBytes || len(b.partial) >= b.limits.MaxEventBytes {
			return "", io.ErrUnexpectedEOF
		}
		if v == '\n' || v == '\r' {
			b.skipLF = v == '\r'
			line := string(b.partial)
			b.partial = nil
			return line, nil
		}
		b.partial = append(b.partial, v)
	}
}

func (b *body) frame() error {
	for {
		line, err := b.line()
		if err == errProgress {
			return errProgress
		}
		if err != nil || b.rawBytes-b.frameStart > int64(b.limits.MaxEventBytes) {
			return io.ErrUnexpectedEOF
		}
		if line == "" {
			if len(b.data) == 0 {
				b.frameStart = b.rawBytes
				continue
			}
			payload := strings.Join(b.data, "\n")
			b.data = nil
			b.frameStart = b.rawBytes
			var normalized []byte
			if payload == "[DONE]" {
				normalized = []byte(payload)
				b.finished = true
			} else {
				normalized, err = normalize([]byte(payload), b.s.PublicModel, b.s.Target.UpstreamModel)
				if err != nil {
					return err
				}
			}
			b.pending = append(append([]byte("data: "), normalized...), '\n', '\n')
			if len(b.pending) > b.limits.MaxEventBytes || int64(len(b.transcript)+len(b.pending)) > b.limits.MaxBytes {
				return io.ErrUnexpectedEOF
			}
			b.transcript = append(b.transcript, b.pending...)
			if b.finished {
				b.prove(b.transcript, true)
				b.transcript = nil
			}
			if b.comment {
				b.pending = append([]byte("\n"), b.pending...)
				b.comment = false
			}
			return nil
		}
		if line == "data" {
			b.data = append(b.data, "")
		} else if strings.HasPrefix(line, "data:") {
			b.data = append(b.data, strings.TrimPrefix(line[5:], " "))
		} else if !strings.HasPrefix(line, ":") && !strings.HasPrefix(line, "id:") && !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "retry:") {
			return io.ErrUnexpectedEOF
		}
	}
}

// A remote terminal protocol response, validated by the same D2 state machine,
// is stop evidence. EOF alone, arbitrary DONE, Close, HTTP errors and context
// cancellation are not. This check authorizes no usage settlement: Core still
// applies the actual pinned policy, usage attestation and accounting.
func (b *body) prove(payload []byte, stream bool) {
	v := b.s.Request
	v.Model = b.s.PublicModel
	const safe = int64(9007199254740991)
	r, err := modelprotocol.ValidateRequest(b.ctx, v, modelprotocol.Policy{Model: v.Model, ContextWindow: safe, MaxOutputTokens: safe, Tools: true, Temperature: true, TopP: true, Stop: true, MeasureInput: func(context.Context, api.ChatCompletionRequest) (int64, error) { return safe - v.MaxTokens, nil }, VerifyUsage: func(context.Context, api.TokenUsage) bool { return true }})
	if err != nil {
		return
	}
	var result modelprotocol.Result
	if stream {
		result, err = modelprotocol.Stream(b.ctx, io.NopCloser(bytes.NewReader(payload)), r, b.limits, b.s.RequestID, func(_ context.Context, p []byte) (int, error) { return len(p), nil })
	} else {
		result, err = modelprotocol.Complete(b.ctx, io.NopCloser(bytes.NewReader(payload)), r, b.limits)
	}
	if err == nil && result.Completed && b.ctx.Err() == nil {
		b.stopOnce.Do(func() { close(b.proof) })
	}
}

// Keep provider content and numeric spellings intact. Duplicate keys, malformed
// JSON and excessive nesting fail closed before alias substitution. Only known
// OpenAI metadata outside D2 is discarded; Core validates the resulting schema.
func normalize(raw []byte, public, upstream string) ([]byte, error) {
	if !utf8.Valid(raw) {
		return nil, io.ErrUnexpectedEOF
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := value(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, io.ErrUnexpectedEOF
	}
	m, ok := v.(map[string]any)
	if !ok || m["model"] != upstream {
		return nil, io.ErrUnexpectedEOF
	}
	m["model"] = public
	delete(m, "system_fingerprint")
	delete(m, "service_tier")
	if m["usage"] == nil {
		delete(m, "usage")
	} else if u, ok := m["usage"].(map[string]any); ok {
		delete(u, "prompt_tokens_details")
		delete(u, "completion_tokens_details")
	}
	if choices, ok := m["choices"].([]any); ok {
		for _, c := range choices {
			if choice, ok := c.(map[string]any); ok {
				delete(choice, "logprobs")
				for _, key := range []string{"message", "delta"} {
					if message, ok := choice[key].(map[string]any); ok && message["refusal"] == nil {
						delete(message, "refusal")
					}
				}
			}
		}
	}
	return json.Marshal(m)
}

func value(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, io.ErrUnexpectedEOF
	}
	t, err := d.Token()
	if err != nil {
		return nil, io.ErrUnexpectedEOF
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			key, e := d.Token()
			k, ok := key.(string)
			if e != nil || !ok {
				return nil, io.ErrUnexpectedEOF
			}
			if _, exists := m[k]; exists {
				return nil, io.ErrUnexpectedEOF
			}
			m[k], e = value(d, depth+1)
			if e != nil {
				return nil, e
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, io.ErrUnexpectedEOF
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := value(d, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, io.ErrUnexpectedEOF
		}
		return a, nil
	default:
		if _, delim := t.(json.Delim); delim {
			return nil, io.ErrUnexpectedEOF
		}
		return t, nil
	}
}
