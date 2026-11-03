package bifrostbackend

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBudgetedInputOnlyAdmitsBoundedTextShapes(t *testing.T) {
	for _, content := range []string{
		`"the literal image_url and input_audio are just text"`,
		`null`,
		`[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]`,
		`[{"type":"refusal","refusal":"No"}]`,
	} {
		raw := []byte(`{"model":"auto","messages":[{"role":"user","content":` + content + `}],"max_tokens":1}`)
		if n, err := inputTokenEstimate(raw, false); err != nil || n <= 0 {
			t.Fatalf("bounded text rejected: %s %d %v", content, n, err)
		}
	}
	for _, content := range []string{
		`[{"type":"image_url","image_url":{"url":"https://media.example.com/large.png"}}]`,
		`[{"type":"input_audio","input_audio":{"data":"small-url-or-base64","format":"wav"}}]`,
		`[{"type":"video_url","video_url":{"url":"https://media.example.com/large.mp4"}}]`,
		`[{"type":"file","file":{"file_data":"small-reference"}}]`,
		`[{"type":"text","text":"hello","image_url":{"url":"https://media.example.com/large.png"}}]`,
		`[{"type":"unknown","text":"hello"}]`,
		`{"image_url":"https://media.example.com/large.png"}`,
	} {
		raw := []byte(`{"model":"auto","messages":[{"role":"user","content":` + content + `}],"max_tokens":1}`)
		if n, err := inputTokenEstimate(raw, false); n != 0 || !errors.Is(err, invalid) {
			t.Fatalf("unbounded input acquired a budget estimate: %s %d %v", content, n, err)
		}
		if n, err := inputTokenEstimate(raw, true); err != nil || n <= 0 {
			t.Fatal("metering-only upstream semantics were restricted")
		}
	}
	w := httptest.NewRecorder()
	rejectInputAccounting(w, invalid)
	var problem struct {
		Code   string            `json:"code"`
		Params map[string]string `json:"params"`
	}
	if json.Unmarshal(w.Body.Bytes(), &problem) != nil || w.Code != 400 || problem.Code != string(invalid) || problem.Params["field"] != "/messages" || strings.Contains(w.Body.String(), "media.example.com") {
		t.Fatal("input accounting rejection lost its safe field or leaked a target")
	}
}

