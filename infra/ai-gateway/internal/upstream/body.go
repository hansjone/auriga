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
)

// Normalization is lazy and synchronous: no response pump, unbounded read or
// queue. Close directly interrupts the network Read. Raw and normalized data
// both obey deployment bounds, before Core applies its own protocol limits.
type body struct {
	ctx          context.Context
	raw          io.ReadCloser
	reader       *bufio.Reader
	s            relay.Submission
	limits       modelprotocol.Limits
	proof        chan struct{}
	stopOnce     sync.Once
	closeOnce    sync.Once
	closeErr     error
	pending      []byte
	validation   *streamValidation
	rawBytes     int64
	finished     bool
	skipLF       bool
	jsonRaw      []byte
	partial      []byte
	data         []string
	emitted      int64
	frameSize    int
	progressSent bool
}

var errProgress = errors.New("upstream read progress")

func newBody(ctx context.Context, raw io.ReadCloser, s relay.Submission, l modelprotocol.Limits, proof chan struct{}) *body {
	b := &body{ctx: ctx, raw: raw, reader: bufio.NewReaderSize(io.LimitReader(raw, l.MaxBytes+1), 4096), s: s, limits: l, proof: proof}
	if s.Request.GetStream() {
		b.validation = newStreamValidation(ctx, s, l)
	}
	return b
}
func (b *body) Close() error {
	b.closeOnce.Do(func() { b.validation.close(); b.closeErr = b.raw.Close() })
	return b.closeErr
}

func (b *body) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(b.pending) == 0 {
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
			// frame. Legal whitespace and at most one separate comment per frame
			// expose progress to Core without an adapter timer or event growth.
			if b.s.Request.GetStream() {
				if b.progressSent {
					continue
				}
				b.progressSent = true
				b.pending = []byte(":\n\n")
			} else {
				b.pending = []byte(" ")
			}
		} else if err != nil {
			b.finished = true
			b.pending = nil
			return 0, fault("ai.upstream_unavailable")
		}
	}
	if int64(len(b.pending)) > b.limits.MaxBytes-b.emitted {
		b.finished = true
		b.pending = nil
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
	normalized, err := normalize(b.jsonRaw, b.s.PublicModel)
	b.jsonRaw = nil
	if err != nil || int64(len(normalized)) > b.limits.MaxBytes {
		return fault("ai.upstream_rejected")
	}
	b.pending, b.finished = normalized, true
	b.proveJSON(normalized)
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
		if b.rawBytes > b.limits.MaxBytes || (len(b.partial) >= b.limits.MaxEventBytes && b.partial[0] != ':') {
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
		if err != nil {
			return io.ErrUnexpectedEOF
		}
		if line == "" {
			b.frameSize = 0
			b.progressSent = false
			if len(b.data) == 0 {
				continue
			}
			payload := strings.Join(b.data, "\n")
			b.data = nil
			var normalized []byte
			if payload == "[DONE]" {
				normalized = []byte(payload)
				b.finished = true
			} else {
				normalized, err = normalize([]byte(payload), b.s.PublicModel)
				if err != nil {
					return err
				}
			}
			b.pending = append(append([]byte("data: "), normalized...), '\n', '\n')
			if len(b.pending) > b.limits.MaxEventBytes {
				return io.ErrUnexpectedEOF
			}
			if !b.validation.feed(b.pending, b.finished) {
				return io.ErrUnexpectedEOF
			}
			if b.finished && b.ctx.Err() == nil {
				b.stopOnce.Do(func() { close(b.proof) })
			}
			return nil
		}
		// Comments are not data and never consume the data-event size budget.
		// They are still bounded by the raw total-byte and line limits.
		if strings.HasPrefix(line, ":") {
			continue
		}
		b.frameSize += len(line) + 1
		if b.frameSize > b.limits.MaxEventBytes {
			return io.ErrUnexpectedEOF
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
// is stop evidence. EOF alone, arbitrary DONE, Close and context
// cancellation are not. This check authorizes no usage settlement: Core still
// applies the actual pinned policy, usage attestation and accounting.
func (b *body) proveJSON(payload []byte) {
	r, err := validationRequest(b.ctx, b.s)
	if err != nil {
		return
	}
	result, err := modelprotocol.Complete(b.ctx, io.NopCloser(bytes.NewReader(payload)), r, b.limits)
	if err == nil && result.Completed && b.ctx.Err() == nil {
		b.stopOnce.Do(func() { close(b.proof) })
	}
}

// Keep provider content and numeric spellings intact. Duplicate keys, malformed
// JSON and excessive nesting fail closed before alias substitution. Project only
// the fields in D2; provider extensions are not public protocol fields.
func normalize(raw []byte, public string) ([]byte, error) {
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
	model, validModel := m["model"].(string)
	if !ok || !validModel || model == "" {
		return nil, io.ErrUnexpectedEOF
	}
	m = project(m, "id object created model choices usage")
	m["model"] = public
	if m["usage"] == nil {
		delete(m, "usage")
	} else if u, ok := m["usage"].(map[string]any); ok {
		m["usage"] = project(u, "prompt_tokens completion_tokens total_tokens")
	}
	if choices, ok := m["choices"].([]any); ok {
		for i, c := range choices {
			if choice, ok := c.(map[string]any); ok {
				keys := "index message finish_reason"
				if m["object"] == "chat.completion.chunk" {
					keys = "index delta finish_reason"
				}
				choice = project(choice, keys)
				choices[i] = choice
				for _, key := range []string{"message", "delta"} {
					if message, ok := choice[key].(map[string]any); ok {
						message = project(message, "role content tool_calls")
						choice[key] = message
						if calls, ok := message["tool_calls"].([]any); ok {
							for j, call := range calls {
								if tool, ok := call.(map[string]any); ok {
									fields := "id type function"
									if key == "delta" {
										fields += " index"
									}
									tool = project(tool, fields)
									calls[j] = tool
									if function, ok := tool["function"].(map[string]any); ok {
										tool["function"] = project(function, "name arguments")
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return json.Marshal(m)
}

func project(m map[string]any, keys string) map[string]any {
	out := map[string]any{}
	for _, key := range strings.Fields(keys) {
		if v, ok := m[key]; ok {
			out[key] = v
		}
	}
	return out
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
