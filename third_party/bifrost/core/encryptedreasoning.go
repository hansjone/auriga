// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package bifrost

import (
	"bytes"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var encryptedReasoningFieldMarkers = []string{
	"encrypted_content",
	"encrypted content",
	"redacted_thinking",
	"thought_signature",
	"thoughtsignature",
	"reasoningcontent",
}
var reasoningTokenWords = []string{"encrypted", "reasoning", "thinking", "thought"}
var reasoningConfigParams = []string{
	"reasoning.effort",
	"reasoning_effort",
	"reasoning.summary",
	"reasoning_summary",
	"budget_tokens",
	"thinking.type",
	"thinking_budget",
	"thinkingbudget",
	"thinking_level",
	"thinkinglevel",
	"include_thoughts",
	"includethoughts",
}
var unsupportedFieldMarkers = []string{
	"does not support",
	"doesn't support",
}
var unverifiablePayloadMarkers = []string{
	"invalid",
	"could not be verified",
	"cannot be verified",
	"could not be decrypted",
	"failed to verify",
	"malformed",
	"missing recognized prefix",
	"unrecognized prefix",
}
var contentBlockReasoningCarriers = []string{"signature", "encrypted_content"}
var detachableItemTypes = map[string]bool{
	string(schemas.ResponsesMessageTypeReasoning):      true,
	string(schemas.ResponsesMessageTypeMessage):        true,
	string(schemas.ResponsesMessageTypeFunctionCall):   true,
	string(schemas.ResponsesMessageTypeCustomToolCall): true,
	string(schemas.ResponsesMessageTypeWebSearchCall):  true,
	string(schemas.ResponsesMessageTypeComputerCall):   true,
	string(schemas.ResponsesMessageTypeMCPCall):        true,
	string(schemas.ResponsesMessageTypeMCPListTools):   true,
}

const encryptedContentErrorCode = "invalid_encrypted_content"

func namesEncryptedReasoningField(message string) bool {
	for _, marker := range encryptedReasoningFieldMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}

	if !strings.Contains(message, "signature") {
		return false
	}
	return strings.Contains(message, "reasoning") ||
		strings.Contains(message, "thinking") ||
		strings.Contains(message, "thought")
}

