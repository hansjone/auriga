// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type openRouterCacheRef struct {
	tool, msg, block, toolCall int
}

func (req *OpenAIChatRequest) ToBifrostChatRequest(ctx *schemas.BifrostContext) *schemas.BifrostChatRequest {
	provider, model := schemas.ParseModelString(req.Model, "")
	params := req.ChatParameters
	if params.MaxCompletionTokens == nil && req.MaxTokens != nil {
		params.MaxCompletionTokens = req.MaxTokens
	}

	if params.Reasoning != nil && params.Reasoning.Summary != nil {
		reasoning := *params.Reasoning
		reasoning.Summary = nil
		params.Reasoning = &reasoning
	}

	return &schemas.BifrostChatRequest{
		Provider:  provider,
		Model:     model,
		Input:     ConvertOpenAIMessagesToBifrostMessages(req.Messages),
		Params:    &params,
		Fallbacks: schemas.ParseFallbacks(req.Fallbacks),
	}
}

func ToOpenAIChatRequest(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostChatRequest) *OpenAIChatRequest {
	if bifrostReq == nil || bifrostReq.Input == nil {
		return nil
	}

	capModel := schemas.ResolveCanonicalModel(ctx, bifrostReq.Model)
	caps := schemas.ResolveModelCaps(schemas.ResolveBaseProvider(ctx, bifrostReq.Provider), capModel)

	openaiReq := &OpenAIChatRequest{
		Model:    bifrostReq.Model,
		Messages: ConvertBifrostMessagesToOpenAIMessages(bifrostReq.Input),
		Provider: bifrostReq.Provider,
	}

	if !caps.SupportsDeveloperRole(true) {
		for i := range openaiReq.Messages {
			if openaiReq.Messages[i].Role == schemas.ChatMessageRoleDeveloper {
				openaiReq.Messages[i].Role = schemas.ChatMessageRoleSystem
			}
		}
	}

	if !caps.SupportsMidConversationSystem(true) {
		openaiReq.Messages = inlineChatMidConversationSystem(openaiReq.Messages)
	}

	if bifrostReq.Params != nil {
		openaiReq.ChatParameters = *bifrostReq.Params
		openaiReq.ServiceTier = serviceTierForModel(caps, openaiReq.ServiceTier)
		openaiReq.ChatParameters.User = SanitizeUserField(openaiReq.ChatParameters.User)

		if openaiReq.ChatParameters.ToolChoice.IsForced() &&
			!caps.SupportsForcedToolChoice(schemas.DefaultSupportsForcedToolChoice(capModel)) {
			openaiReq.ChatParameters.ToolChoice = nil
		}

		if tc := openaiReq.ChatParameters.ToolChoice; tc != nil && tc.ChatToolChoiceStr != nil &&
			*tc.ChatToolChoiceStr == string(schemas.ChatToolChoiceTypeAny) &&
			!caps.ToolChoiceAnySupported(toolChoiceAnySupported(bifrostReq.Provider, capModel)) {
			openaiReq.ChatParameters.ToolChoice = &schemas.ChatToolChoice{
				ChatToolChoiceStr: schemas.Ptr(string(schemas.ChatToolChoiceTypeRequired)),
			}
		}
		openaiReq.ExtraParams = bifrostReq.Params.ExtraParams

		customToolsAsFunctions := customToolsUnsupported(ctx, bifrostReq.Provider, bifrostReq.Model)
		if customToolsAsFunctions {
			openaiReq.ChatParameters.ToolChoice = chatCustomToolChoiceAsFunction(openaiReq.ChatParameters.ToolChoice)
		}

		if len(openaiReq.ChatParameters.Tools) > 0 {
			normalizedTools := make([]schemas.ChatTool, len(openaiReq.ChatParameters.Tools))
			for i, tool := range openaiReq.ChatParameters.Tools {
				if customToolsAsFunctions && tool.Type == schemas.ChatToolTypeCustom {
					tool = chatCustomToolAsFunction(tool)
				}
				normalizedTools[i] = tool
				if tool.Function != nil && tool.Function.Parameters != nil {
					funcCopy := *tool.Function
					funcCopy.Parameters = tool.Function.Parameters.Normalized()
					normalizedTools[i].Function = &funcCopy

					normalizedTools[i].InvalidateSerialized()
				}
			}
			openaiReq.ChatParameters.Tools = normalizedTools
		}
	}

	if ctx != nil && ctx.Value(schemas.BifrostContextKeyDisableStore) == true {
		openaiReq.Store = new(false)
		if _, ok := openaiReq.ExtraParams["store"]; ok {
			openaiReq.ExtraParams = maps.Clone(openaiReq.ExtraParams)
			openaiReq.ExtraParams["store"] = false
		}
	}

	if chatUsesPromptCacheBreakpoints(caps, schemas.ResolveBaseProvider(ctx, bifrostReq.Provider), capModel) {
		ceiling := promptCacheBreakpointCeiling(ctx, true, openaiReq.ChatParameters.PromptCacheOptions, openaiReq.ExtraParams)
		openaiReq.Messages = applyChatCacheBreakpoints(openaiReq.Messages, ceiling)
	}

	if schemas.ResolveBaseProvider(ctx, bifrostReq.Provider) == schemas.OpenRouter && schemas.IsAnthropicModel(capModel) {
		clampOpenRouterCacheControl(openaiReq, maxResponsesCacheBreakpoints)
	}

	switch bifrostReq.Provider {
	case schemas.OpenAI, schemas.Azure:
		openaiReq.normalizeReasoningEffort(caps)
		openaiReq.stripUnsupportedSamplingParams(caps)

		return openaiReq
	case schemas.DeepSeek:
		openaiReq.filterOpenAISpecificParameters(caps)

		if openaiReq.MaxCompletionTokens != nil {
			openaiReq.MaxTokens = openaiReq.MaxCompletionTokens
			openaiReq.MaxCompletionTokens = nil
		}

		openaiReq.stripReasoningDetailsExceptToolCalls()
		openaiReq.downgradeJSONSchemaToJSONObject()
		return openaiReq
	case schemas.Groq, schemas.Cerebras:
		openaiReq.filterOpenAISpecificParameters(caps)
		openaiReq.renameAssistantReasoningToAlias()
		return openaiReq
	case schemas.XAI:
		openaiReq.filterOpenAISpecificParameters(caps)
		openaiReq.applyXAICompatibility(caps)
		return openaiReq
	case schemas.Gemini:
		openaiReq.filterOpenAISpecificParameters(caps)

		openaiReq.ServiceTier = nil
		return openaiReq
	case schemas.Mistral:
		openaiReq.filterOpenAISpecificParameters(caps)
		openaiReq.applyMistralCompatibility()
		return openaiReq
	case schemas.MiniMax:
		openaiReq.filterOpenAISpecificParameters(caps)
		openaiReq.applyMiniMaxCompatibility(capModel)
		return openaiReq
	case schemas.BytePlus:
		openaiReq.filterOpenAISpecificParameters(caps)
		openaiReq.applyBytePlusCompatibility()
		return openaiReq
	case schemas.OpencodeGo, schemas.OpencodeZen:
		openaiReq.filterOpenAISpecificParameters(caps)

		if openaiReq.MaxCompletionTokens != nil {
			openaiReq.MaxTokens = openaiReq.MaxCompletionTokens
			openaiReq.MaxCompletionTokens = nil
		}
		return openaiReq
	case schemas.Databricks:
		openaiReq.filterOpenAISpecificParameters(caps)

		if openaiReq.MaxCompletionTokens != nil {
			openaiReq.MaxTokens = openaiReq.MaxCompletionTokens
			openaiReq.MaxCompletionTokens = nil
		}
		return openaiReq
	case schemas.Ollama:
		openaiReq.filterOpenAISpecificParameters(caps)

		if openaiReq.MaxCompletionTokens != nil {
			openaiReq.MaxTokens = openaiReq.MaxCompletionTokens
			openaiReq.MaxCompletionTokens = nil
		}
		return openaiReq
	case schemas.Vertex:
		openaiReq.filterOpenAISpecificParameters(caps)

		if schemas.GetVertexEndpoint(ctx) != nil {
			return openaiReq
		}

		if schemas.IsMistralModel(bifrostReq.Model) {
			openaiReq.applyMistralCompatibility()
		} else if openaiReq.Reasoning != nil && openaiReq.Reasoning.Effort != nil &&
			*openaiReq.Reasoning.Effort == "none" {

			openaiReq.Reasoning.Effort = nil
		}
		return openaiReq
	case schemas.Fireworks:

		if openaiReq.ChatParameters.PromptCacheKey != nil && openaiReq.PromptCacheIsolationKey == nil {
			openaiReq.PromptCacheIsolationKey = openaiReq.ChatParameters.PromptCacheKey
		}

		prediction := openaiReq.ChatParameters.Prediction
		openaiReq.filterOpenAISpecificParameters(caps)
		openaiReq.ChatParameters.Prediction = prediction
		return openaiReq
	default:

		if isCustomProvider, ok := ctx.Value(schemas.BifrostContextKeyIsCustomProvider).(bool); ok && isCustomProvider {

			if schemas.ResolveBaseProvider(ctx, bifrostReq.Provider) == schemas.DeepSeek {
				openaiReq.downgradeJSONSchemaToJSONObject()
			}
			return openaiReq
		}
		openaiReq.filterOpenAISpecificParameters(caps)
		return openaiReq
	}
}

