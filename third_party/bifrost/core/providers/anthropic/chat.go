// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type AnthropicStreamState struct {
	nextToolCallIndex         int
	contentBlockToToolCallIdx map[int]int

	sawArgsDelta map[int]bool

	reasoningDetailIdxByBlock map[int]int
	nextReasoningDetailIdx    int

	contentLen int

	textBlockStart map[int]int

	pendingCitations map[int][]AnthropicTextCitation
}
type anthropicAnnotationKey struct {
	url        string
	start, end int
}

const documentPlaceholderText = "."

func hasAnthropicDocumentBlock(blocks []AnthropicContentBlock) bool {
	for _, b := range blocks {
		if b.Type == AnthropicContentBlockTypeDocument {
			return true
		}
	}
	return false
}

func leadingAnthropicReasoningBlockCount(blocks []AnthropicContentBlock) int {
	count := 0
	for _, b := range blocks {
		if b.Type != AnthropicContentBlockTypeThinking && b.Type != AnthropicContentBlockTypeRedactedThinking {
			break
		}
		count++
	}
	return count
}

func convertFunctionToolToAnthropic(tool schemas.ChatTool) (AnthropicTool, error) {
	anthropicTool := AnthropicTool{
		Name: tool.Function.Name,
	}
	if tool.Function.Description != nil {
		anthropicTool.Description = tool.Function.Description
	}

	if tool.Function.Parameters != nil && (tool.Function.Parameters.Type != "" || tool.Function.Parameters.Properties != nil) {
		anthropicTool.InputSchema = schemas.DeepCopyToolFunctionParameters(tool.Function.Parameters)
	}

	if anthropicTool.InputSchema != nil {
		var err error
		anthropicTool.InputSchema, err = NormalizeAnthropicToolInputSchema(anthropicTool.InputSchema)
		if err != nil {
			return AnthropicTool{}, err
		}
		anthropicTool.InputSchema = anthropicTool.InputSchema.Normalized()
	}

	if tool.CacheControl != nil {
		anthropicTool.CacheControl = tool.CacheControl
	}
	if tool.DeferLoading != nil {
		anthropicTool.DeferLoading = tool.DeferLoading
	}
	if len(tool.AllowedCallers) > 0 {
		anthropicTool.AllowedCallers = tool.AllowedCallers
	}
	if len(tool.InputExamples) > 0 {
		anthropicTool.InputExamples = make([]AnthropicToolInputExample, len(tool.InputExamples))
		for i, ex := range tool.InputExamples {
			anthropicTool.InputExamples[i] = AnthropicToolInputExample{
				Input:       ex.Input,
				Description: ex.Description,
			}
		}
	}
	if tool.EagerInputStreaming != nil {
		anthropicTool.EagerInputStreaming = tool.EagerInputStreaming
	}

	if tool.Function.Strict != nil {
		anthropicTool.Strict = tool.Function.Strict
	}
	return anthropicTool, nil
}

func convertServerToolToAnthropic(tool schemas.ChatTool, caps schemas.ModelCaps, provider schemas.ModelProvider) (AnthropicTool, bool) {
	typeStr := string(tool.Type)
	if typeStr == "" {
		return AnthropicTool{}, false
	}

	if typeStr == "mcp_toolset" {
		if tool.MCPServerName == "" {
			return AnthropicTool{}, false
		}
		toolset := &AnthropicMCPToolsetTool{
			Type:          "mcp_toolset",
			MCPServerName: tool.MCPServerName,
			DefaultConfig: convertMCPToolsetConfig(tool.DefaultConfig),
			Configs:       convertMCPToolsetConfigMap(tool.Configs),
			CacheControl:  tool.CacheControl,
		}
		return AnthropicTool{MCPToolset: toolset}, true
	}

	toolName := tool.Name

	if baseTool := computerUseBaseTool(typeStr); baseTool != "" {
		generation := ComputerUseGeneration(caps)
		if baseTool == "text_editor" {
			generation = TextEditorGeneration(caps)
		}
		if wantType, wantName := NormalizedToolSpec(generation, baseTool); wantType != "" {
			typeStr = wantType
			if toolName == "" || toolName != wantName {
				toolName = wantName
			}

			if wantName == "" {
				atype := AnthropicToolType(typeStr)
				return AnthropicTool{Type: &atype, CacheControl: tool.CacheControl}, true
			}
		}
	}
	if toolName == "" {
		return AnthropicTool{}, false
	}

	atype := AnthropicToolType(typeStr)
	anthropicTool := AnthropicTool{
		Name:                toolName,
		Type:                &atype,
		CacheControl:        tool.CacheControl,
		DeferLoading:        tool.DeferLoading,
		AllowedCallers:      tool.AllowedCallers,
		EagerInputStreaming: tool.EagerInputStreaming,
	}
	if len(tool.InputExamples) > 0 {
		anthropicTool.InputExamples = make([]AnthropicToolInputExample, len(tool.InputExamples))
		for i, ex := range tool.InputExamples {
			anthropicTool.InputExamples[i] = AnthropicToolInputExample{
				Input:       ex.Input,
				Description: ex.Description,
			}
		}
	}

	switch {
	case strings.HasPrefix(typeStr, "web_search_"):
		anthropicTool.AnthropicToolWebSearch = &AnthropicToolWebSearch{
			MaxUses:        tool.MaxUses,
			AllowedDomains: tool.AllowedDomains,
			BlockedDomains: tool.BlockedDomains,
			UserLocation:   convertUserLocation(tool.UserLocation),
		}
	case strings.HasPrefix(typeStr, "web_fetch_"):
		anthropicTool.AnthropicToolWebFetch = &AnthropicToolWebFetch{
			MaxUses:          tool.MaxUses,
			AllowedDomains:   tool.AllowedDomains,
			BlockedDomains:   tool.BlockedDomains,
			MaxContentTokens: tool.MaxContentTokens,
			Citations:        convertCitationsConfig(tool.Citations),
			UseCache:         tool.UseCache,
		}
	case strings.HasPrefix(typeStr, "computer_"):
		anthropicTool.AnthropicToolComputerUse = &AnthropicToolComputerUse{
			DisplayWidthPx:  tool.DisplayWidthPx,
			DisplayHeightPx: tool.DisplayHeightPx,
			DisplayNumber:   tool.DisplayNumber,
			EnableZoom:      tool.EnableZoom,
		}
	case strings.HasPrefix(typeStr, "text_editor_"):
		anthropicTool.AnthropicToolTextEditor = &AnthropicToolTextEditor{
			MaxCharacters: tool.MaxCharacters,
		}
	case strings.HasPrefix(typeStr, "bash_"),
		strings.HasPrefix(typeStr, "memory_"),
		strings.HasPrefix(typeStr, "code_execution_"),
		strings.HasPrefix(typeStr, "tool_search_tool_"):

	default:

	}
	return anthropicTool, true
}

func convertUserLocation(loc *schemas.ChatToolUserLocation) *AnthropicToolWebSearchUserLocation {
	if loc == nil {
		return nil
	}
	return &AnthropicToolWebSearchUserLocation{
		Type:     loc.Type,
		City:     loc.City,
		Region:   loc.Region,
		Country:  loc.Country,
		Timezone: loc.Timezone,
	}
}

func convertCitationsConfig(c *schemas.ChatToolCitationsConfig) *AnthropicCitations {
	if c == nil {
		return nil
	}
	return &AnthropicCitations{Config: &schemas.Citations{Enabled: c.Enabled}}
}

