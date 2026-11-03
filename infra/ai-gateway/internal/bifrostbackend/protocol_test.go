package bifrostbackend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type protocolCapture struct{ text, finish, callID, name, arguments string }

func (c *protocolCapture) emit(t *testing.T) func([]byte) error {
	t.Helper()
	return func(raw []byte) error {
		var reply struct {
			Model   string
			Extra   json.RawMessage `json:"extra_fields"`
			Choices []struct {
				Message, Delta struct {
					Content   string
					ToolCalls []struct {
						ID       string
						Function struct{ Name, Arguments string }
					} `json:"tool_calls"`
				}
				Finish *string `json:"finish_reason"`
			}
		}
		if err := json.Unmarshal(raw, &reply); err != nil {
			return err
		}
		if reply.Model != "public-pool" || reply.Extra != nil {
			t.Errorf("alias/private fields: %s", raw)
		}
		for _, choice := range reply.Choices {
			c.text += choice.Message.Content + choice.Delta.Content
			if choice.Finish != nil {
				if c.finish != "" {
					t.Error("duplicate terminal chunk")
				}
				c.finish = *choice.Finish
			}
			calls := append(choice.Message.ToolCalls, choice.Delta.ToolCalls...)
			for _, call := range calls {
				if call.ID != "" {
					c.callID = call.ID
				}
				if call.Function.Name != "" {
					c.name = call.Function.Name
				}
				c.arguments += call.Function.Arguments
			}
		}
		return nil
	}
}

