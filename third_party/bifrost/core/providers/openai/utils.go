package openai

import (
	"strings"

	"github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type responseHandler[T any] func(responseBody []byte, response *T, requestBody []byte, sendBackRawRequest bool, sendBackRawResponse bool) (rawRequest interface{}, rawResponse interface{}, bifrostErr *schemas.BifrostError)

func defaultSupportsReasoningContentBlocks(model string) bool {
	return strings.Contains(strings.ToLower(model), "gpt-oss")
}

func IsOpenAIReasoningModel(model string) bool {
	_, parsedModel := schemas.ParseModelString(model, schemas.OpenAI)
	if parsedModel != "" {
		model = parsedModel
	}
	modelLower := strings.ToLower(model)
	if strings.Contains(modelLower, "gpt-oss") {
		return true
	}
	for _, prefix := range []string{"o1", "o3", "o4"} {
		if strings.HasPrefix(modelLower, prefix) {
			if len(modelLower) == len(prefix) ||
				modelLower[len(prefix)] == '-' ||
				modelLower[len(prefix)] == '_' {
				return true
			}
		}
		if strings.Contains(modelLower, "-"+prefix+"-") ||
			strings.Contains(modelLower, "_"+prefix+"_") ||
			strings.HasSuffix(modelLower, "-"+prefix) ||
			strings.HasSuffix(modelLower, "_"+prefix) {
			return true
		}
	}
	return strings.Contains(modelLower, "gpt-5") || strings.Contains(modelLower, "gpt-6")
}

func defaultCanDisableReasoning(model string) bool {
	m := bareModelLower(model)
	switch {
	case strings.Contains(m, "gpt-6-sol"), strings.Contains(m, "gpt-6-luna"):
		return true
	case strings.Contains(m, "gpt-6"), acceptsMinimalEffort(m):
		return false
	case strings.Contains(m, "gpt-5"):
		return !strings.Contains(m, "-pro")
	default:
		return !IsOpenAIReasoningModel(m) || strings.Contains(m, "gpt-oss")
	}
}

func SupportsToolSearch(provider schemas.ModelProvider, model string) bool {
	return schemas.ResolveModelCaps(provider, model).SupportsToolSearch(defaultSupportsToolSearch(provider, model))
}

func defaultSupportsToolSearch(provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.OpenAI, schemas.Azure, schemas.Bedrock, schemas.BedrockMantle:
	default:
		return false
	}
	m := bareModelLower(model)
	return strings.Contains(m, "gpt-5.4") ||
		strings.Contains(m, "gpt-5.5") ||
		strings.Contains(m, "gpt-5.6") ||
		strings.Contains(m, "gpt-6")
}

func defaultSupportsAsyncTools(model string) bool {
	return strings.Contains(bareModelLower(model), "gpt-6")
}

func defaultReasoningEffort(model string) string {
	if !IsOpenAIReasoningModel(model) {
		return ""
	}
	m := bareModelLower(model)
	switch {
	case strings.Contains(m, "-pro"):
		return schemas.ReasoningEffortHigh
	case strings.Contains(m, "gpt-5.1"), strings.Contains(m, "gpt-5.2"),
		strings.Contains(m, "gpt-5.3"), strings.Contains(m, "gpt-5.4"):
		return schemas.ReasoningEffortNone
	default:
		return schemas.ReasoningEffortMedium
	}
}

func omittedEffortReasons(caps schemas.ModelCaps, model string) bool {
	effort := caps.DefaultReasoningEffort(defaultReasoningEffort(model))
	return effort != "" && effort != schemas.ReasoningEffortNone
}

func defaultEffortControl(provider schemas.ModelProvider, model string) *schemas.EffortControl {
	levels := []string{schemas.ReasoningEffortLow, schemas.ReasoningEffortMedium, schemas.ReasoningEffortHigh}

	if acceptsMinimalEffort(model) || provider == schemas.BytePlus {
		levels = append([]string{schemas.ReasoningEffortMinimal}, levels...)
	}
	if includeXHighInDefaultLadder(provider, model) {
		levels = append(levels, schemas.ReasoningEffortXHigh)
	}

	if acceptsMaxEffort(model) || provider == schemas.MiniMax {
		levels = append(levels, schemas.ReasoningEffortMax)
	}
	return &schemas.EffortControl{Levels: levels}
}

func includeXHighInDefaultLadder(provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.OpenAI, schemas.Azure, schemas.XAI:
		return acceptsXHighEffort(model)
	case schemas.BytePlus:

		return false
	default:
		return true
	}
}

func normalizeReasoningEffort(provider schemas.ModelProvider, caps schemas.ModelCaps, effort string) string {
	if provider == "" {
		provider = caps.Provider()
	}
	return caps.NormalizeReasoningEffort(effort, defaultEffortControl(provider, caps.Model()))
}