func containsAnyMarker(message string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func shouldStripReasoningAfterClientError(err *schemas.BifrostError) bool {
	if err == nil || err.Error == nil || err.StatusCode == nil || *err.StatusCode != 400 {
		return false
	}
	message := strings.ToLower(err.Error.Message)
	if strings.Contains(message, "cannot be modified") {
		return false
	}
	if containsAnyMarker(message, reasoningConfigParams) && !namesEncryptedReasoningField(message) {
		return false
	}
	return containsAnyMarker(message, reasoningTokenWords)
}

func isEncryptedReasoningRejection(err *schemas.BifrostError) bool {
	if err == nil || err.Error == nil {
		return false
	}
	if err.StatusCode == nil || *err.StatusCode != 400 {
		return false
	}
	if err.Error.Code != nil && strings.Contains(*err.Error.Code, encryptedContentErrorCode) {
		return true
	}
	message := strings.ToLower(err.Error.Message)
	if strings.Contains(message, encryptedContentErrorCode) ||
		(strings.Contains(message, "encrypted content") && strings.Contains(message, "could not be verified")) {
		return true
	}

	if strings.Contains(message, "cannot be modified") {
		return false
	}

	if strings.Contains(message, "encrypted reasoning was created for a different account or model") {
		return true
	}

	if !namesEncryptedReasoningField(message) {
		return false
	}
	return containsAnyMarker(message, unverifiablePayloadMarkers) ||
		containsAnyMarker(message, unsupportedFieldMarkers)
}

func encryptedReasoningCarriers(req *schemas.BifrostRequest) (input *[]schemas.ResponsesMessage, rawBody *[]byte) {
	if req == nil {
		return nil, nil
	}
	switch {
	case req.ResponsesRequest != nil:
		return &req.ResponsesRequest.Input, &req.ResponsesRequest.RawRequestBody
	case req.CountTokensRequest != nil:
		return &req.CountTokensRequest.Input, &req.CountTokensRequest.RawRequestBody
	case req.CompactionRequest != nil:
		return &req.CompactionRequest.Input, &req.CompactionRequest.RawRequestBody
	default:
		return nil, nil
	}
}

func stripUnverifiableReasoning(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) bool {
	if req == nil {
		return false
	}
	if req.ChatRequest != nil {
		return stripChatUnverifiableReasoning(ctx, req.ChatRequest)
	}
	return stripResponsesEncryptedContent(ctx, req)
}

func stripChatUnverifiableReasoning(ctx *schemas.BifrostContext, req *schemas.BifrostChatRequest) bool {
	if req == nil || len(req.Input) == 0 {
		return false
	}

	if ctx != nil {
		if isLargePayload, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool); ok && isLargePayload {
			return false
		}
		if useRawBody, ok := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); ok && useRawBody {

			if integrationType, _ := ctx.Value(schemas.BifrostContextKeyIntegrationType).(string); integrationType == "anthropic" {
				return stripRawAnthropicChatThinking(&req.RawRequestBody)
			}
			return false
		}
	}

	rewritten := make([]schemas.ChatMessage, len(req.Input))
	copy(rewritten, req.Input)

	dropWhole := dropsWholeReasoningBlocks(ctx, req.Model)

	changed := false
	for i := range rewritten {

		if assistant := rewritten[i].ChatAssistantMessage; assistant != nil && len(assistant.ToolCalls) > 0 {
			var cloned []schemas.ChatAssistantMessageToolCall
			for j := range assistant.ToolCalls {
				id := assistant.ToolCalls[j].ID
				if id == nil {
					continue
				}
				base := providerUtils.StripThoughtSignature(*id)
				if base == *id {
					continue
				}
				if cloned == nil {
					cloned = make([]schemas.ChatAssistantMessageToolCall, len(assistant.ToolCalls))
					copy(cloned, assistant.ToolCalls)
				}
				cloned[j].ID = &base
			}
			if cloned != nil {
				assistantCopy := *assistant
				assistantCopy.ToolCalls = cloned
				rewritten[i].ChatAssistantMessage = &assistantCopy
				changed = true
			}
		}
		if tool := rewritten[i].ChatToolMessage; tool != nil && tool.ToolCallID != nil {
			if base := providerUtils.StripThoughtSignature(*tool.ToolCallID); base != *tool.ToolCallID {
				toolCopy := *tool
				toolCopy.ToolCallID = &base
				rewritten[i].ChatToolMessage = &toolCopy
				changed = true
			}
		}

		assistant := rewritten[i].ChatAssistantMessage
		if assistant == nil || len(assistant.ReasoningDetails) == 0 {
			continue
		}
		details, detailsChanged := stripChatReasoningDetails(assistant.ReasoningDetails)
		if dropWhole {
			details, detailsChanged = dropChatReasoningDetails(assistant.ReasoningDetails)
		}
		if !detailsChanged {
			continue
		}
		assistantCopy := *assistant
		assistantCopy.ReasoningDetails = details
		rewritten[i].ChatAssistantMessage = &assistantCopy
		changed = true
	}

	if !changed {
		return false
	}
	req.Input = rewritten
	return true
}

func stripChatReasoningDetails(details []schemas.ChatReasoningDetails) ([]schemas.ChatReasoningDetails, bool) {
	changed := false
	kept := make([]schemas.ChatReasoningDetails, 0, len(details))

	for _, detail := range details {
		if detail.Signature == nil && detail.Data == nil {
			kept = append(kept, detail)
			continue
		}
		changed = true
		detail.Signature = nil
		detail.Data = nil
		if detail.Text == nil && detail.Summary == nil {
			continue
		}
		kept = append(kept, detail)
	}
	if !changed {
		return nil, false
	}
	return kept, true
}

func dropChatReasoningDetails(details []schemas.ChatReasoningDetails) ([]schemas.ChatReasoningDetails, bool) {
	kept := make([]schemas.ChatReasoningDetails, 0, len(details))
	changed := false
	for _, detail := range details {
		if detail.Signature != nil || detail.Data != nil {
			changed = true
			continue
		}
		kept = append(kept, detail)
	}
	if !changed {
		return nil, false
	}
	return kept, true
}

func dropsWholeReasoningBlocks(ctx *schemas.BifrostContext, model string) bool {
	return schemas.IsAnthropicModelFamily(ctx, model)
}

func dropReasoningContentBlocks(content *schemas.ResponsesMessageContent) ([]schemas.ResponsesMessageContentBlock, bool) {
	if content == nil || len(content.ContentBlocks) == 0 {
		return nil, false
	}
	kept := make([]schemas.ResponsesMessageContentBlock, 0, len(content.ContentBlocks))
	changed := false
	for _, block := range content.ContentBlocks {
		if block.Signature != nil || block.EncryptedContent != nil {
			changed = true
			continue
		}
		kept = append(kept, block)
	}
	if !changed {
		return nil, false
	}
	return kept, true
}

