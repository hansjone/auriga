// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/valyala/fasthttp"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type anthropicMessageBetaSignals struct {
	scopedCacheControl bool

	fileSource bool

	midConversationToolChanges bool

	inlineToolDefinition bool

	inlineMCPToolset bool

	inlineDefinitions []*AnthropicTool

	midConversationOutputConfig bool
}

var anthropicFinishReasonToBifrost = map[AnthropicStopReason]string{
	AnthropicStopReasonEndTurn:      "stop",
	AnthropicStopReasonMaxTokens:    "length",
	AnthropicStopReasonStopSequence: "stop",
	AnthropicStopReasonToolUse:      "tool_calls",
	AnthropicStopReasonCompaction:   "compaction",
}
var unsupportedRawToolTypes = map[schemas.ModelProvider][]string{
	schemas.Vertex: {
		"web_fetch_",
		"code_execution",
		"advisor_",
	},
	schemas.Bedrock: {
		"web_search_",
		"web_fetch_",
		"code_execution",
		"advisor_",
	},
	schemas.Azure: {
		"advisor_",
	},
}
var betaHeaderPrefixToFeature = map[string]func(ProviderFeatureSupport) bool{
	AnthropicDangerousToolUseBetaHeaderPrefix:   func(f ProviderFeatureSupport) bool { return f.Safeguards },
	AnthropicAutoModeClassifierBetaHeaderPrefix: func(f ProviderFeatureSupport) bool { return f.Safeguards },
	"computer-use-": func(f ProviderFeatureSupport) bool { return f.ComputerUse },
	AnthropicStructuredOutputsBetaHeaderPrefix:  func(f ProviderFeatureSupport) bool { return f.StructuredOutputs },
	AnthropicMCPClientBetaHeaderPrefix:          func(f ProviderFeatureSupport) bool { return f.MCP },
	AnthropicPromptCachingScopeBetaHeaderPrefix: func(f ProviderFeatureSupport) bool { return f.PromptCachingScope },
	"compact-":                                   func(f ProviderFeatureSupport) bool { return f.Compaction },
	"context-management-":                        func(f ProviderFeatureSupport) bool { return f.ContextEditing },
	"files-api-":                                 func(f ProviderFeatureSupport) bool { return f.FilesAPI },
	AnthropicAdvancedToolUseBetaHeaderPrefix:     func(f ProviderFeatureSupport) bool { return f.AdvancedToolUse },
	AnthropicToolSearchBetaHeaderPrefix:          func(f ProviderFeatureSupport) bool { return f.ToolSearch },
	AnthropicToolExamplesBetaHeaderPrefix:        func(f ProviderFeatureSupport) bool { return f.InputExamples },
	AnthropicInterleavedThinkingBetaHeaderPrefix: func(f ProviderFeatureSupport) bool { return f.InterleavedThinking },
	AnthropicSkillsBetaHeaderPrefix:              func(f ProviderFeatureSupport) bool { return f.Skills },
	AnthropicContext1MBetaHeaderPrefix:           func(f ProviderFeatureSupport) bool { return f.Context1M },
	AnthropicFastModeBetaHeaderPrefix:            func(f ProviderFeatureSupport) bool { return f.FastMode },
	AnthropicRedactThinkingBetaHeaderPrefix:      func(f ProviderFeatureSupport) bool { return f.RedactThinking },
	AnthropicTaskBudgetsBetaHeaderPrefix:         func(f ProviderFeatureSupport) bool { return f.TaskBudgets },
	AnthropicEagerInputStreamingBetaHeaderPrefix: func(f ProviderFeatureSupport) bool { return f.EagerInputStreaming },
	AnthropicAdvisorBetaHeaderPrefix:             func(f ProviderFeatureSupport) bool { return f.AdvisorTool },
	AnthropicCacheDiagnosisBetaHeaderPrefix:      func(f ProviderFeatureSupport) bool { return f.Diagnostics },
	AnthropicServerSideFallbackBetaHeaderPrefix:  func(f ProviderFeatureSupport) bool { return f.ServerSideFallback },
	AnthropicFallbackCreditBetaHeaderPrefix:      func(f ProviderFeatureSupport) bool { return f.FallbackCredit },

	AnthropicMidConversationToolChangesBetaHeaderPrefix:  func(f ProviderFeatureSupport) bool { return f.MidConvToolChanges },
	AnthropicMidConversationOutputConfigBetaHeaderPrefix: func(f ProviderFeatureSupport) bool { return f.MidConvOutputConfig },
	AnthropicInlineToolsBetaHeaderPrefix:                 func(f ProviderFeatureSupport) bool { return f.MidConvToolChanges },
	AnthropicThinkingDisplayUpdatesBetaHeaderPrefix:      func(f ProviderFeatureSupport) bool { return f.ThinkingDisplayUpdates },
}
var betaHeaderPrefixKnown = []string{
	AnthropicDangerousToolUseBetaHeaderPrefix,
	AnthropicAutoModeClassifierBetaHeaderPrefix,
	"computer-use-",
	AnthropicStructuredOutputsBetaHeaderPrefix,
	AnthropicMCPClientBetaHeaderPrefix,
	AnthropicPromptCachingScopeBetaHeaderPrefix,
	"compact-",
	"context-management-",
	"files-api-",
	AnthropicAdvancedToolUseBetaHeaderPrefix,
	AnthropicToolSearchBetaHeaderPrefix,
	AnthropicToolExamplesBetaHeaderPrefix,
	AnthropicInterleavedThinkingBetaHeaderPrefix,
	AnthropicSkillsBetaHeaderPrefix,
	AnthropicContext1MBetaHeaderPrefix,
	AnthropicFastModeBetaHeaderPrefix,
	AnthropicRedactThinkingBetaHeaderPrefix,
	AnthropicTaskBudgetsBetaHeaderPrefix,
	AnthropicEagerInputStreamingBetaHeaderPrefix,
	AnthropicAdvisorBetaHeaderPrefix,
	AnthropicCacheDiagnosisBetaHeaderPrefix,
	AnthropicServerSideFallbackBetaHeaderPrefix,
	AnthropicFallbackCreditBetaHeaderPrefix,
	AnthropicMidConversationToolChangesBetaHeaderPrefix,
	AnthropicMidConversationOutputConfigBetaHeaderPrefix,
	AnthropicInlineToolsBetaHeaderPrefix,
	AnthropicThinkingDisplayUpdatesBetaHeaderPrefix,
}
var providerToolVersionRemaps = map[schemas.ModelProvider][]ToolVersionRemap{
	schemas.Vertex: {

		{From: string(AnthropicToolTypeWebSearch20260209), To: string(AnthropicToolTypeWebSearch20250305)},

		{From: string(AnthropicToolTypeComputer20251124), To: string(AnthropicToolTypeComputer20250124)},
	},
}
var fallbackBlockTypeToken = []byte(`"` + string(AnthropicContentBlockTypeFallback) + `"`)
var ErrReasoningMaxTokensTooLow = errors.New("max_tokens too low for reasoning/thinking configuration")

type ToolVersionRemap struct {
	From string
	To   string
}

var toolRemovalTypeToken = []byte(`_removal"`)
var outputConfigKeyToken = []byte(`"output_config"`)
var cacheControlScopeKeyToken = []byte(`"scope"`)
var toolAdditionTypeToken = []byte(`_addition"`)
var anthropicToolTypePrefixToFeature = map[string]func(ProviderFeatureSupport, schemas.ModelCaps) bool{

	"web_search_": func(f ProviderFeatureSupport, c schemas.ModelCaps) bool {
		return c.SupportsWebSearch(f.WebSearch || f.WebSearchNova)
	},
	"web_fetch_": func(f ProviderFeatureSupport, c schemas.ModelCaps) bool { return c.SupportsWebFetch(f.WebFetch) },
	"code_execution_": func(f ProviderFeatureSupport, c schemas.ModelCaps) bool {
		return c.SupportsCodeExecution(f.CodeExecution || f.CodeExecNova)
	},
	"computer_": func(f ProviderFeatureSupport, c schemas.ModelCaps) bool { return f.ComputerUse },
	"bash_":     func(f ProviderFeatureSupport, c schemas.ModelCaps) bool { return c.SupportsBashTool(f.Bash) },
	"memory_":   func(f ProviderFeatureSupport, c schemas.ModelCaps) bool { return c.SupportsMemoryTool(f.Memory) },
	"text_editor_": func(f ProviderFeatureSupport, c schemas.ModelCaps) bool {
		return c.SupportsTextEditorTool(f.TextEditor)
	},
	"tool_search_tool_": func(f ProviderFeatureSupport, c schemas.ModelCaps) bool {
		return c.SupportsToolSearch(f.ToolSearch)
	},
	"advisor_": func(f ProviderFeatureSupport, c schemas.ModelCaps) bool { return c.SupportsAdvisorTool(f.AdvisorTool) },
}

func isAnthropicServerToolSupported(toolType string, features ProviderFeatureSupport, caps schemas.ModelCaps) bool {

	if toolType == "mcp_toolset" {
		return caps.SupportsMCP(features.MCP)
	}

	for prefix, check := range anthropicToolTypePrefixToFeature {
		if strings.HasPrefix(toolType, prefix) {
			return check(features, caps)
		}
	}
	return true
}

func ValidateChatToolsForProvider(tools []schemas.ChatTool, caps schemas.ModelCaps) (keep []schemas.ChatTool, dropped []string) {
	features, ok := ProviderFeatures[caps.Provider()]
	if !ok {
		return tools, nil
	}
	for _, tool := range tools {

		if tool.Function != nil || tool.Custom != nil {
			keep = append(keep, tool)
			continue
		}
		t := string(tool.Type)
		if isAnthropicServerToolSupported(t, features, caps) {
			keep = append(keep, tool)
		} else {
			dropped = append(dropped, t)
		}
	}
	return keep, dropped
}

func ProviderRequiresSyntheticStructuredOutput(provider schemas.ModelProvider) bool {
	return provider == schemas.Vertex || provider == schemas.BedrockMantle || provider == schemas.Azure
}

func SupportsToolSearch(provider schemas.ModelProvider, model string) bool {
	features, ok := ProviderFeatures[provider]
	return ok && schemas.ResolveModelCaps(provider, model).SupportsToolSearch(features.ToolSearch)
}

func ValidateResponsesToolsForProvider(tools []schemas.ResponsesTool, caps schemas.ModelCaps) (keep []schemas.ResponsesTool, dropped []string) {
	features, ok := ProviderFeatures[caps.Provider()]
	if !ok {

		return tools, nil
	}

	for _, tool := range tools {
		supported := true
		switch tool.Type {
		case schemas.ResponsesToolTypeWebSearch, schemas.ResponsesToolTypeWebSearchPreview:
			supported = caps.SupportsWebSearch(features.WebSearch || features.WebSearchNova)
		case schemas.ResponsesToolTypeWebFetch:
			supported = caps.SupportsWebFetch(features.WebFetch)
		case schemas.ResponsesToolTypeCodeInterpreter:
			supported = caps.SupportsCodeExecution(features.CodeExecution || features.CodeExecNova)
		case schemas.ResponsesToolTypeComputerUsePreview:
			supported = features.ComputerUse
		case schemas.ResponsesToolTypeComputer:
			supported = features.ComputerUse &&
				(ComputerUseGeneration(caps) == ComputerUseGenToolset20260801 || AcceptsComputerToolset(caps))
		case schemas.ResponsesToolTypeMCP:
			supported = caps.SupportsMCP(features.MCP)
		case schemas.ResponsesToolTypeLocalShell:
			supported = caps.SupportsBashTool(features.Bash)
		case schemas.ResponsesToolTypeMemory:
			supported = caps.SupportsMemoryTool(features.Memory)
		case schemas.ResponsesToolTypeToolSearch:
			supported = caps.SupportsToolSearch(features.ToolSearch)
		case schemas.ResponsesToolTypeFileSearch:
			supported = features.FileSearch
		case schemas.ResponsesToolTypeImageGeneration:
			supported = features.ImageGeneration
		case schemas.ResponsesToolTypeAdvisor:
			supported = caps.SupportsAdvisorTool(features.AdvisorTool)
		}

		if supported {
			keep = append(keep, tool)
		} else {
			dropped = append(dropped, string(tool.Type))
		}
	}
	return keep, dropped
}

func supportsPerMessageOutputConfig(caps schemas.ModelCaps) bool {
	return caps.SupportsMidConvOutputConfig(
		ProviderFeatures[caps.Provider()].MidConvOutputConfig &&
			DefaultSupportsMidConversationOutputConfig(caps.Provider(), caps.Model()))
}

func mayCarryToolChangeBlocks(jsonBody []byte) bool {
	return bytes.Contains(jsonBody, toolAdditionTypeToken) || bytes.Contains(jsonBody, toolRemovalTypeToken)
}

func mayCarryPerMessageOutputConfig(jsonBody []byte) bool {
	switch bytes.Count(jsonBody, outputConfigKeyToken) {
	case 0:
		return false
	case 1:
		return !providerUtils.JSONFieldExists(jsonBody, "output_config")
	}
	return true
}

func isEffortOnlyRawSystemMessage(msg gjson.Result) bool {
	if msg.Get("role").String() != string(AnthropicMessageRoleSystem) {
		return false
	}
	content := msg.Get("content")
	switch {
	case !content.Exists():
		return true
	case content.IsArray():
		empty := true
		content.ForEach(func(_, _ gjson.Result) bool {
			empty = false
			return false
		})
		return empty
	case content.Type == gjson.String:
		return content.String() == ""
	}
	return false
}

