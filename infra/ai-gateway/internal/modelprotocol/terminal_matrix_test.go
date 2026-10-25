package modelprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

type terminalFixture struct {
	name    string
	content any
	tools   []api.ToolCall
}

func matrixTool(id, name, args string) api.ToolCall {
	return api.ToolCall{Id: id, Type: "function", Function: api.ToolCallFunction{Name: name, Arguments: args}}
}

func matrixRequest(t *testing.T, format, choice string, p Policy) *Request {
	t.Helper()
	v := request(t, format == "SSE", true, p).Value()
	if choice != "default" {
		v.ToolChoice = &choice
	}
	r, err := ValidateRequest(t.Context(), v, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Build the same terminal message as complete JSON or a D2 delta plus final chunk.
func matrixResponse(t *testing.T, format, finish string, shape terminalFixture, usage string) string {
	t.Helper()
	message := map[string]any{"role": "assistant", "content": shape.content}
	if len(shape.tools) > 0 {
		message["tool_calls"] = shape.tools
	}
	body := map[string]any{"id": "c", "object": "chat.completion", "created": 1, "model": "synthetic"}
	if usage != "" {
		body["usage"] = json.RawMessage(usage)
	}
	if format == "JSON" {
		body["choices"] = []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var calls []api.ToolCallDelta
	for i, tool := range shape.tools {
		name, args := tool.Function.Name, tool.Function.Arguments
		calls = append(calls, api.ToolCallDelta{Index: int64(i), Id: &tool.Id, Type: &tool.Type, Function: &api.ToolCallDeltaFunction{Name: &name, Arguments: &args}})
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
	}
	delta := map[string]any{"id": "c", "object": "chat.completion.chunk", "created": 1, "model": "synthetic", "choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": nil}}}
	body["object"] = "chat.completion.chunk"
	body["choices"] = []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}
	a, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return sse(string(a), string(b), "[DONE]")
}

func runMatrixResponse(t *testing.T, format, raw string, r *Request) (Result, string, error) {
	t.Helper()
	if format == "JSON" {
		result, err := Complete(t.Context(), io.NopCloser(strings.NewReader(raw)), r, limits())
		b, marshalErr := json.Marshal(result.Completion)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return result, string(b), err
	}
	var out bytes.Buffer
	result, err := Stream(t.Context(), io.NopCloser(strings.NewReader(raw)), r, limits(), "matrix", collect(&out))
	return result, out.String(), err
}

