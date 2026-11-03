package bifrostbackend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func reworkTools() []any {
	var tools []any
	for _, name := range []string{"lookup_a", "lookup_b"} {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": name, "parameters": map[string]any{"type": "object", "properties": map[string]any{"key": map[string]string{"type": "string"}}}}})
	}
	return tools
}

func reworkCall(id, name, args string) map[string]any {
	return map[string]any{"id": "fc_" + id, "type": "function_call", "call_id": id, "name": name, "arguments": args, "status": "completed"}
}

func reworkText(id, text string) map[string]any {
	return map[string]any{"id": id, "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}
}

func reworkReasoning(id string) map[string]any {
	return map[string]any{"id": id, "type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": id}}}
}

// Tool argument deltas deliberately interleave after all item-added events.
// Prefix items are not Chat tool indices, and arrival order does not join calls.
func reworkRespond(w http.ResponseWriter, stream bool, output []map[string]any) {
	response := map[string]any{"id": "resp_rework", "object": "response", "created_at": 1, "model": "private-model", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	protocolEvent(w, "response.created", map[string]any{"response": map[string]any{"id": "resp_rework", "model": "private-model", "created_at": 1, "output": []any{}}})
	for i, item := range output {
		initial := make(map[string]any)
		for k, v := range item {
			initial[k] = v
		}
		switch item["type"] {
		case "function_call":
			initial["arguments"] = ""
		case "message":
			initial["content"] = []any{}
		}
		protocolEvent(w, "response.output_item.added", map[string]any{"output_index": i, "item": initial})
		if item["type"] == "message" {
			text := item["content"].([]any)[0].(map[string]any)["text"]
			protocolEvent(w, "response.output_text.delta", map[string]any{"output_index": i, "item_id": item["id"], "content_index": 0, "delta": text})
		}
	}
	for part := range 2 {
		for i, item := range output {
			if item["type"] != "function_call" {
				continue
			}
			args := item["arguments"].(string)
			middle := len(args) / 2
			delta := args[:middle]
			if part == 1 {
				delta = args[middle:]
			}
			protocolEvent(w, "response.function_call_arguments.delta", map[string]any{"output_index": i, "item_id": item["id"], "delta": delta})
		}
	}
	for i, item := range output {
		protocolEvent(w, "response.output_item.done", map[string]any{"output_index": i, "item": item})
	}
	protocolEvent(w, "response.completed", map[string]any{"response": response})
}

type reworkChatCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type reworkChatMessage struct {
	Role      string           `json:"role,omitempty"`
	Content   string           `json:"content"`
	ToolCalls []reworkChatCall `json:"tool_calls,omitempty"`
}

type reworkClient struct {
	message reworkChatMessage
	calls   map[int]reworkChatCall
	finish  string
}

// A normal n=1 Chat consumer reads only choices[0], and stream tools join by index.
func (c *reworkClient) emit(t *testing.T, stream bool) func([]byte) error {
	t.Helper()
	c.calls = make(map[int]reworkChatCall)
	return func(raw []byte) error {
		var reply struct {
			Model   string
			Extra   json.RawMessage `json:"extra_fields"`
			Choices []struct {
				Index          int
				Message, Delta reworkChatMessage
				Finish         *string `json:"finish_reason"`
			}
		}
		if err := json.Unmarshal(raw, &reply); err != nil {
			return err
		}
		if reply.Model != "public-pool" || reply.Extra != nil {
			t.Error("public identity changed")
		}
		if len(reply.Choices) == 0 {
			return nil
		}
		if len(reply.Choices) != 1 || reply.Choices[0].Index != 0 {
			t.Error("one response became alternative choices")
		}
		choice := reply.Choices[0]
		message := choice.Message
		if stream {
			message = choice.Delta
		}
		c.message.Content += message.Content
		for i, call := range message.ToolCalls {
			index := i
			if stream {
				index = call.Index
			}
			prior := c.calls[index]
			if call.ID != "" {
				if prior.ID != "" && prior.ID != call.ID {
					t.Errorf("tool calls collide at index %d", index)
				}
				prior.ID, prior.Type = call.ID, call.Type
			}
			if call.Function.Name != "" {
				prior.Function.Name = call.Function.Name
			}
			prior.Index = index
			prior.Function.Arguments += call.Function.Arguments
			c.calls[index] = prior
		}
		if choice.Finish != nil {
			if c.finish != "" {
				t.Error("duplicate finish")
			}
			c.finish = *choice.Finish
		}
		return nil
	}
}

func TestProtocolAssistantHistoryWithTools(t *testing.T) {
	for _, blocks := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, fallback := range []bool{false, true} {
				t.Run(fmt.Sprintf("blocks=%v/stream=%v/fallback=%v", blocks, stream, fallback), func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body := protocolBody(t, r, true)
						calls.Add(1)
						for _, want := range []string{"assistant context", "call_a", "call_b", "result_a", "result_b", `\"a\":1`, `\"b\":2`} {
							if !strings.Contains(string(body["input"]), want) {
								t.Errorf("history lost %q: %s", want, body["input"])
							}
						}
						if fallback && string(body["model"]) == `"first"` {
							w.WriteHeader(503)
							_, _ = fmt.Fprint(w, `{"error":{"message":"fixture unavailable","type":"server_error"}}`)
							return
						}
						protocolRespond(w, "private-model", stream, true, false)
					}))
					defer server.Close()
					var content any = "assistant context"
					if blocks {
						content = []any{map[string]string{"type": "text", "text": "assistant context"}}
					}
					extra := map[string]any{"tools": reworkTools(), "messages": []any{map[string]string{"role": "user", "content": "question"}, map[string]any{"role": "assistant", "content": content, "tool_calls": []any{map[string]any{"id": "call_a", "type": "function", "function": map[string]string{"name": "lookup_a", "arguments": `{"a":1}`}}, map[string]any{"id": "call_b", "type": "function", "function": map[string]string{"name": "lookup_b", "arguments": `{"b":2}`}}}}, map[string]string{"role": "tool", "tool_call_id": "call_a", "content": "result_a"}, map[string]string{"role": "tool", "tool_call_id": "call_b", "content": "result_b"}}}
					routes := []Route{{ID: "provider", Kind: "openai", Model: "first", URL: server.URL, Secret: "fixture", Weight: 1}}
					wantCalls := int32(1)
					if fallback {
						routes = append(routes, Route{ID: "provider", Kind: "openai", Model: "second", URL: server.URL, Secret: "fixture", Weight: 0})
						wantCalls = 2
					}
					var client reworkClient
					result, err := Run(t.Context(), routes, protocolRequest(t, stream, extra), true, client.emit(t, stream))
					if err != nil || !result.Confirmed || client.message.Content != "done" || calls.Load() != wantCalls {
						t.Fatalf("result=%+v content=%q calls=%d err=%v", result, client.message.Content, calls.Load(), err)
					}
				})
			}
		}
	}
}