func serviceTierForModel(caps schemas.ModelCaps, tier *schemas.BifrostServiceTier) *schemas.BifrostServiceTier {
	if tier == nil {
		return nil
	}
	fallback := true
	if caps.Provider() == schemas.Bedrock || caps.Provider() == schemas.BedrockMantle {

		if *tier == schemas.BifrostServiceTierDefault || *tier == schemas.BifrostServiceTierAuto {
			return nil
		}
		fallback = false
	}
	if !caps.ServiceTierSupported(*tier, fallback) {
		return nil
	}
	return tier
}

func (req *OpenAIChatRequest) filterOpenAISpecificParameters(caps schemas.ModelCaps) {

	req.normalizeReasoningEffort(caps)

	if caps.FieldUnsupported(schemas.FieldPrediction, true) {
		req.ChatParameters.Prediction = nil
	}
	if caps.FieldUnsupported(schemas.FieldPromptCacheKey, true) {
		req.ChatParameters.PromptCacheKey = nil
	}
	if caps.FieldUnsupported(schemas.FieldPromptCacheRetention, true) {
		req.ChatParameters.PromptCacheRetention = nil
	}
	if caps.FieldUnsupported(schemas.FieldPromptCacheOptions, true) {
		req.ChatParameters.PromptCacheOptions = nil
	}
	if caps.FieldUnsupported(schemas.FieldVerbosity, true) {
		req.ChatParameters.Verbosity = nil
	}
	if caps.FieldUnsupported(schemas.FieldStore, true) {
		req.ChatParameters.Store = nil
	}
	if caps.FieldUnsupported(schemas.FieldWebSearchOptions, true) {
		req.ChatParameters.WebSearchOptions = nil
	}
}