func protocolRequest(t *testing.T, stream bool, extra map[string]any) []byte {
	t.Helper()
	body := map[string]any{"model": "public-pool", "messages": []any{map[string]string{"role": "user", "content": "hello"}}, "stream": stream}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func protocolBody(t *testing.T, r *http.Request, responses bool) map[string]json.RawMessage {
	t.Helper()
	path := "/chat/completions"
	if responses {
		path = "/responses"
	}
	if !strings.HasSuffix(r.URL.Path, path) || r.Header.Get("Authorization") != "Bearer fixture" {
		t.Errorf("wire boundary: %s want %s", r.URL.Path, path)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Error(err)
	}
	return body
}

func protocolEvent(w http.ResponseWriter, event string, data map[string]any) {
	if event != "" {
		data["type"] = event
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	raw, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
}

func protocolRespond(w http.ResponseWriter, model string, stream, responses, tool bool) {
	item := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "done", "annotations": []any{}}}}
	if tool {
		item = map[string]any{"id": "fc_fixture", "type": "function_call", "call_id": "call_fixture", "name": "lookup", "arguments": `{"key":"value"}`, "status": "completed"}
	}
	response := map[string]any{"id": "resp_fixture", "object": "response", "created_at": 1, "model": model, "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}}
	if !responses {
		message := map[string]any{"role": "assistant", "content": "done"}
		response = map[string]any{"id": "chat_fixture", "object": "chat.completion", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			protocolEvent(w, "", map[string]any{"id": "chat_fixture", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": message}}})
			protocolEvent(w, "", map[string]any{"id": "chat_fixture", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": response["usage"]})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
	}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	protocolEvent(w, "response.created", map[string]any{"response": map[string]any{"id": "resp_fixture", "model": model, "created_at": 1, "status": "in_progress", "output": []any{}}})
	if tool {
		protocolEvent(w, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "fc_fixture", "type": "function_call", "call_id": "call_fixture", "name": "lookup", "arguments": ""}})
		protocolEvent(w, "response.function_call_arguments.delta", map[string]any{"item_id": "fc_fixture", "output_index": 0, "delta": `{"key":"value"}`})
	} else {
		protocolEvent(w, "response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "content": []any{}}})
		protocolEvent(w, "response.output_text.delta", map[string]any{"item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": "done"})
	}
	protocolEvent(w, "response.output_item.done", map[string]any{"output_index": 0, "item": item})
	protocolEvent(w, "response.completed", map[string]any{"response": response})
}

func TestProtocolNativeAndCompatibleMatrix(t *testing.T) {
	for _, kind := range []string{"openai", "openai-compatible"} {
		for _, model := range []string{"arbitrary-iris", "delta-42"} {
			for _, stream := range []bool{false, true} {
				for _, limit := range []int{0, 1, 65536} {
					t.Run(fmt.Sprintf("%s/%s/stream=%v/limit=%d", kind, model, stream, limit), func(t *testing.T) {
						responses := kind == "openai"
						prompt := strings.Repeat("complete context ", 1024)
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							body := protocolBody(t, r, responses)
							input, token := "messages", "max_completion_tokens"
							if responses {
								input, token = "input", "max_output_tokens"
							}
							if string(body["model"]) != `"`+model+`"` || !strings.Contains(string(body[input]), prompt) {
								t.Error("model/context changed")
							}
							want := limit
							if responses && limit > 0 && limit < 16 {
								want = 16
							} // Existing SDK normalization.
							if (want == 0 && body[token] != nil) || (want > 0 && string(body[token]) != fmt.Sprint(want)) {
								t.Errorf("wire token=%s want=%d", body[token], want)
							}
							protocolRespond(w, model, stream, responses, false)
						}))
						defer server.Close()
						extra := map[string]any{"messages": []any{map[string]string{"role": "user", "content": prompt}}}
						if limit > 0 {
							extra["max_completion_tokens"] = limit
						}
						var capture protocolCapture
						result, err := Run(t.Context(), []Route{{ID: "provider", Kind: kind, Model: model, URL: server.URL, Secret: "fixture", Weight: 1}}, protocolRequest(t, stream, extra), true, capture.emit(t))
						if err != nil || !result.Confirmed || result.InputTokens != 3 || result.OutputTokens != 2 || capture.text != "done" || capture.finish != "stop" {
							t.Fatalf("result=%+v reply=%+v err=%v", result, capture, err)
						}
					})
				}
			}
		}
	}
}

func TestProtocolChatOnlyParameters(t *testing.T) {
	for key, value := range map[string]any{"stop": []string{"END"}, "seed": 7, "n": 2, "logit_bias": map[string]int{"42": 1}, "presence_penalty": 0.25, "frequency_penalty": 0.5, "logprobs": true, "audio": map[string]string{"voice": "alloy", "format": "wav"}, "prediction": map[string]string{"type": "content", "content": "known"}, "web_search_options": map[string]string{"search_context_size": "low"}} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", key, stream), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := protocolBody(t, r, false)
					var actual, expected any
					want, _ := json.Marshal(value)
					if json.Unmarshal(body[key], &actual) != nil || json.Unmarshal(want, &expected) != nil || !reflect.DeepEqual(actual, expected) {
						t.Errorf("Chat-only %s lost: %s", key, body[key])
					}
					protocolRespond(w, "arbitrary-iris", stream, false, false)
				}))
				defer server.Close()
				var capture protocolCapture
				result, err := Run(t.Context(), []Route{{ID: "provider", Kind: "openai", Model: "arbitrary-iris", URL: server.URL, Secret: "fixture", Weight: 1}}, protocolRequest(t, stream, map[string]any{key: value}), true, capture.emit(t))
				if err != nil || !result.Confirmed || capture.text != "done" {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestProtocolFallbackMatrix(t *testing.T) {
	for _, kinds := range [][2]string{{"openai", "openai-compatible"}, {"openai-compatible", "openai"}, {"openai", "openai"}} {
		for _, stream := range []bool{false, true} {
			for _, chatOnly := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-to-%s/stream=%v/chatOnly=%v", kinds[0], kinds[1], stream, chatOnly), func(t *testing.T) {
					var mu sync.Mutex
					var called []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body struct{ Model string }
						if json.NewDecoder(r.Body).Decode(&body) != nil {
							t.Error("bad body")
						}
						index := 0
						if body.Model == "second" {
							index = 1
						}
						responses := kinds[index] == "openai" && !chatOnly
						path := "/chat/completions"
						if responses {
							path = "/responses"
						}
						if !strings.HasSuffix(r.URL.Path, path) {
							t.Errorf("fallback protocol=%s want=%s", r.URL.Path, path)
						}
						mu.Lock()
						called = append(called, body.Model)
						mu.Unlock()
						if index == 0 {
							w.WriteHeader(503)
							_, _ = fmt.Fprint(w, `{"error":{"message":"fixture unavailable","type":"server_error"}}`)
							return
						}
						protocolRespond(w, body.Model, stream, responses, false)
					}))
					defer server.Close()
					secondID := "second-provider"
					if kinds[0] == kinds[1] {
						secondID = "first-provider"
					}
					routes := []Route{{ID: "first-provider", Kind: kinds[0], Model: "first", URL: server.URL, Secret: "fixture", Weight: 1}, {ID: secondID, Kind: kinds[1], Model: "second", URL: server.URL, Secret: "fixture", Weight: 0}}
					extra := map[string]any{}
					if chatOnly {
						extra["seed"] = 7
					}
					var capture protocolCapture
					result, err := Run(t.Context(), routes, protocolRequest(t, stream, extra), true, capture.emit(t))
					if err != nil || !result.Confirmed || capture.text != "done" || capture.finish != "stop" {
						t.Fatalf("result=%+v reply=%+v err=%v", result, capture, err)
					}
					mu.Lock()
					defer mu.Unlock()
					if !reflect.DeepEqual(called, []string{"first", "second"}) {
						t.Fatalf("calls=%v", called)
					}
				})
			}
		}
	}
}