func convertMCPToolsetConfig(c *schemas.ChatMCPToolsetConfig) *AnthropicMCPToolsetConfig {
	if c == nil {
		return nil
	}
	return &AnthropicMCPToolsetConfig{
		Enabled:      c.Enabled,
		DeferLoading: c.DeferLoading,
	}
}

func convertMCPToolsetConfigMap(m map[string]*schemas.ChatMCPToolsetConfig) map[string]*AnthropicMCPToolsetConfig {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]*AnthropicMCPToolsetConfig, len(m))
	for k, v := range m {
		out[k] = convertMCPToolsetConfig(v)
	}
	return out
}

func promoteThinkingFromExtraParams(value interface{}) (*schemas.ChatReasoning, *AnthropicThinking) {
	if value == nil {
		return nil, nil
	}

	var thinking AnthropicThinking
	switch v := value.(type) {
	case *AnthropicThinking:
		if v == nil {
			return nil, nil
		}
		thinking = *v
	case AnthropicThinking:
		thinking = v
	default:
		data, err := providerUtils.MarshalSorted(value)
		if err != nil {
			return nil, nil
		}
		if err := sonic.Unmarshal(data, &thinking); err != nil {
			return nil, nil
		}
	}

	switch thinking.Type {
	case "adaptive":
		return nil, &AnthropicThinking{Type: "adaptive", Display: thinking.Display}
	case "enabled":
		if thinking.BudgetTokens == nil {

			return nil, &AnthropicThinking{Type: "enabled", Display: thinking.Display}
		}
		return &schemas.ChatReasoning{MaxTokens: thinking.BudgetTokens, Display: thinking.Display}, nil
	case "disabled":

		return &schemas.ChatReasoning{Effort: schemas.Ptr("none")}, nil
	case "between_tools":
		return &schemas.ChatReasoning{Type: schemas.Ptr("between_tools")}, nil
	default:
		return nil, nil
	}
}