func stripUnsupportedAnthropicFields(req *AnthropicMessageRequest, provider schemas.ModelProvider, model string) {
	if req == nil {
		return
	}
	features, ok := ProviderFeatures[provider]
	if !ok {

		return
	}
	caps := schemas.ResolveModelCaps(provider, model)

	if req.Container != nil {

		hasSkills := req.Container.ContainerObject != nil && len(req.Container.ContainerObject.Skills) > 0

		if !caps.SupportsSkills(features.Skills) && req.Container.ContainerObject != nil && req.Container.ContainerObject.Skills != nil {
			req.Container.ContainerObject.Skills = nil
		}
		switch {
		case hasSkills && !caps.SupportsSkills(features.Skills):

			req.Container = nil
		case !hasSkills && !features.ContainerBasic:
			req.Container = nil
		}
	}
	if len(req.MCPServers) > 0 && !caps.SupportsMCP(features.MCP) {
		req.MCPServers = nil
	}

	if req.Speed != nil && (!features.FastMode || !caps.SupportsFastMode(DefaultSupportsFastMode(caps.Model()))) {
		req.Speed = nil
	}
	if req.OutputConfig != nil && req.OutputConfig.TaskBudget != nil && !caps.SupportsTaskBudgets(features.TaskBudgets) {
		req.OutputConfig.TaskBudget = nil

		if req.OutputConfig.Format == nil && req.OutputConfig.Effort == nil {
			req.OutputConfig = nil
		}
	}

	if req.Thinking != nil && req.Thinking.Type == "adaptive" && schemas.IsAnthropicModel(caps.Model()) &&
		!caps.SupportsAdaptiveThinking(DefaultSupportsAdaptiveThinking(caps.Model())) {
		var effort *string
		if req.OutputConfig != nil {
			effort = req.OutputConfig.Effort
		}
		if budget, ok := fitThinkingBudget(req.Thinking.BudgetTokens, effort, req.MaxTokens); ok {
			req.Thinking.Type = "enabled"
			req.Thinking.BudgetTokens = &budget
		} else {
			req.Thinking = nil
		}
	}

	if req.OutputConfig != nil && req.OutputConfig.Effort != nil && !caps.SupportsNativeEffort(DefaultSupportsNativeEffort(caps.Model())) {
		req.OutputConfig.Effort = nil
		if req.OutputConfig.Format == nil && req.OutputConfig.TaskBudget == nil {
			req.OutputConfig = nil
		}
	}

	if req.OutputConfig != nil && req.OutputConfig.Effort != nil && schemas.IsAnthropicModel(caps.Model()) {
		req.OutputConfig.Effort = new(caps.NormalizeReasoningEffort(*req.OutputConfig.Effort, DefaultEffortControl(caps.Model())))
	}

	if req.Thinking != nil && caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(model)) {

		if req.Thinking.Type == "enabled" && RejectsEnabledThinking(caps) {
			req.Thinking.Type = "adaptive"
		}
		if req.Thinking.Type == "adaptive" {
			req.Thinking.BudgetTokens = nil
		}
	}

	if req.Thinking != nil && req.Thinking.Type == "between_tools" &&
		!caps.SupportsBetweenToolsThinking(DefaultSupportsBetweenToolsThinking(model)) {
		req.Thinking.Type = "disabled"
	}
	if req.Thinking != nil && req.Thinking.Type == "disabled" {
		var effort *string
		if req.OutputConfig != nil {
			effort = req.OutputConfig.Effort
		}
		if RejectsDisabledThinking(caps, effort) {
			req.Thinking.Type = "adaptive"
			req.Thinking.BudgetTokens = nil
		}
	}

	if req.Thinking != nil && req.Thinking.Display != nil && *req.Thinking.Display == AnthropicThinkingDisplayUpdates &&
		!SupportsThinkingDisplayUpdates(caps) {
		req.Thinking.Display = new("omitted")
	}
	if req.InferenceGeo != nil && !caps.SupportsInferenceGeo(features.InferenceGeo) {
		req.InferenceGeo = nil
	}
	if req.ServiceTier != nil && !features.ServiceTier {
		req.ServiceTier = nil
	}

	if req.Diagnostics != nil && !features.Diagnostics {
		req.Diagnostics = nil
	}

	if len(req.Safeguards) > 0 &&
		(!features.Safeguards || !caps.SupportsSafeguards(DefaultSupportsSafeguards(provider, caps.Model()))) {
		req.Safeguards = nil
	}

	if !features.PromptCachingScope {

		if req.CacheControl != nil && req.CacheControl.Scope != nil {
			req.CacheControl.Scope = nil

			if req.CacheControl.TTL == nil && req.CacheControl.Type == "" {
				req.CacheControl = nil
			}
		}

		for i := range req.Tools {
			if req.Tools[i].CacheControl != nil && req.Tools[i].CacheControl.Scope != nil {
				req.Tools[i].CacheControl.Scope = nil

				if req.Tools[i].CacheControl.TTL == nil && req.Tools[i].CacheControl.Type == "" {
					req.Tools[i].CacheControl = nil
				}
			}
		}

		if req.System != nil {
			for i := range req.System.ContentBlocks {
				if req.System.ContentBlocks[i].CacheControl != nil && req.System.ContentBlocks[i].CacheControl.Scope != nil {
					req.System.ContentBlocks[i].CacheControl.Scope = nil
					if req.System.ContentBlocks[i].CacheControl.TTL == nil && req.System.ContentBlocks[i].CacheControl.Type == "" {
						req.System.ContentBlocks[i].CacheControl = nil
					}
				}
			}
		}

		for mi := range req.Messages {
			for ci := range req.Messages[mi].Content.ContentBlocks {
				cc := req.Messages[mi].Content.ContentBlocks[ci].CacheControl
				if cc != nil && cc.Scope != nil {
					cc.Scope = nil
					if cc.TTL == nil && cc.Type == "" {
						req.Messages[mi].Content.ContentBlocks[ci].CacheControl = nil
					}
				}
			}
		}
	}

	if !features.FallbackCredit {
		req.FallbackCreditToken = nil
	}

	if !features.ServerSideFallback {
		for mi := range req.Messages {
			blocks := req.Messages[mi].Content.ContentBlocks
			if len(blocks) == 0 {
				continue
			}
			kept := make([]AnthropicContentBlock, 0, len(blocks))
			for _, b := range blocks {
				if b.Type == AnthropicContentBlockTypeFallback {
					continue
				}
				kept = append(kept, b)
			}
			if len(kept) != len(blocks) {
				req.Messages[mi].Content.ContentBlocks = kept
			}
		}
	}
	if req.ContextManagement != nil {

		kept := make([]ContextManagementEdit, 0, len(req.ContextManagement.Edits))
		for _, edit := range req.ContextManagement.Edits {
			switch edit.Type {
			case ContextManagementEditTypeCompact:
				if caps.SupportsCompaction(features.Compaction) {
					kept = append(kept, edit)
				}
			case ContextManagementEditTypeClearToolUses, ContextManagementEditTypeClearThinking:
				if caps.SupportsContextEditing(features.ContextEditing) {
					kept = append(kept, edit)
				}
			default:

				kept = append(kept, edit)
			}
		}
		if len(kept) == 0 {
			req.ContextManagement = nil
		} else {
			req.ContextManagement.Edits = kept
		}
	}

	for i := range req.Tools {
		tool := &req.Tools[i]

		if tool.DeferLoading != nil && !caps.SupportsToolSearch(features.ToolSearch) {
			tool.DeferLoading = nil
		}
		if len(tool.AllowedCallers) > 0 && !caps.SupportsAdvancedToolUse(features.AdvancedToolUse) {
			tool.AllowedCallers = nil
		}

		if len(tool.InputExamples) > 0 && !caps.SupportsInputExamples(features.InputExamples) {
			tool.InputExamples = nil
		}
		if tool.EagerInputStreaming != nil && !caps.SupportsEagerInputStreaming(features.EagerInputStreaming) {
			tool.EagerInputStreaming = nil
		}
		if tool.Strict != nil && !features.StructuredOutputs {
			tool.Strict = nil
		}
	}
}

func StripUnsupportedFieldsFromRawBody(jsonBody []byte, provider schemas.ModelProvider, model string) ([]byte, error) {
	if len(jsonBody) == 0 {
		return jsonBody, nil
	}
	features, ok := ProviderFeatures[provider]
	if !ok {
		return jsonBody, nil
	}

	if model == "" {
		if modelResult := providerUtils.GetJSONField(jsonBody, "model"); modelResult.Exists() {
			model = modelResult.String()
		}
	}
	caps := schemas.ResolveModelCaps(provider, model)

	var err error

	if !features.Diagnostics && providerUtils.JSONFieldExists(jsonBody, "diagnostics") {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "diagnostics")
		if err != nil {
			return nil, fmt.Errorf("strip raw diagnostics: %w", err)
		}
	}

	if (!features.Safeguards ||
		!caps.SupportsSafeguards(DefaultSupportsSafeguards(provider, caps.Model()))) &&
		providerUtils.JSONFieldExists(jsonBody, "safeguards") {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "safeguards")
		if err != nil {
			return nil, fmt.Errorf("strip raw safeguards: %w", err)
		}
	}

	if providerUtils.JSONFieldExists(jsonBody, "speed") {
		if !features.FastMode || !caps.SupportsFastMode(DefaultSupportsFastMode(caps.Model())) {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "speed")
			if err != nil {
				return nil, fmt.Errorf("strip raw speed: %w", err)
			}
		}
	}

	if !caps.SupportsInferenceGeo(features.InferenceGeo) && providerUtils.JSONFieldExists(jsonBody, "inference_geo") {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "inference_geo")
		if err != nil {
			return nil, fmt.Errorf("strip raw inference_geo: %w", err)
		}
	}

	if !features.ServiceTier && providerUtils.JSONFieldExists(jsonBody, "service_tier") {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "service_tier")
		if err != nil {
			return nil, fmt.Errorf("strip raw service_tier: %w", err)
		}
	}

	if !features.FallbackCredit {
		var err error
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "fallback_credit_token")
		if err != nil {
			return nil, err
		}
	}

	if !features.ServerSideFallback && bytes.Contains(jsonBody, fallbackBlockTypeToken) {
		if msgs := providerUtils.GetJSONField(jsonBody, "messages"); msgs.IsArray() {
			mi := -1
			msgs.ForEach(func(_, msg gjson.Result) bool {
				mi++
				content := msg.Get("content")
				if !content.IsArray() {
					return true
				}
				isFallback := func(block gjson.Result) bool {
					return block.Get("type").String() == string(AnthropicContentBlockTypeFallback)
				}
				dropped := false
				content.ForEach(func(_, block gjson.Result) bool {
					dropped = isFallback(block)
					return !dropped
				})
				if !dropped {
					return true
				}
				var kept []string
				content.ForEach(func(_, block gjson.Result) bool {
					if !isFallback(block) {
						kept = append(kept, block.Raw)
					}
					return true
				})

				jsonBody, err = sjson.SetRawBytes(jsonBody, fmt.Sprintf("messages.%d.content", mi), []byte("["+strings.Join(kept, ",")+"]"))
				return err == nil
			})
			if err != nil {
				return nil, fmt.Errorf("strip raw fallback blocks: %w", err)
			}
		}
	}

	if !supportsPerMessageOutputConfig(caps) && mayCarryPerMessageOutputConfig(jsonBody) {
		if msgs := providerUtils.GetJSONField(jsonBody, "messages"); msgs.IsArray() {
			type hit struct {
				index       int
				dropMessage bool
			}
			var hits []hit
			mi := -1
			msgs.ForEach(func(_, msg gjson.Result) bool {
				mi++
				if msg.Get("output_config").Exists() {
					hits = append(hits, hit{index: mi, dropMessage: isEffortOnlyRawSystemMessage(msg)})
				}
				return true
			})

			for j := len(hits) - 1; j >= 0; j-- {
				path := fmt.Sprintf("messages.%d", hits[j].index)
				if !hits[j].dropMessage {
					path += ".output_config"
				}
				if jsonBody, err = providerUtils.DeleteJSONField(jsonBody, path); err != nil {
					return nil, fmt.Errorf("strip raw %s: %w", path, err)
				}
			}
		}
	}

	if !features.MidConvToolChanges && mayCarryToolChangeBlocks(jsonBody) {
		if msgs := providerUtils.GetJSONField(jsonBody, "messages"); msgs.IsArray() {
			type edit struct {
				index   int
				content []byte
			}
			var edits []edit
			mi := -1
			msgs.ForEach(func(_, msg gjson.Result) bool {
				mi++
				content := msg.Get("content")
				if !content.IsArray() {
					return true
				}
				changed := false
				var kept []string
				content.ForEach(func(_, block gjson.Result) bool {
					blockType := AnthropicContentBlockType(block.Get("type").String())
					if isToolChangeBlock(blockType) {
						changed = true
						return true
					}
					raw := block.Raw
					if blockType == AnthropicContentBlockTypeCompaction && block.Get("tool_changes").Exists() {
						var delErr error
						if raw, delErr = sjson.Delete(raw, "tool_changes"); delErr != nil {
							err = delErr
							return false
						}
						changed = true
					}
					kept = append(kept, raw)
					return true
				})
				if err != nil {
					return false
				}
				if changed {
					e := edit{index: mi}
					if len(kept) > 0 {
						e.content = []byte("[" + strings.Join(kept, ",") + "]")
					}
					edits = append(edits, e)
				}
				return true
			})
			if err != nil {
				return nil, fmt.Errorf("strip raw tool-change blocks: %w", err)
			}

			for j := len(edits) - 1; j >= 0; j-- {
				if edits[j].content == nil {
					jsonBody, err = providerUtils.DeleteJSONField(jsonBody, fmt.Sprintf("messages.%d", edits[j].index))
				} else {
					jsonBody, err = sjson.SetRawBytes(jsonBody, fmt.Sprintf("messages.%d.content", edits[j].index), edits[j].content)
				}
				if err != nil {
					return nil, fmt.Errorf("strip raw tool-change blocks: %w", err)
				}
			}
		}
	}

	if !caps.SupportsMCP(features.MCP) && providerUtils.JSONFieldExists(jsonBody, "mcp_servers") {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "mcp_servers")
		if err != nil {
			return nil, fmt.Errorf("strip raw mcp_servers: %w", err)
		}
	}

	if containerResult := providerUtils.GetJSONField(jsonBody, "container"); containerResult.Exists() {
		hasSkillsField, hasNonEmptySkills := false, false
		if containerResult.IsObject() {
			if skills := containerResult.Get("skills"); skills.Exists() {
				hasSkillsField = true
				if skills.IsArray() && len(skills.Array()) > 0 {
					hasNonEmptySkills = true
				}
			}
		}

		if !caps.SupportsSkills(features.Skills) && hasSkillsField {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "container.skills")
			if err != nil {
				return nil, fmt.Errorf("strip raw container.skills: %w", err)
			}
		}
		drop := false
		switch {
		case hasNonEmptySkills:
			drop = !caps.SupportsSkills(features.Skills)
		default:
			drop = !features.ContainerBasic
		}
		if drop {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "container")
			if err != nil {
				return nil, fmt.Errorf("strip raw container: %w", err)
			}
		}
	}

	if !caps.SupportsTaskBudgets(features.TaskBudgets) && providerUtils.JSONFieldExists(jsonBody, "output_config.task_budget") {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "output_config.task_budget")
		if err != nil {
			return nil, fmt.Errorf("strip raw output_config.task_budget: %w", err)
		}

		if oc := providerUtils.GetJSONField(jsonBody, "output_config"); oc.IsObject() && len(oc.Map()) == 0 {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "output_config")
			if err != nil {
				return nil, fmt.Errorf("strip raw output_config: %w", err)
			}
		}
	}

	if providerUtils.GetJSONField(jsonBody, "thinking.type").String() == "adaptive" && schemas.IsAnthropicModel(caps.Model()) &&
		!caps.SupportsAdaptiveThinking(DefaultSupportsAdaptiveThinking(caps.Model())) {
		var budget *int
		if b := providerUtils.GetJSONField(jsonBody, "thinking.budget_tokens"); b.Exists() {
			budget = new(int(b.Int()))
		}
		var effort *string
		if e := providerUtils.GetJSONField(jsonBody, "output_config.effort"); e.Exists() {
			effort = new(e.String())
		}
		maxTokens := providerUtils.GetMaxOutputTokensOrDefault(provider, caps.Model(), AnthropicDefaultMaxTokens)
		if m := providerUtils.GetJSONField(jsonBody, "max_tokens"); m.Exists() {
			maxTokens = int(m.Int())
		}
		if b, ok := fitThinkingBudget(budget, effort, maxTokens); ok {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "thinking.type", "enabled")
			if err != nil {
				return nil, fmt.Errorf("rewrite raw thinking.type to enabled: %w", err)
			}
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "thinking.budget_tokens", b)
			if err != nil {
				return nil, fmt.Errorf("set raw thinking.budget_tokens: %w", err)
			}
		} else {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "thinking")
			if err != nil {
				return nil, fmt.Errorf("strip raw thinking: %w", err)
			}
		}
	}

	if providerUtils.JSONFieldExists(jsonBody, "output_config.effort") &&
		!caps.SupportsNativeEffort(DefaultSupportsNativeEffort(caps.Model())) {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "output_config.effort")
		if err != nil {
			return nil, fmt.Errorf("strip raw output_config.effort: %w", err)
		}
		if oc := providerUtils.GetJSONField(jsonBody, "output_config"); oc.IsObject() && len(oc.Map()) == 0 {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "output_config")
			if err != nil {
				return nil, fmt.Errorf("strip raw output_config: %w", err)
			}
		}
	}

	if e := providerUtils.GetJSONField(jsonBody, "output_config.effort"); e.Exists() && schemas.IsAnthropicModel(caps.Model()) {
		if normalized := caps.NormalizeReasoningEffort(e.String(), DefaultEffortControl(caps.Model())); normalized != e.String() {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "output_config.effort", normalized)
			if err != nil {
				return nil, fmt.Errorf("normalize raw output_config.effort: %w", err)
			}
		}
	}

	if caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(model)) {

		if RejectsEnabledThinking(caps) && !PreservesCallerRequestSurface(model) &&
			providerUtils.GetJSONField(jsonBody, "thinking.type").String() == "enabled" {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "thinking.type", "adaptive")
			if err != nil {
				return nil, fmt.Errorf("rewrite raw thinking.type to adaptive: %w", err)
			}
		}

		if providerUtils.GetJSONField(jsonBody, "thinking.type").String() == "adaptive" &&
			providerUtils.JSONFieldExists(jsonBody, "thinking.budget_tokens") {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "thinking.budget_tokens")
			if err != nil {
				return nil, fmt.Errorf("strip raw thinking.budget_tokens: %w", err)
			}
		}
	}

	if providerUtils.GetJSONField(jsonBody, "thinking.type").String() == "between_tools" &&
		!caps.SupportsBetweenToolsThinking(DefaultSupportsBetweenToolsThinking(model)) {
		jsonBody, err = providerUtils.SetJSONField(jsonBody, "thinking.type", "disabled")
		if err != nil {
			return nil, fmt.Errorf("rewrite raw thinking.type to disabled: %w", err)
		}
	}
	if providerUtils.GetJSONField(jsonBody, "thinking.type").String() == "disabled" {
		var effort *string
		if e := providerUtils.GetJSONField(jsonBody, "output_config.effort"); e.Exists() {
			effort = new(e.String())
		}
		if RejectsDisabledThinking(caps, effort) {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "thinking.type", "adaptive")
			if err != nil {
				return nil, fmt.Errorf("rewrite raw thinking.type to adaptive: %w", err)
			}
			if providerUtils.JSONFieldExists(jsonBody, "thinking.budget_tokens") {
				jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "thinking.budget_tokens")
				if err != nil {
					return nil, fmt.Errorf("strip raw thinking.budget_tokens: %w", err)
				}
			}
		}
	}

	if !SupportsThinkingDisplayUpdates(caps) && providerUtils.GetJSONField(jsonBody, "thinking.display").String() == AnthropicThinkingDisplayUpdates {
		jsonBody, err = providerUtils.SetJSONField(jsonBody, "thinking.display", "omitted")
		if err != nil {
			return nil, fmt.Errorf("rewrite raw thinking.display to omitted: %w", err)
		}
	}

	if !features.PromptCachingScope && providerUtils.JSONFieldExists(jsonBody, "cache_control.scope") {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "cache_control.scope")
		if err != nil {
			return nil, fmt.Errorf("strip raw cache_control.scope: %w", err)
		}

		if cc := providerUtils.GetJSONField(jsonBody, "cache_control"); cc.IsObject() && len(cc.Map()) == 0 {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "cache_control")
			if err != nil {
				return nil, fmt.Errorf("strip raw cache_control: %w", err)
			}
		}
	}

	if providerUtils.JSONFieldExists(jsonBody, "context_management") {
		if !features.ContextManagementField {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "context_management")
			if err != nil {
				return nil, fmt.Errorf("strip raw context_management: %w", err)
			}
		} else if editsResult := providerUtils.GetJSONField(jsonBody, "context_management.edits"); editsResult.Exists() && editsResult.IsArray() {
			edits := editsResult.Array()

			dropIndices := []int{}
			for i, edit := range edits {
				editType := edit.Get("type").String()
				keep := true
				switch editType {
				case string(ContextManagementEditTypeCompact):
					keep = caps.SupportsCompaction(features.Compaction)
				case string(ContextManagementEditTypeClearToolUses), string(ContextManagementEditTypeClearThinking):
					keep = caps.SupportsContextEditing(features.ContextEditing)
				}
				if !keep {
					dropIndices = append(dropIndices, i)
				}
			}
			if len(dropIndices) == len(edits) {

				jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "context_management")
				if err != nil {
					return nil, fmt.Errorf("strip raw context_management: %w", err)
				}
			} else {
				for i := len(dropIndices) - 1; i >= 0; i-- {
					path := fmt.Sprintf("context_management.edits.%d", dropIndices[i])
					jsonBody, err = providerUtils.DeleteJSONField(jsonBody, path)
					if err != nil {
						return nil, fmt.Errorf("strip raw context_management.edits[%d]: %w", dropIndices[i], err)
					}
				}
			}
		}
	}

	if toolsResult := providerUtils.GetJSONField(jsonBody, "tools"); toolsResult.Exists() && toolsResult.IsArray() {
		for i := range toolsResult.Array() {
			base := fmt.Sprintf("tools.%d", i)

			if modelResult := providerUtils.GetJSONField(jsonBody, base+".model"); modelResult.Exists() && modelResult.Type == gjson.String {
				if prefixProvider, bare := schemas.ParseModelString(modelResult.String(), ""); prefixProvider != "" {
					jsonBody, err = providerUtils.SetJSONField(jsonBody, base+".model", bare)
					if err != nil {
						return nil, fmt.Errorf("strip raw %s.model prefix: %w", base, err)
					}
				}
			}

			if !caps.SupportsToolSearch(features.ToolSearch) {
				if providerUtils.JSONFieldExists(jsonBody, base+".defer_loading") {
					jsonBody, err = providerUtils.DeleteJSONField(jsonBody, base+".defer_loading")
					if err != nil {
						return nil, fmt.Errorf("strip raw %s.defer_loading: %w", base, err)
					}
				}
			}
			if !caps.SupportsAdvancedToolUse(features.AdvancedToolUse) {
				if providerUtils.JSONFieldExists(jsonBody, base+".allowed_callers") {
					jsonBody, err = providerUtils.DeleteJSONField(jsonBody, base+".allowed_callers")
					if err != nil {
						return nil, fmt.Errorf("strip raw %s.allowed_callers: %w", base, err)
					}
				}
			}
			if !caps.SupportsInputExamples(features.InputExamples) && providerUtils.JSONFieldExists(jsonBody, base+".input_examples") {
				jsonBody, err = providerUtils.DeleteJSONField(jsonBody, base+".input_examples")
				if err != nil {
					return nil, fmt.Errorf("strip raw %s.input_examples: %w", base, err)
				}
			}
			if !caps.SupportsEagerInputStreaming(features.EagerInputStreaming) && providerUtils.JSONFieldExists(jsonBody, base+".eager_input_streaming") {
				jsonBody, err = providerUtils.DeleteJSONField(jsonBody, base+".eager_input_streaming")
				if err != nil {
					return nil, fmt.Errorf("strip raw %s.eager_input_streaming: %w", base, err)
				}
			}
			if !features.StructuredOutputs && providerUtils.JSONFieldExists(jsonBody, base+".strict") {
				jsonBody, err = providerUtils.DeleteJSONField(jsonBody, base+".strict")
				if err != nil {
					return nil, fmt.Errorf("strip raw %s.strict: %w", base, err)
				}
			}
			if !features.PromptCachingScope && providerUtils.JSONFieldExists(jsonBody, base+".cache_control.scope") {
				jsonBody, err = providerUtils.DeleteJSONField(jsonBody, base+".cache_control.scope")
				if err != nil {
					return nil, fmt.Errorf("strip raw %s.cache_control.scope: %w", base, err)
				}

				if ccResult := providerUtils.GetJSONField(jsonBody, base+".cache_control"); ccResult.Exists() && ccResult.IsObject() && len(ccResult.Map()) == 0 {
					jsonBody, err = providerUtils.DeleteJSONField(jsonBody, base+".cache_control")
					if err != nil {
						return nil, fmt.Errorf("strip raw %s.cache_control empty parent: %w", base, err)
					}
				}
			}
		}
	}

	if !features.PromptCachingScope && bytes.Contains(jsonBody, cacheControlScopeKeyToken) {
		if systemResult := providerUtils.GetJSONField(jsonBody, "system"); systemResult.Exists() && systemResult.IsArray() {
			blocks := systemResult.Array()
			rebuilt := make([][]byte, len(blocks))
			changed := false
			for i, block := range blocks {
				stripped, didStrip, stripErr := stripCacheControlScope([]byte(block.Raw))
				if stripErr != nil {
					return nil, fmt.Errorf("strip raw system[%d].cache_control.scope: %w", i, stripErr)
				}
				rebuilt[i] = stripped
				changed = changed || didStrip
			}
			if changed {
				jsonBody, err = providerUtils.SetRawJSONField(jsonBody, "system", rawJSONArrayOf(rebuilt))
				if err != nil {
					return nil, fmt.Errorf("strip raw system cache_control.scope: %w", err)
				}
			}
		}

		if messagesResult := providerUtils.GetJSONField(jsonBody, "messages"); messagesResult.Exists() && messagesResult.IsArray() &&
			messagesCarryCacheControlScope(messagesResult) {
			messages := messagesResult.Array()
			rebuiltMessages := make([][]byte, len(messages))
			anyMessageChanged := false
			for mi, msg := range messages {
				rebuiltMessages[mi] = []byte(msg.Raw)
				contentResult := msg.Get("content")
				if !contentResult.Exists() || !contentResult.IsArray() {
					continue
				}
				blocks := contentResult.Array()
				rebuiltBlocks := make([][]byte, len(blocks))
				messageChanged := false
				for ci, block := range blocks {
					stripped, didStrip, stripErr := stripCacheControlScope([]byte(block.Raw))
					if stripErr != nil {
						return nil, fmt.Errorf("strip raw messages[%d].content[%d].cache_control.scope: %w", mi, ci, stripErr)
					}
					rebuiltBlocks[ci] = stripped
					messageChanged = messageChanged || didStrip
				}
				if messageChanged {
					rebuiltMessages[mi], err = providerUtils.SetRawJSONField(rebuiltMessages[mi], "content", rawJSONArrayOf(rebuiltBlocks))
					if err != nil {
						return nil, fmt.Errorf("strip raw messages[%d].content cache_control.scope: %w", mi, err)
					}
					anyMessageChanged = true
				}
			}
			if anyMessageChanged {
				jsonBody, err = providerUtils.SetRawJSONField(jsonBody, "messages", rawJSONArrayOf(rebuiltMessages))
				if err != nil {
					return nil, fmt.Errorf("strip raw messages cache_control.scope: %w", err)
				}
			}
		}
	}

	return jsonBody, nil
}

