package upstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"hash"
	"io"
	"log/slog"
)

// Provider-only compatibility stays outside D2. Empty projected deltas are
// forwarded, so raw events cannot disappear from the validation event budget.
type streamNormalizer struct {
	finished  bool
	summary   bool
	content   hash.Hash
	zeroUsage []byte
}

func tokenInteger(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil && i >= 0 && i <= 9007199254740991
}

func zeroTokens(u map[string]any) bool {
	p, pok := tokenInteger(u["prompt_tokens"])
	c, cok := tokenInteger(u["completion_tokens"])
	return pok && cok && p == 0 && c == 0
}

// Delay only a complete zero-usage finish chunk, preserving the standard wire
// shape at DONE. A following authoritative usage replaces its placeholder.
func (s *streamNormalizer) frame(ctx context.Context, payload, public string) ([][]byte, error) {
	previous := s.zeroUsage
	s.zeroUsage = nil
	normalized := []byte(payload)
	if payload != "[DONE]" {
		var err error
		normalized, err = s.normalize(ctx, normalized, public)
		if err != nil {
			return nil, err
		}
	}
	var frames [][]byte
	if previous != nil {
		var fields map[string]json.RawMessage
		if payload != "[DONE]" {
			if err := json.Unmarshal(normalized, &fields); err != nil {
				return nil, err
			}
		}
		if fields["usage"] != nil {
			if err := json.Unmarshal(previous, &fields); err != nil {
				return nil, err
			}
			delete(fields, "usage")
			var err error
			previous, err = json.Marshal(fields)
			if err != nil {
				return nil, err
			}
		}
		frames = append(frames, previous)
	}
	if normalized != nil {
		frames = append(frames, normalized)
	}
	return frames, nil
}

func (s *streamNormalizer) normalize(ctx context.Context, raw []byte, public string) ([]byte, error) {
	if s.summary {
		return nil, io.ErrUnexpectedEOF
	}
	normalized, err := normalize(raw, public)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(normalized))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	if m["object"] == "chat.completion" {
		return s.normalizeSummary(ctx, m)
	}
	if m["object"] != "chat.completion.chunk" {
		return nil, io.ErrUnexpectedEOF
	}
	choices, ok := m["choices"].([]any)
	if !ok || len(choices) > 1 {
		return nil, io.ErrUnexpectedEOF
	}
	finish := false
	for _, v := range choices {
		choice, ok := v.(map[string]any)
		if !ok || s.finished {
			return nil, io.ErrUnexpectedEOF
		}
		if _, exists := choice["finish_reason"]; !exists {
			choice["finish_reason"] = nil
		}
		finish = choice["finish_reason"] != nil
		if delta, ok := choice["delta"].(map[string]any); ok {
			if text, ok := delta["content"].(string); ok {
				if s.content == nil {
					s.content = sha256.New()
				}
				_, _ = s.content.Write([]byte(text))
			}
		}
	}
	if u, ok := m["usage"].(map[string]any); ok {
		_, prompt := u["prompt_tokens"]
		_, completion := u["completion_tokens"]
		switch {
		case !prompt && !completion && len(choices) > 0:
			delete(m, "usage")
		case zeroTokens(u) && !finish && !s.finished:
			delete(m, "usage")
		case zeroTokens(u) && finish:
			// A finish chunk may still carry placeholder zero usage. Wait one
			// event to distinguish it from the standard final zero-token case.
			if total, valid := tokenInteger(u["total_tokens"]); valid && total == 0 {
				s.zeroUsage, err = json.Marshal(m)
				if err != nil {
					return nil, err
				}
				s.finished = true
				return nil, nil
			}
		default:
			// A following standard usage-only chunk supersedes placeholders.
			s.zeroUsage = nil
		}
	}
	s.finished = s.finished || finish
	return json.Marshal(m)
}

func (s *streamNormalizer) normalizeSummary(ctx context.Context, m map[string]any) ([]byte, error) {
	choices, ok := m["choices"].([]any)
	if !s.finished || !ok || len(choices) != 1 {
		return nil, io.ErrUnexpectedEOF
	}
	choice, ok := choices[0].(map[string]any)
	index, valid := tokenInteger(choice["index"])
	if !ok || !valid || index != 0 {
		return nil, io.ErrUnexpectedEOF
	}
	u, ok := m["usage"].(map[string]any)
	p, pok := tokenInteger(u["prompt_tokens"])
	c, cok := tokenInteger(u["completion_tokens"])
	t, tok := tokenInteger(u["total_tokens"])
	if !ok || !pok || !cok || !tok || p+c != t {
		return nil, io.ErrUnexpectedEOF
	}
	message, _ := choice["message"].(map[string]any)
	text, _ := message["content"].(string)
	expected := sha256.Sum256(nil)
	if s.content != nil {
		copy(expected[:], s.content.Sum(nil))
	}
	actual := sha256.Sum256([]byte(text))
	if actual != expected {
		// No provider identifiers, content, arguments or hashes in diagnostics.
		slog.WarnContext(ctx, "upstream summary content mismatch")
	}
	s.summary = true
	s.zeroUsage = nil
	m["object"] = "chat.completion.chunk"
	m["choices"] = []any{}
	return json.Marshal(m)
}