func stripRawAnthropicChatThinking(rawBody *[]byte) bool {
	if rawBody == nil || len(*rawBody) == 0 {
		return false
	}
	body := *rawBody
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return false
	}

	changed := false
	var rebuilt [][]byte
	for _, message := range messages.Array() {
		messageRaw := []byte(message.Raw)
		content := message.Get("content")

		if !content.IsArray() {
			rebuilt = append(rebuilt, messageRaw)
			continue
		}

		blocks := content.Array()
		kept := make([]string, 0, len(blocks))
		messageChanged := false
		for _, block := range blocks {
			switch block.Get("type").String() {
			case "redacted_thinking":
				messageChanged = true
				continue
			case "thinking":
				if !block.Get("signature").Exists() {
					break
				}
				messageChanged = true
				continue
			}
			kept = append(kept, block.Raw)
		}

		if !messageChanged || len(kept) == 0 {
			rebuilt = append(rebuilt, messageRaw)
			continue
		}
		updated, err := sjson.SetRawBytes(messageRaw, "content", []byte("["+strings.Join(kept, ",")+"]"))
		if err != nil {
			rebuilt = append(rebuilt, messageRaw)
			continue
		}
		rebuilt = append(rebuilt, updated)
		changed = true
	}

	if !changed {
		return false
	}
	updated, err := sjson.SetRawBytes(body, "messages", joinRawJSONArray(rebuilt, len(body)))
	if err != nil {
		return false
	}
	*rawBody = updated
	return true
}

