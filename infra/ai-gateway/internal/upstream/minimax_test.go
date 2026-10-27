package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func miniMaxEvents(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("testdata/minimax.sse")
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "data: ") {
			events = append(events, strings.TrimSuffix(line[6:], "\r"))
		}
	}
	return events
}

func editEvent(t *testing.T, event string, edit func(map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(event), &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func miniMaxStream(t *testing.T, events []string, limits modelprotocol.Limits) (modelprotocol.Result, string, bool, error) {
	t.Helper()
	s := submission("https://models.example.com", true)
	s.Request.MaxTokens = 512
	r, err := modelprotocol.ValidateRequest(t.Context(), api.ChatCompletionRequest{Model: s.PublicModel, Stream: s.Request.Stream, MaxTokens: 512, Messages: s.Request.Messages}, modelprotocol.Policy{
		Model: s.PublicModel, ContextWindow: 4096, MaxOutputTokens: 512,
		MeasureInput: func(context.Context, api.ChatCompletionRequest) (int64, error) { return 219, nil },
		VerifyUsage:  func(context.Context, api.TokenUsage) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	proof := make(chan struct{})
	b := newBody(t.Context(), io.NopCloser(strings.NewReader("data: "+strings.Join(events, "\n\ndata: ")+"\n\n")), s, limits, proof)
	var output strings.Builder
	result, err := modelprotocol.Stream(t.Context(), b, r, limits, s.RequestID, func(_ context.Context, p []byte) (int, error) { return output.Write(p) })
	stopped := false
	select {
	case <-proof:
		stopped = true
	default:
	}
	return result, output.String(), stopped, err
}

func miniMaxLimits() modelprotocol.Limits {
	return modelprotocol.Limits{MaxBytes: 32768, MaxEvents: 32, MaxEventBytes: 4096}
}

func TestMiniMaxSampleSSE(t *testing.T) {
	r, output, stopped, err := miniMaxStream(t, miniMaxEvents(t), miniMaxLimits())
	if err != nil || !r.Completed || !r.UsageKnown || !stopped {
		t.Fatalf("sample failed: result=%+v stopped=%t error=%v", r, stopped, err)
	}
	if *r.Usage != (api.TokenUsage{PromptTokens: 219, CompletionTokens: 112, TotalTokens: 331}) {
		t.Fatalf("incorrect final usage: %+v", r.Usage)
	}
	for _, forbidden := range []string{"reasoning", "MiniMax", "audio_content", "total_characters", "cached_tokens", "input_sensitive", `"message"`} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("provider field leaked: %s", forbidden)
		}
	}
	if strings.Count(output, "1+1=2。") != 1 || !strings.HasSuffix(output, "data: [DONE]\n\n") {
		t.Fatal("content duplicated or terminal event missing")
	}
}

func TestMiniMaxStreamRules(t *testing.T) {
	for _, mode := range []string{"empty_delta", "zero_placeholder", "zero_finish_summary", "early_nonzero", "partial_nonzero", "early_summary", "second_summary", "delta_after_summary", "usage_after_summary", "no_finish", "wrong_object", "no_choices", "multi_choice", "bad_index", "changed_id", "changed_created", "missing_usage", "missing_token", "negative", "fractional", "overflow", "bad_total", "duplicate_usage", "multi_delta", "incomplete_tool", "no_done", "raw_limit", "event_limit", "count_limit"} {
		t.Run(mode, func(t *testing.T) {
			e := miniMaxEvents(t)
			last := len(e) - 2
			limits := miniMaxLimits()
			success := mode == "empty_delta" || mode == "zero_placeholder" || mode == "zero_finish_summary"
			switch mode {
			case "empty_delta":
				e[0] = strings.Replace(e[0], `"role":"assistant",`, "", 1)
			case "zero_finish_summary":
				e[last-1] = strings.Replace(e[last-1], `"total_tokens":0,"total_characters":0`, `"prompt_tokens":0,"completion_tokens":0,"total_tokens":0`, 1)
			case "zero_placeholder", "early_nonzero", "partial_nonzero":
				e[0] = editEvent(t, e[0], func(m map[string]any) {
					m["usage"] = map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
					if mode == "early_nonzero" {
						m["usage"] = map[string]any{"prompt_tokens": 1, "completion_tokens": 0, "total_tokens": 1}
					}
					if mode == "partial_nonzero" {
						m["usage"] = map[string]any{"prompt_tokens": 1, "total_tokens": 1}
					}
				})
			case "early_summary":
				e[0], e[last] = e[last], e[0]
			case "second_summary", "delta_after_summary", "usage_after_summary":
				extra := e[last]
				if mode == "delta_after_summary" {
					extra = e[0]
				}
				if mode == "usage_after_summary" {
					extra = strings.Replace(e[last], `"object":"chat.completion"`, `"object":"chat.completion.chunk"`, 1)
					extra = editEvent(t, extra, func(m map[string]any) { m["choices"] = []any{} })
				}
				e = append(append(e[:last+1:last+1], extra), e[last+1:]...)
			case "no_finish":
				e[last-1] = strings.Replace(e[last-1], `"finish_reason":"stop",`, "", 1)
			case "wrong_object", "no_choices", "multi_choice", "bad_index", "changed_id", "changed_created", "missing_usage", "missing_token", "negative", "fractional", "overflow", "bad_total":
				e[last] = editEvent(t, e[last], func(m map[string]any) {
					u := m["usage"].(map[string]any)
					choices := m["choices"].([]any)
					switch mode {
					case "wrong_object":
						m["object"] = "chat.completion.chunk"
					case "no_choices":
						m["choices"] = []any{}
					case "multi_choice":
						m["choices"] = append(choices, choices[0])
					case "bad_index":
						choices[0].(map[string]any)["index"] = 1
					case "changed_id":
						m["id"] = "different"
					case "changed_created":
						m["created"] = 2
					case "missing_usage":
						delete(m, "usage")
					case "missing_token":
						delete(u, "prompt_tokens")
					case "negative":
						u["prompt_tokens"] = -1
					case "fractional":
						u["completion_tokens"] = 1.5
					case "overflow":
						u["total_tokens"] = json.Number("9007199254740992")
					case "bad_total":
						u["total_tokens"] = 330
					}
				})
			case "duplicate_usage":
				e[last-1] = strings.Replace(e[last-1], `"total_tokens":0,"total_characters":0`, `"prompt_tokens":219,"completion_tokens":112,"total_tokens":331`, 1)
			case "multi_delta", "incomplete_tool":
				e[0] = editEvent(t, e[0], func(m map[string]any) {
					choices := m["choices"].([]any)
					if mode == "multi_delta" {
						m["choices"] = append(choices, choices[0])
					} else {
						choices[0].(map[string]any)["delta"] = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": "{"}}}}
					}
				})
			case "no_done":
				e = e[:len(e)-1]
			case "raw_limit":
				limits.MaxBytes = 1024
			case "event_limit":
				limits.MaxEventBytes = 256
			case "count_limit":
				limits.MaxEvents = 4
			}
			r, output, stopped, err := miniMaxStream(t, e, limits)
			if success {
				if err != nil || !r.Completed || !stopped || r.Usage == nil || r.Usage.TotalTokens != 331 {
					t.Fatalf("valid variant rejected: %+v %v", r, err)
				}
			} else {
				var protocolErr *modelprotocol.Error
				if !errors.As(err, &protocolErr) || protocolErr.Code != "ai.stream_interrupted" || r.Completed || stopped || strings.Contains(output, "[DONE]") {
					t.Fatalf("invalid variant accepted: %+v stopped=%t error=%v", r, stopped, err)
				}
			}
		})
	}
}

func TestMiniMaxSummaryMismatchDiagnostic(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	e := miniMaxEvents(t)
	e[len(e)-2] = strings.Replace(e[len(e)-2], `"content":"1+1=2。"`, `"content":"private summary text"`, 1)
	r, output, stopped, err := miniMaxStream(t, e, miniMaxLimits())
	if err != nil || !r.Completed || !stopped || !strings.Contains(output, "1+1=2。") || strings.Contains(output, "private summary text") {
		t.Fatalf("summary overrode stream: %+v %v", r, err)
	}
	if !strings.Contains(logs.String(), "upstream summary content mismatch") || strings.Contains(logs.String(), "private summary text") || strings.Contains(logs.String(), "1+1") {
		t.Fatal("missing or sensitive mismatch diagnostic")
	}
}

func TestStandardFinalUsageUnchanged(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonzero", true: "zero"}[zero], func(t *testing.T) {
			payload := chunks()
			want := api.TokenUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
			if zero {
				payload = strings.Replace(payload, `"prompt_tokens":1,"completion_tokens":2,"total_tokens":3`, `"prompt_tokens":0,"completion_tokens":0,"total_tokens":0`, 1)
				want = api.TokenUsage{}
			}
			proof := make(chan struct{})
			limits := miniMaxLimits()
			limits.MaxEvents = 4 // Three data events and one adapter progress comment.
			b := newBody(t.Context(), io.NopCloser(strings.NewReader(payload)), submission("https://models.example.com", true), limits, proof)
			events := 0
			r, err := modelprotocol.Stream(t.Context(), b, protocolRequest(t, true), limits, "local-request", func(_ context.Context, p []byte) (int, error) { events++; return len(p), nil })
			if err != nil || !r.Completed || r.Usage == nil || *r.Usage != want {
				t.Fatalf("standard usage changed: %+v %v", r, err)
			}
			if events != 3 {
				t.Fatal("standard stream gained extra data events")
			}
			select {
			case <-proof:
			default:
				t.Fatal("standard stop proof missing")
			}
		})
	}
}