func ToAnthropicChatRequest(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostChatRequest) (*AnthropicMessageRequest, error) {
	if bifrostReq == nil || bifrostReq.Input == nil {
		return nil, fmt.Errorf("bifrost request is nil or input is nil")
	}

	messages := bifrostReq.Input
	if ctx.Value(schemas.BifrostContextKeySupportsAssistantPrefill) == false {
		trimmed := len(messages)
		for trimmed > 0 && messages[trimmed-1].Role == schemas.ChatMessageRoleAssistant {
			trimmed--
		}
		messages = messages[:trimmed]
	}

	anthropicReq := &AnthropicMessageRequest{
		Model:     bifrostReq.Model,
		MaxTokens: providerUtils.GetMaxOutputTokensOrDefault(bifrostReq.Provider, bifrostReq.Model, AnthropicDefaultMaxTokens),
	}

	capModel := schemas.ResolveCanonicalModel(ctx, bifrostReq.Model)
	caps := schemas.ResolveModelCaps(bifrostReq.Provider, capModel)

	forcedToolChoiceSupported := caps.SupportsForcedToolChoice(schemas.DefaultSupportsForcedToolChoice(capModel))

	if bifrostReq.Params != nil {
		anthropicReq.ExtraParams = bifrostReq.Params.ExtraParams
		if safeguards, exists := anthropicReq.ExtraParams["safeguards"]; exists {

			extra := make(map[string]interface{}, len(anthropicReq.ExtraParams))
			for k, v := range anthropicReq.ExtraParams {
				extra[k] = v
			}
			anthropicReq.ExtraParams = extra
			delete(anthropicReq.ExtraParams, "safeguards")
			switch v := safeguards.(type) {
			case json.RawMessage:
				anthropicReq.Safeguards = v
			case []byte:
				anthropicReq.Safeguards = json.RawMessage(v)
			default:
				if data, err := providerUtils.MarshalSorted(v); err == nil {
					anthropicReq.Safeguards = data
				}
			}
		}

		reasoningParams := bifrostReq.Params.Reasoning
		var promotedThinking *AnthropicThinking
		if reasoningParams == nil {
			if passthrough, ok := ctx.Value(schemas.BifrostContextKeyPassthroughExtraParams).(bool); !ok || !passthrough {
				reasoningParams, promotedThinking = promoteThinkingFromExtraParams(bifrostReq.Params.ExtraParams["thinking"])
			}
		}

		if bifrostReq.Params.MaxCompletionTokens != nil {
			anthropicReq.MaxTokens = clampToModelOutputCeiling(caps, *bifrostReq.Params.MaxCompletionTokens)
		}

		if !caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {

			if bifrostReq.Params.Temperature != nil {
				anthropicReq.Temperature = bifrostReq.Params.Temperature
			} else if bifrostReq.Params.TopP != nil {
				anthropicReq.TopP = bifrostReq.Params.TopP
			}
		}
		anthropicReq.StopSequences = bifrostReq.Params.Stop

		if bifrostReq.Params.TopK != nil {
			if !caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {
				anthropicReq.TopK = bifrostReq.Params.TopK
			}
		} else if topK, ok := schemas.SafeExtractIntPointer(bifrostReq.Params.ExtraParams["top_k"]); ok {
			delete(anthropicReq.ExtraParams, "top_k")
			if !caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {
				anthropicReq.TopK = topK
			}
		}

		if bifrostReq.Params.Speed != nil {
			anthropicReq.Speed = bifrostReq.Params.Speed
		} else if speed, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["speed"]); ok {
			delete(anthropicReq.ExtraParams, "speed")
			anthropicReq.Speed = speed
		}

		if bifrostReq.Params.InferenceGeo != nil {
			anthropicReq.InferenceGeo = bifrostReq.Params.InferenceGeo
		} else if inferenceGeo, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["inference_geo"]); ok {
			delete(anthropicReq.ExtraParams, "inference_geo")
			anthropicReq.InferenceGeo = inferenceGeo
		}

		if len(bifrostReq.Params.ContextManagement) > 0 {
			var cm ContextManagement
			if err := sonic.Unmarshal(bifrostReq.Params.ContextManagement, &cm); err != nil {
				return nil, fmt.Errorf("context_management: failed to parse: %w", err)
			}
			anthropicReq.ContextManagement = &cm
		} else if cmVal := bifrostReq.Params.ExtraParams["context_management"]; cmVal != nil {
			if cm, ok := cmVal.(*ContextManagement); ok && cm != nil {
				delete(anthropicReq.ExtraParams, "context_management")
				anthropicReq.ContextManagement = cm
			} else if data, err := providerUtils.MarshalSorted(cmVal); err == nil {
				var cm ContextManagement
				if sonic.Unmarshal(data, &cm) == nil {
					delete(anthropicReq.ExtraParams, "context_management")
					anthropicReq.ContextManagement = &cm
				}
			}
		}

		if bifrostReq.Params.Container != nil {
			c := &AnthropicContainer{}
			if bifrostReq.Params.Container.ContainerStr != nil {
				c.ContainerStr = bifrostReq.Params.Container.ContainerStr
			} else if bifrostReq.Params.Container.ContainerObject != nil {
				obj := &AnthropicContainerObject{
					ID: bifrostReq.Params.Container.ContainerObject.ID,
				}
				if len(bifrostReq.Params.Container.ContainerObject.Skills) > 0 {
					obj.Skills = make([]AnthropicContainerSkill, len(bifrostReq.Params.Container.ContainerObject.Skills))
					for i, sk := range bifrostReq.Params.Container.ContainerObject.Skills {
						obj.Skills[i] = AnthropicContainerSkill{
							SkillID: sk.SkillID,
							Type:    sk.Type,
							Version: sk.Version,
						}
					}
				}
				c.ContainerObject = obj
			}
			anthropicReq.Container = c
		}

		if bifrostReq.Params.CacheControl != nil {
			anthropicReq.CacheControl = bifrostReq.Params.CacheControl
		}

		if dVal := bifrostReq.Params.ExtraParams["diagnostics"]; dVal != nil {
			parsed := false
			switch v := dVal.(type) {
			case *AnthropicDiagnostics:
				anthropicReq.Diagnostics = v
				parsed = true
			case AnthropicDiagnostics:
				anthropicReq.Diagnostics = &v
				parsed = true
			default:
				if data, err := providerUtils.MarshalSorted(v); err == nil {
					var d AnthropicDiagnostics
					if sonic.Unmarshal(data, &d) == nil {
						anthropicReq.Diagnostics = &d
						parsed = true
					}
				}
			}
			if parsed {
				delete(anthropicReq.ExtraParams, "diagnostics")
			}
		}

		if fbVal, exists := bifrostReq.Params.ExtraParams["fallbacks"]; exists {
			var natives []AnthropicNativeFallback
			switch v := fbVal.(type) {
			case string:

				delete(anthropicReq.ExtraParams, "fallbacks")
				anthropicReq.Fallbacks = &AnthropicFallbacks{Preset: v}
			case []AnthropicNativeFallback:
				natives = v
			default:
				if data, err := providerUtils.MarshalSorted(v); err == nil {
					_ = sonic.Unmarshal(data, &natives)
				}
			}
			if len(natives) > 0 {
				delete(anthropicReq.ExtraParams, "fallbacks")
				entries := make([]AnthropicFallbackEntry, len(natives))
				for i := range natives {
					n := natives[i]
					entries[i] = AnthropicFallbackEntry{Native: &n}
				}
				anthropicReq.Fallbacks = &AnthropicFallbacks{Entries: entries}
			}
		}

		if tokenVal, exists := bifrostReq.Params.ExtraParams["fallback_credit_token"]; exists {
			if token, ok := tokenVal.(string); ok && token != "" {
				delete(anthropicReq.ExtraParams, "fallback_credit_token")
				anthropicReq.FallbackCreditToken = &token
			}
		}

		if bifrostReq.Params.TaskBudget != nil {
			tb := &AnthropicTaskBudget{
				Type:      bifrostReq.Params.TaskBudget.Type,
				Total:     bifrostReq.Params.TaskBudget.Total,
				Remaining: bifrostReq.Params.TaskBudget.Remaining,
			}
			if anthropicReq.OutputConfig == nil {
				anthropicReq.OutputConfig = &AnthropicOutputConfig{}
			}
			anthropicReq.OutputConfig.TaskBudget = tb
		}

		if len(bifrostReq.Params.MCPServers) > 0 {
			servers := make([]AnthropicMCPServerV2, len(bifrostReq.Params.MCPServers))
			for i, s := range bifrostReq.Params.MCPServers {
				servers[i] = AnthropicMCPServerV2{
					Type:               s.Type,
					URL:                s.URL,
					Name:               s.Name,
					AuthorizationToken: s.AuthorizationToken,
				}
			}
			anthropicReq.MCPServers = servers
		}
		if bifrostReq.Params.ResponseFormat != nil {

			if ProviderRequiresSyntheticStructuredOutput(bifrostReq.Provider) {
				responseFormatTool := convertChatResponseFormatToTool(ctx, bifrostReq.Params)
				if responseFormatTool != nil {
					anthropicReq.Tools = append(anthropicReq.Tools, *responseFormatTool)

					thinkingEnabled := (reasoningParams != nil &&
						(reasoningParams.MaxTokens != nil ||
							(reasoningParams.Effort != nil && *reasoningParams.Effort != "none"))) ||
						promotedThinking != nil
					if !thinkingEnabled && forcedToolChoiceSupported {
						anthropicReq.ToolChoice = &AnthropicToolChoice{
							Type: "tool",
							Name: responseFormatTool.Name,
						}
					}
				}
			} else {

				outputFormat := convertChatResponseFormatToAnthropicOutputFormat(bifrostReq.Params.ResponseFormat)
				if outputFormat != nil {
					anthropicReq.OutputConfig = &AnthropicOutputConfig{
						Format: outputFormat,
					}
				}
			}
		}

		if bifrostReq.Params.Tools != nil {

			filtered, _ := ValidateChatToolsForProvider(bifrostReq.Params.Tools, caps)

			programmaticCaller := ""
			for _, tool := range filtered {
				if hasProgrammaticCaller(tool.AllowedCallers) {
					declaredVersion, hasCodeExecution := declaredChatCodeExecutionVersion(filtered)
					programmaticCaller, _ = resolveAnthropicProgrammaticCaller(declaredVersion, hasCodeExecution)
					if programmaticCaller == "" {
						return nil, fmt.Errorf("code execution version %q has no allowed_callers value, so a tool restricted to programmatic callers cannot be expressed", declaredVersion)
					}
					break
				}
			}
			tools := make([]AnthropicTool, 0, len(filtered))
			for _, tool := range filtered {
				if tool.Function != nil {
					converted, err := convertFunctionToolToAnthropic(tool)
					if err != nil {
						return nil, err
					}
					converted.AllowedCallers = anthropicAllowedCallers(converted.AllowedCallers, programmaticCaller)
					tools = append(tools, converted)
					continue
				}

				if converted, ok := convertServerToolToAnthropic(tool, caps, bifrostReq.Provider); ok {
					converted.AllowedCallers = anthropicAllowedCallers(converted.AllowedCallers, programmaticCaller)
					tools = append(tools, converted)
				}
			}
			if anthropicReq.Tools == nil {
				anthropicReq.Tools = tools
			} else {
				anthropicReq.Tools = append(anthropicReq.Tools, tools...)
			}
		}

		forcedToolChoiceRejected := !forcedToolChoiceSupported && bifrostReq.Params.ToolChoice.IsForced()

		if bifrostReq.Params.ToolChoice != nil && !forcedToolChoiceRejected {
			toolChoice := &AnthropicToolChoice{}
			if bifrostReq.Params.ToolChoice.ChatToolChoiceStr != nil {
				switch schemas.ChatToolChoiceType(*bifrostReq.Params.ToolChoice.ChatToolChoiceStr) {
				case schemas.ChatToolChoiceTypeAny:
					toolChoice.Type = "any"
				case schemas.ChatToolChoiceTypeRequired:
					toolChoice.Type = "any"
				case schemas.ChatToolChoiceTypeNone:
					toolChoice.Type = "none"
				default:
					toolChoice.Type = "auto"
				}
			} else if bifrostReq.Params.ToolChoice.ChatToolChoiceStruct != nil {
				switch bifrostReq.Params.ToolChoice.ChatToolChoiceStruct.Type {
				case schemas.ChatToolChoiceTypeFunction:
					toolChoice.Type = "tool"
					if bifrostReq.Params.ToolChoice.ChatToolChoiceStruct.Function != nil {
						toolChoice.Name = bifrostReq.Params.ToolChoice.ChatToolChoiceStruct.Function.Name
					}
				case schemas.ChatToolChoiceTypeAllowedTools:

					allowed := bifrostReq.Params.ToolChoice.ChatToolChoiceStruct.AllowedTools
					if allowed != nil {
						anthropicReq.Tools = filterToolsByAllowed(anthropicReq.Tools, allowed.Tools)
						switch {
						case countFunctionTools(anthropicReq.Tools) == 0:

							toolChoice.Type = "none"
						case allowed.Mode == "required":
							toolChoice.Type = "any"
						default:
							toolChoice.Type = "auto"
						}
					} else {
						toolChoice.Type = "any"
					}
				case schemas.ChatToolChoiceTypeCustom:
					toolChoice.Type = "auto"
				default:
					toolChoice.Type = "auto"
				}
			}
			applyParallelToolUse(toolChoice, bifrostReq.Params.ParallelToolCalls)
			anthropicReq.ToolChoice = toolChoice
		}

		if anthropicReq.ToolChoice == nil && len(anthropicReq.Tools) > 0 &&
			bifrostReq.Params.ParallelToolCalls != nil && !*bifrostReq.Params.ParallelToolCalls {
			toolChoice := &AnthropicToolChoice{Type: "auto"}
			applyParallelToolUse(toolChoice, bifrostReq.Params.ParallelToolCalls)
			anthropicReq.ToolChoice = toolChoice
		}

		if reasoningParams != nil {
			if reasoningParams.Type != nil && *reasoningParams.Type == "between_tools" && schemas.IsAnthropicModelFamily(ctx, bifrostReq.Model) {

				anthropicReq.Thinking = BetweenToolsThinking(caps, reasoningParams.Effort)
				if reasoningParams.Effort != nil && *reasoningParams.Effort != "none" &&
					caps.SupportsNativeEffort(DefaultSupportsNativeEffort(caps.Model())) {
					setEffortOnOutputConfig(anthropicReq, MapBifrostEffortToAnthropic(*reasoningParams.Effort))
				}
			} else if reasoningParams.MaxTokens != nil {
				if caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {

					anthropicReq.Thinking = &AnthropicThinking{Type: "adaptive"}
				} else {
					budgetTokens := *reasoningParams.MaxTokens
					if *reasoningParams.MaxTokens == -1 {

						budgetTokens = MinimumReasoningMaxTokens
					}
					if budgetTokens < MinimumReasoningMaxTokens {
						return nil, fmt.Errorf("reasoning.max_tokens must be >= %d for anthropic: %w", MinimumReasoningMaxTokens, ErrReasoningMaxTokensTooLow)
					}

					if requested := bifrostReq.Params.MaxCompletionTokens; requested != nil && *requested > anthropicReq.MaxTokens {
						if fitted, ok := fitThinkingBudget(&budgetTokens, reasoningParams.Effort, anthropicReq.MaxTokens); ok {
							budgetTokens = fitted
						}
					}
					anthropicReq.Thinking = &AnthropicThinking{
						Type:         "enabled",
						BudgetTokens: schemas.Ptr(budgetTokens),
					}
				}
			} else if reasoningParams.Effort != nil && *reasoningParams.Effort != "none" {
				effort := MapBifrostEffortToAnthropic(*reasoningParams.Effort)
				if caps.SupportsAdaptiveThinking(DefaultSupportsAdaptiveThinking(caps.Model())) {

					anthropicReq.Thinking = &AnthropicThinking{Type: "adaptive"}
					setEffortOnOutputConfig(anthropicReq, effort)
				} else if SupportsNativeEffort(caps) {

					setEffortOnOutputConfig(anthropicReq, effort)
					budgetTokens, err := providerUtils.GetBudgetTokensFromReasoningEffort(effort, MinimumReasoningMaxTokens, anthropicReq.MaxTokens)
					if err != nil {
						return nil, fmt.Errorf("%w: %w", ErrReasoningMaxTokensTooLow, err)
					}
					anthropicReq.Thinking = &AnthropicThinking{
						Type:         "enabled",
						BudgetTokens: schemas.Ptr(budgetTokens),
					}
				} else {

					budgetTokens, err := providerUtils.GetBudgetTokensFromReasoningEffort(*reasoningParams.Effort, MinimumReasoningMaxTokens, anthropicReq.MaxTokens)
					if err != nil {
						return nil, fmt.Errorf("%w: %w", ErrReasoningMaxTokensTooLow, err)
					}
					anthropicReq.Thinking = &AnthropicThinking{
						Type:         "enabled",
						BudgetTokens: schemas.Ptr(budgetTokens),
					}
				}
			} else if caps.CanDisableReasoning(DefaultCanDisableReasoning(capModel)) {

				anthropicReq.Thinking = &AnthropicThinking{
					Type: "disabled",
				}
			}

			if anthropicReq.Thinking != nil && anthropicReq.Thinking.Type != "disabled" && anthropicReq.Thinking.Type != "between_tools" {
				if reasoningParams.Display != nil {
					anthropicReq.Thinking.Display = reasoningParams.Display
				} else if caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {
					anthropicReq.Thinking.Display = schemas.Ptr("summarized")
				}
			}
		}

		if promotedThinking != nil && anthropicReq.Thinking == nil {
			anthropicReq.Thinking = promotedThinking

			if anthropicReq.Thinking.Display == nil && caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {
				anthropicReq.Thinking.Display = schemas.Ptr("summarized")
			}
		}

		if bifrostReq.Provider == schemas.DeepSeek && anthropicReq.ToolChoice != nil &&
			anthropicReq.ToolChoice.Type == "tool" {
			anthropicReq.Thinking = &AnthropicThinking{Type: "disabled"}
		}

		if bifrostReq.Params.ServiceTier != nil {
			mapped := MapBifrostServiceTierToAnthropicRequest(*bifrostReq.Params.ServiceTier)
			anthropicReq.ServiceTier = &mapped
		}
	}

	var anthropicMessages []AnthropicMessage
	var systemContent *AnthropicContent

	seenConversation := false
	midConvSystemSupported := caps.SupportsMidConversationSystem(
		DefaultSupportsMidConversationSystem(caps.Provider(), caps.Model()))

	inlineMidConvSystem := true

	i := 0
	for i < len(messages) {
		msg := messages[i]

		switch msg.Role {
		case schemas.ChatMessageRoleSystem, schemas.ChatMessageRoleDeveloper:

			midConvPlacementOK := (i == len(messages)-1 ||
				messages[i+1].Role == schemas.ChatMessageRoleAssistant) &&
				i > 0 && messages[i-1].Role == schemas.ChatMessageRoleUser
			if seenConversation && midConvSystemSupported && midConvPlacementOK {

				var content AnthropicContent
				if msg.Content != nil {
					if msg.Content.ContentStr != nil && *msg.Content.ContentStr != "" {
						content = AnthropicContent{ContentStr: msg.Content.ContentStr}
					} else if msg.Content.ContentBlocks != nil {
						blocks := make([]AnthropicContentBlock, 0, len(msg.Content.ContentBlocks))
						for _, block := range msg.Content.ContentBlocks {
							if block.Text != nil && *block.Text != "" {
								blocks = append(blocks, AnthropicContentBlock{
									Type:         AnthropicContentBlockTypeText,
									Text:         block.Text,
									CacheControl: block.CacheControl,
								})
							}
						}
						if len(blocks) > 0 {
							content = AnthropicContent{ContentBlocks: blocks}
						}
					}
				}
				if content.ContentStr != nil || len(content.ContentBlocks) > 0 {
					anthropicMessages = append(anthropicMessages, AnthropicMessage{
						Role:    AnthropicMessageRoleSystem,
						Content: content,
					})
				}
			} else {
				var newContent AnthropicContent
				if msg.Content != nil {
					if msg.Content.ContentStr != nil && *msg.Content.ContentStr != "" {
						newContent = AnthropicContent{ContentStr: msg.Content.ContentStr}
					} else if msg.Content.ContentBlocks != nil {
						blocks := make([]AnthropicContentBlock, 0, len(msg.Content.ContentBlocks))
						for _, block := range msg.Content.ContentBlocks {
							if block.Text != nil && *block.Text != "" {
								blocks = append(blocks, AnthropicContentBlock{
									Type:         AnthropicContentBlockTypeText,
									Text:         block.Text,
									CacheControl: block.CacheControl,
								})
							}
						}
						if len(blocks) > 0 {
							newContent = AnthropicContent{ContentBlocks: blocks}
						}
					}
				}

				var inlined *AnthropicMessage
				if seenConversation && inlineMidConvSystem {
					inlined = inlineMidConversationSystem(&newContent)
				}
				if inlined != nil {
					anthropicMessages = append(anthropicMessages, *inlined)
					i++
					continue
				}
				systemContent = appendToSystemContent(systemContent, newContent)

				if i == len(messages)-1 && len(anthropicMessages) == 0 {
					if systemContent != nil {
						content := systemContent
						systemContent = nil
						anthropicMessages = append(anthropicMessages, AnthropicMessage{
							Role:    AnthropicMessageRoleUser,
							Content: *content,
						})
					}
				}
			}
			i++

		case schemas.ChatMessageRoleTool:

			var toolResults []AnthropicContentBlock

			for i < len(messages) && messages[i].Role == schemas.ChatMessageRoleTool {
				toolMsg := messages[i]
				if toolMsg.ChatToolMessage != nil && toolMsg.ChatToolMessage.ToolCallID != nil {
					sanitizedToolUseID := providerUtils.SanitizeAnthropicToolUseID(*toolMsg.ChatToolMessage.ToolCallID)
					toolResult := AnthropicContentBlock{
						Type:      AnthropicContentBlockTypeToolResult,
						ToolUseID: &sanitizedToolUseID,
						IsError:   toolMsg.ChatToolMessage.IsError,
					}

					if content := schemas.FlattenToolResultMedia(toolMsg.Content, schemas.IsImageBlock); content != nil {
						if content.ContentStr != nil && *content.ContentStr != "" {
							toolResult.Content = &AnthropicContent{ContentStr: content.ContentStr}
						} else if content.ContentBlocks != nil {
							blocks := make([]AnthropicContentBlock, 0, len(content.ContentBlocks))
							for _, block := range content.ContentBlocks {

								if block.CacheControl != nil && toolResult.CacheControl == nil {
									toolResult.CacheControl = block.CacheControl
								}
								if block.Text != nil && *block.Text != "" {
									blocks = append(blocks, AnthropicContentBlock{
										Type: AnthropicContentBlockTypeText,
										Text: block.Text,
									})
								} else if block.ImageURLStruct != nil {
									imageBlock := ConvertToAnthropicImageBlock(block)

									imageBlock.CacheControl = nil
									blocks = append(blocks, imageBlock)
								}
							}
							if len(blocks) > 0 {
								toolResult.Content = &AnthropicContent{ContentBlocks: blocks}
							}
						}
					}

					toolResults = append(toolResults, toolResult)
				}
				i++
			}

			if len(toolResults) > 0 {
				anthropicMessages = append(anthropicMessages, AnthropicMessage{
					Role:    "user",
					Content: AnthropicContent{ContentBlocks: toolResults},
				})
				seenConversation = true
			}

		default:

			anthropicMsg := AnthropicMessage{
				Role: AnthropicMessageRole(msg.Role),
			}

			var content []AnthropicContentBlock

			if msg.ChatAssistantMessage != nil && msg.ChatAssistantMessage.ReasoningDetails != nil {
				for _, reasoningDetail := range msg.ChatAssistantMessage.ReasoningDetails {

					if reasoningDetail.Type == schemas.BifrostReasoningDetailsTypeEncrypted && reasoningDetail.Data != nil && *reasoningDetail.Data != "" {
						content = append(content, AnthropicContentBlock{
							Type: AnthropicContentBlockTypeRedactedThinking,
							Data: reasoningDetail.Data,
						})
						continue
					}
					content = append(content, AnthropicContentBlock{
						Type:      AnthropicContentBlockTypeThinking,
						Signature: reasoningDetail.Signature,
						Thinking:  reasoningDetail.Text,
					})
				}
			}

			if msg.Content != nil {

				if msg.Content.ContentStr != nil && *msg.Content.ContentStr != "" {
					content = append(content, AnthropicContentBlock{
						Type: AnthropicContentBlockTypeText,
						Text: msg.Content.ContentStr,
					})
				} else if msg.Content.ContentBlocks != nil {
					for _, block := range msg.Content.ContentBlocks {
						if block.Text != nil && *block.Text != "" {
							content = append(content, AnthropicContentBlock{
								Type:         AnthropicContentBlockTypeText,
								Text:         block.Text,
								CacheControl: block.CacheControl,
							})
						} else if block.ImageURLStruct != nil {
							content = append(content, ConvertToAnthropicImageBlock(block))
						} else if block.File != nil {
							content = append(content, ConvertToAnthropicDocumentBlock(block))
						}
					}
				}
			}

			if msg.ChatAssistantMessage != nil && msg.ChatAssistantMessage.ToolCalls != nil {
				for _, toolCall := range msg.ChatAssistantMessage.ToolCalls {
					toolUse := AnthropicContentBlock{
						Type:         AnthropicContentBlockTypeToolUse,
						ID:           providerUtils.SanitizeAnthropicToolUseIDPtr(toolCall.ID),
						Name:         toolCall.Function.Name,
						CacheControl: toolCall.CacheControl,
					}

					if toolCall.Function.Arguments == "" {
						toolUse.Input = json.RawMessage("{}")
					} else if compacted := compactJSONBytes([]byte(toolCall.Function.Arguments)); compacted != nil {
						toolUse.Input = json.RawMessage(compacted)
					} else {

						toolUse.Input = json.RawMessage([]byte(toolCall.Function.Arguments))
					}

					content = append(content, toolUse)
				}
			}

			if hasAnthropicDocumentBlock(content) {
				filtered := content[:0]
				hasUsableText := false
				for _, b := range content {
					if b.Type == AnthropicContentBlockTypeText {
						if b.Text == nil || strings.TrimSpace(*b.Text) == "" {
							continue
						}
						hasUsableText = true
					}
					filtered = append(filtered, b)
				}
				content = filtered
				if !hasUsableText {

					at := leadingAnthropicReasoningBlockCount(content)
					withPlaceholder := make([]AnthropicContentBlock, 0, len(content)+1)
					withPlaceholder = append(withPlaceholder, content[:at]...)
					withPlaceholder = append(withPlaceholder, AnthropicContentBlock{
						Type: AnthropicContentBlockTypeText,
						Text: schemas.Ptr(documentPlaceholderText),
					})
					withPlaceholder = append(withPlaceholder, content[at:]...)
					content = withPlaceholder
				}
			}

			if len(content) == 1 && content[0].Type == AnthropicContentBlockTypeText {

				anthropicMsg.Content = AnthropicContent{ContentBlocks: content}
			} else if len(content) > 0 {

				anthropicMsg.Content = AnthropicContent{ContentBlocks: content}
			}

			anthropicMessages = append(anthropicMessages, anthropicMsg)
			seenConversation = true
			i++
		}
	}

	anthropicReq.Messages = anthropicMessages
	anthropicReq.System = systemContent

	lastMsgIndex := len(anthropicReq.Messages) - 1
	if lastMsgIndex >= 0 && anthropicReq.Messages[lastMsgIndex].Role == AnthropicMessageRoleAssistant {
		blocks := anthropicReq.Messages[lastMsgIndex].Content.ContentBlocks
		for j := len(blocks) - 1; j >= 0; j-- {
			if blocks[j].Type == AnthropicContentBlockTypeText && blocks[j].Text != nil {
				anthropicReq.Messages[lastMsgIndex].Content.ContentBlocks[j].Text = schemas.Ptr(strings.TrimRight(*blocks[j].Text, " \n\r\t"))
				break
			}
		}
	}

	stripUnsupportedAnthropicFields(anthropicReq, bifrostReq.Provider, capModel)

	clampAnthropicCacheBreakpoints(anthropicReq)

	return anthropicReq, nil
}