func (req *OpenAIChatRequest) normalizeReasoningEffort(caps schemas.ModelCaps) {
	if req.ChatParameters.Reasoning != nil {
		reasoningCopy := *req.ChatParameters.Reasoning
		req.ChatParameters.Reasoning = &reasoningCopy
		if req.ChatParameters.Reasoning.Effort != nil {

			effort := *req.ChatParameters.Reasoning.Effort
			req.ChatParameters.Reasoning.Effort = schemas.Ptr(normalizeReasoningEffort(req.Provider, caps, effort))

			req.ChatParameters.Reasoning.MaxTokens = nil
		} else if req.ChatParameters.Reasoning.MaxTokens != nil {

			maxTokens := *req.ChatParameters.Reasoning.MaxTokens
			maxCompletionTokens := utils.GetMaxOutputTokensOrDefault(req.Provider, req.Model, DefaultCompletionMaxTokens)
			if req.ChatParameters.MaxCompletionTokens != nil {
				maxCompletionTokens = *req.ChatParameters.MaxCompletionTokens
			}
			effort := utils.GetReasoningEffortFromBudgetTokens(maxTokens, MinReasoningMaxTokens, maxCompletionTokens)
			req.ChatParameters.Reasoning.Effort = schemas.Ptr(effort)

			req.ChatParameters.Reasoning.MaxTokens = nil
		}

		if e := req.ChatParameters.Reasoning.Effort; e != nil && *e == schemas.ReasoningEffortNone &&
			!caps.CanDisableReasoning(defaultCanDisableReasoning(caps.Model())) {
			req.ChatParameters.Reasoning.Effort = schemas.Ptr(caps.NormalizeReasoningEffort(schemas.ReasoningEffortMinimal, defaultEffortControl(req.Provider, caps.Model())))
		}
	}
}