func TestProtocolToolsTwoRounds(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := protocolBody(t, r, true)
				var reasoning struct{ Effort string }
				_, oldField := body["reasoning_effort"]
				if json.Unmarshal(body["reasoning"], &reasoning) != nil || reasoning.Effort != "medium" || oldField {
					t.Errorf("reasoning protocol=%s old=%s", body["reasoning"], body["reasoning_effort"])
				}
				var tools []map[string]json.RawMessage
				if json.Unmarshal(body["tools"], &tools) != nil || len(tools) != 1 || string(tools[0]["name"]) != `"lookup"` || tools[0]["function"] != nil {
					t.Errorf("tool conversion=%s", body["tools"])
				}
				if calls.Add(1) == 2 {
					var input []map[string]json.RawMessage
					if json.Unmarshal(body["input"], &input) != nil || len(input) != 3 || string(input[1]["type"]) != `"function_call"` || string(input[2]["type"]) != `"function_call_output"` || string(input[2]["call_id"]) != `"call_fixture"` || string(input[2]["output"]) != `"tool result"` {
						t.Errorf("two-round input=%s", body["input"])
					}
					protocolRespond(w, "tool-model", stream, true, false)
					return
				}
				protocolRespond(w, "tool-model", stream, true, true)
			}))
			defer server.Close()
			routes := []Route{{ID: "provider", Kind: "openai", Model: "tool-model", URL: server.URL, Secret: "fixture", Weight: 1}}
			function := map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object", "properties": map[string]any{"key": map[string]string{"type": "string"}}, "required": []string{"key"}}}
			extra := map[string]any{"tools": []any{map[string]any{"type": "function", "function": function}}, "reasoning_effort": "medium"}
			var first protocolCapture
			result, err := Run(t.Context(), routes, protocolRequest(t, stream, extra), true, first.emit(t))
			if err != nil || !result.Confirmed || first.finish != "tool_calls" || first.callID != "call_fixture" || first.name != "lookup" || first.arguments != `{"key":"value"}` {
				t.Fatalf("first=%+v result=%+v err=%v", first, result, err)
			}
			extra["messages"] = []any{map[string]string{"role": "user", "content": "hello"}, map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": first.callID, "type": "function", "function": map[string]string{"name": first.name, "arguments": first.arguments}}}}, map[string]string{"role": "tool", "tool_call_id": first.callID, "content": "tool result"}}
			var second protocolCapture
			result, err = Run(t.Context(), routes, protocolRequest(t, stream, extra), true, second.emit(t))
			if err != nil || !result.Confirmed || second.text != "done" || second.finish != "stop" || calls.Load() != 2 {
				t.Fatalf("second=%+v result=%+v calls=%d err=%v", second, result, calls.Load(), err)
			}
		})
	}
}

func TestProtocolInterruptedStream(t *testing.T) {
	for _, mode := range []string{"rejected", "cancelled", "writer-disconnected", "upstream-disconnected"} {
		t.Run(mode, func(t *testing.T) {
			var calls, fallback atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_ = protocolBody(t, r, true)
				if mode == "rejected" {
					w.WriteHeader(400)
					_, _ = fmt.Fprint(w, `{"error":{"message":"unsupported parameter","type":"invalid_request_error","param":"reasoning_effort"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				protocolEvent(w, "response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "delta": "once"})
				w.(http.Flusher).Flush()
			}))
			defer server.Close()
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fallback.Add(1); w.WriteHeader(500) }))
			defer backup.Close()
			routes := []Route{{ID: "provider", Kind: "openai", Model: "arbitrary-iris", URL: server.URL, Secret: "fixture", Weight: 1}}
			if mode != "rejected" {
				routes = append(routes, Route{ID: "backup", Kind: "openai-compatible", Model: "backup", URL: backup.URL, Secret: "fixture", Weight: 0})
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var capture protocolCapture
			emit := capture.emit(t)
			result, err := Run(ctx, routes, protocolRequest(t, true, nil), true, func(raw []byte) error {
				if err := emit(raw); err != nil {
					return err
				}
				if capture.text == "once" && mode == "cancelled" {
					cancel()
				}
				if mode == "writer-disconnected" {
					return errors.New("fixture downstream closed")
				}
				return nil
			})
			if err == nil || calls.Load() != 1 || fallback.Load() != 0 || result.Confirmed || (mode == "rejected" && !result.NoCharge) || (mode != "rejected" && (capture.text != "once" || result.NoCharge)) {
				t.Fatalf("reply=%+v result=%+v calls=%d/%d err=%v", capture, result, calls.Load(), fallback.Load(), err)
			}
		})
	}
}