func (response *AnthropicMessageResponse) ToBifrostChatResponse(ctx *schemas.BifrostContext) *schemas.BifrostChatResponse {
	if response == nil {
		return nil
	}

	bifrostResponse := &schemas.BifrostChatResponse{
		ID:      response.ID,
		Model:   response.Model,
		Created: int(time.Now().Unix()),
	}

	bifrostResponse.ExtraFields.RoutingInfo.ServerSideFallbackModel = response.Usage.ServerSideFallbackModel()

	var structuredOutputToolName string
	if ctx != nil {
		if toolName, ok := ctx.Value(schemas.BifrostContextKeyStructuredOutputToolName).(string); ok {
			structuredOutputToolName = toolName
		}
	}
	var usedStructuredOutputTool bool

	var toolCalls []schemas.ChatAssistantMessageToolCall
	var contentBlocks []schemas.ChatContentBlock
	var reasoningDetails []schemas.ChatReasoningDetails
	var reasoningText string
	var contentStr *string

	var annotations []schemas.ChatAssistantMessageAnnotation
	seenAnnotations := make(map[anthropicAnnotationKey]struct{})
	textOffset := 0

	if response.Content != nil {
		for _, c := range response.Content {
			switch c.Type {
			case AnthropicContentBlockTypeText:
				if c.Text != nil {
					contentBlocks = append(contentBlocks, schemas.ChatContentBlock{
						Type: schemas.ChatContentBlockTypeText,
						Text: c.Text,
					})
					start := textOffset
					textOffset += len(*c.Text)
					if c.Citations != nil {
						annotations = appendAnthropicWebSearchAnnotations(annotations, c.Citations.TextCitations, start, textOffset, seenAnnotations)
					}
				}
			case AnthropicContentBlockTypeToolUse:
				if c.ID != nil && c.Name != nil {

					if structuredOutputToolName != "" && *c.Name == structuredOutputToolName {

						var jsonStr string
						if c.Input != nil {
							if argBytes, err := providerUtils.MarshalSorted(c.Input); err == nil {
								jsonStr = string(argBytes)
							} else {
								jsonStr = fmt.Sprintf("%v", c.Input)
							}
						} else {
							jsonStr = "{}"
						}
						contentStr = &jsonStr
						usedStructuredOutputTool = true
						continue
					}

					function := schemas.ChatAssistantMessageToolCallFunction{
						Name: c.Name,
					}

					if c.Input != nil {
						args, err := providerUtils.MarshalSorted(c.Input)
						if err != nil {
							function.Arguments = fmt.Sprintf("%v", c.Input)
						} else {
							function.Arguments = string(args)
						}
					} else {
						function.Arguments = "{}"
					}

					toolCalls = append(toolCalls, schemas.ChatAssistantMessageToolCall{
						Index:    uint16(len(toolCalls)),
						Type:     schemas.Ptr(string(schemas.ChatToolTypeFunction)),
						ID:       c.ID,
						Function: function,
					})
				}
			case AnthropicContentBlockTypeThinking:
				reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
					Index:     len(reasoningDetails),
					Type:      schemas.BifrostReasoningDetailsTypeText,
					Text:      c.Thinking,
					Signature: c.Signature,
				})
				if c.Thinking != nil {
					reasoningText += *c.Thinking + "\n"
				}
			case AnthropicContentBlockTypeRedactedThinking:

				if c.Data != nil && *c.Data != "" {
					reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
						Index: len(reasoningDetails),
						Type:  schemas.BifrostReasoningDetailsTypeEncrypted,
						Data:  c.Data,
					})
				}
			}
		}
	}

	if len(contentBlocks) > 0 {
		allText := true
		for _, block := range contentBlocks {
			if block.Type != schemas.ChatContentBlockTypeText || block.Text == nil {
				allText = false
				break
			}
		}
		if allText {
			var joined strings.Builder
			for _, block := range contentBlocks {
				joined.WriteString(*block.Text)
			}
			contentStr = schemas.Ptr(joined.String())
			contentBlocks = nil
		}
	}

	messageContent := schemas.ChatMessageContent{
		ContentStr:    contentStr,
		ContentBlocks: contentBlocks,
	}

	var assistantMessage *schemas.ChatAssistantMessage

	if len(toolCalls) > 0 {
		assistantMessage = &schemas.ChatAssistantMessage{
			ToolCalls: toolCalls,
		}
	}

	if len(reasoningDetails) > 0 {
		if assistantMessage == nil {
			assistantMessage = &schemas.ChatAssistantMessage{}
		}
		assistantMessage.ReasoningDetails = reasoningDetails
		if reasoningText != "" {
			assistantMessage.Reasoning = &reasoningText
		}
	}

	if len(annotations) > 0 {
		if assistantMessage == nil {
			assistantMessage = &schemas.ChatAssistantMessage{}
		}
		assistantMessage.Annotations = annotations
	}

	message := schemas.ChatMessage{
		Role:                 schemas.ChatMessageRoleAssistant,
		Content:              &messageContent,
		ChatAssistantMessage: assistantMessage,
	}

	choice := schemas.BifrostResponseChoice{
		Index: 0,
		ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
			Message:    &message,
			StopString: response.StopSequence,
		},
		FinishReason: func() *string {
			if response.StopReason != "" {
				mapped := ConvertAnthropicFinishReasonToBifrost(response.StopReason)

				if usedStructuredOutputTool && len(toolCalls) == 0 &&
					mapped == string(schemas.BifrostFinishReasonToolCalls) {
					mapped = string(schemas.BifrostFinishReasonStop)
				}
				return &mapped
			}
			return nil
		}(),
	}

	bifrostResponse.Choices = []schemas.BifrostResponseChoice{choice}

	if response.Usage != nil {
		billable := billableAnthropicUsage(response.Usage)
		promptTokensDetails := &schemas.ChatPromptTokensDetails{
			CachedReadTokens:  billable.CacheReadInputTokens,
			CachedWriteTokens: billable.CacheCreationInputTokens,
		}
		if billable.CacheCreation.Ephemeral5mInputTokens > 0 || billable.CacheCreation.Ephemeral1hInputTokens > 0 {
			promptTokensDetails.CachedWriteTokenDetails = &schemas.ChatCachedWriteTokenDetails{
				CachedWriteTokens5m: billable.CacheCreation.Ephemeral5mInputTokens,
				CachedWriteTokens1h: billable.CacheCreation.Ephemeral1hInputTokens,
			}
		}
		bifrostResponse.Usage = &schemas.BifrostLLMUsage{
			PromptTokens:        billable.InputTokens + billable.CacheReadInputTokens + billable.CacheCreationInputTokens,
			PromptTokensDetails: promptTokensDetails,
			CompletionTokens:    billable.OutputTokens,
		}

		if billable.ServerToolUse != nil && billable.ServerToolUse.WebSearchRequests > 0 {
			n := billable.ServerToolUse.WebSearchRequests
			bifrostResponse.Usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{
				NumSearchQueries: &n,
			}
			bifrostResponse.Usage.ToolUsage = &schemas.ToolUsage{WebSearch: &schemas.WebSearchToolUsage{NumRequests: n}}
		}

		if billable.OutputTokensDetails != nil {
			if bifrostResponse.Usage.CompletionTokensDetails == nil {
				bifrostResponse.Usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
			}
			bifrostResponse.Usage.CompletionTokensDetails.ReasoningTokens = billable.OutputTokensDetails.ThinkingTokens
		}
		bifrostResponse.Usage.TotalTokens = bifrostResponse.Usage.PromptTokens + bifrostResponse.Usage.CompletionTokens

		if billable.ServiceTier != nil {
			mapped := MapAnthropicServiceTierToBifrost(*billable.ServiceTier)
			bifrostResponse.ServiceTier = &mapped
		}

		if billable.Speed != nil {
			bifrostResponse.Speed = billable.Speed
		}

		if billable.InferenceGeo != nil {
			bifrostResponse.InferenceGeo = billable.InferenceGeo
		}
	}

	if response.Diagnostics != nil {
		bifrostResponse.Diagnostics = response.Diagnostics
	}

	return bifrostResponse
}