func (req *OpenAIChatRequest) stripUnsupportedSamplingParams(caps schemas.ModelCaps) {
	effort := ""
	if req.ChatParameters.Reasoning != nil && req.ChatParameters.Reasoning.Effort != nil {
		effort = *req.ChatParameters.Reasoning.Effort
	}
	model := caps.Model()
	if samplingParamUnsupported(caps, schemas.FieldTopP, model, effort) {
		req.ChatParameters.TopP = nil
	}
	if samplingParamUnsupported(caps, schemas.FieldTemperature, model, effort) {
		req.ChatParameters.Temperature = nil
	}
	if samplingParamUnsupported(caps, schemas.FieldTopLogprobs, model, effort) {
		req.ChatParameters.TopLogProbs = nil
	}
	if samplingParamUnsupported(caps, schemas.FieldLogprobs, model, effort) {
		req.ChatParameters.LogProbs = nil
	}
}

func (req *OpenAIChatRequest) applyMistralCompatibility() {

	if req.MaxCompletionTokens != nil {
		req.MaxTokens = req.MaxCompletionTokens
		req.MaxCompletionTokens = nil
	}

	if req.ToolChoice != nil && req.ToolChoice.ChatToolChoiceStruct != nil {
		req.ToolChoice.ChatToolChoiceStr = schemas.Ptr("any")
		req.ToolChoice.ChatToolChoiceStruct = nil
	}

	if req.Reasoning != nil && req.Reasoning.Effort != nil {
		if *req.Reasoning.Effort != "none" && *req.Reasoning.Effort != "high" {
			req.Reasoning.Effort = schemas.Ptr("high")
		}
	}

	for i := range req.Messages {
		if assistantMessage := req.Messages[i].OpenAIChatAssistantMessage; assistantMessage != nil {
			assistantMessage.Reasoning = nil
		}
	}
}

func (req *OpenAIChatRequest) applyMiniMaxCompatibility(model string) {
	if req.Reasoning == nil || req.Reasoning.Effort == nil || *req.Reasoning.Effort != schemas.ReasoningEffortNone {
		return
	}
	req.Reasoning = nil
	if strings.Contains(strings.ToLower(model), "m3.1") {
		return
	}
	if _, ok := req.ExtraParams["thinking"]; !ok {
		extraParams := make(map[string]interface{}, len(req.ExtraParams)+1)
		maps.Copy(extraParams, req.ExtraParams)
		extraParams["thinking"] = map[string]interface{}{"type": "disabled"}
		req.ExtraParams = extraParams
	}
}

func (req *OpenAIChatRequest) applyBytePlusCompatibility() {
	if req.Reasoning == nil || req.Reasoning.Effort == nil {
		return
	}
	if thinking, ok := req.ExtraParams["thinking"].(map[string]interface{}); ok {
		if thinking["type"] == "disabled" || *req.Reasoning.Effort == schemas.ReasoningEffortNone {
			req.Reasoning = nil
		}
		return
	}
	if *req.Reasoning.Effort != schemas.ReasoningEffortNone {
		return
	}
	req.Reasoning = nil
	extraParams := make(map[string]interface{}, len(req.ExtraParams)+1)
	maps.Copy(extraParams, req.ExtraParams)
	extraParams["thinking"] = map[string]interface{}{"type": "disabled"}
	req.ExtraParams = extraParams
}

func (req *OpenAIChatRequest) stripReasoningDetailsExceptToolCalls() {
	for i := range req.Messages {
		assistantMessage := req.Messages[i].OpenAIChatAssistantMessage
		if assistantMessage == nil || len(assistantMessage.ToolCalls) > 0 {
			continue
		}
		assistantMessage.Reasoning = nil
	}
}