func messagesCarryCacheControlScope(messages gjson.Result) bool {
	found := false
	messages.ForEach(func(_, msg gjson.Result) bool {
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, block gjson.Result) bool {
			found = block.Get("cache_control.scope").Exists()
			return !found
		})
		return !found
	})
	return found
}

func stripCacheControlScope(block []byte) ([]byte, bool, error) {
	if !providerUtils.JSONFieldExists(block, "cache_control.scope") {
		return block, false, nil
	}
	out, err := providerUtils.DeleteJSONField(block, "cache_control.scope")
	if err != nil {
		return nil, false, err
	}
	if cc := providerUtils.GetJSONField(out, "cache_control"); cc.Exists() && cc.IsObject() && len(cc.Map()) == 0 {
		out, err = providerUtils.DeleteJSONField(out, "cache_control")
		if err != nil {
			return nil, false, err
		}
	}
	return out, true, nil
}

func IsOpus47Plus(model string) bool {
	model = strings.ToLower(model)
	if !strings.Contains(model, "opus") {
		return false
	}
	return strings.Contains(model, "4-7") || strings.Contains(model, "4.7") ||
		strings.Contains(model, "4-8") || strings.Contains(model, "4.8") ||
		IsOpus5Plus(model)
}

func IsOpus5Plus(model string) bool {
	return strings.Contains(strings.ToLower(model), "opus-5")
}

func IsSonnet5Plus(model string) bool {
	return strings.Contains(strings.ToLower(model), "sonnet-5")
}

func IsSonnet55Plus(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "sonnet-5-5") || strings.Contains(m, "sonnet-5.5")
}

func IsFableFamily(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "fable") || strings.Contains(m, "mythos")
}

func IsMythosPreview(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "mythos") && strings.Contains(m, "preview")
}

func RejectsEnabledThinking(caps schemas.ModelCaps) bool {
	model := caps.Model()
	return caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(model)) && !IsMythosPreview(model)
}

func RejectsDisabledThinking(caps schemas.ModelCaps, effort *string) bool {
	model := caps.Model()
	if !caps.CanDisableReasoning(DefaultCanDisableReasoning(model)) {
		return true
	}
	if IsOpus5Plus(model) && effort != nil {
		switch strings.ToLower(*effort) {
		case "xhigh", "max":
			return true
		}
	}
	return false
}

func DefaultSupportsFastMode(model string) bool {
	if IsOpus47Plus(model) {
		return true
	}
	m := strings.ToLower(model)
	return strings.Contains(m, "opus") &&
		(strings.Contains(m, "4-6") || strings.Contains(m, "4.6"))
}

func DefaultSupportsSafeguards(provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.Anthropic, schemas.Bedrock, schemas.BedrockMantle, schemas.Vertex, schemas.Azure:
	default:
		return false
	}
	return IsSonnet5Plus(model) || IsOpus47Plus(model) || IsFableFamily(model)
}

func DefaultEagerInputStreaming(provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.Vertex:
		return schemas.IsAnthropicModel(strings.ToLower(model))
	case schemas.Bedrock:
		m := strings.ToLower(model)
		isSonnet46 := strings.Contains(m, "sonnet") && (strings.Contains(m, "4-6") || strings.Contains(m, "4.6"))
		return isSonnet46 || IsSonnet5Plus(m) || IsOpus47Plus(m) || IsFableFamily(m)
	default:
		return false
	}
}

func ShouldDefaultEagerInputStreaming(provider schemas.ModelProvider, model string) bool {
	features, ok := ProviderFeatures[provider]
	if !ok || !features.EagerInputStreaming {
		return false
	}
	caps := schemas.ResolveModelCaps(provider, model)
	if !caps.SupportsEagerInputStreaming(true) {
		return false
	}
	return DefaultEagerInputStreaming(provider, caps.Model())
}

func isAnthropicCustomTool(tool *AnthropicTool) bool {
	if tool.MCPToolset != nil || tool.Name == "" {
		return false
	}
	return tool.Type == nil || *tool.Type == AnthropicToolTypeCustom
}

func applyDefaultEagerInputStreaming(req *AnthropicMessageRequest, provider schemas.ModelProvider, model string) {
	if req == nil || len(req.Tools) == 0 || !ShouldDefaultEagerInputStreaming(provider, model) {
		return
	}
	for i := range req.Tools {
		tool := &req.Tools[i]
		if tool.EagerInputStreaming == nil && isAnthropicCustomTool(tool) {
			tool.EagerInputStreaming = schemas.Ptr(true)
		}
	}
}

func ApplyDefaultEagerInputStreamingToRawBody(jsonBody []byte, provider schemas.ModelProvider, model string) ([]byte, error) {
	toolsResult := providerUtils.GetJSONField(jsonBody, "tools")
	if !toolsResult.Exists() || !toolsResult.IsArray() || !ShouldDefaultEagerInputStreaming(provider, model) {
		return jsonBody, nil
	}

	tools := toolsResult.Array()
	parts := make([][]byte, len(tools))
	changed := false
	for i, tool := range tools {
		parts[i] = []byte(tool.Raw)
		if tool.Get("eager_input_streaming").Exists() || tool.Get("name").String() == "" {
			continue
		}
		if toolType := tool.Get("type"); toolType.Exists() && toolType.String() != string(AnthropicToolTypeCustom) {
			continue
		}
		updated, err := providerUtils.SetJSONField(parts[i], "eager_input_streaming", true)
		if err != nil {
			return nil, fmt.Errorf("set raw tools.%d.eager_input_streaming: %w", i, err)
		}
		parts[i] = updated
		changed = true
	}
	if !changed {
		return jsonBody, nil
	}
	jsonBody, err := providerUtils.SetRawJSONField(jsonBody, "tools", rawJSONArrayOf(parts))
	if err != nil {
		return nil, fmt.Errorf("set raw tools eager_input_streaming: %w", err)
	}
	return jsonBody, nil
}

func DefaultSupportsAdaptiveThinking(model string) bool {
	if IsOpus47Plus(model) || IsSonnet5Plus(model) || IsFableFamily(model) {
		return true
	}
	m := strings.ToLower(model)
	if !strings.Contains(m, "4-6") && !strings.Contains(m, "4.6") {
		return false
	}
	return strings.Contains(m, "opus") || strings.Contains(m, "sonnet")
}

func DefaultEffortControl(model string) *schemas.EffortControl {
	levels := []string{schemas.ReasoningEffortLow, schemas.ReasoningEffortMedium, schemas.ReasoningEffortHigh}
	switch {
	case IsOpus47Plus(model) || IsSonnet5Plus(model) || IsFableFamily(model):
		levels = append(levels, schemas.ReasoningEffortXHigh, schemas.ReasoningEffortMax)
	case DefaultSupportsAdaptiveThinking(model):
		levels = append(levels, schemas.ReasoningEffortMax)
	}
	return &schemas.EffortControl{Levels: levels}
}