func TestBudgetedInputRejectsDynamicRequestCapabilities(t *testing.T) {
	for _, kind := range []string{
		"web_fetch_20250910", "web_search_20250305", "code_execution_20250825",
		"mcp_toolset", "file_search", "computer_20250124", "bash_20250124",
		"text_editor_20250728", "memory_20250818", "tool_search_tool_regex_20251119",
		"custom", "future_server_tool",
	} {
		t.Run(kind, func(t *testing.T) {
			assertBudgetedDynamicInputRejected(t, `,"tools":[{"type":"`+kind+`","name":"web_fetch","mcp_server_name":"remote"}]`)
		})
	}
	for name, extra := range map[string]string{
		"nested-server-choice": `,"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"web_search"}]}}`,
		"unknown-choice":       `,"tool_choice":{"type":"web_fetch"}`,
		"opaque-output-format": `,"response_format":{"type":"future_remote_format","url":"https://remote.example.com/schema"}`,
		"server-in-local-tool": `,"tools":[{"type":"function","function":{"name":"local"},"mcp_servers":[{"url":"https://remote.example.com"}]}]`,
		"server-callers":       `,"tools":[{"type":"function","function":{"name":"local"},"allowed_callers":["code_execution_20250825"]}]`,
		"remote-schema":        `,"tools":[{"type":"function","function":{"name":"local","parameters":{"$ref":"https://remote.example.com/schema"}}}]`,
		"encoded-schema":       `,"tools":[{"type":"function","function":{"name":"local","parameters":"{\"$ref\":\"https://remote.example.com/schema\"}"}}]`,
		"remote-format-schema": `,"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":{"properties":{"x":{"$dynamicRef":"https://remote.example.com/schema"}}}}}`,
		"top-mcp":              `,"mcp_servers":[{"type":"url","url":"https://remote.example.com","name":"remote"}]`,
		"top-container":        `,"container":{"id":"remote-state"}`,
		"top-search":           `,"web_search_options":{}`,
		"top-context":          `,"context_management":{"edits":[{"type":"resume_remote_state"}]}`,
		"top-state":            `,"previous_response_id":"remote-state"`,
	} {
		t.Run(name, func(t *testing.T) { assertBudgetedDynamicInputRejected(t, extra) })
	}
	for name, extra := range map[string]string{
		"opaque-history":    `,"reasoning_details":[{"type":"reasoning.encrypted","data":"remote-state"}]`,
		"signed-history":    `,"reasoning_details":[{"type":"reasoning.text","text":"visible text","signature":"opaque-state"}]`,
		"signed-call-id":    `,"tool_calls":[{"id":"call-1_ts_b3BhcXVl","type":"function","function":{"name":"local","arguments":"{}"}}]`,
		"signed-call-name":  `,"tool_calls":[{"type":"function","function":{"name":"local_ts_b3BhcXVl","arguments":"{}"}}]`,
		"remote-audio":      `,"audio":{"id":"remote-state"}`,
		"message-extension": `,"future_remote_input":{"id":"remote-state"}`,
		"tool-extension":    `,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"local","arguments":"{}"},"extra_content":{"remote_state":"id"}}]`,
		"server-history":    `,"tool_calls":[{"id":"call-1","type":"web_fetch","function":{"name":"web_fetch","arguments":"{}"}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(`{"model":"auto","messages":[{"role":"assistant","content":"inline text"` + extra + `}],"max_tokens":128}`)
			if n, err := inputTokenEstimate(raw, false); n != 0 || err == nil {
				t.Fatalf("opaque history acquired a budget estimate: n=%d err=%v", n, err)
			}
			if n, err := inputTokenEstimate(raw, true); n <= 0 || err != nil {
				t.Fatal("metering-only history was restricted")
			}
		})
	}
}

func assertBudgetedDynamicInputRejected(t *testing.T, extra string) {
	t.Helper()
	raw := []byte(`{"model":"auto","messages":[{"role":"user","content":"fetch https://remote.example.com/large"}],"max_tokens":128` + extra + `}`)
	if n, err := inputTokenEstimate(raw, false); n != 0 || err == nil {
		t.Fatalf("dynamic input acquired a strict budget estimate: n=%d err=%v", n, err)
	}
	if n, err := inputTokenEstimate(raw, true); n <= 0 || err != nil {
		t.Fatal("metering-only dynamic input was restricted")
	}
}

func TestBudgetedInputKeepsLocalFunctionConversationAndInlineOptions(t *testing.T) {
	const conversation = `{"model":"auto","messages":[{"role":"user","content":"use local web_fetch"},{"role":"assistant","content":null,"reasoning_content":"inline reasoning","reasoning_details":[{"type":"reasoning.text","text":"inline reasoning"}],"tool_calls":[{"id":"call-1","type":"function","function":{"name":"web_fetch","arguments":"{\"url\":\"https://remote.example.com\"}"}}]},{"role":"tool","tool_call_id":"call-1","content":"already fetched local text"}],"tools":[{"type":"function","function":{"name":"web_fetch","description":"Local tool; names are not capabilities","parameters":{"type":"object","properties":{"mcp_servers":{"type":"string"}},"$defs":{"text":{"type":"string"}},"additionalProperties":false},"strict":true},"allowed_callers":["direct"],"cache_control":{"type":"ephemeral"},"input_examples":[{"input":{"url":"https://remote.example.com"}}]}],"max_completion_tokens":128,"parallel_tool_calls":false,"temperature":0.1,"top_p":0.9,"seed":1,"presence_penalty":0,"frequency_penalty":0,"logit_bias":{"1":-1},"logprobs":true,"top_logprobs":1,"reasoning_effort":"low","service_tier":"default","stream":false,"stream_options":{"include_usage":true},"stop":["STOP"],"n":1,"user":"account"`
	for _, extra := range []string{
		``,
		`,"tool_choice":"auto","response_format":{"type":"text"}`,
		`,"tool_choice":{"type":"function","function":{"name":"web_fetch"}},"response_format":{"type":"json_object"}`,
		`,"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":{"properties":{"$ref":{"type":"string"},"text":{"$ref":"#/$defs/text"}},"const":{"$ref":"literal inert data"},"$defs":{"text":{"type":"string"}}}}}`,
		`,"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"web_fetch"}}]}},"response_format":{"type":"json_schema","json_schema":{"name":"result","strict":true,"schema":{"type":"object","properties":{"literal_url":{"type":"string"}},"$defs":{"text":{"type":"string"}},"additionalProperties":false}}}`,
	} {
		raw := []byte(conversation + extra + `}`)
		if n, err := inputTokenEstimate(raw, false); err != nil || n != int64(len(raw))*2+64 {
			t.Fatalf("ordinary local function request changed accounting: n=%d err=%v", n, err)
		}
	}
}

func TestRunnerInputAccountingKeepsLocalAndMeteredServerTools(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var wire struct {
			Tools []struct {
				Type        string          `json:"type"`
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"input_schema"`
			} `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&wire) != nil || len(wire.Tools) != 1 || wire.Tools[0].Name != "web_fetch" {
			t.Error("missing tool on actual Anthropic wire")
			w.WriteHeader(400)
			return
		}
		input := "3"
		if wire.Tools[0].Type == "web_fetch_20250910" {
			input = "5000"
		} else if wire.Tools[0].Type != "" || len(wire.Tools[0].InputSchema) == 0 {
			t.Error("local function turned into a server tool")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_fixture","type":"message","role":"assistant","model":"fixture","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":` + input + `,"output_tokens":1}}`))
	}))
	defer upstream.Close()
	for _, tc := range []struct {
		name, tool string
		metered    bool
		wantInput  int64
	}{
		{"local-function", `{"type":"function","function":{"name":"web_fetch","parameters":{"type":"object","properties":{"url":{"type":"string"}}}}}`, false, 3},
		{"member-server-tool", `{"type":"web_fetch_20250910","name":"web_fetch"}`, true, 5000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"model":"auto","messages":[{"role":"user","content":"Fetch https://remote.example.com/large"}],"tools":[` + tc.tool + `],"max_tokens":128}`)
			if _, err := inputTokenEstimate(raw, tc.metered); err != nil {
				t.Fatalf("normal flow rejected before Run: %v", err)
			}
			result, err := Run(t.Context(), []Route{{ID: "fixture-provider", Kind: "anthropic", URL: upstream.URL, Secret: "fixture-key", Model: "upstream", Weight: 1}}, raw, true, func([]byte) error { return nil })
			if err != nil || !result.Confirmed || result.InputTokens != tc.wantInput || result.OutputTokens != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
	if calls.Load() != 2 {
		t.Fatal("ordinary flows did not dispatch exactly once")
	}
}