func appendAnthropicWebSearchAnnotations(out []schemas.ChatAssistantMessageAnnotation, citations []AnthropicTextCitation, start, end int, seen map[anthropicAnnotationKey]struct{}) []schemas.ChatAssistantMessageAnnotation {
	for _, citation := range citations {
		if citation.Type != AnthropicCitationTypeWebSearchResultLocation || citation.URL == nil || *citation.URL == "" {
			continue
		}
		key := anthropicAnnotationKey{url: *citation.URL, start: start, end: end}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		annotation := schemas.ChatAssistantMessageAnnotation{
			Type: "url_citation",
			URLCitation: schemas.ChatAssistantMessageAnnotationCitation{
				StartIndex: start,
				EndIndex:   end,
				URL:        schemas.Ptr(*citation.URL),
			},
		}
		if citation.Title != nil {
			annotation.URLCitation.Title = *citation.Title
		}
		if citation.CitedText != "" {
			annotation.URLCitation.Text = schemas.Ptr(citation.CitedText)
		}
		out = append(out, annotation)
	}
	return out
}

func NewAnthropicStreamState() *AnthropicStreamState {
	return &AnthropicStreamState{
		contentBlockToToolCallIdx: make(map[int]int),
		sawArgsDelta:              make(map[int]bool),
		reasoningDetailIdxByBlock: make(map[int]int),
		textBlockStart:            make(map[int]int),
		pendingCitations:          make(map[int][]AnthropicTextCitation),
	}
}