func fitThinkingBudget(budget *int, effort *string, maxTokens int) (int, bool) {
	if budget != nil && *budget >= MinimumReasoningMaxTokens && *budget < maxTokens {
		return *budget, true
	}
	if maxTokens <= MinimumReasoningMaxTokens {
		return 0, false
	}
	level := "high"
	if effort != nil {
		level = MapBifrostEffortToAnthropic(*effort)
	}
	b, err := providerUtils.GetBudgetTokensFromReasoningEffort(level, MinimumReasoningMaxTokens, maxTokens)
	return b, err == nil
}

func fitRawThinkingBudget(jsonBody []byte, maxTokens int) ([]byte, error) {
	if providerUtils.GetJSONField(jsonBody, "thinking.type").String() != "enabled" {
		return jsonBody, nil
	}
	b := providerUtils.GetJSONField(jsonBody, "thinking.budget_tokens")
	if !b.Exists() || b.Int() < int64(maxTokens) {
		return jsonBody, nil
	}
	var effort *string
	if e := providerUtils.GetJSONField(jsonBody, "output_config.effort"); e.Exists() {
		effort = new(e.String())
	}
	fitted, ok := fitThinkingBudget(nil, effort, maxTokens)
	if !ok {

		return providerUtils.DeleteJSONField(jsonBody, "thinking")
	}
	return providerUtils.SetJSONField(jsonBody, "thinking.budget_tokens", fitted)
}

func DefaultAdaptiveOnlyThinking(model string) bool {
	return IsOpus47Plus(model) || IsSonnet5Plus(model) || IsFableFamily(model)
}

func DefaultCanDisableReasoning(model string) bool {
	return !IsFableFamily(model) && !schemas.IsOpus55Plus(model) && !IsSonnet55Plus(model)
}

func DefaultSupportsThinkingDisplayUpdates(model string) bool {
	return DefaultAdaptiveOnlyThinking(model)
}

func SupportsThinkingDisplayUpdates(caps schemas.ModelCaps) bool {
	return caps.SupportsThinkingDisplayUpdates(
		ProviderFeatures[caps.Provider()].ThinkingDisplayUpdates &&
			DefaultSupportsThinkingDisplayUpdates(caps.Model()))
}

func DefaultSupportsBetweenToolsThinking(model string) bool {
	return IsSonnet55Plus(model)
}

func PreservesCallerRequestSurface(model string) bool {
	return IsSonnet55Plus(model)
}

func BetweenToolsThinking(caps schemas.ModelCaps, effort *string) *AnthropicThinking {
	if caps.SupportsBetweenToolsThinking(DefaultSupportsBetweenToolsThinking(caps.Model())) {
		return &AnthropicThinking{Type: "between_tools"}
	}
	if !RejectsDisabledThinking(caps, effort) {
		return &AnthropicThinking{Type: "disabled"}
	}
	return nil
}

func SupportsNativeEffort(caps schemas.ModelCaps) bool {
	return caps.SupportsNativeEffort(DefaultSupportsNativeEffort(caps.Model())) && !caps.SupportsAdaptiveThinking(DefaultSupportsAdaptiveThinking(caps.Model()))
}

func DefaultSupportsNativeEffort(model string) bool {
	m := strings.ToLower(model)
	if IsFableFamily(m) || IsSonnet5Plus(m) || IsOpus5Plus(m) {
		return true
	}
	if strings.Contains(m, "haiku") {
		return false
	}
	if strings.Contains(m, "opus") {
		return strings.Contains(m, "4-5") || strings.Contains(m, "4.5") ||
			strings.Contains(m, "4-6") || strings.Contains(m, "4.6") ||
			strings.Contains(m, "4-7") || strings.Contains(m, "4.7") ||
			strings.Contains(m, "4-8") || strings.Contains(m, "4.8")
	}
	if strings.Contains(m, "sonnet") {
		return strings.Contains(m, "4-6") || strings.Contains(m, "4.6")
	}
	return false
}

func appendToSystemContent(existing *AnthropicContent, newContent AnthropicContent) *AnthropicContent {
	newEmpty := (newContent.ContentStr == nil || *newContent.ContentStr == "") && len(newContent.ContentBlocks) == 0
	if newEmpty {
		return existing
	}
	if existing == nil {
		return &AnthropicContent{ContentStr: newContent.ContentStr, ContentBlocks: newContent.ContentBlocks}
	}
	toBlocks := func(c AnthropicContent) []AnthropicContentBlock {
		if c.ContentStr != nil && *c.ContentStr != "" {
			return []AnthropicContentBlock{{Type: AnthropicContentBlockTypeText, Text: c.ContentStr}}
		}
		return c.ContentBlocks
	}
	merged := append(toBlocks(*existing), toBlocks(newContent)...)
	if len(merged) == 0 {
		return existing
	}
	return &AnthropicContent{ContentBlocks: merged}
}

const AnthropicMaxCacheBreakpoints = 4

func clampAnthropicCacheBreakpoints(req *AnthropicMessageRequest) int {
	if req == nil {
		return 0
	}

	var refs []**schemas.CacheControl
	for i := range req.Tools {
		if req.Tools[i].CacheControl != nil {
			refs = append(refs, &req.Tools[i].CacheControl)
		}
	}
	if req.System != nil {
		for i := range req.System.ContentBlocks {
			if req.System.ContentBlocks[i].CacheControl != nil {
				refs = append(refs, &req.System.ContentBlocks[i].CacheControl)
			}
		}
	}
	for i := range req.Messages {
		blocks := req.Messages[i].Content.ContentBlocks
		for j := range blocks {
			if blocks[j].CacheControl != nil {
				refs = append(refs, &blocks[j].CacheControl)
			}
		}
	}

	if req.CacheControl != nil {
		refs = append(refs, &req.CacheControl)
	}

	excess := len(refs) - AnthropicMaxCacheBreakpoints
	if excess <= 0 {
		return 0
	}
	for i := 0; i < excess; i++ {
		*refs[i] = nil
	}
	return excess
}

func inlineMidConversationSystem(content *AnthropicContent) *AnthropicMessage {
	if content == nil {
		return nil
	}

	wrap := func(text string) string {
		return "<system-reminder>\n" + text + "\n</system-reminder>\n"
	}

	var blocks []AnthropicContentBlock
	if content.ContentStr != nil && *content.ContentStr != "" {

		blocks = append(blocks, AnthropicContentBlock{
			Type: AnthropicContentBlockTypeText,
			Text: schemas.Ptr(wrap(*content.ContentStr)),
		})
	}

	var lastCacheControl *schemas.CacheControl
	for _, block := range content.ContentBlocks {
		if block.Text == nil || *block.Text == "" {
			continue
		}
		blocks = append(blocks, AnthropicContentBlock{
			Type: AnthropicContentBlockTypeText,
			Text: schemas.Ptr(wrap(*block.Text)),
		})
		if block.CacheControl != nil {
			lastCacheControl = block.CacheControl
		}
	}

	if len(blocks) == 0 {
		return nil
	}
	if lastCacheControl != nil {
		blocks[len(blocks)-1].CacheControl = lastCacheControl
	}

	return &AnthropicMessage{
		Role:    AnthropicMessageRoleUser,
		Content: AnthropicContent{ContentBlocks: blocks},
	}
}

func DefaultSupportsMidConversationSystem(provider schemas.ModelProvider, model string) bool {
	if provider != schemas.Anthropic {
		return false
	}
	m := strings.ToLower(model)
	if IsFableFamily(m) || IsOpus5Plus(m) || IsSonnet55Plus(m) {
		return true
	}
	return strings.Contains(m, "opus") &&
		(strings.Contains(m, "4-8") || strings.Contains(m, "4.8"))
}

func DefaultSupportsMidConversationOutputConfig(provider schemas.ModelProvider, model string) bool {
	if provider != schemas.Anthropic {
		return false
	}
	m := strings.ToLower(model)
	return schemas.IsFable51(m) || IsOpus5Plus(m) || IsSonnet55Plus(m)
}

const (
	ServerToolComputerUse = "computer_use"
	ServerToolTextEditor  = "text_editor"
	ServerToolWebSearch   = "web_search"
	ServerToolWebFetch    = "web_fetch"
)

const (
	ComputerUseGenToolset20260801 = "20260801"
	ComputerUseGen20251124        = "20251124"
	ComputerUseGen20250124        = "20250124"
)

func ComputerUseGeneration(caps schemas.ModelCaps) string {
	if computerUse, ok := caps.ServerTool(ServerToolComputerUse); ok {
		switch computerUse {
		case string(AnthropicToolTypeComputerToolset20260801):
			return ComputerUseGenToolset20260801
		case string(AnthropicToolTypeComputer20251124):
			return ComputerUseGen20251124
		default:
			return ComputerUseGen20250124
		}
	}
	m := strings.ToLower(caps.Model())
	if schemas.IsOpus55Plus(m) && toolsetOnlyComputerUseProvider(caps.Provider()) {
		return ComputerUseGenToolset20260801
	}

	if IsOpus47Plus(m) || IsSonnet5Plus(m) || IsFableFamily(m) {
		return ComputerUseGen20251124
	}

	if strings.Contains(m, "opus") {
		if strings.Contains(m, "4-5") || strings.Contains(m, "4.5") ||
			strings.Contains(m, "4-6") || strings.Contains(m, "4.6") {
			return ComputerUseGen20251124
		}
	}
	if strings.Contains(m, "sonnet") {
		if strings.Contains(m, "4-6") || strings.Contains(m, "4.6") {
			return ComputerUseGen20251124
		}
	}
	return ComputerUseGen20250124
}

func toolsetOnlyComputerUseProvider(provider schemas.ModelProvider) bool {
	return provider == schemas.Anthropic || provider == schemas.Vertex
}

func AcceptsComputerToolset(caps schemas.ModelCaps) bool {
	if !toolsetOnlyComputerUseProvider(caps.Provider()) {
		return false
	}
	return caps.SupportsComputerToolset(DefaultSupportsComputerToolset(caps.Model()))
}

func DefaultSupportsComputerToolset(model string) bool {
	m := strings.ToLower(model)
	if IsFableFamily(m) || schemas.IsOpus55Plus(m) || IsOpus5Plus(m) || IsSonnet5Plus(m) {
		return true
	}
	return strings.Contains(m, "opus") &&
		(strings.Contains(m, "4-8") || strings.Contains(m, "4.8"))
}

func TextEditorGeneration(caps schemas.ModelCaps) string {
	if textEditor, ok := caps.ServerTool(ServerToolTextEditor); ok {
		if textEditor == string(AnthropicToolTypeTextEditor20250728) {
			return ComputerUseGen20251124
		}
		return ComputerUseGen20250124
	}
	m := strings.ToLower(caps.Model())
	if IsOpus47Plus(m) || IsSonnet5Plus(m) || IsFableFamily(m) {
		return ComputerUseGen20251124
	}
	if strings.Contains(m, "opus") {
		if strings.Contains(m, "4-5") || strings.Contains(m, "4.5") ||
			strings.Contains(m, "4-6") || strings.Contains(m, "4.6") {
			return ComputerUseGen20251124
		}
	}
	if strings.Contains(m, "sonnet") {
		if strings.Contains(m, "4-5") || strings.Contains(m, "4.5") ||
			strings.Contains(m, "4-6") || strings.Contains(m, "4.6") {
			return ComputerUseGen20251124
		}
	}
	return ComputerUseGen20250124
}

func NormalizedToolSpec(generation, baseTool string) (toolType, toolName string) {
	switch baseTool {
	case "computer":
		switch generation {
		case ComputerUseGenToolset20260801:

			return string(AnthropicToolTypeComputerToolset20260801), ""
		case ComputerUseGen20251124:
			return string(AnthropicToolTypeComputer20251124), "computer"
		}
		return string(AnthropicToolTypeComputer20250124), "computer"
	case "bash":

		return string(AnthropicToolTypeBash20250124), "bash"
	case "text_editor":
		if generation == ComputerUseGen20251124 {
			return string(AnthropicToolTypeTextEditor20250728), "str_replace_based_edit_tool"
		}
		return string(AnthropicToolTypeTextEditor20250124), "str_replace_editor"
	}
	return "", ""
}

func computerUseBaseTool(toolType string) string {
	switch {
	case strings.HasPrefix(toolType, "computer_"):
		return "computer"
	case strings.HasPrefix(toolType, "text_editor_"):
		return "text_editor"
	case strings.HasPrefix(toolType, "bash_"):
		return "bash"
	}
	return ""
}

func MapBifrostEffortToAnthropic(effort string) string {
	switch effort {
	case "minimal":
		return "low"
	case "adaptive":

		return "high"
	}
	return effort
}

func setEffortOnOutputConfig(req *AnthropicMessageRequest, effort string) {
	if req.OutputConfig == nil {
		req.OutputConfig = &AnthropicOutputConfig{}
	}
	req.OutputConfig.Effort = &effort
}

func (s *anthropicMessageBetaSignals) noteToolChangeBlock(block *AnthropicContentBlock) {
	if isToolChangeBlock(block.Type) {
		s.midConversationToolChanges = true
		if block.Tool != nil {
			s.noteToolChangeTarget(block.Tool)
		}
	}
	for i := range block.ToolChanges {
		change := &block.ToolChanges[i]
		if isToolChangeBlock(change.Type) {
			s.midConversationToolChanges = true
		}
		if change.Tool != nil {
			s.noteToolChangeTarget(change.Tool)
		}
	}
}

func (s *anthropicMessageBetaSignals) noteToolChangeTarget(target *AnthropicToolChangeTarget) {
	if target.Type != AnthropicToolChangeTargetTypeToolDefinition {
		return
	}
	s.inlineToolDefinition = true
	if target.Definition == nil {
		return
	}
	if target.Definition.MCPToolset != nil {
		s.inlineMCPToolset = true
	}
	s.inlineDefinitions = append(s.inlineDefinitions, target.Definition)
}

func scanMessagesForBetaSignals(messages []AnthropicMessage) anthropicMessageBetaSignals {
	var signals anthropicMessageBetaSignals
	for _, message := range messages {
		if message.OutputConfig != nil && message.OutputConfig.Effort != nil {
			signals.midConversationOutputConfig = true
		}
		if message.Content.ContentBlocks == nil {
			continue
		}
		for i := range message.Content.ContentBlocks {
			block := &message.Content.ContentBlocks[i]
			if block.CacheControl != nil && block.CacheControl.Scope != nil {
				signals.scopedCacheControl = true
			}
			if block.Source != nil && block.Source.SourceObj != nil && block.Source.SourceObj.Type == "file" {
				signals.fileSource = true
			}
			signals.noteToolChangeBlock(block)
		}
	}
	return signals
}

func AddMissingBetaHeadersToContext(ctx *schemas.BifrostContext, req *AnthropicMessageRequest, provider schemas.ModelProvider) error {
	return addMissingBetaHeadersToContext(ctx, req, provider, scanMessagesForBetaSignals(req.Messages))
}