func acceptsXHighEffort(model string) bool {
	modelLower := bareModelLower(model)
	if schemas.SupportsGrokXHighReasoningEffort(modelLower) {
		return true
	}

	return strings.Contains(modelLower, "gpt-5.2") ||
		strings.Contains(modelLower, "gpt-5.3-codex") ||
		strings.Contains(modelLower, "gpt-5.4") ||
		strings.Contains(modelLower, "gpt-5.5") ||
		strings.Contains(modelLower, "gpt-5.6") ||
		strings.Contains(modelLower, "gpt-6")
}

func defaultReasoningContexts(model string) []string {
	contexts := []string{schemas.ReasoningContextAuto, schemas.ReasoningContextCurrentTurn}
	if acceptsAllTurnsContext(model) {
		contexts = append(contexts, schemas.ReasoningContextAllTurns)
	}
	return contexts
}

func acceptsAllTurnsContext(model string) bool {
	m := bareModelLower(model)
	return strings.Contains(m, "gpt-5.4") ||
		strings.Contains(m, "gpt-5.5") ||
		strings.Contains(m, "gpt-5.6") ||
		strings.Contains(m, "gpt-6")
}

func acceptsMinimalEffort(model string) bool {
	m := bareModelLower(model)
	i := strings.Index(m, "gpt-5")
	if i < 0 {
		return false
	}
	rest := strings.TrimPrefix(m[i+len("gpt-5"):], "-mini")
	rest = strings.TrimPrefix(rest, "-nano")

	return rest == "" || strings.HasPrefix(rest, "-2")
}

func acceptsMaxEffort(model string) bool {
	modelLower := bareModelLower(model)
	return strings.Contains(modelLower, "gpt-5.6") ||
		strings.Contains(modelLower, "gpt-6") ||
		strings.Contains(modelLower, "deepseek-v4") ||
		strings.Contains(modelLower, "glm-5.2")
}

func bareModelLower(model string) string {
	if _, parsed := schemas.ParseModelString(model, schemas.OpenAI); parsed != "" {
		model = parsed
	}
	return strings.ToLower(model)
}

func ConvertOpenAIMessagesToBifrostMessages(messages []OpenAIMessage) []schemas.ChatMessage {
	bifrostMessages := make([]schemas.ChatMessage, len(messages))
	for i, message := range messages {
		bifrostMessages[i] = schemas.ChatMessage{
			Name:            message.Name,
			Role:            message.Role,
			Content:         message.Content,
			ChatToolMessage: message.ChatToolMessage,
		}
		if message.OpenAIChatAssistantMessage != nil {

			reasoning := message.OpenAIChatAssistantMessage.Reasoning
			if reasoning == nil {
				reasoning = message.OpenAIChatAssistantMessage.ReasoningAlias
			}
			if reasoning == nil {
				for _, detail := range message.OpenAIChatAssistantMessage.ReasoningDetails {
					if detail.Text != nil {
						reasoning = detail.Text
						break
					}
				}
			}
			bifrostMessages[i].ChatAssistantMessage = &schemas.ChatAssistantMessage{
				Refusal:          message.OpenAIChatAssistantMessage.Refusal,
				Reasoning:        reasoning,
				ReasoningDetails: message.OpenAIChatAssistantMessage.ReasoningDetails,
				Annotations:      message.OpenAIChatAssistantMessage.Annotations,
				ToolCalls:        message.OpenAIChatAssistantMessage.ToolCalls,
			}
		}
	}
	return bifrostMessages
}