func (state *AnthropicStreamState) reasoningDetailIndex(blockIndex int) int {
	if idx, ok := state.reasoningDetailIdxByBlock[blockIndex]; ok {
		return idx
	}
	idx := state.nextReasoningDetailIdx
	state.reasoningDetailIdxByBlock[blockIndex] = idx
	state.nextReasoningDetailIdx++
	return idx
}

func (chunk *AnthropicStreamEvent) ToBifrostChatCompletionStream(ctx *schemas.BifrostContext, structuredOutputToolName string, state *AnthropicStreamState) (*schemas.BifrostChatResponse, *schemas.BifrostError, bool) {
	if state == nil {
		state = NewAnthropicStreamState()
	}
	if state.contentBlockToToolCallIdx == nil {
		state.contentBlockToToolCallIdx = make(map[int]int)
	}
	if state.sawArgsDelta == nil {
		state.sawArgsDelta = make(map[int]bool)
	}
	if state.textBlockStart == nil {
		state.textBlockStart = make(map[int]int)
	}
	if state.pendingCitations == nil {
		state.pendingCitations = make(map[int][]AnthropicTextCitation)
	}

	switch chunk.Type {
	case AnthropicStreamEventTypeMessageStart:
		if chunk.Message != nil && chunk.Message.Role != "" {
			role := chunk.Message.Role
			streamResponse := &schemas.BifrostChatResponse{
				Object: "chat.completion.chunk",
				Choices: []schemas.BifrostResponseChoice{
					{
						Index: 0,
						ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
							Delta: &schemas.ChatStreamResponseChoiceDelta{
								Role: &role,
							},
						},
					},
				},
			}

			if chunk.Message.Diagnostics != nil {
				streamResponse.Diagnostics = chunk.Message.Diagnostics
			}
			return streamResponse, nil, false
		}
		return nil, nil, false

	case AnthropicStreamEventTypeMessageStop:
		return nil, nil, true

	case AnthropicStreamEventTypeContentBlockStart:
		if chunk.Index != nil && chunk.ContentBlock != nil {
			switch chunk.ContentBlock.Type {
			case AnthropicContentBlockTypeToolUse:

				if structuredOutputToolName != "" && chunk.ContentBlock.Name != nil && *chunk.ContentBlock.Name == structuredOutputToolName {

					return nil, nil, false
				}

				toolCallIdx := state.nextToolCallIndex
				state.contentBlockToToolCallIdx[*chunk.Index] = toolCallIdx
				state.nextToolCallIndex++

				streamResponse := &schemas.BifrostChatResponse{
					Object: "chat.completion.chunk",
					Choices: []schemas.BifrostResponseChoice{
						{
							Index: 0,
							ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
								Delta: &schemas.ChatStreamResponseChoiceDelta{
									ToolCalls: []schemas.ChatAssistantMessageToolCall{
										{
											Index: uint16(toolCallIdx),
											Type:  schemas.Ptr(string(schemas.ChatToolTypeFunction)),
											ID:    chunk.ContentBlock.ID,
											Function: schemas.ChatAssistantMessageToolCallFunction{
												Name:      chunk.ContentBlock.Name,
												Arguments: "",
											},
										},
									},
								},
							},
						},
					},
				}

				return streamResponse, nil, false

			case AnthropicContentBlockTypeText:

				state.textBlockStart[*chunk.Index] = state.contentLen
				if chunk.ContentBlock.Citations != nil && len(chunk.ContentBlock.Citations.TextCitations) > 0 {
					state.pendingCitations[*chunk.Index] = append(state.pendingCitations[*chunk.Index], chunk.ContentBlock.Citations.TextCitations...)
				}
				return nil, nil, false

			case AnthropicContentBlockTypeRedactedThinking:

				if chunk.ContentBlock.Data == nil || *chunk.ContentBlock.Data == "" {
					return nil, nil, false
				}
				return &schemas.BifrostChatResponse{
					Object: "chat.completion.chunk",
					Choices: []schemas.BifrostResponseChoice{
						{
							Index: 0,
							ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
								Delta: &schemas.ChatStreamResponseChoiceDelta{
									ReasoningDetails: []schemas.ChatReasoningDetails{
										{
											Index: state.reasoningDetailIndex(*chunk.Index),
											Type:  schemas.BifrostReasoningDetailsTypeEncrypted,
											Data:  chunk.ContentBlock.Data,
										},
									},
								},
							},
						},
					},
				}, nil, false

			default:
				return nil, nil, false
			}
		}

		return nil, nil, false

	case AnthropicStreamEventTypeContentBlockDelta:
		if chunk.Index != nil && chunk.Delta != nil {

			switch chunk.Delta.Type {
			case AnthropicStreamDeltaTypeText:
				if chunk.Delta.Text != nil && *chunk.Delta.Text != "" {
					state.contentLen += len(*chunk.Delta.Text)

					streamResponse := &schemas.BifrostChatResponse{
						Object: "chat.completion.chunk",
						Choices: []schemas.BifrostResponseChoice{
							{
								Index: 0,
								ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
									Delta: &schemas.ChatStreamResponseChoiceDelta{
										Content: chunk.Delta.Text,
									},
								},
							},
						},
					}

					return streamResponse, nil, false
				}

			case AnthropicStreamDeltaTypeInputJSON:

				if chunk.Delta.PartialJSON != nil {

					if *chunk.Delta.PartialJSON == "" {
						return nil, nil, false
					}

					toolCallIdx, isToolBlock := state.contentBlockToToolCallIdx[*chunk.Index]
					if !isToolBlock {
						return nil, nil, false
					}
					state.sawArgsDelta[*chunk.Index] = true

					streamResponse := &schemas.BifrostChatResponse{
						Object: "chat.completion.chunk",
						Choices: []schemas.BifrostResponseChoice{
							{
								Index: 0,
								ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
									Delta: &schemas.ChatStreamResponseChoiceDelta{
										ToolCalls: []schemas.ChatAssistantMessageToolCall{
											{
												Index: uint16(toolCallIdx),
												Function: schemas.ChatAssistantMessageToolCallFunction{
													Arguments: *chunk.Delta.PartialJSON,
												},
											},
										},
									},
								},
							},
						},
					}

					return streamResponse, nil, false
				}

			case AnthropicStreamDeltaTypeCitations:

				if chunk.Delta.Citation != nil {
					if _, ok := state.textBlockStart[*chunk.Index]; !ok {
						state.textBlockStart[*chunk.Index] = state.contentLen
					}
					state.pendingCitations[*chunk.Index] = append(state.pendingCitations[*chunk.Index], *chunk.Delta.Citation)
				}
				return nil, nil, false

			case AnthropicStreamDeltaTypeThinking:

				if chunk.Delta.Thinking != nil && *chunk.Delta.Thinking != "" {
					thinkingText := *chunk.Delta.Thinking

					streamResponse := &schemas.BifrostChatResponse{
						Object: "chat.completion.chunk",
						Choices: []schemas.BifrostResponseChoice{
							{
								Index: 0,
								ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
									Delta: &schemas.ChatStreamResponseChoiceDelta{
										Reasoning: schemas.Ptr(thinkingText),
										ReasoningDetails: []schemas.ChatReasoningDetails{
											{
												Index: state.reasoningDetailIndex(*chunk.Index),
												Type:  schemas.BifrostReasoningDetailsTypeText,
												Text:  schemas.Ptr(thinkingText),
											},
										},
									},
								},
							},
						},
					}

					return streamResponse, nil, false
				}

			case AnthropicStreamDeltaTypeSignature:
				if chunk.Delta.Signature != nil && *chunk.Delta.Signature != "" {

					streamResponse := &schemas.BifrostChatResponse{
						Object: "chat.completion.chunk",
						Choices: []schemas.BifrostResponseChoice{
							{
								Index: 0,
								ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
									Delta: &schemas.ChatStreamResponseChoiceDelta{
										ReasoningDetails: []schemas.ChatReasoningDetails{
											{
												Index:     state.reasoningDetailIndex(*chunk.Index),
												Type:      schemas.BifrostReasoningDetailsTypeText,
												Signature: chunk.Delta.Signature,
											},
										},
									},
								},
							},
						},
					}
					return streamResponse, nil, false
				}
			}
		}

	case AnthropicStreamEventTypeContentBlockStop:

		if chunk.Index != nil {

			if blockStart, isTextBlock := state.textBlockStart[*chunk.Index]; isTextBlock {
				citations := state.pendingCitations[*chunk.Index]
				delete(state.textBlockStart, *chunk.Index)
				delete(state.pendingCitations, *chunk.Index)
				annotations := appendAnthropicWebSearchAnnotations(nil, citations, blockStart, state.contentLen, make(map[anthropicAnnotationKey]struct{}))
				if len(annotations) == 0 {
					return nil, nil, false
				}
				return &schemas.BifrostChatResponse{
					Object: "chat.completion.chunk",
					Choices: []schemas.BifrostResponseChoice{
						{
							Index: 0,
							ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
								Delta: &schemas.ChatStreamResponseChoiceDelta{
									Annotations: annotations,
								},
							},
						},
					},
				}, nil, false
			}

			toolCallIdx, isToolBlock := state.contentBlockToToolCallIdx[*chunk.Index]
			needsFlush := isToolBlock && !state.sawArgsDelta[*chunk.Index]
			delete(state.contentBlockToToolCallIdx, *chunk.Index)
			delete(state.sawArgsDelta, *chunk.Index)
			if needsFlush {
				return &schemas.BifrostChatResponse{
					Object: "chat.completion.chunk",
					Choices: []schemas.BifrostResponseChoice{
						{
							Index: 0,
							ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
								Delta: &schemas.ChatStreamResponseChoiceDelta{
									ToolCalls: []schemas.ChatAssistantMessageToolCall{
										{
											Index: uint16(toolCallIdx),
											Function: schemas.ChatAssistantMessageToolCallFunction{
												Arguments: "{}",
											},
										},
									},
								},
							},
						},
					},
				}, nil, false
			}
		}
		return nil, nil, false

	case AnthropicStreamEventTypeMessageDelta:
		return nil, nil, false

	case AnthropicStreamEventTypePing:

		return nil, nil, false

	case AnthropicStreamEventTypeError:
		if chunk.Error != nil {

			bifrostErr := &schemas.BifrostError{
				IsBifrostError: false,
				StatusCode:     schemas.Ptr(streamErrorStatus(chunk.Error.Type)),
				Error: &schemas.ErrorField{
					Type:    &chunk.Error.Type,
					Message: chunk.Error.Message,
				},
			}

			return nil, bifrostErr, true
		}
	}

	return nil, nil, false
}

func filterToolsByAllowed(tools []AnthropicTool, allowed []schemas.ChatToolChoiceAllowedToolsTool) []AnthropicTool {
	if len(tools) == 0 || len(allowed) == 0 {
		return tools
	}
	permitted := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		if a.Function.Name != "" {
			permitted[a.Function.Name] = struct{}{}
		}
	}
	if len(permitted) == 0 {
		return tools
	}
	kept := make([]AnthropicTool, 0, len(tools))
	for _, t := range tools {

		if t.Type != nil || t.MCPToolset != nil {
			kept = append(kept, t)
			continue
		}
		if _, ok := permitted[t.Name]; ok {
			kept = append(kept, t)
		}
	}

	return kept
}

func applyParallelToolUse(toolChoice *AnthropicToolChoice, parallel *bool) {
	if toolChoice == nil || parallel == nil || *parallel {
		return
	}
	if toolChoice.Type == "none" {
		return
	}
	disable := true
	toolChoice.DisableParallelToolUse = &disable
}

func countFunctionTools(tools []AnthropicTool) int {
	n := 0
	for _, t := range tools {
		if t.Type == nil && t.MCPToolset == nil {
			n++
		}
	}
	return n
}
