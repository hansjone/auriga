// Auriga modifications: optional per-provider protocol preference tests; see UPSTREAM.md.

package bifrost

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type protocolProvider struct{ schemas.Provider }

func (protocolProvider) GetProviderKey() schemas.ModelProvider { return "provider-alias" }

func TestPreferResponsesUsesOnlyRepresentableParameters(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	defer ctx.Cancel()
	ctx.SetValue(schemas.BifrostContextKeyBaseProviderType, schemas.OpenAI)
	config := &schemas.ProviderConfig{PreferResponses: true}
	// Each extension below is absent from mux.go's ToResponsesRequest mapping.
	for name, params := range map[string]*schemas.ChatParameters{
		"top_k":                    {TopK: schemas.Ptr(5)},
		"speed":                    {Speed: schemas.Ptr("fast")},
		"inference_geo":            {InferenceGeo: schemas.Ptr("us")},
		"mcp_servers":              {MCPServers: []schemas.ChatMCPServer{{}}},
		"container":                {Container: &schemas.ChatContainer{}},
		"cache_control":            {CacheControl: &schemas.CacheControl{}},
		"task_budget":              {TaskBudget: &schemas.ChatTaskBudget{Type: "tokens", Total: 100}},
		"context_management":       {ContextManagement: json.RawMessage(`{"keep":true}`)},
		"server_tool_invocations":  {IncludeServerSideToolInvocations: schemas.Ptr(true)},
		"reasoning_display":        {Reasoning: &schemas.ChatReasoning{Display: schemas.Ptr("summarized")}},
		"unmapped_response_format": {ResponseFormat: schemas.Ptr[any]("unknown")},
	} {
		t.Run(name, func(t *testing.T) {
			req := &schemas.BifrostChatRequest{Provider: "provider-alias", Model: "arbitrary-model", Params: params}
			if preferResponsesForChat(ctx, protocolProvider{}, config, req) {
				t.Fatal("unmapped parameter routed to Responses")
			}
		})
	}
	// Supported fields and explicit SDK ExtraParams must not force ordinary tool
	// requests back to Chat. Legacy user follows the existing conversion contract.
	format := any(json.RawMessage(`{"type":"json_object"}`))
	mapped := &schemas.BifrostChatRequest{Provider: "provider-alias", Model: "any-model", Params: &schemas.ChatParameters{
		Tools:               []schemas.ChatTool{{Type: schemas.ChatToolTypeFunction, Function: &schemas.ChatToolFunction{Name: "lookup"}}},
		Reasoning:           &schemas.ChatReasoning{Effort: schemas.Ptr("medium")},
		MaxCompletionTokens: schemas.Ptr(65536), ResponseFormat: &format,
		User: schemas.Ptr("legacy"), SafetyIdentifier: schemas.Ptr("safety"), PromptCacheKey: schemas.Ptr("cache"),
		ExtraParams: map[string]any{"extension": "preserved"},
	}}
	if !preferResponsesForChat(ctx, protocolProvider{}, config, mapped) {
		t.Fatal("representable request did not prefer Responses")
	}
	if mapped.ToResponsesRequest().Params.ExtraParams["extension"] != "preserved" {
		t.Fatal("SDK ExtraParams changed")
	}
	if ctx.Value(schemas.BifrostContextKeyChangeRequestType) != nil {
		t.Fatal("preference leaked into context")
	}
	if preferResponsesForChat(ctx, protocolProvider{}, &schemas.ProviderConfig{}, mapped) {
		t.Fatal("SDK zero config changed")
	}
	ctx.SetValue(schemas.BifrostContextKeyBaseProviderType, schemas.DeepSeek)
	if preferResponsesForChat(ctx, protocolProvider{}, config, mapped) {
		t.Fatal("OpenAI preference leaked to another provider")
	}
}

func TestPreferResponsesChatAliasIsLocal(t *testing.T) {
	ctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	defer ctx.Cancel()
	ctx.SetValue(schemas.BifrostContextKeyBaseProviderType, schemas.OpenAI)
	request := &schemas.BifrostChatRequest{Provider: "provider-alias", Model: "arbitrary-model", Params: &schemas.ChatParameters{Seed: schemas.Ptr(7)}, Fallbacks: []schemas.Fallback{{Provider: "fallback-alias", Model: "other-model"}}}
	prepared := prepareChatRequest(ctx, &schemas.ProviderConfig{PreferResponses: true}, "provider-alias", request)
	if prepared.Provider != schemas.OpenAI || request.Provider != "provider-alias" || request.Fallbacks[0].Provider != "fallback-alias" || request.Fallbacks[0].Model != "other-model" || (protocolProvider{}).GetProviderKey() != "provider-alias" {
		t.Fatal("Chat serialization must use the native base without changing the routed request")
	}
	prepared = prepareChatRequest(ctx, &schemas.ProviderConfig{}, "provider-alias", request)
	if prepared.Provider != "provider-alias" {
		t.Fatal("SDK zero config changed")
	}
}

func TestResponsesOutputKeepsOneChoiceAndAllReasoning(t *testing.T) {
	var response schemas.BifrostResponsesResponse
	raw := []byte(`{"status":"completed","output":[{"id":"rs_a","type":"reasoning","summary":[{"type":"summary_text","text":"first"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"one"},{"type":"output_text","text":"two"}]},{"type":"function_call","call_id":"call_a","name":"lookup_a","arguments":"{}"},{"id":"rs_b","type":"reasoning","summary":[{"type":"summary_text","text":"second"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"three"}]},{"type":"function_call","call_id":"call_b","name":"lookup_b","arguments":"{}"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`)
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	chat := response.ToBifrostChatResponse()
	if len(chat.Choices) != 1 || chat.Choices[0].Index != 0 || chat.Choices[0].Message == nil {
		t.Fatalf("one generation became choices: %+v", chat.Choices)
	}
	message := chat.Choices[0].Message
	if message.Content == nil || message.Content.ContentStr == nil || *message.Content.ContentStr != "onetwothree" || len(message.ToolCalls) != 2 || len(message.ReasoningDetails) != 2 || *message.ReasoningDetails[0].ID != "rs_a" || *message.ReasoningDetails[1].ID != "rs_b" || message.ReasoningDetails[1].Index != 1 || *chat.Choices[0].FinishReason != "tool_calls" || chat.Usage.TotalTokens != 5 {
		t.Fatalf("incomplete generation: %+v", message)
	}
	after, err := json.Marshal(response)
	if err != nil || string(before) != string(after) {
		t.Fatal("conversion mutated provider output")
	}
}
