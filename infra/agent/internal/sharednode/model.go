package sharednode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (r *Relay) model(ctx context.Context, w http.ResponseWriter, raw []byte) error {
	callerContext := ctx
	ctx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	idle := time.AfterFunc(30*time.Second, cancelStream)
	defer idle.Stop()
	var in struct {
		TenantID   string         `json:"tenantId"`
		SessionID  string         `json:"sessionId"`
		TaskID     string         `json:"taskId"`
		CallID     string         `json:"callId"`
		Capability string         `json:"capability"`
		Body       map[string]any `json:"body"`
	}
	if json.Unmarshal(raw, &in) != nil || r.Client.Model == nil || !uuid.MatchString(in.CallID) {
		return errors.New("common.validation_failed")
	}
	cap, e := r.capability(ctx, in.Capability)
	if e != nil {
		return e
	}
	if in.TenantID != cap.Binding.TenantId || in.SessionID != cap.Binding.SessionId || in.TaskID != cap.TaskId || cap.Audience != "auriga.shared.model" || !has(cap.Scopes, "model.invoke") {
		return errors.New("shared.binding_mismatch")
	}
	body, e := openAI(in.Body)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, "POST", r.Client.Settings.SharedModel.Address+"/internal/shared/model/invoke", bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Auriga-Shared-Capability", in.Capability)
	req.Header.Set("Auriga-Shared-Session-Id", in.SessionID)
	req.Header.Set("Auriga-Shared-Task-Id", in.TaskID)
	req.Header.Set("Idempotency-Key", in.CallID)
	// Exactly one RoundTrip. Cancellation, disconnect and partial streams are
	// terminal; net/http cannot retry this POST because GetBody is cleared.
	req.GetBody = nil
	response, e := r.Client.Model.Do(req)
	if e != nil {
		return e
	}
	defer func() { _ = response.Body.Close() }()
	format := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if response.StatusCode != 200 || !has([]string{"text/event-stream", "application/json"}, format) {
		return errors.New("common.unavailable")
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	emit := func(value any) error {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
		if e := json.NewEncoder(w).Encode(value); e != nil {
			return e
		}
		return http.NewResponseController(w).Flush()
	}
	state := newResponseState(fmt.Sprint(body["model"]), emit)
	accept := func(data string) error {
		idle.Reset(30 * time.Second)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, e := r.capability(ctx, in.Capability); e != nil {
			return e
		}
		return state.accept([]byte(data))
	}
	activity := &streamActivity{reader: response.Body, timer: idle}
	if format == "application/json" {
		var completion []byte
		completion, e = io.ReadAll(io.LimitReader(activity, maximum+1))
		if e == nil && len(completion) <= maximum {
			var chunk []byte
			chunk, e = completionChunk(completion)
			if e == nil {
				e = accept(string(chunk))
			}
		} else if e == nil {
			e = errors.New("oversize completion")
		}
	} else {
		e = readSSE(activity, accept)
	}
	if e == nil {
		e = state.complete()
	}
	if e != nil && callerContext.Err() == nil {
		_ = emit(map[string]any{"type": "finish", "reason": map[string]any{"kind": "error", "failure": map[string]string{"code": "SERVER", "message": "Shared model stream unavailable"}}})
	}
	return nil
}

// A non-streaming OpenAI response uses the same validated public state machine
// as SSE. No synthetic DONE is created; an actual terminal choice is required.
func completionChunk(raw []byte) ([]byte, error) {
	var event map[string]json.RawMessage
	if json.Unmarshal(raw, &event) != nil || string(event["object"]) != `"chat.completion"` {
		return nil, errors.New("invalid completion")
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(event["choices"], &choices) != nil || len(choices) != 1 {
		return nil, errors.New("invalid completion choice")
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(choices[0]["message"], &message) != nil || string(message["role"]) != `"assistant"` || len(choices[0]["finish_reason"]) == 0 || string(choices[0]["finish_reason"]) == "null" {
		return nil, errors.New("incomplete completion")
	}
	if toolsRaw, ok := message["tool_calls"]; ok {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(toolsRaw, &tools) != nil || len(tools) > 128 {
			return nil, errors.New("invalid completion tools")
		}
		for i, tool := range tools {
			tool["index"], _ = json.Marshal(i)
		}
		message["tool_calls"], _ = json.Marshal(tools)
	}
	choices[0]["delta"], _ = json.Marshal(message)
	delete(choices[0], "message")
	event["object"] = json.RawMessage(`"chat.completion.chunk"`)
	event["choices"], _ = json.Marshal(choices)
	return json.Marshal(event)
}

type streamActivity struct {
	reader io.Reader
	timer  *time.Timer
}

func (s *streamActivity) Read(p []byte) (int, error) {
	n, e := s.reader.Read(p)
	if n > 0 {
		s.timer.Reset(30 * time.Second)
	}
	return n, e
}
func has(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

// Converts the reviewed public DSH message projection to N's original OpenAI
// chat body. Identity is supplied solely in the four fixed transport headers.
func openAI(input map[string]any) (map[string]any, error) {
	fail := errors.New("common.validation_failed")
	for k := range input {
		if !has([]string{"model", "messages", "tools", "temperature", "maxTokens"}, k) {
			return nil, fail
		}
	}
	model, ok := input["model"].(string)
	if !ok || model == "" || len(model) > 128 {
		return nil, fail
	}
	messages, ok := input["messages"].([]any)
	if !ok || len(messages) == 0 || len(messages) > 256 {
		return nil, fail
	}
	result := map[string]any{"model": model, "stream": true, "stream_options": map[string]bool{"include_usage": true}}
	converted := []any{}
	pending := map[string]bool{}
	for _, v := range messages {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fail
		}
		role, _ := m["role"].(string)
		if !has([]string{"system", "user", "assistant", "tool"}, role) || len(pending) > 0 && role != "tool" {
			return nil, fail
		}
		blocks, ok := m["content"].([]any)
		if !ok {
			return nil, fail
		}
		text := ""
		calls := []any{}
		for _, v := range blocks {
			block, ok := v.(map[string]any)
			if !ok {
				return nil, fail
			}
			switch block["type"] {
			case "text":
				s, ok := block["text"].(string)
				if !ok {
					return nil, fail
				}
				text += s
			case "tool-call":
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				args, _ := block["arguments"].(string)
				if role != "assistant" || id == "" || name == "" || !json.Valid([]byte(args)) || pending[id] {
					return nil, fail
				}
				pending[id] = true
				calls = append(calls, map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": args}})
			default:
				return nil, fail
			}
		}
		item := map[string]any{"role": role, "content": text}
		if len(calls) > 0 {
			item["tool_calls"] = calls
			if text == "" {
				item["content"] = nil
			}
		}
		if role == "tool" {
			id, _ := m["toolCallId"].(string)
			if !pending[id] {
				return nil, fail
			}
			delete(pending, id)
			item["tool_call_id"] = id
		}
		converted = append(converted, item)
	}
	if len(pending) != 0 {
		return nil, fail
	}
	result["messages"] = converted
	if v, exists := input["maxTokens"]; exists {
		n, ok := v.(float64)
		if !ok || n < 1 || n > 9007199254740991 || n != float64(int64(n)) {
			return nil, fail
		}
		result["max_tokens"] = int64(n)
	}
	if v, exists := input["temperature"]; exists {
		n, ok := v.(float64)
		if !ok || n < 0 || n > 2 {
			return nil, fail
		}
		result["temperature"] = n
	}
	if v, exists := input["tools"]; exists {
		tools, ok := v.([]any)
		if !ok || len(tools) > 128 {
			return nil, fail
		}
		out := []any{}
		for _, v := range tools {
			tool, ok := v.(map[string]any)
			if !ok {
				return nil, fail
			}
			out = append(out, map[string]any{"type": "function", "function": tool})
		}
		result["tools"] = out
	}
	b, _ := json.Marshal(result)
	if len(b) > 4*1024*1024 {
		return nil, fail
	}
	return result, nil
}

func readSSE(reader io.Reader, accept func(string) error) error {
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), maximum)
	data := ""
	dispatch := func() (bool, error) {
		if data == "" {
			return false, nil
		}
		value := strings.TrimSuffix(data, "\n")
		data = ""
		if value == "[DONE]" {
			return true, nil
		}
		return false, accept(value)
	}
	for scan.Scan() {
		line := scan.Text()
		if line == "" {
			done, e := dispatch()
			if e != nil {
				return e
			}
			if done {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			s := strings.TrimPrefix(line, "data:")
			s = strings.TrimPrefix(s, " ")
			data += s + "\n"
			if len(data) > maximum {
				return errors.New("oversize SSE event")
			}
		}
	}
	if e := scan.Err(); e != nil {
		return e
	}
	return io.ErrUnexpectedEOF
}

type responseTool struct {
	index          int
	id, name, args string
}
type responseState struct {
	model, id, text, finish string
	next, textIndex         int
	tools                   map[int]*responseTool
	usage                   map[string]any
	emit                    func(any) error
}

func newResponseState(model string, emit func(any) error) *responseState {
	return &responseState{model: model, textIndex: -1, tools: map[int]*responseTool{}, emit: emit}
}
func (s *responseState) accept(raw []byte) error {
	var event struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Role    string `json:"role"`
				Content string `json:"content"`
				Tools   []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			Finish *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			Input  int64 `json:"prompt_tokens"`
			Output int64 `json:"completion_tokens"`
			Total  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	fail := errors.New("invalid completion stream")
	if json.Unmarshal(raw, &event) != nil || event.Object != "chat.completion.chunk" || event.Model != s.model || event.ID == "" || s.id != "" && s.id != event.ID || len(event.Choices) > 1 {
		return fail
	}
	s.id = event.ID
	for _, c := range event.Choices {
		if c.Index != 0 || s.finish != "" || c.Delta.Role != "" && c.Delta.Role != "assistant" {
			return fail
		}
		if c.Delta.Content != "" {
			if s.textIndex < 0 {
				s.textIndex = s.next
				s.next++
				if e := s.emit(map[string]any{"type": "block-start", "index": s.textIndex, "blockType": "text"}); e != nil {
					return e
				}
			}
			s.text += c.Delta.Content
			if len(s.text) > maximum {
				return fail
			}
			if e := s.emit(map[string]any{"type": "text-delta", "index": s.textIndex, "text": c.Delta.Content}); e != nil {
				return e
			}
		}
		for _, call := range c.Delta.Tools {
			if call.Index < 0 || call.Index >= 128 || call.Type != "" && call.Type != "function" {
				return fail
			}
			t := s.tools[call.Index]
			if t == nil {
				if call.ID == "" {
					return fail
				}
				t = &responseTool{index: s.next, id: call.ID}
				s.next++
				s.tools[call.Index] = t
				if e := s.emit(map[string]any{"type": "block-start", "index": t.index, "blockType": "tool-call"}); e != nil {
					return e
				}
			} else if call.ID != "" && call.ID != t.id {
				return fail
			}
			t.name += call.Function.Name
			t.args += call.Function.Arguments
			if len(t.name) > 64 || len(t.args) > 262144 {
				return fail
			}
			chunk := map[string]any{"type": "tool-call-delta", "index": t.index, "id": t.id, "argumentsDelta": call.Function.Arguments}
			if call.Function.Name != "" {
				chunk["name"] = t.name
			}
			if e := s.emit(chunk); e != nil {
				return e
			}
		}
		if c.Finish != nil {
			if !has([]string{"stop", "length", "tool_calls", "content_filter"}, *c.Finish) {
				return fail
			}
			s.finish = *c.Finish
		}
	}
	if event.Usage != nil {
		u := event.Usage
		if s.finish == "" || s.usage != nil || u.Input < 0 || u.Output < 0 || u.Total < 0 || u.Input > 9007199254740991-u.Output || u.Input+u.Output != u.Total {
			return fail
		}
		s.usage = map[string]any{"inputTokens": u.Input, "outputTokens": u.Output, "totalTokens": u.Total}
	}
	return nil
}
func (s *responseState) complete() error {
	if s.finish == "" || (s.finish != "content_filter" && (s.finish == "tool_calls") != (len(s.tools) > 0)) {
		return errors.New("incomplete model stream")
	}
	for _, t := range s.tools {
		var args map[string]any
		if t.name == "" || json.Unmarshal([]byte(t.args), &args) != nil || args == nil {
			return errors.New("invalid tool arguments")
		}
	}
	if s.textIndex >= 0 {
		if e := s.emit(map[string]any{"type": "block-end", "index": s.textIndex, "block": map[string]any{"type": "text", "text": s.text}}); e != nil {
			return e
		}
	}
	keys := []int{}
	for k := range s.tools {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		t := s.tools[k]
		if e := s.emit(map[string]any{"type": "block-end", "index": t.index, "block": map[string]any{"type": "tool-call", "id": t.id, "name": t.name, "arguments": t.args}}); e != nil {
			return e
		}
	}
	if s.usage != nil {
		if e := s.emit(map[string]any{"type": "usage", "usage": s.usage}); e != nil {
			return e
		}
	}
	kind := "stop"
	switch s.finish {
	case "length":
		kind = "max-tokens"
	case "tool_calls":
		kind = "tool-calls"
	case "content_filter":
		return errors.New("filtered model stream")
	}
	return s.emit(map[string]any{"type": "finish", "reason": map[string]any{"kind": kind}})
}