func TestFilteredTerminalMatrix(t *testing.T) {
	tool := matrixTool("a", "lookup", "{}")
	shapes := []terminalFixture{
		{"text", "filtered text", nil}, {"empty", "", nil},
		{"toolNull", nil, []api.ToolCall{tool}}, {"toolText", "filtered text", []api.ToolCall{tool}},
		{"toolEmpty", "", []api.ToolCall{tool}}, {"multipleTools", nil, []api.ToolCall{tool, matrixTool("b", "lookup", `{"n":1}`)}},
		// A nullable streaming delta is allowed; a complete JSON message without tools
		// must still have string content under the D2 cross-field rule.
		{"nullWithoutTools", nil, nil},
	}
	type evidence struct {
		name, raw string
		source    bool
		valid     bool
		total     int64
	}
	values := []evidence{
		{"trusted", `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`, true, true, 12},
		{"trustedZero", `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, true, true, 0},
		{"noVerifier", `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`, false, true, 12},
		{"rejectedSource", `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`, false, true, 12},
		{"missing", "", false, true, 0},
		{"invalidSum", `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":11}`, true, false, 0},
		{"overOutput", `{"prompt_tokens":10,"completion_tokens":9,"total_tokens":19}`, true, false, 0},
		{"overInput", `{"prompt_tokens":100000,"completion_tokens":2,"total_tokens":100002}`, true, false, 0},
		{"negative", `{"prompt_tokens":-1,"completion_tokens":2,"total_tokens":1}`, true, false, 0},
		{"overflow", `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":9007199254740992}`, true, false, 0},
		{"missingCount", `{"prompt_tokens":10,"total_tokens":12}`, true, false, 0},
	}
	for _, format := range []string{"JSON", "SSE"} {
		for _, shape := range shapes {
			for _, choice := range []string{"default", "none", "auto", "required"} {
				for _, e := range values {
					t.Run(format+"/"+shape.name+"/"+choice+"/"+e.name, func(t *testing.T) {
						p := syntheticPolicy()
						verified := 0
						p.VerifyUsage = func(context.Context, api.TokenUsage) bool { verified++; return e.source }
						if e.name == "noVerifier" {
							p.VerifyUsage = nil
						}
						r := matrixRequest(t, format, choice, p)
						raw := matrixResponse(t, format, "content_filter", shape, e.raw)
						result, out, err := runMatrixResponse(t, format, raw, r)
						validShape := (choice != "none" || len(shape.tools) == 0) && (format != "JSON" || shape.content != nil || len(shape.tools) > 0)
						known := validShape && e.valid && e.source
						wantSuccess := validShape && e.valid && (format == "SSE" || known)
						if !wantSuccess {
							code := "ai.upstream_rejected"
							if format == "SSE" {
								code = "ai.stream_interrupted"
							}
							requireCode(t, err, code)
							if result.Completed || result.UsageKnown || result.Usage != nil || strings.Contains(out, "[DONE]") {
								t.Fatalf("invalid terminal/usage succeeded: %+v %s", result, out)
							}
						} else {
							if err != nil || !result.Completed || result.UsageKnown != known {
								t.Fatalf("legal filtered terminal rejected: %+v %v", result, err)
							}
							if known {
								if result.Usage == nil || result.Usage.TotalTokens != e.total {
									t.Fatalf("lost trusted usage: %+v", result.Usage)
								}
							} else if result.Usage != nil || strings.Contains(out, `"usage"`) {
								t.Fatal("invented unknown usage")
							}
							if !strings.Contains(out, `"finish_reason":"content_filter"`) || strings.Contains(out, `"finish_reason":"tool_calls"`) {
								t.Fatalf("filter was turned into tool execution: %s", out)
							}
							if format == "SSE" && strings.Count(out, "[DONE]") != 1 {
								t.Fatal("missing successful DONE")
							}
							if len(shape.tools) > 0 && !strings.Contains(out, `"name":"lookup"`) {
								t.Fatal("lost complete declared tools")
							}
						}
						wantVerified := 0
						if validShape && e.valid && e.raw != "" && e.name != "noVerifier" {
							wantVerified = 1
						}
						if verified != wantVerified {
							t.Fatalf("usage source check bypassed: got %d want %d", verified, wantVerified)
						}
					})
				}
			}
		}
	}
}

func TestTerminalIntegrityMatrix(t *testing.T) {
	tool := matrixTool("a", "lookup", "{}")
	shapes := []struct {
		terminalFixture
		finishes []string
	}{
		{terminalFixture{"text", "text", nil}, []string{"stop", "length", "content_filter"}},
		{terminalFixture{"empty", "", nil}, []string{"stop", "length", "content_filter"}},
		{terminalFixture{"completeTool", nil, []api.ToolCall{tool}}, []string{"tool_calls", "content_filter"}},
		{terminalFixture{"undeclared", nil, []api.ToolCall{matrixTool("a", "other", "{}")}}, nil},
		{terminalFixture{"badJSON", nil, []api.ToolCall{matrixTool("a", "lookup", "{")}}, nil},
		{terminalFixture{"arrayArguments", nil, []api.ToolCall{matrixTool("a", "lookup", "[]")}}, nil},
		{terminalFixture{"duplicateID", nil, []api.ToolCall{tool, tool}}, nil},
		{terminalFixture{"incompleteName", nil, []api.ToolCall{matrixTool("a", "", "{}")}}, nil},
	}
	for _, format := range []string{"JSON", "SSE"} {
		for _, shape := range shapes {
			for _, choice := range []string{"default", "none", "auto", "required"} {
				for _, finish := range []string{"stop", "length", "tool_calls", "content_filter", "unknown_finish"} {
					t.Run(format+"/"+shape.name+"/"+choice+"/"+finish, func(t *testing.T) {
						r := matrixRequest(t, format, choice, syntheticPolicy())
						raw := matrixResponse(t, format, finish, shape.terminalFixture, `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`)
						result, out, err := runMatrixResponse(t, format, raw, r)
						allowed := slices.Contains(shape.finishes, finish)
						if choice == "none" && len(shape.tools) > 0 {
							allowed = false
						}
						if choice == "required" && len(shape.tools) == 0 && finish != "content_filter" {
							allowed = false
						}
						if allowed {
							if err != nil || !result.Completed || !result.UsageKnown || result.Usage == nil {
								t.Fatalf("legal terminal rejected: %+v %v", result, err)
							}
							if !strings.Contains(out, `"finish_reason":"`+finish+`"`) {
								t.Fatal("finish reason changed")
							}
						} else {
							code := "ai.upstream_rejected"
							if format == "SSE" {
								code = "ai.stream_interrupted"
							}
							requireCode(t, err, code)
							if result.Completed || result.UsageKnown || result.Usage != nil || strings.Contains(out, "[DONE]") {
								t.Fatalf("invalid tools/terminal accepted: %+v %s", result, out)
							}
						}
					})
				}
			}
		}
	}
}