func TestProtocolParallelToolsRoundTrip(t *testing.T) {
	callA, callB := reworkCall("call_a", "lookup_a", `{"a":1}`), reworkCall("call_b", "lookup_b", `{"b":2}`)
	for _, tc := range []struct {
		name, text string
		output     []map[string]any
	}{
		{"none", "", []map[string]any{callA, callB}},
		{"reasoning", "", []map[string]any{reworkReasoning("rs_1"), callA, callB}},
		{"two-reasoning", "", []map[string]any{reworkReasoning("rs_1"), reworkReasoning("rs_2"), callA, callB}},
		{"two-text", "AB", []map[string]any{reworkText("msg_a", "A"), reworkText("msg_b", "B"), callA, callB}},
		{"mixed-prefix", "AB", []map[string]any{reworkReasoning("rs_1"), reworkText("msg_a", "A"), reworkReasoning("rs_2"), reworkText("msg_b", "B"), callA, callB}},
		{"interleaved", "AB", []map[string]any{reworkReasoning("rs_1"), reworkText("msg_a", "A"), callA, reworkText("msg_b", "B"), reworkReasoning("rs_2"), callB}},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body := protocolBody(t, r, true)
					if string(body["parallel_tool_calls"]) != "true" {
						t.Error("parallel tools disabled")
					}
					if calls.Add(1) == 1 {
						reworkRespond(w, stream, tc.output)
						return
					}
					var input []map[string]json.RawMessage
					if err := json.Unmarshal(body["input"], &input); err != nil {
						t.Error(err)
					}
					var text string
					var callCount, resultCount int
					callsByID, resultsByID := make(map[string]string), make(map[string]string)
					for _, item := range input {
						var id, name, args, output string
						_ = json.Unmarshal(item["call_id"], &id)
						switch string(item["type"]) {
						case `"function_call"`:
							callCount++
							_ = json.Unmarshal(item["name"], &name)
							_ = json.Unmarshal(item["arguments"], &args)
							callsByID[id] = name + ":" + args
						case `"function_call_output"`:
							resultCount++
							_ = json.Unmarshal(item["output"], &output)
							resultsByID[id] = output
						case `"message"`:
							if string(item["role"]) == `"assistant"` {
								text += string(item["content"])
							}
						}
					}
					if callCount != 2 || resultCount != 2 || len(callsByID) != 2 || len(resultsByID) != 2 || callsByID["call_a"] != `lookup_a:{"a":1}` || callsByID["call_b"] != `lookup_b:{"b":2}` || resultsByID["call_a"] != "result_a" || resultsByID["call_b"] != "result_b" || (tc.text != "" && !strings.Contains(text, tc.text)) {
						t.Errorf("second-round history incomplete: %s", body["input"])
					}
					reworkRespond(w, stream, []map[string]any{reworkText("msg_final", "complete")})
				}))
				defer server.Close()
				routes := []Route{{ID: "provider-alias", Kind: "openai", Model: "private-model", URL: server.URL, Secret: "fixture", Weight: 1}}
				extra := map[string]any{"tools": reworkTools(), "parallel_tool_calls": true}
				var first reworkClient
				result, err := Run(t.Context(), routes, protocolRequest(t, stream, extra), true, first.emit(t, stream))
				if err != nil || !result.Confirmed || first.message.Content != tc.text || first.finish != "tool_calls" || len(first.calls) != 2 || first.calls[0].ID != "call_a" || first.calls[1].ID != "call_b" || first.calls[0].Function.Name != "lookup_a" || first.calls[1].Function.Name != "lookup_b" || first.calls[0].Function.Arguments != `{"a":1}` || first.calls[1].Function.Arguments != `{"b":2}` {
					t.Fatalf("first=%+v calls=%+v result=%+v err=%v", first, first.calls, result, err)
				}
				first.message.Role = "assistant"
				first.message.ToolCalls = []reworkChatCall{first.calls[0], first.calls[1]}
				extra["messages"] = []any{map[string]string{"role": "user", "content": "question"}, first.message, map[string]string{"role": "tool", "tool_call_id": first.calls[0].ID, "content": "result_a"}, map[string]string{"role": "tool", "tool_call_id": first.calls[1].ID, "content": "result_b"}}
				var second reworkClient
				result, err = Run(t.Context(), routes, protocolRequest(t, stream, extra), true, second.emit(t, stream))
				if err != nil || !result.Confirmed || second.message.Content != "complete" || second.finish != "stop" || calls.Load() != 2 {
					t.Fatalf("second=%+v result=%+v calls=%d err=%v", second, result, calls.Load(), err)
				}
			})
		}
	}
}