func (req *OpenAIChatRequest) downgradeJSONSchemaToJSONObject() {
	if req.ResponseFormat == nil {
		return
	}
	rf, ok := schemas.ParseChatResponseFormat(req.ResponseFormat)
	if !ok || rf.Type != "json_schema" {
		return
	}

	var jsonObject interface{} = json.RawMessage(`{"type":"json_object"}`)
	req.ResponseFormat = &jsonObject

	instruction := jsonSchemaInstruction(rf)

	insertAt := 0
	for insertAt < len(req.Messages) &&
		(req.Messages[insertAt].Role == schemas.ChatMessageRoleSystem || req.Messages[insertAt].Role == schemas.ChatMessageRoleDeveloper) {
		insertAt++
	}
	messages := make([]OpenAIMessage, 0, len(req.Messages)+1)
	messages = append(messages, req.Messages[:insertAt]...)
	messages = append(messages, OpenAIMessage{
		Role:    schemas.ChatMessageRoleSystem,
		Content: &schemas.ChatMessageContent{ContentStr: &instruction},
	})
	messages = append(messages, req.Messages[insertAt:]...)
	req.Messages = messages
}

func jsonSchemaInstruction(rf *schemas.ChatResponseFormat) string {
	const lead = "Respond with only a valid json object, with no surrounding text or code fences"
	schema := rf.RawSchema()
	if len(schema) == 0 {
		return lead + "."
	}
	name, _ := rf.Name()
	description, _ := rf.Description()
	var sb strings.Builder
	sb.Grow(len(lead) + len(name) + len(description) + len(schema) + 80)
	sb.WriteString(lead)
	sb.WriteString(", that conforms to the following JSON Schema")
	if name != "" {
		sb.WriteString(" (")
		sb.WriteString(name)
		sb.WriteString(")")
	}
	sb.WriteString(".")
	if description != "" {
		sb.WriteString(" ")
		sb.WriteString(description)
	}
	sb.WriteString("\n\nJSON Schema:\n")
	sb.Write(schema)
	return sb.String()
}

func (req *OpenAIChatRequest) renameAssistantReasoningToAlias() {
	for i := range req.Messages {
		assistantMessage := req.Messages[i].OpenAIChatAssistantMessage
		if assistantMessage == nil || assistantMessage.Reasoning == nil {
			continue
		}
		if assistantMessage.ReasoningAlias == nil {
			assistantMessage.ReasoningAlias = assistantMessage.Reasoning
		}
		assistantMessage.Reasoning = nil
	}
}

func (req *OpenAIChatRequest) applyXAICompatibility(caps schemas.ModelCaps) {
	model := caps.Model()
	isGrokReasoning := schemas.IsGrokReasoningModel(model)

	if caps.FieldUnsupported(schemas.FieldPresencePenalty, isGrokReasoning) {
		req.ChatParameters.PresencePenalty = nil
	}

	penaltyUnsupported := isGrokReasoning &&
		(!strings.Contains(model, "grok-3") || strings.Contains(model, "grok-3-mini"))
	if caps.FieldUnsupported(schemas.FieldFrequencyPenalty, penaltyUnsupported) {
		req.ChatParameters.FrequencyPenalty = nil
	}
	if caps.FieldUnsupported(schemas.FieldStop, penaltyUnsupported) {
		req.ChatParameters.Stop = nil
	}

	effortUnsupported := isGrokReasoning && !schemas.SupportsGrokReasoningEffort(model)
	if req.ChatParameters.Reasoning != nil &&
		caps.FieldUnsupported(schemas.FieldReasoningEffort, effortUnsupported) {
		req.ChatParameters.Reasoning.Effort = nil
	}
}

func chatUsesPromptCacheBreakpoints(caps schemas.ModelCaps, provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.OpenAI, schemas.Azure, schemas.BedrockMantle, schemas.Bedrock:
		return caps.SupportsPromptCacheBreakpoint(schemas.ModelSupportsPromptCacheBreakpoint(model))
	default:
		return false
	}
}