func ConvertBifrostMessagesToOpenAIMessages(messages []schemas.ChatMessage) []OpenAIMessage {
	openaiMessages := make([]OpenAIMessage, len(messages))
	for i, message := range messages {
		openaiMessages[i] = OpenAIMessage{
			Name:            message.Name,
			Role:            message.Role,
			Content:         message.Content,
			ChatToolMessage: message.ChatToolMessage,
		}

		if message.Role == schemas.ChatMessageRoleTool {
			openaiMessages[i].Content = schemas.FlattenToolResultMedia(message.Content, nil)
		}

		if message.ChatToolMessage != nil && message.ChatToolMessage.ToolCallID != nil &&
			len(*message.ChatToolMessage.ToolCallID) > MaxToolCallIDLength {
			if stripped := utils.StripThoughtSignature(*message.ChatToolMessage.ToolCallID); stripped != *message.ChatToolMessage.ToolCallID {
				toolMsgCopy := *message.ChatToolMessage
				toolMsgCopy.ToolCallID = &stripped
				openaiMessages[i].ChatToolMessage = &toolMsgCopy
			}
		}

		if openaiMessages[i].ChatToolMessage != nil && openaiMessages[i].ChatToolMessage.IsError != nil {
			toolMsgCopy := *openaiMessages[i].ChatToolMessage
			toolMsgCopy.IsError = nil
			openaiMessages[i].ChatToolMessage = &toolMsgCopy
		}
		if message.ChatAssistantMessage != nil {

			toolCalls := message.ChatAssistantMessage.ToolCalls
			needsStrip := false
			for j := range toolCalls {
				if toolCalls[j].ID != nil && len(*toolCalls[j].ID) > MaxToolCallIDLength &&
					strings.Contains(*toolCalls[j].ID, utils.ThoughtSignatureSeparator) {
					needsStrip = true
					break
				}
			}
			if needsStrip {
				cloned := make([]schemas.ChatAssistantMessageToolCall, len(toolCalls))
				copy(cloned, toolCalls)
				for j := range cloned {
					if cloned[j].ID != nil && len(*cloned[j].ID) > MaxToolCallIDLength {
						stripped := utils.StripThoughtSignature(*cloned[j].ID)
						cloned[j].ID = &stripped
					}
				}
				toolCalls = cloned
			}
			openaiMessages[i].OpenAIChatAssistantMessage = &OpenAIChatAssistantMessage{
				Refusal:     message.ChatAssistantMessage.Refusal,
				Reasoning:   message.ChatAssistantMessage.Reasoning,
				Annotations: message.ChatAssistantMessage.Annotations,
				ToolCalls:   toolCalls,
			}
		}
	}
	return openaiMessages
}

const MaxUserFieldLength = 64

const MaxToolCallIDLength = 64

func SanitizeUserField(user *string) *string {
	if user != nil && len(*user) > MaxUserFieldLength {
		return nil
	}
	return user
}

func toolChoiceAnySupported(provider schemas.ModelProvider, model string) bool {
	switch provider {
	case schemas.Mistral, schemas.Fireworks:
		return true
	case schemas.Vertex:
		return schemas.IsMistralModel(model)
	default:
		return false
	}
}

func wrapSystemReminder(text string) string {
	return "<system-reminder>\n" + text + "\n</system-reminder>\n"
}

func inlineChatMidConversationSystem(messages []OpenAIMessage) []OpenAIMessage {
	leading := true
	for i := range messages {
		m := &messages[i]
		isSystem := m.Role == schemas.ChatMessageRoleSystem || m.Role == schemas.ChatMessageRoleDeveloper
		if leading && isSystem {
			continue
		}
		leading = false
		if !isSystem {
			continue
		}
		if m.Content == nil {
			*m = OpenAIMessage{Name: m.Name, Role: schemas.ChatMessageRoleUser}
			continue
		}
		content := &schemas.ChatMessageContent{}
		if m.Content.ContentStr != nil {
			content.ContentStr = new(wrapSystemReminder(*m.Content.ContentStr))
		}
		for _, block := range m.Content.ContentBlocks {
			if block.Text != nil {
				block.Text = new(wrapSystemReminder(*block.Text))
			}
			content.ContentBlocks = append(content.ContentBlocks, block)
		}
		*m = OpenAIMessage{Name: m.Name, Role: schemas.ChatMessageRoleUser, Content: content}
	}
	return messages
}

func inlineResponsesMidConversationSystem(messages []schemas.ResponsesMessage) []schemas.ResponsesMessage {
	leading := true
	for i := range messages {
		m := &messages[i]

		if m.Type != nil && (*m.Type == schemas.ResponsesMessageTypeAdditionalTools || *m.Type == schemas.ResponsesMessageTypeConfigurationUpdate) {
			continue
		}
		isSystem := m.Role != nil &&
			(*m.Role == schemas.ResponsesInputMessageRoleSystem || *m.Role == schemas.ResponsesInputMessageRoleDeveloper) &&
			(m.Type == nil || *m.Type == schemas.ResponsesMessageTypeMessage)
		if leading && isSystem {
			continue
		}
		leading = false
		if !isSystem {
			continue
		}
		m.Role = schemas.Ptr(schemas.ResponsesInputMessageRoleUser)
		if m.Content == nil {
			continue
		}
		content := &schemas.ResponsesMessageContent{}
		if m.Content.ContentStr != nil {
			content.ContentStr = schemas.Ptr(wrapSystemReminder(*m.Content.ContentStr))
		}
		for _, block := range m.Content.ContentBlocks {
			if block.Text != nil {
				block.Text = schemas.Ptr(wrapSystemReminder(*block.Text))
			}
			content.ContentBlocks = append(content.ContentBlocks, block)
		}
		m.Content = content
	}
	return messages
}