func appendAnthropicToolBetaHeaders(headers []string, tool *AnthropicTool, caps schemas.ModelCaps, features ProviderFeatureSupport, hasProvider bool, hasCachingScope *bool) []string {
	if tool == nil {
		return headers
	}

	if tool.Type != nil {
		switch *tool.Type {
		case AnthropicToolTypeComputer20251124:
			if !hasProvider || features.ComputerUse {
				headers = appendUniqueHeader(headers, AnthropicComputerUseBetaHeader20251124)
			}
		case AnthropicToolTypeComputer20250124:
			if !hasProvider || features.ComputerUse {
				headers = appendUniqueHeader(headers, AnthropicComputerUseBetaHeader20250124)
			}
		case AnthropicToolTypeAdvisor20260301:
			if !hasProvider || caps.SupportsAdvisorTool(features.AdvisorTool) {
				headers = appendUniqueHeader(headers, AnthropicAdvisorBetaHeader)
			}
		}
	}

	if tool.Strict != nil && *tool.Strict {
		if !hasProvider || features.StructuredOutputs {
			headers = appendUniqueHeader(headers, AnthropicStructuredOutputsBetaHeader)
		}
	}

	if tool.DeferLoading != nil && *tool.DeferLoading {
		if !hasProvider || caps.SupportsToolSearch(features.ToolSearch) {
			headers = appendUniqueHeader(headers, AnthropicToolSearchBetaHeader)
		}
	}
	if len(tool.InputExamples) > 0 {
		if !hasProvider || caps.SupportsAdvancedToolUse(features.AdvancedToolUse) {

			headers = appendUniqueHeader(headers, AnthropicAdvancedToolUseBetaHeader)
		} else if caps.SupportsInputExamples(features.InputExamples) {

			headers = appendUniqueHeader(headers, AnthropicToolExamplesBetaHeader)
		}
	}
	if len(tool.AllowedCallers) > 0 {
		if !hasProvider || caps.SupportsAdvancedToolUse(features.AdvancedToolUse) {
			headers = appendUniqueHeader(headers, AnthropicAdvancedToolUseBetaHeader)
		}
	}

	if len(tool.InputExamples) > 0 {
		if !hasProvider || caps.SupportsAdvancedToolUse(features.AdvancedToolUse) {
			headers = appendUniqueHeader(headers, AnthropicAdvancedToolUseBetaHeader)
		} else if caps.SupportsInputExamples(features.InputExamples) {
			headers = appendUniqueHeader(headers, AnthropicToolExamplesBetaHeader)
		}
	}

	if tool.EagerInputStreaming != nil && *tool.EagerInputStreaming {
		if !hasProvider || caps.SupportsEagerInputStreaming(features.EagerInputStreaming) {
			headers = appendUniqueHeader(headers, AnthropicEagerInputStreamingBetaHeader)
		}
	}

	if !*hasCachingScope && tool.CacheControl != nil && tool.CacheControl.Scope != nil {
		if !hasProvider || features.PromptCachingScope {
			headers = appendUniqueHeader(headers, AnthropicPromptCachingScopeBetaHeader)
			*hasCachingScope = true
		}
	}
	return headers
}

func addMissingBetaHeadersToContext(ctx *schemas.BifrostContext, req *AnthropicMessageRequest, provider schemas.ModelProvider, signals anthropicMessageBetaSignals) error {
	features, hasProvider := ProviderFeatures[provider]
	caps := schemas.ResolveModelCaps(provider, schemas.ResolveCanonicalModel(ctx, req.Model))
	headers := []string{}
	if len(req.Safeguards) > 0 && (!hasProvider || (features.Safeguards && caps.SupportsSafeguards(DefaultSupportsSafeguards(provider, caps.Model())))) {
		headers = appendUniqueHeader(headers, AnthropicDangerousToolUseBetaHeader)
	}
	hasCachingScope := false
	for i := range req.Tools {
		headers = appendAnthropicToolBetaHeaders(headers, &req.Tools[i], caps, features, hasProvider, &hasCachingScope)
	}

	for _, tool := range signals.inlineDefinitions {
		headers = appendAnthropicToolBetaHeaders(headers, tool, caps, features, hasProvider, &hasCachingScope)
	}

	if !hasCachingScope && req.CacheControl != nil && req.CacheControl.Scope != nil {
		if !hasProvider || features.PromptCachingScope {
			headers = appendUniqueHeader(headers, AnthropicPromptCachingScopeBetaHeader)
			hasCachingScope = true
		}
	}

	if req.ContextManagement != nil {
		for _, edit := range req.ContextManagement.Edits {
			if edit.Type == ContextManagementEditTypeCompact {
				if !hasProvider || caps.SupportsCompaction(features.Compaction) {
					headers = appendUniqueHeader(headers, AnthropicCompactionBetaHeader)
				}
			}
			if edit.Type == ContextManagementEditTypeClearToolUses || edit.Type == ContextManagementEditTypeClearThinking {
				if !hasProvider || caps.SupportsContextEditing(features.ContextEditing) {
					headers = appendUniqueHeader(headers, AnthropicContextManagementBetaHeader)
				}
			}
		}
	}

	if len(req.MCPServers) > 0 || signals.inlineMCPToolset {
		if !hasProvider || caps.SupportsMCP(features.MCP) {
			mcpBeta := AnthropicMCPClientBetaHeader
			if signals.inlineMCPToolset {
				mcpBeta = AnthropicMCPClientInlineToolsBetaHeader
			}
			headers = appendUniqueHeader(headers, mcpBeta)
		}
	}

	if req.Thinking != nil && req.Thinking.Type == "enabled" {
		if !hasProvider || caps.SupportsInterleavedThinking(features.InterleavedThinking) {
			headers = appendUniqueHeader(headers, AnthropicInterleavedThinkingBetaHeader)
		}
	}

	if req.Thinking != nil && (req.Thinking.Type == "adaptive" || req.Thinking.Type == "enabled") &&
		req.Thinking.Display != nil && *req.Thinking.Display == AnthropicThinkingDisplayUpdates {
		if !hasProvider || SupportsThinkingDisplayUpdates(caps) {
			headers = appendUniqueHeader(headers, AnthropicThinkingDisplayUpdatesBetaHeader)
		}
	}

	if req.Speed != nil {
		if (!hasProvider || features.FastMode) && caps.SupportsFastMode(DefaultSupportsFastMode(caps.Model())) {
			headers = appendUniqueHeader(headers, AnthropicFastModeBetaHeader)
		}
	}

	if !hasProvider || features.FastMode {
		for _, fb := range req.nativeFallbacks() {
			if fb.Speed != nil && schemas.ResolveModelCaps(provider, fb.Model).SupportsFastMode(DefaultSupportsFastMode(fb.Model)) {
				headers = appendUniqueHeader(headers, AnthropicFastModeBetaHeader)
				break
			}
		}
	}

	if req.OutputConfig != nil && req.OutputConfig.TaskBudget != nil {
		if !hasProvider || caps.SupportsTaskBudgets(features.TaskBudgets) {
			headers = appendUniqueHeader(headers, AnthropicTaskBudgetsBetaHeader)
		}
	}

	if req.OutputFormat != nil {
		if !hasProvider || features.StructuredOutputs {
			headers = appendUniqueHeader(headers, AnthropicStructuredOutputsBetaHeader)
		}
	}

	if req.Diagnostics != nil {
		if !hasProvider || features.Diagnostics {
			headers = appendUniqueHeader(headers, AnthropicCacheDiagnosisBetaHeader)
		}
	}

	if len(req.nativeFallbacks()) > 0 {
		if !hasProvider || features.ServerSideFallback {
			headers = appendUniqueHeader(headers, AnthropicServerSideFallbackBetaHeader)
		}
	}

	if req.fallbacksDefaultRouting() {
		if !hasProvider || features.ServerSideFallback {
			headers = appendUniqueHeader(headers, AnthropicServerSideFallbackDefaultBetaHeader)
		}
	}

	if req.FallbackCreditToken != nil {
		if !hasProvider || features.FallbackCredit {
			headers = appendUniqueHeader(headers, AnthropicFallbackCreditBetaHeader)
		}
	}

	if !hasCachingScope && req.System != nil && req.System.ContentBlocks != nil {
		for _, block := range req.System.ContentBlocks {
			if block.CacheControl != nil && block.CacheControl.Scope != nil {
				if !hasProvider || features.PromptCachingScope {
					headers = appendUniqueHeader(headers, AnthropicPromptCachingScopeBetaHeader)
					hasCachingScope = true
				}
				break
			}
		}
	}

	if !hasCachingScope && signals.scopedCacheControl {
		if !hasProvider || features.PromptCachingScope {
			headers = appendUniqueHeader(headers, AnthropicPromptCachingScopeBetaHeader)
		}
	}

	if signals.fileSource {
		if !hasProvider || caps.SupportsFilesAPI(features.FilesAPI) {
			headers = appendUniqueHeader(headers, AnthropicFilesAPIBetaHeader)
		}
	}

	if signals.midConversationOutputConfig {
		if !hasProvider || supportsPerMessageOutputConfig(caps) {
			headers = appendUniqueHeader(headers, AnthropicMidConversationOutputConfigBetaHeader)
		}
	}

	if signals.midConversationToolChanges {
		if !hasProvider || features.MidConvToolChanges {
			headers = appendUniqueHeader(headers, AnthropicMidConversationToolChangesBetaHeader)
		}
	}
	if signals.inlineToolDefinition {
		if !hasProvider || features.MidConvToolChanges {
			headers = appendUniqueHeader(headers, AnthropicInlineToolsBetaHeader)
		}
	}
	if len(headers) == 0 {
		return nil
	}
	var extraHeaders map[string][]string
	if ctx.Value(schemas.BifrostContextKeyExtraHeaders) == nil {
		extraHeaders = map[string][]string{}
	} else {
		if ctxExtraHeaders, ok := ctx.Value(schemas.BifrostContextKeyExtraHeaders).(map[string][]string); ok {
			extraHeaders = ctxExtraHeaders
		}
	}
	existing := extraHeaders[AnthropicBetaHeader]

	claimed := existing
	if passthrough, ok := ctx.Value(schemas.BifrostContextKeyPassthroughHeaders).(map[string][]string); ok {
		for k, vals := range passthrough {
			if strings.EqualFold(k, AnthropicBetaHeader) {
				claimed = append(append(make([]string, 0, len(claimed)+len(vals)), claimed...), vals...)
			}
		}
	}
	for _, h := range headers {
		if !betaHeaderPrefixExists(claimed, h) {
			existing = append(existing, h)
		}
	}
	extraHeaders[AnthropicBetaHeader] = existing
	ctx.SetValue(schemas.BifrostContextKeyExtraHeaders, extraHeaders)
	return nil
}