func clampOpenRouterCacheControl(req *OpenAIChatRequest, n int) {
	if req == nil {
		return
	}
	var buf [8]openRouterCacheRef
	refs := buf[:0]
	for i := range req.ChatParameters.Tools {
		if req.ChatParameters.Tools[i].CacheControl != nil {
			refs = append(refs, openRouterCacheRef{tool: i, msg: -1, block: -1, toolCall: -1})
		}
	}
	for i := range req.Messages {
		msg := &req.Messages[i]
		if msg.Content != nil {
			for j := range msg.Content.ContentBlocks {
				if msg.Content.ContentBlocks[j].CacheControl != nil {
					refs = append(refs, openRouterCacheRef{tool: -1, msg: i, block: j, toolCall: -1})
				}
			}
		}
		if msg.OpenAIChatAssistantMessage != nil {
			for j := range msg.OpenAIChatAssistantMessage.ToolCalls {
				if msg.OpenAIChatAssistantMessage.ToolCalls[j].CacheControl != nil {
					refs = append(refs, openRouterCacheRef{tool: -1, msg: i, block: -1, toolCall: j})
				}
			}
		}
	}
	excess := len(refs) - n
	if excess <= 0 {
		return
	}

	toolsCopied := false
	contentCopied := map[int]bool{}
	assistantCopied := map[int]bool{}
	for _, ref := range refs[:excess] {
		switch {
		case ref.tool >= 0:
			if !toolsCopied {
				req.ChatParameters.Tools = slices.Clone(req.ChatParameters.Tools)
				toolsCopied = true
			}
			req.ChatParameters.Tools[ref.tool].CacheControl = nil
			req.ChatParameters.Tools[ref.tool].InvalidateSerialized()
		case ref.block >= 0:
			msg := &req.Messages[ref.msg]
			if !contentCopied[ref.msg] {
				contentCopy := *msg.Content
				contentCopy.ContentBlocks = slices.Clone(msg.Content.ContentBlocks)
				msg.Content = &contentCopy
				contentCopied[ref.msg] = true
			}
			msg.Content.ContentBlocks[ref.block].CacheControl = nil
		case ref.toolCall >= 0:
			msg := &req.Messages[ref.msg]
			if !assistantCopied[ref.msg] {
				assistantCopy := *msg.OpenAIChatAssistantMessage
				assistantCopy.ToolCalls = slices.Clone(msg.OpenAIChatAssistantMessage.ToolCalls)
				msg.OpenAIChatAssistantMessage = &assistantCopy
				assistantCopied[ref.msg] = true
			}
			msg.OpenAIChatAssistantMessage.ToolCalls[ref.toolCall].CacheControl = nil
		}
	}
}

func applyChatCacheBreakpoints(messages []OpenAIMessage, ceiling int) []OpenAIMessage {
	type blockRef struct{ msg, block int }
	var refs []blockRef
	existing := 0
	for i := range messages {
		if messages[i].Content == nil {
			continue
		}
		for j, block := range messages[i].Content.ContentBlocks {
			if block.PromptCacheBreakpoint != nil {
				existing++
				continue
			}
			if block.CacheControl == nil || block.CacheControl.Type != schemas.CacheControlTypeEphemeral ||
				block.Type != schemas.ChatContentBlockTypeText || block.Text == nil {
				continue
			}
			refs = append(refs, blockRef{msg: i, block: j})
		}
	}
	if len(refs) == 0 {
		return messages
	}
	budget := ceiling - existing
	if budget <= 0 {
		return messages
	}
	if len(refs) > budget {
		refs = refs[len(refs)-budget:]
	}

	out := make([]OpenAIMessage, len(messages))
	copy(out, messages)
	copied := make(map[int]bool, len(refs))
	for _, ref := range refs {
		if !copied[ref.msg] {
			contentCopy := *out[ref.msg].Content
			contentCopy.ContentBlocks = append(
				make([]schemas.ChatContentBlock, 0, len(contentCopy.ContentBlocks)), contentCopy.ContentBlocks...)
			out[ref.msg].Content = &contentCopy
			copied[ref.msg] = true
		}
		out[ref.msg].Content.ContentBlocks[ref.block].PromptCacheBreakpoint = &schemas.PromptCacheBreakpoint{
			Mode: schemas.Ptr(PromptCacheBreakpointModeExplicit),
		}
	}
	return out
}
