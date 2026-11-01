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

var ProviderFeatures = map[schemas.ModelProvider]ResponsesFeatureSupport{
	schemas.OpenAI: {AdditionalToolsItem: true, ContextManagement: true, WebSearchContentTypes: true, ConfigurationUpdateItem: true},

	schemas.Bedrock:       {AdditionalToolsItem: false, ContextManagement: false},
	schemas.BedrockMantle: {AdditionalToolsItem: false, ContextManagement: false},
}
var reservedToolNamespaces = map[schemas.ModelProvider][]string{
	schemas.Bedrock:       {"web", "image_gen", "browser", "python"},
	schemas.BedrockMantle: {"web", "image_gen", "browser", "python"},
}

type ResponsesFeatureSupport struct {
	AdditionalToolsItem bool

	ContextManagement bool

	WebSearchContentTypes bool

	ConfigurationUpdateItem bool
}
type OpenAICompactionRequest struct {
	Model                string                      `json:"model"`
	Input                OpenAIResponsesRequestInput `json:"input,omitempty"`
	Instructions         *string                     `json:"instructions,omitempty"`
	PreviousResponseID   *string                     `json:"previous_response_id,omitempty"`
	PromptCacheKey       *string                     `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention *string                     `json:"prompt_cache_retention,omitempty"`
	PromptCacheOptions   *schemas.PromptCacheOptions `json:"prompt_cache_options,omitempty"`
	ServiceTier          *schemas.BifrostServiceTier `json:"service_tier,omitempty"`
	ExtraParams          map[string]interface{}      `json:"-"`
}

func resolveReservedToolNamespaces(toolProvider schemas.ModelProvider, capModel string) map[string]bool {
	names := schemas.ResolveModelCaps(toolProvider, capModel).ReservedToolNamespaces(reservedToolNamespaces[toolProvider])
	if len(names) == 0 {
		return nil
	}
	reserved := make(map[string]bool, len(names))
	for _, name := range names {
		if name != "" {
			reserved[name] = true
		}
	}
	return reserved
}

func dropReservedNamespaceTools(tools []schemas.ResponsesTool, reserved map[string]bool, substituteWebSearch bool) []schemas.ResponsesTool {
	out := make([]schemas.ResponsesTool, 0, len(tools)+1)
	droppedWeb, hasWebSearch := false, false
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeWebSearch {
			hasWebSearch = true
		}
		if tool.Type == schemas.ResponsesToolTypeNamespace && tool.Name != nil && reserved[*tool.Name] {
			if *tool.Name == "web" {
				droppedWeb = true
			}
			continue
		}
		out = append(out, tool)
	}
	if substituteWebSearch && droppedWeb && !hasWebSearch {
		out = append(out, schemas.ResponsesTool{
			Type:                   schemas.ResponsesToolTypeWebSearch,
			ResponsesToolWebSearch: &schemas.ResponsesToolWebSearch{ExternalWebAccess: new(false)},
		})
	}
	return out
}

func supportsAdditionalToolsItem(provider schemas.ModelProvider) bool {
	features, ok := ProviderFeatures[provider]
	if !ok {
		return true
	}
	return features.AdditionalToolsItem
}

func supportsConfigurationUpdateItem(ctx *schemas.BifrostContext, provider schemas.ModelProvider) bool {
	features, ok := ProviderFeatures[schemas.ResolveBaseProvider(ctx, provider)]
	if !ok {
		return true
	}
	return features.ConfigurationUpdateItem
}

func supportsWebSearchContentTypes(caps schemas.ModelCaps, provider schemas.ModelProvider) bool {
	features, ok := ProviderFeatures[provider]
	if !ok {
		return !caps.FieldUnsupported(schemas.FieldSearchContentTypes, false)
	}
	return !caps.FieldUnsupported(schemas.FieldSearchContentTypes, !features.WebSearchContentTypes)
}

func hoistAdditionalTools(message schemas.ResponsesMessage) []schemas.ResponsesTool {
	if len(message.AdditionalTools) == 0 {
		return nil
	}
	var tools []schemas.ResponsesTool
	if err := schemas.Unmarshal(message.AdditionalTools, &tools); err != nil {
		return nil
	}
	return tools
}

func normalizeAsyncTools(tools []schemas.ResponsesTool, supported bool) []schemas.ResponsesTool {
	if len(tools) == 0 {
		return tools
	}

	normalized := make([]schemas.ResponsesTool, len(tools))
	for i, tool := range tools {
		if !supported || (tool.Type != schemas.ResponsesToolTypeFunction && tool.Type != schemas.ResponsesToolTypeCustom) {
			tool.Async = nil
		}
		if tool.ResponsesToolNamespace != nil && len(tool.ResponsesToolNamespace.Tools) > 0 {
			namespaceCopy := *tool.ResponsesToolNamespace
			namespaceCopy.Tools = normalizeAsyncTools(tool.ResponsesToolNamespace.Tools, supported)
			tool.ResponsesToolNamespace = &namespaceCopy
		}
		normalized[i] = tool
	}
	return normalized
}

func normalizeAsyncCallItems(messages []schemas.ResponsesMessage, supported bool) []schemas.ResponsesMessage {
	for i := range messages {
		message := &messages[i]
		if message.ResponsesToolMessage == nil || message.ResponsesToolMessage.Async == nil {
			continue
		}
		allowedType := message.Type != nil &&
			(*message.Type == schemas.ResponsesMessageTypeFunctionCall ||
				*message.Type == schemas.ResponsesMessageTypeCustomToolCall)
		if supported && allowedType {
			continue
		}

		toolMessageCopy := *message.ResponsesToolMessage
		toolMessageCopy.Async = nil
		message.ResponsesToolMessage = &toolMessageCopy
	}
	return messages
}

const PromptCacheBreakpointModeExplicit = "explicit"

const maxResponsesCacheBreakpoints = 4

const maxImplicitModeCacheBreakpoints = 3

func promptCacheBreakpointCeiling(ctx *schemas.BifrostContext, openAIFamily bool, options *schemas.PromptCacheOptions, extraParams map[string]interface{}) int {
	if !openAIFamily || effectivePromptCacheMode(ctx, options, extraParams) == PromptCacheBreakpointModeExplicit {
		return maxResponsesCacheBreakpoints
	}
	return maxImplicitModeCacheBreakpoints
}

func effectivePromptCacheMode(ctx *schemas.BifrostContext, options *schemas.PromptCacheOptions, extraParams map[string]interface{}) string {
	if ctx != nil && ctx.Value(schemas.BifrostContextKeyPassthroughExtraParams) == true {
		switch v := extraParams["prompt_cache_options"].(type) {
		case map[string]interface{}:
			if mode, ok := v["mode"].(string); ok {
				return mode
			}
		case *schemas.PromptCacheOptions:
			if v != nil && v.Mode != nil {
				return *v.Mode
			}
		case schemas.PromptCacheOptions:
			if v.Mode != nil {
				return *v.Mode
			}
		}
	}
	if options != nil && options.Mode != nil {
		return *options.Mode
	}
	return ""
}

func responsesUsesPromptCacheBreakpoints(caps schemas.ModelCaps, provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.OpenRouter:
		return caps.SupportsPromptCacheBreakpoints(
			schemas.IsAnthropicModel(model) || schemas.ModelSupportsPromptCacheBreakpoint(model),
		)
	case schemas.OpenAI, schemas.Azure, schemas.BedrockMantle, schemas.Bedrock:
		return caps.SupportsPromptCacheBreakpoint(schemas.ModelSupportsPromptCacheBreakpoint(model))
	default:
		return false
	}
}

func isMarkableResponsesInputBlock(b schemas.ResponsesMessageContentBlock, openAIFamily bool) bool {
	switch b.Type {
	case schemas.ResponsesInputMessageContentBlockTypeText:
		return b.Text != nil
	case schemas.ResponsesInputMessageContentBlockTypeImage, schemas.ResponsesInputMessageContentBlockTypeFile:
		return openAIFamily
	}
	return false
}

func isMarkableResponsesToolOutputBlock(b schemas.ResponsesMessageContentBlock) bool {
	if b.Type == schemas.ResponsesOutputMessageContentTypeText {
		return b.Text != nil
	}
	return isMarkableResponsesInputBlock(b, true)
}

func responsesIsOpenAIFamilyBreakpointTarget(caps schemas.ModelCaps, provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.OpenAI, schemas.Azure, schemas.BedrockMantle, schemas.Bedrock:
		return caps.SupportsPromptCacheBreakpoint(schemas.ModelSupportsPromptCacheBreakpoint(model))
	default:
		return false
	}
}

func applyResponsesCacheBreakpoints(messages []schemas.ResponsesMessage, openAIFamily bool, ceiling int) {

	type blockRef struct {
		msg, block int
		toolOutput bool
	}
	var refs []blockRef
	existing := 0

	isEphemeral := func(cc *schemas.CacheControl) bool {
		return cc != nil && cc.Type == schemas.CacheControlTypeEphemeral
	}
	for i := range messages {
		if messages[i].Content != nil {
			for j, block := range messages[i].Content.ContentBlocks {
				if block.PromptCacheBreakpoint != nil {
					existing++
					continue
				}
				if !isEphemeral(block.CacheControl) || !isMarkableResponsesInputBlock(block, openAIFamily) {
					continue
				}
				refs = append(refs, blockRef{msg: i, block: j})
			}
		}
		if messages[i].Type == nil || *messages[i].Type != schemas.ResponsesMessageTypeFunctionCallOutput ||
			messages[i].ResponsesToolMessage == nil || messages[i].ResponsesToolMessage.Output == nil {
			continue
		}
		output := messages[i].ResponsesToolMessage.Output
		if blocks := output.ResponsesFunctionToolCallOutputBlocks; blocks != nil {

			marked := -1
			for j, block := range blocks {
				if block.PromptCacheBreakpoint != nil {
					existing++
					continue
				}
				if openAIFamily && isEphemeral(block.CacheControl) && isMarkableResponsesToolOutputBlock(block) {
					marked = j
				}
			}
			if marked < 0 && openAIFamily && isEphemeral(messages[i].CacheControl) {

				for j := len(blocks) - 1; j >= 0; j-- {
					if isMarkableResponsesToolOutputBlock(blocks[j]) && blocks[j].PromptCacheBreakpoint == nil {
						marked = j
						break
					}
				}
			}
			if marked >= 0 {
				refs = append(refs, blockRef{msg: i, block: marked, toolOutput: true})
			}
		} else if openAIFamily && output.ResponsesToolCallOutputStr != nil && isEphemeral(messages[i].CacheControl) {
			refs = append(refs, blockRef{msg: i, block: -1, toolOutput: true})
		}
	}
	if len(refs) == 0 {
		return
	}

	budget := ceiling - existing
	if budget <= 0 {
		return
	}

	if len(refs) > budget {
		refs = refs[len(refs)-budget:]
	}

	breakpoint := func() *schemas.PromptCacheBreakpoint {
		return &schemas.PromptCacheBreakpoint{Mode: schemas.Ptr(PromptCacheBreakpointModeExplicit)}
	}
	copiedContent := make(map[int]bool, len(refs))
	for _, ref := range refs {
		if ref.toolOutput {

			toolMsgCopy := *messages[ref.msg].ResponsesToolMessage
			outputCopy := *toolMsgCopy.Output
			if ref.block < 0 {
				text := *outputCopy.ResponsesToolCallOutputStr
				outputCopy.ResponsesToolCallOutputStr = nil
				outputCopy.ResponsesFunctionToolCallOutputBlocks = []schemas.ResponsesMessageContentBlock{{
					Type:                  schemas.ResponsesInputMessageContentBlockTypeText,
					Text:                  &text,
					PromptCacheBreakpoint: breakpoint(),
				}}
			} else {
				blocks := outputCopy.ResponsesFunctionToolCallOutputBlocks
				outputCopy.ResponsesFunctionToolCallOutputBlocks = append(
					make([]schemas.ResponsesMessageContentBlock, 0, len(blocks)), blocks...)
				outputCopy.ResponsesFunctionToolCallOutputBlocks[ref.block].PromptCacheBreakpoint = breakpoint()
			}
			toolMsgCopy.Output = &outputCopy
			messages[ref.msg].ResponsesToolMessage = &toolMsgCopy
			continue
		}
		if !copiedContent[ref.msg] {
			contentCopy := *messages[ref.msg].Content
			contentCopy.ContentBlocks = append(
				make([]schemas.ResponsesMessageContentBlock, 0, len(messages[ref.msg].Content.ContentBlocks)),
				messages[ref.msg].Content.ContentBlocks...,
			)
			messages[ref.msg].Content = &contentCopy
			copiedContent[ref.msg] = true
		}
		messages[ref.msg].Content.ContentBlocks[ref.block].PromptCacheBreakpoint = breakpoint()
	}
}

const bareTextContentBlockType schemas.ResponsesMessageContentBlockType = "text"

func isNativeWebSearchCall(message schemas.ResponsesMessage) bool {
	if message.Type == nil || *message.Type != schemas.ResponsesMessageTypeWebSearchCall ||
		message.ID == nil || message.Status == nil {
		return false
	}
	suffix, ok := strings.CutPrefix(*message.ID, "ws_")
	if !ok || suffix == "" || strings.Trim(suffix, "0123456789") == "" {
		return false
	}
	switch *message.Status {
	case "in_progress", "searching", "completed", "failed", "incomplete":
		return true
	}
	return false
}

func ToOpenAIResponsesRequest(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostResponsesRequest) *OpenAIResponsesRequest {
	if bifrostReq == nil || bifrostReq.Input == nil {
		return nil
	}

	capModel := schemas.ResolveCanonicalModel(ctx, bifrostReq.Model)
	caps := schemas.ResolveModelCaps(schemas.ResolveBaseProvider(ctx, bifrostReq.Provider), capModel)

	var messages []schemas.ResponsesMessage

	messages = make([]schemas.ResponsesMessage, 0, len(bifrostReq.Input))

	var hoistedTools []schemas.ResponsesTool

	customToolsAsFunctions := customToolsUnsupported(ctx, bifrostReq.Provider, bifrostReq.Model)
	developerAsSystem := !caps.SupportsDeveloperRole(true)
	keepAdditionalTools := supportsAdditionalToolsItem(bifrostReq.Provider) && !customToolsAsFunctions
	keepConfigurationUpdate := supportsConfigurationUpdateItem(ctx, bifrostReq.Provider)
	replayAssistantTextAsInput := isMantleGPTOSSResponses(ctx, bifrostReq.Provider, capModel)
	replayComputerActions := bifrostReq.Params != nil && slices.ContainsFunc(bifrostReq.Params.Tools, func(t schemas.ResponsesTool) bool {
		return t.Type == schemas.ResponsesToolTypeComputer
	})
	for _, message := range bifrostReq.Input {
		if !keepAdditionalTools && message.Type != nil &&
			*message.Type == schemas.ResponsesMessageTypeAdditionalTools {
			hoistedTools = append(hoistedTools, hoistAdditionalTools(message)...)
			continue
		}
		if !keepConfigurationUpdate && message.Type != nil &&
			*message.Type == schemas.ResponsesMessageTypeConfigurationUpdate {
			continue
		}

		if message.OutputConfig != nil {
			if message.IsEffortOnlySystemItem() {
				continue
			}
			message.OutputConfig = nil
		}

		if developerAsSystem && (message.Type == nil || *message.Type == schemas.ResponsesMessageTypeMessage) &&
			message.Role != nil && *message.Role == schemas.ResponsesInputMessageRoleDeveloper {
			message.Role = new(schemas.ResponsesInputMessageRoleSystem)
		}

		if message.Content != nil && len(message.Content.ContentBlocks) > 0 {
			needsRewrite := false
			for _, block := range message.Content.ContentBlocks {
				if block.Type == schemas.ResponsesOutputMessageContentTypeCompaction ||
					block.Type == schemas.ResponsesOutputMessageContentTypeFallback ||
					block.Type == schemas.ResponsesInputMessageContentBlockTypeToolAddition ||
					block.Type == schemas.ResponsesInputMessageContentBlockTypeToolRemoval {
					needsRewrite = true
					break
				}
			}

			if needsRewrite {

				newMessage := message
				newContentBlocks := make([]schemas.ResponsesMessageContentBlock, 0, len(message.Content.ContentBlocks))

				for _, block := range message.Content.ContentBlocks {
					switch block.Type {
					case schemas.ResponsesOutputMessageContentTypeCompaction:

						if block.ResponsesOutputMessageContentCompaction != nil && block.ResponsesOutputMessageContentCompaction.Summary != "" {
							newContentBlocks = append(newContentBlocks, schemas.ResponsesMessageContentBlock{
								Type: schemas.ResponsesOutputMessageContentTypeText,
								Text: schemas.Ptr(block.ResponsesOutputMessageContentCompaction.Summary),
							})
						}

					case schemas.ResponsesOutputMessageContentTypeFallback:

					case schemas.ResponsesInputMessageContentBlockTypeToolAddition,
						schemas.ResponsesInputMessageContentBlockTypeToolRemoval:

					default:

						newContentBlocks = append(newContentBlocks, block)
					}
				}

				if len(newContentBlocks) > 0 {
					newMessage.Content = &schemas.ResponsesMessageContent{
						ContentBlocks: newContentBlocks,
					}
					message = newMessage
				} else {

					continue
				}
			}
		}

		if message.Content != nil && (message.Type == nil || *message.Type == schemas.ResponsesMessageTypeMessage) {
			for _, b := range message.Content.ContentBlocks {
				if b.Type != bareTextContentBlockType {
					continue
				}
				target := schemas.ResponsesInputMessageContentBlockTypeText
				if message.Role != nil && *message.Role == schemas.ResponsesInputMessageRoleAssistant {
					target = schemas.ResponsesOutputMessageContentTypeText
				}
				contentCopy := *message.Content
				contentCopy.ContentBlocks = slices.Clone(message.Content.ContentBlocks)
				for i := range contentCopy.ContentBlocks {
					if contentCopy.ContentBlocks[i].Type == bareTextContentBlockType {
						contentCopy.ContentBlocks[i].Type = target
					}
				}
				message.Content = &contentCopy
				break
			}
		}

		message = defaultImageDetail(message)

		if replayComputerActions {
			message = computerCallActionAsActions(message)
		}

		if replayAssistantTextAsInput {
			message = assistantOutputTextAsInputText(message)
		}

		if message.ResponsesToolMessage != nil && message.ResponsesToolMessage.CallID != nil &&
			len(*message.ResponsesToolMessage.CallID) > MaxToolCallIDLength {
			if stripped := utils.StripThoughtSignature(*message.ResponsesToolMessage.CallID); stripped != *message.ResponsesToolMessage.CallID {
				toolMsgCopy := *message.ResponsesToolMessage
				toolMsgCopy.CallID = &stripped
				message.ResponsesToolMessage = &toolMsgCopy
			}
		}

		if message.Type != nil && *message.Type == schemas.ResponsesMessageTypeFunctionCall &&
			message.ID != nil && *message.ID != "" && !strings.HasPrefix(*message.ID, "fc") {
			message.ID = nil
		}

		if (message.Type != nil && *message.Type != schemas.ResponsesMessageTypeMessage) ||
			(message.Type == nil && message.ResponsesReasoning != nil) {
			message.Role = nil
		}

		switch {
		case message.Type != nil && (*message.Type == schemas.ResponsesMessageTypeApplyPatchCall ||
			*message.Type == schemas.ResponsesMessageTypeApplyPatchCallOutput):

		case message.Type != nil && *message.Type == schemas.ResponsesMessageTypeFunctionCallOutput &&
			message.Status != nil && *message.Status == "incomplete":

		case isNativeWebSearchCall(message):

		case message.Type != nil && *message.Type == schemas.ResponsesMessageTypeMessage &&
			message.Role != nil && *message.Role == schemas.ResponsesInputMessageRoleAssistant &&
			message.ID != nil && *message.ID != "" && message.Status != nil &&
			(*message.Status == "completed" || *message.Status == "in_progress" || *message.Status == "incomplete"):

		default:
			message.Status = nil
		}

		if message.Type != nil && (*message.Type == schemas.ResponsesMessageTypeReasoning ||
			*message.Type == schemas.ResponsesMessageTypeFunctionCallOutput ||
			*message.Type == schemas.ResponsesMessageTypeWebSearchCall) &&
			message.ID != nil && *message.ID != "" &&
			!(*message.Type == schemas.ResponsesMessageTypeReasoning && strings.HasPrefix(*message.ID, "rs_")) &&
			!isNativeWebSearchCall(message) {
			message.ID = nil
		}

		if message.Type != nil && *message.Type == schemas.ResponsesMessageTypeFunctionCallOutput &&
			message.ResponsesToolMessage != nil && message.ResponsesToolMessage.Name != nil {

			toolMsgCopy := *message.ResponsesToolMessage
			toolMsgCopy.Name = nil
			message.ResponsesToolMessage = &toolMsgCopy
		}

		if message.Content != nil {
			for _, b := range message.Content.ContentBlocks {
				if b.Signature == nil {
					continue
				}

				contentCopy := *message.Content
				contentCopy.ContentBlocks = slices.Clone(message.Content.ContentBlocks)
				for i := range contentCopy.ContentBlocks {
					contentCopy.ContentBlocks[i].Signature = nil
				}
				message.Content = &contentCopy
				break
			}
		}

		if message.Type != nil && *message.Type == schemas.ResponsesMessageTypeWebSearchCall &&
			message.ResponsesToolMessage != nil &&
			(message.ResponsesToolMessage.CallID != nil || message.ResponsesToolMessage.Name != nil) {
			toolMsgCopy := *message.ResponsesToolMessage
			toolMsgCopy.CallID = nil
			toolMsgCopy.Name = nil
			message.ResponsesToolMessage = &toolMsgCopy
		}

		if message.Type != nil && *message.Type == schemas.ResponsesMessageTypeFunctionCall &&
			(message.ResponsesToolMessage == nil || message.ResponsesToolMessage.Arguments == nil ||
				*message.ResponsesToolMessage.Arguments == "") {
			var toolMsgCopy schemas.ResponsesToolMessage
			if message.ResponsesToolMessage != nil {
				toolMsgCopy = *message.ResponsesToolMessage
			}
			toolMsgCopy.Arguments = schemas.Ptr("{}")
			message.ResponsesToolMessage = &toolMsgCopy
		}

		if message.Type != nil && *message.Type == schemas.ResponsesMessageTypeReasoning {
			if message.ResponsesReasoning == nil {
				message.ResponsesReasoning = &schemas.ResponsesReasoning{Summary: []schemas.ResponsesReasoningSummary{}}
			} else if message.ResponsesReasoning.Summary == nil {
				reasoningCopy := *message.ResponsesReasoning
				reasoningCopy.Summary = []schemas.ResponsesReasoningSummary{}
				message.ResponsesReasoning = &reasoningCopy
			}
		}

		if message.ResponsesReasoning != nil {
			usesContentBlocks := caps.SupportsReasoningContentBlocks(defaultSupportsReasoningContentBlocks(capModel))
			isReasoning := caps.SupportsReasoning(IsOpenAIReasoningModel(capModel))

			if len(message.ResponsesReasoning.Summary) == 0 &&
				message.Content != nil &&
				len(message.Content.ContentBlocks) > 0 &&
				!usesContentBlocks &&
				(message.ResponsesReasoning.EncryptedContent == nil || !isReasoning) {
				continue
			}

			if len(message.ResponsesReasoning.Summary) > 0 && usesContentBlocks &&
				(message.Content == nil || len(message.Content.ContentBlocks) == 0) {
				var newMessage schemas.ResponsesMessage
				newMessage.ID = message.ID
				newMessage.Type = message.Type
				newMessage.Status = message.Status
				newMessage.Role = message.Role

				contentBlocks := make([]schemas.ResponsesMessageContentBlock, 0, len(message.ResponsesReasoning.Summary))
				for _, summary := range message.ResponsesReasoning.Summary {
					contentBlocks = append(contentBlocks, schemas.ResponsesMessageContentBlock{
						Type: schemas.ResponsesOutputMessageContentTypeReasoning,
						Text: schemas.Ptr(summary.Text),
					})
				}
				newMessage.Content = &schemas.ResponsesMessageContent{
					ContentBlocks: contentBlocks,
				}
				messages = append(messages, newMessage)
			} else {

				reasoningCopy := *message.ResponsesReasoning
				message.ResponsesReasoning = &reasoningCopy

				isCompactionMessage := message.Type != nil && *message.Type == schemas.ResponsesMessageTypeCompaction
				if !isReasoning && !isCompactionMessage {
					message.ResponsesReasoning.EncryptedContent = nil
				}

				if message.Content != nil {
					switch {
					case !usesContentBlocks:

						message.Content = nil
					case message.Content.ContentStr != nil:

						if text := *message.Content.ContentStr; text == "" {
							message.Content = nil
						} else {
							message.Content = &schemas.ResponsesMessageContent{
								ContentBlocks: []schemas.ResponsesMessageContentBlock{{
									Type: schemas.ResponsesOutputMessageContentTypeReasoning,
									Text: schemas.Ptr(text),
								}},
							}
						}
					case len(message.Content.ContentBlocks) == 0:
						message.Content = nil
					}
				}
				messages = append(messages, message)
			}
		} else if message.ResponsesToolMessage != nil {
			message.ResponsesToolMessage = openAIComputerToolMessage(message.ResponsesToolMessage)
			messages = append(messages, message)
		} else {
			messages = append(messages, message)
		}
	}
	if !caps.SupportsMidConversationSystem(true) {
		messages = inlineResponsesMidConversationSystem(messages)
	}

	cachePromptProvider := schemas.ResolveBaseProvider(ctx, bifrostReq.Provider)
	if responsesUsesPromptCacheBreakpoints(caps, cachePromptProvider, capModel) {

		openAIFamily := responsesIsOpenAIFamilyBreakpointTarget(caps, cachePromptProvider, capModel)
		var callerCacheOptions *schemas.PromptCacheOptions
		var extraParams map[string]interface{}
		if bifrostReq.Params != nil {
			callerCacheOptions = bifrostReq.Params.PromptCacheOptions
			extraParams = bifrostReq.Params.ExtraParams
		}
		applyResponsesCacheBreakpoints(messages, openAIFamily, promptCacheBreakpointCeiling(ctx, openAIFamily, callerCacheOptions, extraParams))
	}

	params := bifrostReq.Params
	asyncToolsSupported := caps.SupportsAsyncTools(defaultSupportsAsyncTools(capModel))
	messages = normalizeAsyncCallItems(messages, asyncToolsSupported)
	if customToolsAsFunctions {
		messages = responsesCustomCallItemsAsFunctions(messages)
	}

	req := &OpenAIResponsesRequest{
		Model:    bifrostReq.Model,
		Provider: bifrostReq.Provider,
		Input: OpenAIResponsesRequestInput{
			OpenAIResponsesRequestInputArray: messages,
		},
	}

	if params != nil {
		req.ResponsesParameters = *params
		req.ServiceTier = serviceTierForModel(caps, req.ServiceTier)
		if req.ResponsesParameters.MaxOutputTokens != nil && *req.ResponsesParameters.MaxOutputTokens < MinMaxCompletionTokens {
			req.ResponsesParameters.MaxOutputTokens = schemas.Ptr(MinMaxCompletionTokens)
		}

		req.ResponsesParameters.User = SanitizeUserField(req.ResponsesParameters.User)

		if req.ResponsesParameters.ToolChoice.IsForced() &&
			!caps.SupportsForcedToolChoice(schemas.DefaultSupportsForcedToolChoice(capModel)) {
			req.ResponsesParameters.ToolChoice = nil
		}

		if tc := req.ResponsesParameters.ToolChoice; tc != nil && tc.ResponsesToolChoiceStr != nil &&
			*tc.ResponsesToolChoiceStr == string(schemas.ResponsesToolChoiceTypeAny) &&
			!caps.ToolChoiceAnySupported(toolChoiceAnySupported(bifrostReq.Provider, capModel)) {
			req.ResponsesParameters.ToolChoice = &schemas.ResponsesToolChoice{
				ResponsesToolChoiceStr: schemas.Ptr(string(schemas.ResponsesToolChoiceTypeRequired)),
			}
		}

		if req.ResponsesParameters.Reasoning != nil {

			reasoningCopy := *req.ResponsesParameters.Reasoning
			req.ResponsesParameters.Reasoning = &reasoningCopy
			if req.ResponsesParameters.Reasoning.Effort != nil {

				effort := *req.ResponsesParameters.Reasoning.Effort
				req.ResponsesParameters.Reasoning.Effort = schemas.Ptr(normalizeReasoningEffort(req.Provider, caps, effort))

				req.ResponsesParameters.Reasoning.MaxTokens = nil
			} else if req.ResponsesParameters.Reasoning.MaxTokens != nil {

				maxTokens := *req.ResponsesParameters.Reasoning.MaxTokens
				maxOutputTokens := utils.GetMaxOutputTokensOrDefault(req.Provider, capModel, DefaultCompletionMaxTokens)
				if req.ResponsesParameters.MaxOutputTokens != nil {
					maxOutputTokens = *req.ResponsesParameters.MaxOutputTokens
				}
				effort := utils.GetReasoningEffortFromBudgetTokens(maxTokens, MinReasoningMaxTokens, maxOutputTokens)
				req.ResponsesParameters.Reasoning.Effort = schemas.Ptr(effort)

				req.ResponsesParameters.Reasoning.MaxTokens = nil
			}

			if e := req.ResponsesParameters.Reasoning.Effort; e != nil && *e == schemas.ReasoningEffortNone &&
				!caps.CanDisableReasoning(defaultCanDisableReasoning(capModel)) {
				req.ResponsesParameters.Reasoning.Effort = schemas.Ptr(caps.NormalizeReasoningEffort(schemas.ReasoningEffortMinimal, defaultEffortControl(req.Provider, capModel)))
			}

			if req.ResponsesParameters.Reasoning.Summary != nil && *req.ResponsesParameters.Reasoning.Summary == "none" {
				req.ResponsesParameters.Reasoning.Summary = nil
			}

			contextProvider := schemas.ResolveBaseProvider(ctx, bifrostReq.Provider)
			if c := req.ResponsesParameters.Reasoning.Context; c != nil &&
				(contextProvider == schemas.OpenAI || contextProvider == schemas.Azure) &&
				!slices.Contains(caps.SupportedReasoningContexts(defaultReasoningContexts(capModel)), *c) {
				req.ResponsesParameters.Reasoning.Context = nil
			}

			if summary := req.ResponsesParameters.Reasoning.Summary; summary != nil && *summary != "auto" {
				switch schemas.ResolveBaseProvider(ctx, bifrostReq.Provider) {
				case schemas.Bedrock, schemas.BedrockMantle:
					req.ResponsesParameters.Reasoning.Summary = schemas.Ptr("auto")
				}
			}

			if bifrostReq.Provider == schemas.XAI &&
				caps.FieldUnsupported(schemas.FieldReasoningEffort,
					schemas.IsGrokReasoningModel(capModel) && !schemas.SupportsGrokReasoningEffort(capModel)) {
				req.ResponsesParameters.Reasoning.Effort = nil
			}

			if (bifrostReq.Provider == schemas.OpenAI || bifrostReq.Provider == schemas.Azure) && !caps.SupportsReasoning(IsOpenAIReasoningModel(capModel)) {

				req.ResponsesParameters.Reasoning = nil
			}
		}

		effort := ""
		if req.ResponsesParameters.Reasoning != nil &&
			req.ResponsesParameters.Reasoning.Effort != nil {
			effort = *req.ResponsesParameters.Reasoning.Effort
		}
		if samplingParamUnsupported(caps, schemas.FieldTopP, capModel, effort) {
			req.ResponsesParameters.TopP = nil
		}

		if base := schemas.ResolveBaseProvider(ctx, bifrostReq.Provider); base == schemas.OpenAI || base == schemas.Azure {
			if samplingParamUnsupported(caps, schemas.FieldTemperature, capModel, effort) {
				req.ResponsesParameters.Temperature = nil
			}
			if samplingParamUnsupported(caps, schemas.FieldTopLogprobs, capModel, effort) {
				req.ResponsesParameters.TopLogProbs = nil
			}
			if samplingParamUnsupported(caps, schemas.FieldLogprobs, capModel, effort) &&
				slices.Contains(req.ResponsesParameters.Include, "message.output_text.logprobs") {

				req.ResponsesParameters.Include = slices.DeleteFunc(slices.Clone(req.ResponsesParameters.Include), func(s string) bool {
					return s == "message.output_text.logprobs"
				})
			}

			if !caps.SupportsReasoning(IsOpenAIReasoningModel(capModel)) &&
				slices.Contains(req.ResponsesParameters.Include, "reasoning.encrypted_content") {
				req.ResponsesParameters.Include = slices.DeleteFunc(slices.Clone(req.ResponsesParameters.Include), func(s string) bool {
					return s == "reasoning.encrypted_content"
				})
			}
		}
	}

	if len(hoistedTools) > 0 {
		req.Tools = append(append(make([]schemas.ResponsesTool, 0, len(req.Tools)+len(hoistedTools)), req.Tools...), hoistedTools...)
	}

	toolProvider := schemas.ResolveBaseProvider(ctx, bifrostReq.Provider)
	if reserved := resolveReservedToolNamespaces(toolProvider, capModel); len(reserved) > 0 && len(req.Tools) > 0 {
		substitute := toolProvider == schemas.BedrockMantle && caps.SupportsWebSearch(true)
		req.Tools = dropReservedNamespaceTools(req.Tools, reserved, substitute)
	}

	if customToolsAsFunctions {
		req.Tools = responsesCustomToolsAsFunctions(req.Tools)
		req.ToolChoice = responsesCustomToolChoiceAsFunction(req.ToolChoice)
	}

	if len(req.Tools) > 0 {
		normalizedTools := normalizeAsyncTools(req.Tools, asyncToolsSupported)
		for i, tool := range normalizedTools {
			if tool.Type == schemas.ResponsesToolTypeFunction &&
				tool.ResponsesToolFunction != nil {
				funcCopy := *tool.ResponsesToolFunction
				if funcCopy.Parameters != nil {
					funcCopy.Parameters = funcCopy.Parameters.Normalized()
				}
				if funcCopy.Strict == nil {
					funcCopy.Strict = new(false)
				}
				normalizedTools[i].ResponsesToolFunction = &funcCopy
			}
		}
		req.Tools = normalizedTools
	}

	toolCaps := schemas.ResolveModelCaps(toolProvider, capModel)
	req.filterUnsupportedTools(supportsWebSearchContentTypes(toolCaps, toolProvider), toolProvider)
	req.provisionToolSearch(toolCaps.SupportsToolSearch(defaultSupportsToolSearch(toolProvider, capModel)))

	if bifrostReq.Params != nil {
		req.ExtraParams = bifrostReq.Params.ExtraParams
	}

	if ctx != nil && ctx.Value(schemas.BifrostContextKeyDisableStore) == true {
		req.Store = new(false)
		if _, ok := req.ExtraParams["store"]; ok {
			req.ExtraParams = maps.Clone(req.ExtraParams)
			req.ExtraParams["store"] = false
		}
	}

	if features, ok := ProviderFeatures[bifrostReq.Provider]; ok && !features.ContextManagement {
		req.ContextManagement = nil
		delete(req.ExtraParams, "context_management")
	}

	return req
}

func samplingParamUnsupported(caps schemas.ModelCaps, field, model, effort string) bool {
	effortIsNone := effort == schemas.ReasoningEffortNone || (effort == "" && !omittedEffortReasons(caps, model))

	if caps.FieldUnsupported(field, false) {
		return true
	}
	if caps.FieldCondition(field) == schemas.ConditionWhenEffortNone {
		return !effortIsNone
	}

	fallback := true
	if !IsOpenAIReasoningModel(model) {
		fallback = false
	} else {
		_, parsedModel := schemas.ParseModelString(model, schemas.OpenAI)
		modelLower := strings.ToLower(parsedModel)
		if strings.Contains(modelLower, "gpt-5.") && effortIsNone &&
			!strings.Contains(modelLower, "-pro") {
			fallback = false
		}
	}
	return caps.FieldUnsupported(field, fallback)
}

func defaultImageDetail(message schemas.ResponsesMessage) schemas.ResponsesMessage {
	if message.Content != nil {
		if blocks, changed := withDefaultImageDetail(message.Content.ContentBlocks); changed {
			contentCopy := *message.Content
			contentCopy.ContentBlocks = blocks
			message.Content = &contentCopy
		}
	}
	if message.ResponsesToolMessage != nil && message.ResponsesToolMessage.Output != nil {
		if blocks, changed := withDefaultImageDetail(message.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks); changed {
			outputCopy := *message.ResponsesToolMessage.Output
			outputCopy.ResponsesFunctionToolCallOutputBlocks = blocks
			toolMsgCopy := *message.ResponsesToolMessage
			toolMsgCopy.Output = &outputCopy
			message.ResponsesToolMessage = &toolMsgCopy
		}
	}
	return message
}

func withDefaultImageDetail(blocks []schemas.ResponsesMessageContentBlock) ([]schemas.ResponsesMessageContentBlock, bool) {
	needsDetail := func(block schemas.ResponsesMessageContentBlock) bool {
		return block.Type == schemas.ResponsesInputMessageContentBlockTypeImage &&
			block.ResponsesInputMessageContentBlockImage != nil &&
			block.ResponsesInputMessageContentBlockImage.Detail == nil
	}
	if !slices.ContainsFunc(blocks, needsDetail) {
		return blocks, false
	}

	newBlocks := slices.Clone(blocks)
	for i, block := range newBlocks {
		if needsDetail(block) {
			imageCopy := *block.ResponsesInputMessageContentBlockImage
			imageCopy.Detail = schemas.Ptr("auto")
			newBlocks[i].ResponsesInputMessageContentBlockImage = &imageCopy
		}
	}
	return newBlocks, true
}

func isMantleGPTOSSResponses(ctx *schemas.BifrostContext, provider schemas.ModelProvider, capModel string) bool {
	base := schemas.ResolveBaseProvider(ctx, provider)
	return (base == schemas.Bedrock || base == schemas.BedrockMantle) &&
		strings.Contains(strings.ToLower(capModel), "gpt-oss") &&
		schemas.ResolveBedrockMantleBasePath(capModel) == schemas.BedrockMantleBasePathV1
}

func openAIComputerAction(action schemas.ResponsesComputerToolCallAction) (schemas.ResponsesComputerToolCallAction, bool) {
	if action.Type != "zoom" && action.Region == nil {
		return action, false
	}
	action.Region = nil
	if action.Type == "zoom" {
		action.Type = "screenshot"
	}
	return action, true
}

func openAIComputerToolMessage(toolMsg *schemas.ResponsesToolMessage) *schemas.ResponsesToolMessage {
	var out *schemas.ResponsesToolMessage
	own := func() *schemas.ResponsesToolMessage {
		if out == nil {
			c := *toolMsg
			out = &c
		}
		return out
	}
	if toolMsg.Action != nil && toolMsg.Action.ResponsesComputerToolCallAction != nil {
		if action, changed := openAIComputerAction(*toolMsg.Action.ResponsesComputerToolCallAction); changed {
			actionStruct := *toolMsg.Action
			actionStruct.ResponsesComputerToolCallAction = &action
			own().Action = &actionStruct
		}
	}
	if toolMsg.ResponsesComputerToolCall != nil {
		var actions []schemas.ResponsesComputerToolCallAction
		for i, entry := range toolMsg.ResponsesComputerToolCall.Actions {
			if cleaned, changed := openAIComputerAction(entry); changed {
				if actions == nil {
					actions = slices.Clone(toolMsg.ResponsesComputerToolCall.Actions)
				}
				actions[i] = cleaned
			}
		}
		if actions != nil {
			call := *toolMsg.ResponsesComputerToolCall
			call.Actions = actions
			own().ResponsesComputerToolCall = &call
		}
	}
	if out == nil {
		return toolMsg
	}
	return out
}

func computerCallActionAsActions(message schemas.ResponsesMessage) schemas.ResponsesMessage {
	if message.Type == nil || *message.Type != schemas.ResponsesMessageTypeComputerCall || message.ResponsesToolMessage == nil {
		return message
	}
	toolMsg := *message.ResponsesToolMessage
	if toolMsg.Action == nil || toolMsg.Action.ResponsesComputerToolCallAction == nil {
		return message
	}
	call := schemas.ResponsesComputerToolCall{}
	if toolMsg.ResponsesComputerToolCall != nil {
		call = *toolMsg.ResponsesComputerToolCall
	}
	if len(call.Actions) == 0 {
		call.Actions = []schemas.ResponsesComputerToolCallAction{*toolMsg.Action.ResponsesComputerToolCallAction}
	}
	toolMsg.ResponsesComputerToolCall = &call
	toolMsg.Action = nil
	message.ResponsesToolMessage = &toolMsg
	return message
}

func assistantOutputTextAsInputText(message schemas.ResponsesMessage) schemas.ResponsesMessage {
	if message.Role == nil || *message.Role != schemas.ResponsesInputMessageRoleAssistant ||
		message.Content == nil || len(message.Content.ContentBlocks) == 0 {
		return message
	}
	fixNeeded := false
	for _, block := range message.Content.ContentBlocks {
		if block.Type == schemas.ResponsesOutputMessageContentTypeText {
			fixNeeded = true
			break
		}
	}
	if !fixNeeded {
		return message
	}

	newBlocks := make([]schemas.ResponsesMessageContentBlock, len(message.Content.ContentBlocks))
	copy(newBlocks, message.Content.ContentBlocks)
	for i := range newBlocks {
		if newBlocks[i].Type == schemas.ResponsesOutputMessageContentTypeText {
			newBlocks[i].Type = schemas.ResponsesInputMessageContentBlockTypeText
			newBlocks[i].ResponsesOutputMessageContentText = nil
		}
	}

	contentCopy := *message.Content
	contentCopy.ContentBlocks = newBlocks
	message.Content = &contentCopy
	return message
}

func isOpenAISupportedToolType(t schemas.ResponsesToolType, provider schemas.ModelProvider) bool {
	switch t {
	case schemas.ResponsesToolTypeFunction,
		schemas.ResponsesToolTypeFileSearch,
		schemas.ResponsesToolTypeComputer,
		schemas.ResponsesToolTypeWebSearch,
		schemas.ResponsesToolTypeWebFetch,
		schemas.ResponsesToolTypeMCP,
		schemas.ResponsesToolTypeApplyPatch,
		schemas.ResponsesToolTypeCustom,
		schemas.ResponsesToolTypeWebSearchPreview,
		schemas.ResponsesToolTypeMemory,
		schemas.ResponsesToolTypeToolSearch,
		schemas.ResponsesToolTypeNamespace:
		return true
	case schemas.ResponsesToolTypeShell,
		schemas.ResponsesToolTypeLocalShell,
		schemas.ResponsesToolTypeCodeInterpreter,
		schemas.ResponsesToolTypeImageGeneration,
		schemas.ResponsesToolTypeComputerUsePreview,
		schemas.ResponsesToolTypeProgrammaticToolCalling:
		return provider != schemas.BedrockMantle && provider != schemas.Bedrock
	case schemas.ResponsesToolTypeXSearch:
		return provider == schemas.XAI
	}
	return false
}

func (resp *OpenAIResponsesRequest) filterUnsupportedTools(webSearchContentTypesSupported bool, baseProvider schemas.ModelProvider) {
	if len(resp.Tools) == 0 {
		return
	}

	filteredTools := make([]schemas.ResponsesTool, 0, len(resp.Tools))
	for i := range resp.Tools {
		tool := &resp.Tools[i]

		isOpenRouterServerTool := resp.Provider == schemas.OpenRouter &&
			strings.HasPrefix(string(tool.Type), schemas.ResponsesToolTypeOpenRouterPrefix)
		if isOpenAISupportedToolType(tool.Type, baseProvider) || isOpenRouterServerTool {

			if tool.Type == schemas.ResponsesToolTypeComputerUsePreview && tool.ResponsesToolComputerUsePreview != nil && tool.ResponsesToolComputerUsePreview.EnableZoom != nil {
				newTool := *tool
				newComputerUse := &schemas.ResponsesToolComputerUsePreview{
					DisplayHeight: tool.ResponsesToolComputerUsePreview.DisplayHeight,
					DisplayWidth:  tool.ResponsesToolComputerUsePreview.DisplayWidth,
					Environment:   tool.ResponsesToolComputerUsePreview.Environment,
				}
				newTool.ResponsesToolComputerUsePreview = newComputerUse
				filteredTools = append(filteredTools, newTool)
			} else if tool.Type == schemas.ResponsesToolTypeWebSearch && tool.ResponsesToolWebSearch != nil {

				newTool := *tool
				newWebSearch := &schemas.ResponsesToolWebSearch{}

				if tool.ResponsesToolWebSearch.Filters != nil {
					hasAllowedDomains := len(tool.ResponsesToolWebSearch.Filters.AllowedDomains) > 0

					if hasAllowedDomains {

						newWebSearch.Filters = &schemas.ResponsesToolWebSearchFilters{
							AllowedDomains: append([]string(nil), tool.ResponsesToolWebSearch.Filters.AllowedDomains...),
						}
					}

				}

				if tool.ResponsesToolWebSearch.ExternalWebAccess != nil {
					externalWebAccess := *tool.ResponsesToolWebSearch.ExternalWebAccess
					newWebSearch.ExternalWebAccess = &externalWebAccess
				}
				if webSearchContentTypesSupported && len(tool.ResponsesToolWebSearch.SearchContentTypes) > 0 {
					newWebSearch.SearchContentTypes = append([]string(nil), tool.ResponsesToolWebSearch.SearchContentTypes...)
				}

				if tool.ResponsesToolWebSearch.UserLocation != nil {
					newWebSearch.UserLocation = tool.ResponsesToolWebSearch.UserLocation
				}
				if tool.ResponsesToolWebSearch.SearchContextSize != nil {
					newWebSearch.SearchContextSize = tool.ResponsesToolWebSearch.SearchContextSize
				}

				newTool.ResponsesToolWebSearch = newWebSearch
				filteredTools = append(filteredTools, newTool)
			} else {
				filteredTools = append(filteredTools, *tool)
			}
		}
	}
	resp.Tools = filteredTools

	if len(resp.Tools) == 0 {
		resp.ToolChoice = nil
	}
}

func (r *OpenAICompactionRequest) GetExtraParams() map[string]interface{} { return r.ExtraParams }

func (r *OpenAICompactionRequest) MarshalJSON() ([]byte, error) {
	type Alias OpenAICompactionRequest
	var input json.RawMessage
	if r.Input.OpenAIResponsesRequestInputStr != nil || r.Input.OpenAIResponsesRequestInputArray != nil {
		b, err := r.Input.MarshalJSON()
		if err != nil {
			return nil, err
		}
		input = json.RawMessage(b)
	}
	return utils.MarshalSorted(struct {
		*Alias
		Input json.RawMessage `json:"input,omitempty"`
	}{Alias: (*Alias)(r), Input: input})
}

func ToOpenAICompactionRequest(ctx *schemas.BifrostContext, req *schemas.BifrostCompactionRequest) *OpenAICompactionRequest {
	if req == nil {
		return nil
	}
	r := &OpenAICompactionRequest{
		Model:                req.Model,
		Instructions:         req.Instructions,
		PreviousResponseID:   req.PreviousResponseID,
		PromptCacheKey:       req.PromptCacheKey,
		PromptCacheRetention: req.PromptCacheRetention,
		PromptCacheOptions:   req.PromptCacheOptions,
		ServiceTier:          req.ServiceTier,
		ExtraParams:          req.ExtraParams,
	}
	if len(req.Input) > 0 {

		normalized := ToOpenAIResponsesRequest(ctx, &schemas.BifrostResponsesRequest{
			Provider: req.Provider,
			Model:    req.Model,
			Input:    req.Input,
		})
		if normalized != nil {
			r.Input = normalized.Input
		}
	}
	return r
}