func stripBifrostFallbacksFromBody(jsonBody []byte, provider schemas.ModelProvider) ([]byte, error) {
	fb := gjson.GetBytes(jsonBody, "fallbacks")
	if !fb.Exists() {
		return jsonBody, nil
	}
	if !fb.IsArray() {

		if fb.Type == gjson.String {
			features, known := ProviderFeatures[provider]
			if !known || features.ServerSideFallback {
				return jsonBody, nil
			}
		}
		return sjson.DeleteBytes(jsonBody, "fallbacks")
	}

	features, known := ProviderFeatures[provider]
	keepNative := !known || features.ServerSideFallback
	var native [][]byte
	if keepNative {
		for _, el := range fb.Array() {
			if el.IsObject() {
				native = append(native, []byte(el.Raw))
			}
		}
	}
	if len(native) == 0 {
		return sjson.DeleteBytes(jsonBody, "fallbacks")
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, n := range native {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(n)
	}
	buf.WriteByte(']')
	return sjson.SetRawBytes(jsonBody, "fallbacks", buf.Bytes())
}

func betaHeaderPrefixExists(existing []string, newHeader string) bool {

	var matchedPrefix string
	for _, prefix := range betaHeaderPrefixKnown {
		if strings.HasPrefix(newHeader, prefix) {
			matchedPrefix = prefix
			break
		}
	}
	match := func(candidate string) bool {
		if matchedPrefix == "" {
			return candidate == newHeader
		}
		return strings.HasPrefix(candidate, matchedPrefix)
	}
	for _, headerValue := range existing {
		for _, candidate := range strings.Split(headerValue, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if match(candidate) {
				return true
			}
		}
	}
	return false
}

func doesWebSearchOrFetchAutoInjectCodeExecution(toolType string) bool {
	switch toolType {
	case string(AnthropicToolTypeWebSearch20250305):
		return false
	case string(AnthropicToolTypeWebSearch20260209):
		return true
	case string(AnthropicToolTypeWebFetch20260309):
		return true
	case string(AnthropicToolTypeWebFetch20260318):
		return true
	case string(AnthropicToolTypeWebFetch20250910):
		return false
	case string(AnthropicToolTypeWebFetch20260209):
		return true
	}

	return true
}

func resolveAnthropicProgrammaticCaller(declaredVersion string, hasCodeExecutionTool bool) (caller string, emitVersion string) {
	switch AnthropicToolType(declaredVersion) {
	case AnthropicToolTypeCodeExecution, AnthropicToolTypeCodeExecution20260120, AnthropicToolTypeCodeExecution20260521:
		return declaredVersion, ""
	case AnthropicToolTypeCodeExecution20250522:

		return "", ""
	}
	if hasCodeExecutionTool {
		if declaredVersion != "" {
			return declaredVersion, ""
		}

		return string(AnthropicToolTypeCodeExecution20260120), string(AnthropicToolTypeCodeExecution20260120)
	}

	return string(AnthropicToolTypeCodeExecution20260120), ""
}

func anthropicAllowedCallers(callers []string, programmaticCaller string) []string {
	if len(callers) == 0 {
		return callers
	}
	mapped := make([]string, 0, len(callers))
	seen := make(map[string]bool, len(callers))
	for _, caller := range callers {
		if caller == schemas.ResponsesToolCallerProgrammatic {
			if programmaticCaller == "" {
				continue
			}
			caller = programmaticCaller
		}
		if seen[caller] {
			continue
		}
		seen[caller] = true
		mapped = append(mapped, caller)
	}
	if len(mapped) == 0 {
		return nil
	}
	return mapped
}

func declaredChatCodeExecutionVersion(tools []schemas.ChatTool) (string, bool) {
	for _, tool := range tools {
		if strings.HasPrefix(string(tool.Type), string(AnthropicToolNameCodeExecution)+"_") {
			return string(tool.Type), true
		}
	}
	return "", false
}

func hasProgrammaticCaller(callers []string) bool {
	for _, caller := range callers {
		if caller == schemas.ResponsesToolCallerProgrammatic {
			return true
		}
	}
	return false
}

func StripEmptyThinkingBlocks(jsonBody []byte) ([]byte, error) {
	messagesResult := providerUtils.GetJSONField(jsonBody, "messages")
	if !messagesResult.Exists() || !messagesResult.IsArray() {
		return jsonBody, nil
	}
	messages := messagesResult.Array()

	keptByMessage := make(map[int][]gjson.Result)
	for mi, msg := range messages {
		contentResult := msg.Get("content")
		if !contentResult.Exists() || !contentResult.IsArray() {
			continue
		}
		blocks := contentResult.Array()
		stripped := false
		kept := make([]gjson.Result, 0, len(blocks))
		for _, block := range blocks {
			if block.Get("type").String() == "thinking" &&
				(block.Get("thinking").String() == "" || block.Get("signature").String() == "") {
				stripped = true
				continue
			}
			kept = append(kept, block)
		}
		if stripped {
			keptByMessage[mi] = kept
		}
	}
	if len(keptByMessage) == 0 {
		return jsonBody, nil
	}

	var rebuilt bytes.Buffer
	rebuilt.Grow(len(jsonBody))
	rebuilt.WriteByte('[')
	for mi, msg := range messages {
		if mi > 0 {
			rebuilt.WriteByte(',')
		}
		kept, needsRewrite := keptByMessage[mi]
		if !needsRewrite {
			rebuilt.WriteString(msg.Raw)
			continue
		}
		rewritten, err := providerUtils.SetRawJSONField([]byte(msg.Raw), "content", rawJSONArray(kept))
		if err != nil {
			return nil, fmt.Errorf("failed to strip empty thinking blocks from messages.%d: %w", mi, err)
		}
		rebuilt.Write(rewritten)
	}
	rebuilt.WriteByte(']')

	jsonBody, err := providerUtils.SetRawJSONField(jsonBody, "messages", rebuilt.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to strip empty thinking blocks: %w", err)
	}
	return jsonBody, nil
}

func rawJSONArray(results []gjson.Result) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, r := range results {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(r.Raw)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

func rawJSONArrayOf(parts [][]byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, p := range parts {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(p)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

func StripAutoInjectableTools(jsonBody []byte) ([]byte, error) {
	toolsResult := providerUtils.GetJSONField(jsonBody, "tools")
	if !toolsResult.Exists() || !toolsResult.IsArray() {
		return jsonBody, nil
	}

	tools := toolsResult.Array()
	if len(tools) == 0 {
		return jsonBody, nil
	}

	hasWebSearchOrFetchWithAutoInjectableCodeExecution := false
	for _, tool := range tools {
		toolType := tool.Get("type").String()
		if strings.HasPrefix(toolType, "web_search_") || strings.HasPrefix(toolType, "web_fetch_") {
			hasWebSearchOrFetchWithAutoInjectableCodeExecution = doesWebSearchOrFetchAutoInjectCodeExecution(toolType)
			break
		}
	}

	if !hasWebSearchOrFetchWithAutoInjectableCodeExecution {
		return jsonBody, nil
	}

	var indicesToStrip []int
	for i, tool := range tools {
		toolType := tool.Get("type").String()
		if strings.HasPrefix(toolType, "code_execution") {
			indicesToStrip = append(indicesToStrip, i)
		}
	}

	if len(indicesToStrip) == 0 {
		return jsonBody, nil
	}

	if len(indicesToStrip) == len(tools) {
		return providerUtils.DeleteJSONField(jsonBody, "tools")
	}

	strip := make(map[int]bool, len(indicesToStrip))
	for _, i := range indicesToStrip {
		strip[i] = true
	}
	kept := make([]gjson.Result, 0, len(tools)-len(indicesToStrip))
	for i, tool := range tools {
		if !strip[i] {
			kept = append(kept, tool)
		}
	}

	jsonBody, err := providerUtils.SetRawJSONField(jsonBody, "tools", rawJSONArray(kept))
	if err != nil {
		return nil, fmt.Errorf("failed to strip auto-injectable tools: %w", err)
	}
	return jsonBody, nil
}

func RemapRawToolVersionsForProvider(jsonBody []byte, provider schemas.ModelProvider, model string) ([]byte, error) {
	toolsResult := providerUtils.GetJSONField(jsonBody, "tools")
	if !toolsResult.Exists() || !toolsResult.IsArray() {
		return jsonBody, nil
	}

	if model == "" {
		if modelResult := providerUtils.GetJSONField(jsonBody, "model"); modelResult.Exists() {
			model = modelResult.String()
		}
	}

	var err error
	tools := toolsResult.Array()

	if prefixes, ok := unsupportedRawToolTypes[provider]; ok {
		for _, tool := range tools {
			toolType := tool.Get("type").String()
			for _, prefix := range prefixes {
				if strings.HasPrefix(toolType, prefix) {
					return nil, fmt.Errorf("tool type '%s' is not supported by provider '%s'", toolType, provider)
				}
			}
		}
	}

	caps := schemas.ResolveModelCaps(provider, model)
	computerGeneration := ComputerUseGeneration(caps)
	textEditorGeneration := TextEditorGeneration(caps)

	normalized := make([][]byte, len(tools))
	dropped := make([]bool, len(tools))
	anyNormalized := false
	for i, tool := range tools {
		normalized[i] = []byte(tool.Raw)
		toolType := tool.Get("type").String()
		baseTool := computerUseBaseTool(toolType)
		if baseTool == "" {
			continue
		}
		generation := computerGeneration
		if baseTool == "text_editor" {
			generation = textEditorGeneration
		}
		wantType, wantName := NormalizedToolSpec(generation, baseTool)
		if wantType == "" {
			continue
		}

		if toolType == string(AnthropicToolTypeComputerToolset20260801) && AcceptsComputerToolset(caps) {
			continue
		}

		if baseTool == "computer" && wantName != "" {
			width := providerUtils.GetJSONField(normalized[i], "display_width_px")
			height := providerUtils.GetJSONField(normalized[i], "display_height_px")
			if !width.Exists() || !height.Exists() || width.Int() <= 0 || height.Int() <= 0 {
				dropped[i] = true
				anyNormalized = true
				continue
			}
		}
		if toolType != wantType {
			normalized[i], err = providerUtils.SetJSONField(normalized[i], "type", wantType)
			if err != nil {
				return nil, fmt.Errorf("failed to normalize tool type: %w", err)
			}
			anyNormalized = true
		}
		if wantName == "" {

			for _, field := range []string{"name", "display_width_px", "display_height_px", "display_number", "enable_zoom"} {
				if !providerUtils.JSONFieldExists(normalized[i], field) {
					continue
				}
				normalized[i], err = providerUtils.DeleteJSONField(normalized[i], field)
				if err != nil {
					return nil, fmt.Errorf("failed to strip %s for toolset: %w", field, err)
				}
				anyNormalized = true
			}
			continue
		}

		if existingName := tool.Get("name").String(); existingName != "" && existingName != wantName {
			normalized[i], err = providerUtils.SetJSONField(normalized[i], "name", wantName)
			if err != nil {
				return nil, fmt.Errorf("failed to normalize tool name: %w", err)
			}
			anyNormalized = true
		}
	}
	if anyNormalized {
		kept := normalized[:0:0]
		for i, tool := range normalized {
			if !dropped[i] {
				kept = append(kept, tool)
			}
		}
		jsonBody, err = providerUtils.SetRawJSONField(jsonBody, "tools", rawJSONArrayOf(kept))
		if err != nil {
			return nil, fmt.Errorf("failed to normalize tool versions: %w", err)
		}
	}

	remaps, ok := providerToolVersionRemaps[provider]
	if !ok {
		return jsonBody, nil
	}

	tools = providerUtils.GetJSONField(jsonBody, "tools").Array()
	remapped := make([][]byte, len(tools))
	anyRemapped := false
	for i, tool := range tools {
		remapped[i] = []byte(tool.Raw)
		toolType := tool.Get("type").String()
		for _, remap := range remaps {
			if toolType == remap.From {
				remapped[i], err = providerUtils.SetJSONField(remapped[i], "type", remap.To)
				if err != nil {
					return nil, fmt.Errorf("failed to remap tool type: %w", err)
				}
				anyRemapped = true
				break
			}
		}
	}
	if anyRemapped {
		jsonBody, err = providerUtils.SetRawJSONField(jsonBody, "tools", rawJSONArrayOf(remapped))
		if err != nil {
			return nil, fmt.Errorf("failed to remap tool versions: %w", err)
		}
	}

	return jsonBody, nil
}

func MergeBetaHeaders(ctx context.Context, providerExtraHeaders map[string]schemas.SecretVar) []string {
	seen := make(map[string]bool)
	var all []string
	add := func(v string) {
		for part := range strings.SplitSeq(v, ",") {
			if t := strings.TrimSpace(part); t != "" && !seen[t] {
				seen[t] = true
				all = append(all, t)
			}
		}
	}
	for k, v := range providerExtraHeaders {
		if strings.EqualFold(k, AnthropicBetaHeader) && v.GetValue() != "" {
			add(v.GetValue())
		}
	}

	for _, key := range []schemas.BifrostContextKey{
		schemas.BifrostContextKeyExtraHeaders,
		schemas.BifrostContextKeyPassthroughHeaders,
	} {
		ctxHeaders, ok := ctx.Value(key).(map[string][]string)
		if !ok {
			continue
		}
		for k, vals := range ctxHeaders {
			if !strings.EqualFold(k, AnthropicBetaHeader) {
				continue
			}
			for _, v := range vals {
				add(v)
			}
		}
	}
	return all
}

func clampToModelOutputCeiling(caps schemas.ModelCaps, maxTokens int) int {
	if ceiling := caps.MaxOutputTokens(providerUtils.KnownClaudeMaxOutputTokens(caps.Model())); ceiling > 0 && maxTokens > ceiling {
		return ceiling
	}
	return maxTokens
}

func FilterBetaHeadersForProvider(headers []string, provider schemas.ModelProvider, overrides ...map[string]bool) []string {
	features, hasProvider := ProviderFeatures[provider]
	if !hasProvider {

		return headers
	}

	var overrideMap map[string]bool
	if len(overrides) > 0 {
		overrideMap = overrides[0]
	}

	filtered := make([]string, 0, len(headers))
	for _, h := range headers {
		for token := range strings.SplitSeq(h, ",") {
			token = strings.TrimSpace(token)

			if token == "" {
				continue
			}

			var matchedPrefix string
			for _, prefix := range betaHeaderPrefixKnown {
				if strings.HasPrefix(token, prefix) {
					matchedPrefix = prefix
					break
				}
			}

			if matchedPrefix == "" {

				if overrideMap != nil {
					matched := false
					for prefix, allowed := range overrideMap {
						if strings.HasPrefix(token, prefix) {
							if allowed {
								filtered = append(filtered, token)
							}

							matched = true
							break
						}
					}
					if matched {
						continue
					}
				}

				if provider == schemas.Anthropic {
					filtered = append(filtered, token)
				}
				continue
			}

			supported := false
			if overrideMap != nil {
				if override, hasOverride := overrideMap[matchedPrefix]; hasOverride {
					supported = override
				} else if featureCheck, ok := betaHeaderPrefixToFeature[matchedPrefix]; ok {
					supported = featureCheck(features)
				}
			} else if featureCheck, ok := betaHeaderPrefixToFeature[matchedPrefix]; ok {
				supported = featureCheck(features)
			}

			if !supported {
				continue
			}
			filtered = append(filtered, token)
		}
	}
	return filtered
}

func appendUniqueHeader(slice []string, item string) []string {
	if slices.Contains(slice, item) {
		return slice
	}
	return append(slice, item)
}

func appendBetaHeader(req *fasthttp.Request, betaHeader string) {
	existing := string(req.Header.Peek(AnthropicBetaHeader))
	if existing == "" {
		req.Header.Set(AnthropicBetaHeader, betaHeader)
		return
	}

	for _, h := range strings.Split(existing, ",") {
		if strings.TrimSpace(h) == betaHeader {
			return
		}
	}
	req.Header.Set(AnthropicBetaHeader, existing+","+betaHeader)
}

func convertChatResponseFormatToTool(ctx *schemas.BifrostContext, params *schemas.ChatParameters) *AnthropicTool {
	if params == nil || params.ResponseFormat == nil {
		return nil
	}

	rf, ok := schemas.ParseChatResponseFormat(params.ResponseFormat)
	if !ok || rf.Type != "json_schema" || !rf.HasJSONSchema() {
		return nil
	}

	toolName, ok := rf.Name()
	if !ok || toolName == "" {
		toolName = "json_response"
	}

	schemaOrdered, ok := rf.SchemaMap()
	if !ok {
		return nil
	}
	schemaObj := schemaOrdered.ToMap()

	description := "Returns structured JSON output"
	if desc, ok := schemaObj["description"].(string); ok && desc != "" {
		description = desc
	}

	toolName = fmt.Sprintf("bf_so_%s", toolName)
	ctx.SetValue(schemas.BifrostContextKeyStructuredOutputToolName, toolName)

	normalizedSchema := normalizeSchemaForAnthropic(schemaObj)
	schemaParams := convertMapToToolFunctionParameters(normalizedSchema)

	return &AnthropicTool{
		Name:        toolName,
		Description: schemas.Ptr(description),
		InputSchema: schemaParams,
	}
}

func convertResponsesTextFormatToTool(ctx *schemas.BifrostContext, textConfig *schemas.ResponsesTextConfig) (*AnthropicTool, error) {
	if textConfig == nil || textConfig.Format == nil {
		return nil, nil
	}

	format := textConfig.Format
	if format.Type != "json_schema" {
		return nil, nil
	}

	if format.JSONSchema == nil {
		return nil, nil
	}

	var schemaParams *schemas.ToolFunctionParameters
	composite, acceptAll, err := format.JSONSchema.CompositeSchema()
	if err != nil {
		return nil, err
	}
	switch {
	case composite != nil:

		schemaParams = convertMapToToolFunctionParameters(normalizeSchemaForAnthropic(composite.ToMap()))
	case acceptAll:

		schemaParams = &schemas.ToolFunctionParameters{Type: "object"}
	default:
		schemaParams = convertJSONSchemaToToolParameters(format.JSONSchema)
	}

	toolName := "json_response"
	if format.Name != nil && strings.TrimSpace(*format.Name) != "" {
		toolName = strings.TrimSpace(*format.Name)
	}

	description := "Returns structured JSON output"
	if format.JSONSchema.Description != nil {
		description = *format.JSONSchema.Description
	}

	toolName = fmt.Sprintf("bf_so_%s", toolName)
	ctx.SetValue(schemas.BifrostContextKeyStructuredOutputToolName, toolName)

	return &AnthropicTool{
		Name:        toolName,
		Description: schemas.Ptr(description),
		InputSchema: schemaParams,
	}, nil
}

func convertJSONSchemaToToolParameters(schema *schemas.ResponsesTextConfigFormatJSONSchema) *schemas.ToolFunctionParameters {
	if schema == nil {
		return nil
	}

	schemaType := "object"
	if schema.Type != nil {
		schemaType = *schema.Type
	}

	params := &schemas.ToolFunctionParameters{
		Type:                 schemaType,
		Description:          schema.Description,
		Required:             schema.Required,
		Enum:                 schema.Enum,
		Ref:                  schema.Ref,
		MinItems:             schema.MinItems,
		MaxItems:             schema.MaxItems,
		Format:               schema.Format,
		Pattern:              schema.Pattern,
		MinLength:            schema.MinLength,
		MaxLength:            schema.MaxLength,
		Minimum:              schema.Minimum,
		Maximum:              schema.Maximum,
		Title:                schema.Title,
		Default:              schema.Default,
		Nullable:             schema.Nullable,
		AdditionalProperties: schema.AdditionalProperties,
	}

	if schema.Properties != nil {
		if orderedMap, ok := schemas.SafeExtractOrderedMap(*schema.Properties); ok {
			params.Properties = orderedMap
		}
	}

	if schema.Defs != nil {
		if orderedMap, ok := schemas.SafeExtractOrderedMap(*schema.Defs); ok {
			params.Defs = orderedMap
		}
	}

	if schema.Definitions != nil {
		if orderedMap, ok := schemas.SafeExtractOrderedMap(*schema.Definitions); ok {
			params.Definitions = orderedMap
		}
	}

	if schema.Items != nil {
		if orderedMap, ok := schemas.SafeExtractOrderedMap(*schema.Items); ok {
			params.Items = orderedMap
		}
	}

	if len(schema.AnyOf) > 0 {
		params.AnyOf = make([]schemas.OrderedMap, 0, len(schema.AnyOf))
		for _, item := range schema.AnyOf {
			if orderedMap, ok := schemas.SafeExtractOrderedMap(item); ok {
				params.AnyOf = append(params.AnyOf, *orderedMap)
			}
		}
	}

	if len(schema.OneOf) > 0 {
		params.OneOf = make([]schemas.OrderedMap, 0, len(schema.OneOf))
		for _, item := range schema.OneOf {
			if orderedMap, ok := schemas.SafeExtractOrderedMap(item); ok {
				params.OneOf = append(params.OneOf, *orderedMap)
			}
		}
	}

	if len(schema.AllOf) > 0 {
		params.AllOf = make([]schemas.OrderedMap, 0, len(schema.AllOf))
		for _, item := range schema.AllOf {
			if orderedMap, ok := schemas.SafeExtractOrderedMap(item); ok {
				params.AllOf = append(params.AllOf, *orderedMap)
			}
		}
	}

	return params
}

func convertMapToToolFunctionParameters(m map[string]interface{}) *schemas.ToolFunctionParameters {
	params := &schemas.ToolFunctionParameters{}

	if typeVal, ok := m["type"].(string); ok {
		params.Type = typeVal
	}
	if desc, ok := m["description"].(string); ok {
		params.Description = &desc
	}
	if props, ok := schemas.SafeExtractOrderedMap(m["properties"]); ok {
		params.Properties = props
	}
	if req, ok := m["required"].([]interface{}); ok {
		required := make([]string, 0, len(req))
		for _, r := range req {
			if str, ok := r.(string); ok {
				required = append(required, str)
			}
		}
		params.Required = required
	}
	if addProps, ok := m["additionalProperties"]; ok {
		if addPropsBool, ok := addProps.(bool); ok {
			params.AdditionalProperties = &schemas.AdditionalPropertiesStruct{
				AdditionalPropertiesBool: &addPropsBool,
			}
		} else if addPropsMap, ok := schemas.SafeExtractOrderedMap(addProps); ok {
			params.AdditionalProperties = &schemas.AdditionalPropertiesStruct{
				AdditionalPropertiesMap: addPropsMap,
			}
		}
	}
	if defs, ok := schemas.SafeExtractOrderedMap(m["$defs"]); ok {
		params.Defs = defs
	}
	if definitions, ok := schemas.SafeExtractOrderedMap(m["definitions"]); ok {
		params.Definitions = definitions
	}
	if ref, ok := m["$ref"].(string); ok {
		params.Ref = &ref
	}
	if items, ok := schemas.SafeExtractOrderedMap(m["items"]); ok {
		params.Items = items
	}
	if minItems, ok := anthropicExtractInt64(m["minItems"]); ok {
		params.MinItems = schemas.Ptr(minItems)
	}
	if maxItems, ok := anthropicExtractInt64(m["maxItems"]); ok {
		params.MaxItems = schemas.Ptr(maxItems)
	}
	if anyOf, ok := m["anyOf"].([]interface{}); ok {
		anyOfMaps := make([]schemas.OrderedMap, 0, len(anyOf))
		for _, item := range anyOf {
			if orderedMap, ok := schemas.SafeExtractOrderedMap(item); ok {
				anyOfMaps = append(anyOfMaps, *orderedMap)
			}
		}
		if len(anyOfMaps) > 0 {
			params.AnyOf = anyOfMaps
		}
	}
	if oneOf, ok := m["oneOf"].([]interface{}); ok {
		oneOfMaps := make([]schemas.OrderedMap, 0, len(oneOf))
		for _, item := range oneOf {
			if orderedMap, ok := schemas.SafeExtractOrderedMap(item); ok {
				oneOfMaps = append(oneOfMaps, *orderedMap)
			}
		}
		if len(oneOfMaps) > 0 {
			params.OneOf = oneOfMaps
		}
	}
	if allOf, ok := m["allOf"].([]interface{}); ok {
		allOfMaps := make([]schemas.OrderedMap, 0, len(allOf))
		for _, item := range allOf {
			if orderedMap, ok := schemas.SafeExtractOrderedMap(item); ok {
				allOfMaps = append(allOfMaps, *orderedMap)
			}
		}
		if len(allOfMaps) > 0 {
			params.AllOf = allOfMaps
		}
	}
	if format, ok := m["format"].(string); ok {
		params.Format = &format
	}
	if pattern, ok := m["pattern"].(string); ok {
		params.Pattern = &pattern
	}
	if minLength, ok := anthropicExtractInt64(m["minLength"]); ok {
		params.MinLength = schemas.Ptr(minLength)
	}
	if maxLength, ok := anthropicExtractInt64(m["maxLength"]); ok {
		params.MaxLength = schemas.Ptr(maxLength)
	}
	if minimum, ok := anthropicExtractFloat64(m["minimum"]); ok {
		params.Minimum = &minimum
	}
	if maximum, ok := anthropicExtractFloat64(m["maximum"]); ok {
		params.Maximum = &maximum
	}
	if title, ok := m["title"].(string); ok {
		params.Title = &title
	}
	if enumVal, ok := m["enum"]; ok {
		switch e := enumVal.(type) {
		case []interface{}:
			enumStrs := make([]string, 0, len(e))
			for _, v := range e {
				if s, ok := v.(string); ok {
					enumStrs = append(enumStrs, s)
				}
			}
			if len(enumStrs) > 0 {
				params.Enum = enumStrs
			}
		case []string:
			if len(e) > 0 {
				params.Enum = e
			}
		}
	}
	if def, ok := m["default"]; ok {
		params.Default = def
	}
	if nullable, ok := m["nullable"].(bool); ok {
		params.Nullable = &nullable
	}

	if params.Type == "" {
		params.Type = "object"
	}

	return params
}

func MapBifrostServiceTierToAnthropicRequest(tier schemas.BifrostServiceTier) string {
	switch tier {
	case schemas.BifrostServiceTierAuto, schemas.BifrostServiceTierPriority:
		return "auto"
	case schemas.BifrostServiceTierDefault, schemas.BifrostServiceTierFlex:
		return "standard_only"
	default:
		return "auto"
	}
}

func MapAnthropicServiceTierToBifrost(tier string) schemas.BifrostServiceTier {
	switch tier {
	case "standard":
		return schemas.BifrostServiceTierDefault
	case "priority":
		return schemas.BifrostServiceTierPriority
	default:
		return schemas.BifrostServiceTier(tier)
	}
}

func ConvertAnthropicFinishReasonToBifrost(providerReason AnthropicStopReason) string {
	if bifrostReason, ok := anthropicFinishReasonToBifrost[providerReason]; ok {
		return bifrostReason
	}
	return string(providerReason)
}

func anthropicResponsesStatus(stopReason *string) (*string, *schemas.ResponsesResponseIncompleteDetails) {
	if stopReason == nil {
		return nil, nil
	}
	reason := *stopReason
	switch AnthropicStopReason(reason) {
	case AnthropicStopReasonRefusal:
		reason = "content_filter"
	case AnthropicStopReasonModelContextWindowExceeded:
		reason = string(schemas.BifrostFinishReasonLength)
	}
	status, details, mapped := schemas.ResponsesStatusFromFinishReason(reason)
	if !mapped {
		return nil, nil
	}
	return &status, details
}

func ConvertToAnthropicImageBlock(block schemas.ChatContentBlock) AnthropicContentBlock {
	imageBlock := AnthropicContentBlock{
		Type:         AnthropicContentBlockTypeImage,
		CacheControl: block.CacheControl,
		Source:       &AnthropicBlockSource{SourceObj: &AnthropicSource{}},
	}

	if block.ImageURLStruct == nil {
		return imageBlock
	}

	sanitizedURL, err := schemas.SanitizeImageURL(block.ImageURLStruct.URL)
	if err != nil {

		imageBlock.Source.SourceObj.Type = "url"
		imageBlock.Source.SourceObj.URL = &block.ImageURLStruct.URL
		return imageBlock
	}
	urlTypeInfo := schemas.ExtractURLTypeInfo(sanitizedURL)

	formattedImgContent := &AnthropicImageContent{
		Type: urlTypeInfo.Type,
	}

	if urlTypeInfo.MediaType != nil {
		formattedImgContent.MediaType = *urlTypeInfo.MediaType
	}

	if urlTypeInfo.DataURLWithoutPrefix != nil {
		formattedImgContent.URL = *urlTypeInfo.DataURLWithoutPrefix
	} else {
		formattedImgContent.URL = sanitizedURL
	}

	if formattedImgContent.Type == schemas.ImageContentTypeURL {
		imageBlock.Source.SourceObj.Type = "url"
		imageBlock.Source.SourceObj.URL = &formattedImgContent.URL
	} else {
		if formattedImgContent.MediaType != "" {
			imageBlock.Source.SourceObj.MediaType = &formattedImgContent.MediaType
		}
		imageBlock.Source.SourceObj.Type = "base64"

		if urlTypeInfo.DataURLWithoutPrefix != nil {
			imageBlock.Source.SourceObj.Data = urlTypeInfo.DataURLWithoutPrefix
		} else {
			imageBlock.Source.SourceObj.Data = &formattedImgContent.URL
		}
	}

	return imageBlock
}

func ConvertToAnthropicDocumentBlock(block schemas.ChatContentBlock) AnthropicContentBlock {
	documentBlock := AnthropicContentBlock{
		Type:         AnthropicContentBlockTypeDocument,
		CacheControl: block.CacheControl,
		Source:       &AnthropicBlockSource{SourceObj: &AnthropicSource{}},
	}

	if block.Citations != nil {
		documentBlock.Citations = &AnthropicCitations{Config: block.Citations}
	}

	if block.File == nil {
		return documentBlock
	}

	file := block.File

	if file.Filename != nil {
		documentBlock.Title = file.Filename
	}

	if file.FileID != nil && *file.FileID != "" {
		documentBlock.Source.SourceObj.Type = "file"
		documentBlock.Source.SourceObj.FileID = file.FileID
		return documentBlock
	}

	if file.FileURL != nil && *file.FileURL != "" {
		documentBlock.Source.SourceObj.Type = "url"
		documentBlock.Source.SourceObj.URL = file.FileURL
		return documentBlock
	}

	if file.FileData != nil && *file.FileData != "" {
		fileData := *file.FileData

		if source := inlineTextDataURL(fileData); source != nil {
			documentBlock.Source.SourceObj = source
			return documentBlock
		}

		if !strings.HasPrefix(fileData, "data:") && file.FileType != nil && (*file.FileType == "text/plain" || *file.FileType == "txt") {
			documentBlock.Source.SourceObj.Type = "text"
			documentBlock.Source.SourceObj.MediaType = schemas.Ptr("text/plain")
			documentBlock.Source.SourceObj.Data = &fileData
			return documentBlock
		}

		if strings.HasPrefix(fileData, "data:") {
			urlTypeInfo := schemas.ExtractURLTypeInfo(fileData)

			if urlTypeInfo.DataURLWithoutPrefix != nil {

				documentBlock.Source.SourceObj.Type = "base64"
				documentBlock.Source.SourceObj.Data = urlTypeInfo.DataURLWithoutPrefix

				if urlTypeInfo.MediaType != nil {
					documentBlock.Source.SourceObj.MediaType = urlTypeInfo.MediaType
				} else if file.FileType != nil {
					documentBlock.Source.SourceObj.MediaType = file.FileType
				}
				return documentBlock
			}
		}

		documentBlock.Source.SourceObj.Type = "base64"
		documentBlock.Source.SourceObj.Data = &fileData

		if file.FileType != nil {
			documentBlock.Source.SourceObj.MediaType = file.FileType
		} else {

			mediaType := "application/pdf"
			documentBlock.Source.SourceObj.MediaType = &mediaType
		}
		return documentBlock
	}

	return documentBlock
}

func ConvertResponsesFileBlockToAnthropic(fileBlock *schemas.ResponsesInputMessageContentBlockFile, fileID *string, cacheControl *schemas.CacheControl, citations *schemas.Citations) AnthropicContentBlock {
	documentBlock := AnthropicContentBlock{
		Type:         AnthropicContentBlockTypeDocument,
		CacheControl: cacheControl,
		Source:       &AnthropicBlockSource{SourceObj: &AnthropicSource{}},
	}

	if citations != nil {
		documentBlock.Citations = &AnthropicCitations{Config: citations}
	}

	if fileBlock != nil && fileBlock.Filename != nil {
		documentBlock.Title = fileBlock.Filename
	}

	if fileID != nil && *fileID != "" {
		documentBlock.Source.SourceObj.Type = "file"
		documentBlock.Source.SourceObj.FileID = fileID
		return documentBlock
	}

	if fileBlock == nil {
		return documentBlock
	}

	if fileBlock.FileData != nil && *fileBlock.FileData != "" {
		fileData := *fileBlock.FileData

		if source := inlineTextDataURL(fileData); source != nil {
			documentBlock.Source.SourceObj = source
			return documentBlock
		}

		if !strings.HasPrefix(fileData, "data:") && fileBlock.FileType != nil && (*fileBlock.FileType == "text/plain" || *fileBlock.FileType == "txt") {
			documentBlock.Source.SourceObj.Type = "text"
			documentBlock.Source.SourceObj.Data = &fileData
			documentBlock.Source.SourceObj.MediaType = schemas.Ptr("text/plain")
			return documentBlock
		}

		if strings.HasPrefix(fileData, "data:") {
			urlTypeInfo := schemas.ExtractURLTypeInfo(fileData)

			if urlTypeInfo.DataURLWithoutPrefix != nil {

				documentBlock.Source.SourceObj.Type = "base64"
				documentBlock.Source.SourceObj.Data = urlTypeInfo.DataURLWithoutPrefix

				if urlTypeInfo.MediaType != nil {
					documentBlock.Source.SourceObj.MediaType = urlTypeInfo.MediaType
				} else if fileBlock.FileType != nil {
					documentBlock.Source.SourceObj.MediaType = fileBlock.FileType
				}
				return documentBlock
			}
		}

		documentBlock.Source.SourceObj.Type = "base64"
		documentBlock.Source.SourceObj.Data = &fileData

		if fileBlock.FileType != nil {
			documentBlock.Source.SourceObj.MediaType = fileBlock.FileType
		} else {

			mediaType := "application/pdf"
			documentBlock.Source.SourceObj.MediaType = &mediaType
		}
		return documentBlock
	}

	if fileBlock.FileURL != nil && *fileBlock.FileURL != "" {
		documentBlock.Source.SourceObj.Type = "url"
		documentBlock.Source.SourceObj.URL = fileBlock.FileURL
		return documentBlock
	}

	return documentBlock
}

func getImageURLFromBlock(block AnthropicContentBlock) string {

	if block.Source == nil || block.Source.SourceObj == nil {
		return ""
	}
	src := block.Source.SourceObj

	if src.Data != nil {
		mime := "image/png"
		if src.MediaType != nil && *src.MediaType != "" {
			mime = *src.MediaType
		}
		return "data:" + mime + ";base64," + *src.Data
	}

	if src.URL != nil {
		return *src.URL
	}

	return ""
}

func parseJSONInput(jsonStr string) json.RawMessage {
	if jsonStr == "" || jsonStr == "{}" {
		return json.RawMessage("{}")
	}

	compacted := compactJSONBytes([]byte(jsonStr))
	if compacted != nil {
		return json.RawMessage(compacted)
	}

	return json.RawMessage(jsonStr)
}

func compactJSONBytes(data []byte) []byte {
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil
	}
	return buf.Bytes()
}

func extractTypesFromValue(typeVal interface{}) []string {
	switch t := typeVal.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []interface{}:
		types := make([]string, 0, len(t))
		for _, item := range t {
			if typeStr, ok := item.(string); ok {
				types = append(types, typeStr)
			}
		}
		return types
	default:
		return nil
	}
}

func filterEnumValuesByType(enumValues []interface{}, schemaType string) []interface{} {
	if len(enumValues) == 0 {
		return nil
	}

	filtered := make([]interface{}, 0, len(enumValues))
	for _, val := range enumValues {

		var actualType string
		switch val.(type) {
		case string:
			actualType = "string"
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			actualType = "integer"
		case float32, float64:

			if fv, ok := val.(float64); ok && fv == float64(int64(fv)) {
				actualType = "integer"
			} else {
				actualType = "number"
			}
		case bool:
			actualType = "boolean"
		case nil:
			actualType = "null"
		default:

			filtered = append(filtered, val)
			continue
		}

		if actualType == schemaType || (schemaType == "number" && actualType == "integer") {
			filtered = append(filtered, val)
		}
	}

	return filtered
}

func sjsonEscapeKey(k string) string {
	if !strings.ContainsAny(k, `.*?#\`) {
		return k
	}
	var b strings.Builder
	b.Grow(len(k) + 2)
	for _, r := range k {
		switch r {
		case '.', '*', '?', '#', '\\':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func filterEnumValuesByTypeRaw(values []gjson.Result, schemaType string) []gjson.Result {
	if len(values) == 0 {
		return nil
	}
	out := make([]gjson.Result, 0, len(values))
	for _, v := range values {
		var actual string
		switch v.Type {
		case gjson.String:
			actual = "string"
		case gjson.Number:
			f := v.Float()
			if f == float64(int64(f)) {
				actual = "integer"
			} else {
				actual = "number"
			}
		case gjson.True, gjson.False:
			actual = "boolean"
		case gjson.Null:
			actual = "null"
		case gjson.JSON:
			out = append(out, v)
			continue
		default:
			continue
		}
		if actual == schemaType || (schemaType == "number" && actual == "integer") {
			out = append(out, v)
		}
	}
	return out
}

func NormalizeSchemaForAnthropicRaw(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return schema
	}
	if !gjson.ParseBytes(schema).IsObject() {
		return schema
	}

	body := append([]byte(nil), schema...)

	if typeVal := gjson.GetBytes(body, "type"); typeVal.IsArray() {
		var types []string
		for _, t := range typeVal.Array() {
			types = append(types, t.String())
		}
		nonNullTypes := make([]string, 0, len(types))
		for _, t := range types {
			if t != "null" {
				nonNullTypes = append(nonNullTypes, t)
			}
		}

		switch {
		case len(nonNullTypes) == 0:
			body, _ = sjson.SetBytes(body, "type", "null")
		case len(nonNullTypes) == 1 && len(types) == 1:
			body, _ = sjson.SetBytes(body, "type", nonNullTypes[0])
		default:
			body, _ = sjson.DeleteBytes(body, "type")

			enumVal := gjson.GetBytes(body, "enum")
			hasEnum := enumVal.Exists() && enumVal.IsArray()
			var enumArr []gjson.Result
			if hasEnum {
				enumArr = enumVal.Array()
			}

			anyOf := []byte("[]")
			i := 0
			for _, t := range nonNullTypes {
				branch := []byte(`{}`)
				branch, _ = sjson.SetBytes(branch, "type", t)
				if hasEnum {
					filtered := filterEnumValuesByTypeRaw(enumArr, t)
					if len(filtered) > 0 {
						enumOut := []byte("[]")
						for j, v := range filtered {
							enumOut, _ = sjson.SetRawBytes(enumOut, fmt.Sprintf("%d", j), []byte(v.Raw))
						}
						branch, _ = sjson.SetRawBytes(branch, "enum", enumOut)
					}
				}
				anyOf, _ = sjson.SetRawBytes(anyOf, fmt.Sprintf("%d", i), branch)
				i++
			}
			if len(nonNullTypes) < len(types) {
				nullBranch, _ := sjson.SetBytes([]byte(`{}`), "type", "null")
				anyOf, _ = sjson.SetRawBytes(anyOf, fmt.Sprintf("%d", i), nullBranch)
			}
			body, _ = sjson.SetRawBytes(body, "anyOf", anyOf)
			body, _ = sjson.DeleteBytes(body, "enum")
		}
	}

	for _, key := range []string{"properties", "definitions", "$defs"} {
		val := gjson.GetBytes(body, key)
		if !val.IsObject() {
			continue
		}
		newObj := []byte("{}")
		val.ForEach(func(k, v gjson.Result) bool {
			child := []byte(v.Raw)
			if v.IsObject() {
				child = NormalizeSchemaForAnthropicRaw(child)
			}
			newObj, _ = sjson.SetRawBytes(newObj, sjsonEscapeKey(k.String()), child)
			return true
		})
		body, _ = sjson.SetRawBytes(body, sjsonEscapeKey(key), newObj)
	}

	if items := gjson.GetBytes(body, "items"); items.IsObject() {
		body, _ = sjson.SetRawBytes(body, "items", NormalizeSchemaForAnthropicRaw([]byte(items.Raw)))
	}

	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		arr := gjson.GetBytes(body, key)
		if !arr.IsArray() {
			continue
		}
		newArr := []byte("[]")
		for i, item := range arr.Array() {
			child := []byte(item.Raw)
			if item.IsObject() {
				child = NormalizeSchemaForAnthropicRaw(child)
			}
			newArr, _ = sjson.SetRawBytes(newArr, fmt.Sprintf("%d", i), child)
		}
		body, _ = sjson.SetRawBytes(body, key, newArr)
	}

	return body
}

func normalizeSchemaForAnthropic(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}

	normalized := make(map[string]interface{})
	for k, v := range schema {
		normalized[k] = v
	}

	if typeVal, exists := normalized["type"]; exists {
		types := extractTypesFromValue(typeVal)
		if len(types) > 0 {
			nonNullTypes := make([]string, 0, len(types))
			for _, t := range types {
				if t != "null" {
					nonNullTypes = append(nonNullTypes, t)
				}
			}

			if len(nonNullTypes) == 0 {

				normalized["type"] = "null"
			} else if len(nonNullTypes) == 1 && len(types) == 1 {

				normalized["type"] = nonNullTypes[0]
			} else {

				delete(normalized, "type")

				anyOfSchemas := make([]interface{}, 0, len(types))
				for _, t := range nonNullTypes {
					typeSchema := map[string]interface{}{"type": t}

					if enumVal, hasEnum := normalized["enum"]; hasEnum {

						var enumArray []interface{}
						switch e := enumVal.(type) {
						case []interface{}:
							enumArray = e
						case []string:
							enumArray = make([]interface{}, len(e))
							for i, v := range e {
								enumArray[i] = v
							}
						default:

							typeSchema["enum"] = enumVal
							anyOfSchemas = append(anyOfSchemas, typeSchema)
							continue
						}

						filteredEnum := filterEnumValuesByType(enumArray, t)
						if len(filteredEnum) > 0 {
							typeSchema["enum"] = filteredEnum
						}
					}

					anyOfSchemas = append(anyOfSchemas, typeSchema)
				}

				if len(nonNullTypes) < len(types) {
					anyOfSchemas = append(anyOfSchemas, map[string]interface{}{"type": "null"})
				}

				normalized["anyOf"] = anyOfSchemas

				delete(normalized, "enum")
			}
		}
	}

	switch properties := schema["properties"].(type) {
	case map[string]interface{}:
		newProps := make(map[string]interface{})
		for key, prop := range properties {
			newProps[key] = normalizeSchemaValueForAnthropic(prop)
		}
		normalized["properties"] = newProps
	case *schemas.OrderedMap:
		newProps := schemas.NewOrderedMapWithCapacity(properties.Len())
		properties.Range(func(key string, prop interface{}) bool {
			newProps.Set(key, normalizeSchemaValueForAnthropic(prop))
			return true
		})
		normalized["properties"] = newProps
	case schemas.OrderedMap:
		newProps := schemas.NewOrderedMapWithCapacity(properties.Len())
		properties.Range(func(key string, prop interface{}) bool {
			newProps.Set(key, normalizeSchemaValueForAnthropic(prop))
			return true
		})
		normalized["properties"] = newProps
	}

	switch schema["items"].(type) {
	case map[string]interface{}, *schemas.OrderedMap, schemas.OrderedMap:
		normalized["items"] = normalizeSchemaValueForAnthropic(schema["items"])
	}

	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		switch schema[key].(type) {
		case []interface{}, []schemas.OrderedMap:
			normalized[key] = normalizeSchemaValueForAnthropic(schema[key])
		}
	}

	switch definitions := schema["definitions"].(type) {
	case map[string]interface{}:
		newDefs := make(map[string]interface{})
		for key, def := range definitions {
			newDefs[key] = normalizeSchemaValueForAnthropic(def)
		}
		normalized["definitions"] = newDefs
	case *schemas.OrderedMap:
		newDefs := schemas.NewOrderedMapWithCapacity(definitions.Len())
		definitions.Range(func(key string, def interface{}) bool {
			newDefs.Set(key, normalizeSchemaValueForAnthropic(def))
			return true
		})
		normalized["definitions"] = newDefs
	case schemas.OrderedMap:
		newDefs := schemas.NewOrderedMapWithCapacity(definitions.Len())
		definitions.Range(func(key string, def interface{}) bool {
			newDefs.Set(key, normalizeSchemaValueForAnthropic(def))
			return true
		})
		normalized["definitions"] = newDefs
	}

	switch defs := schema["$defs"].(type) {
	case map[string]interface{}:
		newDefs := make(map[string]interface{})
		for key, def := range defs {
			newDefs[key] = normalizeSchemaValueForAnthropic(def)
		}
		normalized["$defs"] = newDefs
	case *schemas.OrderedMap:
		newDefs := schemas.NewOrderedMapWithCapacity(defs.Len())
		defs.Range(func(key string, def interface{}) bool {
			newDefs.Set(key, normalizeSchemaValueForAnthropic(def))
			return true
		})
		normalized["$defs"] = newDefs
	case schemas.OrderedMap:
		newDefs := schemas.NewOrderedMapWithCapacity(defs.Len())
		defs.Range(func(key string, def interface{}) bool {
			newDefs.Set(key, normalizeSchemaValueForAnthropic(def))
			return true
		})
		normalized["$defs"] = newDefs
	}

	return normalized
}

func normalizeSchemaValueForAnthropic(v interface{}) interface{} {
	switch tv := v.(type) {
	case []interface{}:
		out := make([]interface{}, len(tv))
		for i, item := range tv {
			out[i] = normalizeSchemaValueForAnthropic(item)
		}
		return out
	case []schemas.OrderedMap:
		out := make([]schemas.OrderedMap, len(tv))
		for i := range tv {
			if normalized := normalizeOrderedSchemaForAnthropic(&tv[i]); normalized != nil {
				out[i] = *normalized
			} else {
				out[i] = tv[i]
			}
		}
		return out
	case map[string]interface{}:
		return normalizeSchemaForAnthropic(tv)
	case *schemas.OrderedMap:
		return normalizeOrderedSchemaForAnthropic(tv)
	case schemas.OrderedMap:
		if normalized := normalizeOrderedSchemaForAnthropic(&tv); normalized != nil {
			return *normalized
		}
		return tv
	}
	return v
}

func normalizeOrderedSchemaForAnthropic(om *schemas.OrderedMap) *schemas.OrderedMap {
	if om == nil {
		return nil
	}
	normalized := normalizeSchemaForAnthropic(om.ToMap())
	out := schemas.NewOrderedMapWithCapacity(om.Len())
	for _, key := range om.Keys() {
		if value, ok := normalized[key]; ok {
			out.Set(key, value)
			delete(normalized, key)
		}
	}
	added := make([]string, 0, len(normalized))
	for key := range normalized {
		added = append(added, key)
	}
	sort.Strings(added)
	for _, key := range added {
		out.Set(key, normalized[key])
	}
	return out
}

func convertChatResponseFormatToAnthropicOutputFormat(responseFormat *interface{}) json.RawMessage {
	if responseFormat == nil {
		return nil
	}

	rf, ok := schemas.ParseChatResponseFormat(responseFormat)
	if !ok || rf.Type != "json_schema" || !rf.HasJSONSchema() {
		return nil
	}

	outputFormat := map[string]interface{}{
		"type": rf.Type,
	}

	if schema := rf.RawSchema(); len(schema) > 0 {
		outputFormat["schema"] = NormalizeSchemaForAnthropicRaw(schema)
	}

	result, err := providerUtils.MarshalSorted(outputFormat)
	if err != nil {
		return nil
	}
	return json.RawMessage(result)
}

func convertResponsesTextConfigToAnthropicOutputFormat(textConfig *schemas.ResponsesTextConfig) json.RawMessage {
	if textConfig == nil || textConfig.Format == nil {
		return nil
	}

	format := textConfig.Format

	if format.Type != "json_schema" {
		return nil
	}

	outputFormat := map[string]interface{}{
		"type": format.Type,
	}

	if format.JSONSchema != nil {

		schema := map[string]interface{}{}

		if format.JSONSchema.Type != nil {
			schema["type"] = *format.JSONSchema.Type
		}

		if format.JSONSchema.Properties != nil {
			schema["properties"] = *format.JSONSchema.Properties
		}

		if len(format.JSONSchema.Required) > 0 {
			schema["required"] = format.JSONSchema.Required
		}

		if format.JSONSchema.Defs != nil {
			schema["$defs"] = *format.JSONSchema.Defs
		}

		if format.JSONSchema.Definitions != nil {
			schema["definitions"] = *format.JSONSchema.Definitions
		}

		if format.JSONSchema.Type != nil && *format.JSONSchema.Type == "object" {
			schema["additionalProperties"] = false
		} else if format.JSONSchema.AdditionalProperties != nil {
			schema["additionalProperties"] = *format.JSONSchema.AdditionalProperties
		}

		normalizedSchema := normalizeSchemaForAnthropic(schema)
		outputFormat["schema"] = normalizedSchema
	}

	result, err := providerUtils.MarshalSorted(outputFormat)
	if err != nil {
		return nil
	}
	return json.RawMessage(result)
}

func sanitizeWebSearchArguments(argumentsJSON string) string {
	var toolArgs map[string]interface{}
	if err := sonic.Unmarshal([]byte(argumentsJSON), &toolArgs); err != nil {
		return argumentsJSON
	}

	allowedVal, hasAllowed := toolArgs["allowed_domains"]
	blockedVal, hasBlocked := toolArgs["blocked_domains"]

	if hasAllowed && hasBlocked {

		isEmptyArray := func(val interface{}) bool {
			if arr, ok := val.([]interface{}); ok {
				return len(arr) == 0
			}
			return false
		}

		allowedEmpty := isEmptyArray(allowedVal)
		blockedEmpty := isEmptyArray(blockedVal)

		var shouldDelete string
		if allowedEmpty && !blockedEmpty {

			shouldDelete = "allowed_domains"
		} else if blockedEmpty && !allowedEmpty {

			shouldDelete = "blocked_domains"
		} else {

			shouldDelete = "blocked_domains"
		}

		delete(toolArgs, shouldDelete)

		if sanitizedBytes, err := providerUtils.MarshalSorted(toolArgs); err == nil {
			return string(sanitizedBytes)
		}
	}

	return argumentsJSON
}

func attachWebSearchSourcesToCall(bifrostMessages []schemas.ResponsesMessage, toolUseID string, resultBlock AnthropicContentBlock, includeExtendedFields bool) {

	for i := len(bifrostMessages) - 1; i >= 0; i-- {
		msg := &bifrostMessages[i]
		if msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeWebSearchCall &&
			msg.ID != nil &&
			*msg.ID == toolUseID {

			if msg.ResponsesToolMessage == nil {
				msg.ResponsesToolMessage = &schemas.ResponsesToolMessage{}
			}

			if resultBlock.Content != nil && len(resultBlock.Content.ContentBlocks) > 0 {
				sources := extractWebSearchSources(resultBlock.Content.ContentBlocks, includeExtendedFields)

				if msg.ResponsesToolMessage.Action == nil {
					msg.ResponsesToolMessage.Action = &schemas.ResponsesToolMessageActionStruct{}
				}
				if msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction == nil {
					msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction = &schemas.ResponsesWebSearchToolCallAction{
						Type: "search",
					}
				}
				msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction.Sources = sources
			}
			break
		}
	}
}

func attachToolSearchReferencesToCall(bifrostMessages []schemas.ResponsesMessage, toolUseID string, resultBlock AnthropicContentBlock) {
	for i := len(bifrostMessages) - 1; i >= 0; i-- {
		msg := &bifrostMessages[i]
		if msg.Type == nil || *msg.Type != schemas.ResponsesMessageTypeToolSearchCall ||
			msg.ID == nil || *msg.ID != toolUseID {
			continue
		}
		var refs []string
		for _, ref := range resultBlock.DiscoveredToolReferences() {
			if ref.ToolName != nil {
				refs = append(refs, *ref.ToolName)
			} else if ref.Name != nil {
				refs = append(refs, *ref.Name)
			}
		}
		if msg.ResponsesToolMessage == nil {
			msg.ResponsesToolMessage = &schemas.ResponsesToolMessage{}
		}
		msg.ResponsesToolMessage.ResponsesToolSearchCall = &schemas.ResponsesToolSearchCall{ToolReferences: refs}
		return
	}
}

func extractWebSearchSources(contentBlocks []AnthropicContentBlock, includeExtendedFields bool) []schemas.ResponsesWebSearchToolCallActionSearchSource {
	sources := make([]schemas.ResponsesWebSearchToolCallActionSearchSource, 0, len(contentBlocks))

	for _, result := range contentBlocks {
		if result.Type == AnthropicContentBlockTypeWebSearchResult && result.URL != nil {
			source := schemas.ResponsesWebSearchToolCallActionSearchSource{
				Type: "url",
				URL:  *result.URL,
			}

			if includeExtendedFields {
				source.EncryptedContent = result.EncryptedContent
				source.PageAge = result.PageAge

				if result.Title != nil {
					source.Title = result.Title
				} else {
					source.Title = schemas.Ptr(*result.URL)
				}
			}

			sources = append(sources, source)
		}
	}

	return sources
}

func anthropicExtractInt64(v interface{}) (int64, bool) {
	switch val := v.(type) {
	case int:
		return int64(val), true
	case int64:
		return val, true
	case float64:
		return int64(val), true
	case float32:
		return int64(val), true
	default:
		return 0, false
	}
}

func anthropicExtractFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}

func IsClaudeCodeMaxMode(ctx *schemas.BifrostContext) bool {
	userAgent, _ := ctx.Value(schemas.BifrostContextKeyUserAgent).(string)
	skipKeySelection, _ := ctx.Value(schemas.BifrostContextKeySkipKeySelection).(bool)
	return schemas.ClaudeCLI.Matches(userAgent) && skipKeySelection
}

func IsClaudeCodeRequest(ctx *schemas.BifrostContext) bool {
	if userAgent, ok := ctx.Value(schemas.BifrostContextKeyUserAgent).(string); ok {
		return schemas.ClaudeCLI.Matches(userAgent)
	}
	return false
}

func ResolveUseAnthropicEndpoints(ctx *schemas.BifrostContext, key schemas.Key) bool {
	if ra := schemas.GetResolvedAlias(ctx); ra != nil && ra.Config != nil && ra.Config.UseAnthropicEndpoints != nil {
		return *ra.Config.UseAnthropicEndpoints
	}
	return key.UseAnthropicEndpoints != nil && *key.UseAnthropicEndpoints
}