func joinRawJSONArray(parts [][]byte, sizeHint int) []byte {
	var buf bytes.Buffer
	buf.Grow(sizeHint)
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

func stripContentBlockReasoningPayloads(content *schemas.ResponsesMessageContent) ([]schemas.ResponsesMessageContentBlock, bool) {
	if content == nil || len(content.ContentBlocks) == 0 {
		return nil, false
	}
	blocks := make([]schemas.ResponsesMessageContentBlock, len(content.ContentBlocks))
	copy(blocks, content.ContentBlocks)
	changed := false
	for i := range blocks {
		if blocks[i].Signature != nil {
			blocks[i].Signature = nil
			changed = true
		}
		if blocks[i].EncryptedContent != nil {
			blocks[i].EncryptedContent = nil
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	return blocks, true
}

func stripResponsesEncryptedContent(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) bool {
	inputRef, rawBodyRef := encryptedReasoningCarriers(req)
	if inputRef == nil {
		return false
	}

	targetProvider, targetModel, _ := req.GetRequestFields()

	dropItemIDs := req.CompactionRequest != nil
	clearIDs := clearsReplayedItemIDs(ctx, targetProvider, targetModel)

	if ctx != nil {
		if isLargePayload, ok := ctx.Value(schemas.BifrostContextKeyLargePayloadMode).(bool); ok && isLargePayload {
			return false
		}
		if useRawBody, ok := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); ok && useRawBody {
			return stripRawResponsesEncryptedContent(rawBodyRef, dropItemIDs, clearIDs)
		}
	}

	if len(*inputRef) == 0 {
		return false
	}

	input := *inputRef

	dropWhole := dropsWholeReasoningBlocks(ctx, targetModel)

	stripped := make([]schemas.ResponsesMessage, 0, len(input))
	changed := false

	for _, message := range input {
		messageChanged := false

		if message.ResponsesToolMessage != nil && message.ResponsesToolMessage.CallID != nil {
			callID := *message.ResponsesToolMessage.CallID
			if base := providerUtils.StripThoughtSignature(callID); base != callID {
				toolCopy := *message.ResponsesToolMessage
				toolCopy.CallID = &base
				message.ResponsesToolMessage = &toolCopy
				if message.ID != nil {
					if baseID := providerUtils.StripThoughtSignature(*message.ID); baseID != *message.ID {
						message.ID = &baseID
					}
				}
				changed = true
				stripped = append(stripped, message)
				continue
			}
		}

		if message.ResponsesReasoning != nil && message.ResponsesReasoning.EncryptedContent != nil {
			reasoningCopy := *message.ResponsesReasoning
			reasoningCopy.EncryptedContent = nil
			if dropWhole {

				reasoningCopy.Summary = nil
			}
			message.ResponsesReasoning = &reasoningCopy
			messageChanged = true
		}

		blocks, ok := stripContentBlockReasoningPayloads(message.Content)
		if dropWhole {
			blocks, ok = dropReasoningContentBlocks(message.Content)
		}
		if ok {
			contentCopy := *message.Content
			contentCopy.ContentBlocks = blocks
			message.Content = &contentCopy
			messageChanged = true
		}

		if !messageChanged {
			stripped = append(stripped, message)
			continue
		}

		changed = true
		if dropItemIDs {

			message.ID = nil
		}

		hasSummary := message.ResponsesReasoning != nil && len(message.ResponsesReasoning.Summary) > 0
		hasContent := message.Content != nil &&
			(len(message.Content.ContentBlocks) > 0 || message.Content.ContentStr != nil)
		if !hasSummary && !hasContent {
			continue
		}
		stripped = append(stripped, message)
	}

	if !changed {
		return false
	}

	for i := range stripped {
		if clearIDs && detachableItemTypes[responsesItemType(&stripped[i])] && !isReasoningReference(&stripped[i]) {
			stripped[i].ID = nil
		}
	}

	*inputRef = stripped
	return true
}

func clearsReplayedItemIDs(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string) bool {
	switch schemas.ResolveBaseProvider(ctx, provider) {

	}
	return false
}

func isReasoningReference(message *schemas.ResponsesMessage) bool {
	if responsesItemType(message) != string(schemas.ResponsesMessageTypeReasoning) || message.Content != nil {
		return false
	}
	reasoning := message.ResponsesReasoning
	return reasoning == nil || (len(reasoning.Summary) == 0 && reasoning.EncryptedContent == nil)
}

func responsesItemType(message *schemas.ResponsesMessage) string {
	switch {
	case message.Type != nil:
		return string(*message.Type)
	case message.ResponsesReasoning != nil:
		return string(schemas.ResponsesMessageTypeReasoning)
	case message.Role != nil:
		return string(schemas.ResponsesMessageTypeMessage)
	}
	return ""
}

func stripRawResponsesEncryptedContent(rawBody *[]byte, dropItemIDs, clearIDs bool) bool {
	if rawBody == nil {
		return false
	}
	body := *rawBody
	if len(body) == 0 {
		return false
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}

	items := make([]string, 0, len(input.Array()))
	changed := false
	for _, item := range input.Array() {
		rest := item.Raw
		itemChanged := false

		if callID := gjson.Get(rest, "call_id"); callID.Type == gjson.String {
			if base := providerUtils.StripThoughtSignature(callID.Str); base != callID.Str {
				updated, err := sjson.Set(rest, "call_id", base)
				if err != nil {
					items = append(items, item.Raw)
					continue
				}
				if itemID := gjson.Get(updated, "id"); itemID.Type == gjson.String {
					if baseID := providerUtils.StripThoughtSignature(itemID.Str); baseID != itemID.Str {
						if withID, err := sjson.Set(updated, "id", baseID); err == nil {
							updated = withID
						}
					}
				}
				changed = true
				items = append(items, updated)
				continue
			}
		}

		if gjson.Get(rest, "encrypted_content").Exists() {
			updated, err := sjson.Delete(rest, "encrypted_content")
			if err != nil {

				items = append(items, item.Raw)
				continue
			}
			rest, itemChanged = updated, true
		}

		if contentResult := gjson.Get(rest, "content"); contentResult.IsArray() {
			var rebuiltBlocks [][]byte
			contentChanged := false
			for _, block := range contentResult.Array() {
				blockRaw := []byte(block.Raw)
				for _, field := range contentBlockReasoningCarriers {
					if !gjson.GetBytes(blockRaw, field).Exists() {
						continue
					}
					updated, err := sjson.DeleteBytes(blockRaw, field)
					if err != nil {
						continue
					}
					blockRaw, contentChanged = updated, true
				}
				rebuiltBlocks = append(rebuiltBlocks, blockRaw)
			}
			if contentChanged {
				if updated, err := sjson.SetRawBytes([]byte(rest), "content", joinRawJSONArray(rebuiltBlocks, len(rest))); err == nil {
					rest, itemChanged = string(updated), true
				}
			}
		}

		if !itemChanged {
			items = append(items, item.Raw)
			continue
		}
		changed = true
		if dropItemIDs {
			if withoutID, err := sjson.Delete(rest, "id"); err == nil {
				rest = withoutID
			}
		}
		if len(gjson.Get(rest, "summary").Array()) == 0 && !gjson.Get(rest, "content").Exists() {
			continue
		}
		items = append(items, rest)
	}
	if !changed {
		return false
	}

	for i, item := range items {
		itemType := gjson.Get(item, "type").Str
		if itemType == "" && gjson.Get(item, "role").Exists() {
			itemType = string(schemas.ResponsesMessageTypeMessage)
		}
		if !clearIDs || !detachableItemTypes[itemType] || !gjson.Get(item, "id").Exists() {
			continue
		}

		if itemType == string(schemas.ResponsesMessageTypeReasoning) && len(gjson.Get(item, "summary").Array()) == 0 &&
			!gjson.Get(item, "content").Exists() && !gjson.Get(item, "encrypted_content").Exists() {
			continue
		}
		if withoutID, err := sjson.Delete(item, "id"); err == nil {
			items[i] = withoutID
		}
	}

	updated, err := sjson.SetRawBytes(body, "input", []byte("["+strings.Join(items, ",")+"]"))
	if err != nil {
		return false
	}
	*rawBody = updated
	return true
}
