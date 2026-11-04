// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"encoding/base64"
	"fmt"
	"maps"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

type GeminiStreamState struct {
	nextToolCallIndex int
	hadToolCalls      bool
}

func ToGeminiChatCompletionRequest(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostChatRequest) (*GeminiGenerationRequest, error) {
	return ToGeminiChatCompletionRequestWithImageURLSchemes(ctx, bifrostReq, defaultGeminiImageURLSchemes...)
}

func ToGeminiChatCompletionRequestWithImageURLSchemes(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostChatRequest, allowedImageURLSchemes ...string) (*GeminiGenerationRequest, error) {
	if bifrostReq == nil {
		return nil, nil
	}

	bifrostReq.Model = NormalizeModelName(bifrostReq.Model)

	geminiReq := &GeminiGenerationRequest{
		Model: bifrostReq.Model,
	}

	capModel := NormalizeModelName(schemas.ResolveCanonicalModel(ctx, bifrostReq.Model))

	if bifrostReq.Params != nil {

		geminiReq.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)
		var err error
		geminiReq.GenerationConfig, err = convertParamsToGenerationConfig(bifrostReq.Params, []string{}, bifrostReq.Provider, capModel)
		if err != nil {
			return nil, err
		}

		if len(bifrostReq.Params.Tools) > 0 {
			geminiReq.Tools, err = convertBifrostToolsToGemini(bifrostReq.Params.Tools)
			if err != nil {
				return nil, err
			}

			if bifrostReq.Params.ToolChoice != nil {
				hasFunctionDeclarations := false
				for _, tool := range geminiReq.Tools {
					if len(tool.FunctionDeclarations) > 0 {
						hasFunctionDeclarations = true
						break
					}
				}
				if hasFunctionDeclarations {
					geminiReq.ToolConfig = convertToolChoiceToToolConfig(bifrostReq.Params.ToolChoice)
				}
			}
		}

		if bifrostReq.Params.WebSearchOptions != nil {
			googleSearch := &GoogleSearch{}
			if filters := bifrostReq.Params.WebSearchOptions.Filters; filters != nil {
				googleSearch.ExcludeDomains = filters.BlockedDomains
				if filters.TimeRangeFilter != nil {
					googleSearch.TimeRangeFilter = &Interval{
						StartTime: filters.TimeRangeFilter.StartTime,
						EndTime:   filters.TimeRangeFilter.EndTime,
					}
				}
			}
			geminiReq.Tools = append(geminiReq.Tools, Tool{GoogleSearch: googleSearch})
		}

		if bifrostReq.Params.IncludeServerSideToolInvocations != nil && *bifrostReq.Params.IncludeServerSideToolInvocations {
			applyServerSideToolInvocations(geminiReq)
		}

		if bifrostReq.Params.ServiceTier != nil {
			geminiReq.ServiceTier = mapBifrostServiceTierToGemini(*bifrostReq.Params.ServiceTier)
		}

		if bifrostReq.Params.ExtraParams != nil {

			if safetySettings, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "safety_settings"); ok {
				delete(geminiReq.ExtraParams, "safety_settings")
				if settings, ok := SafeExtractSafetySettings(safetySettings); ok {
					geminiReq.SafetySettings = settings
				}
			}

			if cachedContent, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["cached_content"]); ok {
				delete(geminiReq.ExtraParams, "cached_content")
				geminiReq.CachedContent = cachedContent
			}

			if labels, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "labels"); ok {
				delete(geminiReq.ExtraParams, "labels")
				if labelMap, ok := schemas.SafeExtractStringMap(labels); ok {
					geminiReq.Labels = labelMap
				}
			}
		}
	}

	input := trimTrailingChatAssistantPrefill(bifrostReq.Input, schemas.ResolveModelCaps(bifrostReq.Provider, capModel))
	contents, systemInstruction, err := convertBifrostMessagesToGemini(input, allowedImageURLSchemes...)
	if err != nil {
		return nil, err
	}
	if systemInstruction != nil {
		geminiReq.SystemInstruction = systemInstruction
	}
	geminiReq.Contents = contents
	return geminiReq, nil
}

func (response *GenerateContentResponse) ToBifrostChatResponse() *schemas.BifrostChatResponse {
	bifrostResp := &schemas.BifrostChatResponse{
		ID:     response.ResponseID,
		Model:  response.ModelVersion,
		Object: "chat.completion",
	}

	if !response.CreateTime.IsZero() {
		bifrostResp.Created = int(response.CreateTime.Unix())
	}

	if len(response.Candidates) == 0 {
		finishReason := ConvertGeminiFinishReasonToBifrost(FinishReasonMalformedFunctionCall)
		if response.promptBlockError() != nil {
			finishReason = ConvertGeminiFinishReasonToBifrost(FinishReasonSafety)
		}
		return createErrorResponse(response, finishReason, false)
	}

	candidate := response.Candidates[0]

	if isErrorFinishReason(candidate.FinishReason) {
		finishReason := ConvertGeminiFinishReasonToBifrost(candidate.FinishReason)
		return createErrorResponse(response, finishReason, false)
	}

	var toolCalls []schemas.ChatAssistantMessageToolCall
	var contentBlocks []schemas.ChatContentBlock
	var reasoningDetails []schemas.ChatReasoningDetails
	var contentStr *string

	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {

			if part.Text != "" && part.Thought {
				reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
					Index: len(reasoningDetails),
					Type:  schemas.BifrostReasoningDetailsTypeText,
					Text:  &part.Text,
				})
				continue
			}

			if part.Text != "" {
				contentBlocks = append(contentBlocks, schemas.ChatContentBlock{
					Type: schemas.ChatContentBlockTypeText,
					Text: &part.Text,
				})

				if len(part.ThoughtSignature) > 0 {
					thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
					reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
						Index:     len(reasoningDetails),
						Type:      schemas.BifrostReasoningDetailsTypeEncrypted,
						Signature: &thoughtSig,
					})
				}
			}
			if part.FunctionCall != nil {
				function := schemas.ChatAssistantMessageToolCallFunction{
					Name: &part.FunctionCall.Name,
				}

				if len(part.FunctionCall.Args) > 0 {
					function.Arguments = string(part.FunctionCall.Args)
				}

				callID := part.FunctionCall.Name
				if part.FunctionCall.ID != "" {
					callID = part.FunctionCall.ID
				}

				if len(part.ThoughtSignature) > 0 && !strings.Contains(callID, thoughtSignatureSeparator) {
					encoded := base64.RawURLEncoding.EncodeToString(part.ThoughtSignature)
					callID = fmt.Sprintf("%s%s%s", callID, thoughtSignatureSeparator, encoded)
				}

				toolCall := schemas.ChatAssistantMessageToolCall{
					Index:    uint16(len(toolCalls)),
					Type:     schemas.Ptr(string(schemas.ChatToolChoiceTypeFunction)),
					ID:       &callID,
					Function: function,
				}

				toolCalls = append(toolCalls, toolCall)

				if len(part.ThoughtSignature) > 0 {
					thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)

					baseCallID := callID
					if strings.Contains(callID, thoughtSignatureSeparator) {
						parts := strings.SplitN(callID, thoughtSignatureSeparator, 2)
						if len(parts) == 2 {
							baseCallID = parts[0]
						}
					}
					reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
						Index:     len(reasoningDetails),
						Type:      schemas.BifrostReasoningDetailsTypeEncrypted,
						Signature: &thoughtSig,
						ID:        schemas.Ptr(fmt.Sprintf("tool_call_%s", baseCallID)),
					})
				}
			}

			if part.FunctionResponse != nil {

				output := extractFunctionResponseOutput(part.FunctionResponse)

				if output != "" {
					contentBlocks = append(contentBlocks, schemas.ChatContentBlock{
						Type: schemas.ChatContentBlockTypeText,
						Text: &output,
					})
				}
			}

			if part.CodeExecutionResult != nil {
				output := part.CodeExecutionResult.Output
				if part.CodeExecutionResult.Outcome != OutcomeOK {
					output = "Error: " + output
				}
				if output != "" {
					contentBlocks = append(contentBlocks, schemas.ChatContentBlock{
						Type: schemas.ChatContentBlockTypeText,
						Text: &output,
					})
				}
			}

			if part.ExecutableCode != nil {
				codeContent := "```" + part.ExecutableCode.Language + "\n" + part.ExecutableCode.Code + "\n```"
				contentBlocks = append(contentBlocks, schemas.ChatContentBlock{
					Type: schemas.ChatContentBlockTypeText,
					Text: &codeContent,
				})
			}

			if part.InlineData != nil && part.InlineData.Data != "" {
				if block := convertGeminiInlineDataToChatContentBlock(part.InlineData); block != nil {
					contentBlocks = append(contentBlocks, *block)
				}
			}

			if len(part.ThoughtSignature) > 0 && part.FunctionCall == nil && part.Text == "" {
				thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
				reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
					Index:     len(reasoningDetails),
					Type:      schemas.BifrostReasoningDetailsTypeEncrypted,
					Signature: &thoughtSig,
				})
			}
		}
	}

	message := &schemas.ChatMessage{
		Role: schemas.ChatMessageRoleAssistant,
	}

	if len(contentBlocks) == 1 && contentBlocks[0].Type == schemas.ChatContentBlockTypeText {
		contentStr = contentBlocks[0].Text
		contentBlocks = nil
	}

	if contentStr == nil && len(contentBlocks) == 0 && len(toolCalls) == 0 {
		contentStr = new("")
	}

	message.Content = &schemas.ChatMessageContent{
		ContentStr:    contentStr,
		ContentBlocks: contentBlocks,
	}

	annotations := convertGroundingMetadataToChatAnnotations(candidate.GroundingMetadata)

	if len(toolCalls) > 0 || len(reasoningDetails) > 0 || len(annotations) > 0 {
		message.ChatAssistantMessage = &schemas.ChatAssistantMessage{
			ToolCalls:        toolCalls,
			ReasoningDetails: reasoningDetails,
			Annotations:      annotations,
		}
	}

	finishReason := ConvertGeminiFinishReasonToBifrost(candidate.FinishReason)
	if len(toolCalls) > 0 && finishReason == "stop" {
		finishReason = "tool_calls"
	}

	bifrostResp.Choices = append(bifrostResp.Choices, schemas.BifrostResponseChoice{
		Index:        0,
		FinishReason: &finishReason,
		LogProbs:     ConvertGeminiLogprobsResultToBifrost(candidate.LogprobsResult),
		ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
			Message: message,
		},
	})

	bifrostResp.Usage = ConvertGeminiUsageMetadataToChatUsage(response.UsageMetadata)
	applyGeminiSearchQueryChatUsage(bifrostResp.Usage, candidate.GroundingMetadata, response.ModelVersion)

	if response.UsageMetadata != nil {
		if t := mapGeminiTrafficTypeToBifrost(response.UsageMetadata.TrafficType); t != nil {
			bifrostResp.ServiceTier = t
		} else if response.UsageMetadata.ServiceTier != "" {
			tier := mapGeminiServiceTierToBifrost(response.UsageMetadata.ServiceTier)
			bifrostResp.ServiceTier = &tier
		}
	}

	return bifrostResp
}

func NewGeminiStreamState() *GeminiStreamState {
	return &GeminiStreamState{}
}

func (response *GenerateContentResponse) ToBifrostChatCompletionStream(state *GeminiStreamState) ([]*schemas.BifrostChatResponse, *schemas.BifrostError, bool) {
	if response == nil {
		return nil, nil, false
	}
	if state == nil {
		state = NewGeminiStreamState()
	}

	var chunks []*schemas.BifrostChatResponse
	isLastChunk := false
	for _, piece := range response.splitInlineMediaParts() {
		chunk, bifrostErr, isLast := piece.toBifrostChatCompletionStreamDelta(state)
		if bifrostErr != nil {
			return nil, bifrostErr, isLast
		}
		if chunk != nil {
			chunks = append(chunks, chunk)
		}
		isLastChunk = isLastChunk || isLast
	}
	return chunks, nil, isLastChunk
}

func (response *GenerateContentResponse) splitInlineMediaParts() []*GenerateContentResponse {
	if len(response.Candidates) == 0 || response.Candidates[0] == nil ||
		response.Candidates[0].Content == nil || len(response.Candidates[0].Content.Parts) < 2 ||
		isErrorFinishReason(response.Candidates[0].FinishReason) {
		return []*GenerateContentResponse{response}
	}
	candidate := response.Candidates[0]

	var groups [][]*Part
	var run []*Part
	for _, part := range candidate.Content.Parts {
		if isInlineMediaPart(part) {
			if len(run) > 0 {
				groups = append(groups, run)
				run = nil
			}
			groups = append(groups, []*Part{part})
			continue
		}
		run = append(run, part)
	}
	if len(run) > 0 {
		groups = append(groups, run)
	}
	if len(groups) < 2 {
		return []*GenerateContentResponse{response}
	}

	pieces := make([]*GenerateContentResponse, 0, len(groups))
	for i, group := range groups {
		pieceCandidate := &Candidate{
			Index:   candidate.Index,
			Content: &Content{Role: candidate.Content.Role, Parts: group},
		}
		piece := &GenerateContentResponse{
			ResponseID:     response.ResponseID,
			ModelVersion:   response.ModelVersion,
			CreateTime:     response.CreateTime,
			PromptFeedback: response.PromptFeedback,
			Candidates:     []*Candidate{pieceCandidate},
		}
		if i == len(groups)-1 {
			pieceCandidate.FinishReason = candidate.FinishReason
			pieceCandidate.FinishMessage = candidate.FinishMessage
			pieceCandidate.TokenCount = candidate.TokenCount
			pieceCandidate.CitationMetadata = candidate.CitationMetadata
			pieceCandidate.URLContextMetadata = candidate.URLContextMetadata
			pieceCandidate.AvgLogprobs = candidate.AvgLogprobs
			pieceCandidate.GroundingMetadata = candidate.GroundingMetadata
			pieceCandidate.LogprobsResult = candidate.LogprobsResult
			pieceCandidate.SafetyRatings = candidate.SafetyRatings
			piece.UsageMetadata = response.UsageMetadata
		}
		pieces = append(pieces, piece)
	}
	return pieces
}

func isInlineMediaPart(part *Part) bool {
	if part == nil || part.InlineData == nil || part.InlineData.Data == "" {
		return false
	}
	return strings.HasPrefix(part.InlineData.MIMEType, "image/") ||
		strings.HasPrefix(part.InlineData.MIMEType, "audio/")
}

func (response *GenerateContentResponse) toBifrostChatCompletionStreamDelta(state *GeminiStreamState) (*schemas.BifrostChatResponse, *schemas.BifrostError, bool) {
	if response == nil {
		return nil, nil, false
	}

	if state == nil {
		state = NewGeminiStreamState()
	}

	if len(response.Candidates) == 0 {
		finishReason := ConvertGeminiFinishReasonToBifrost(FinishReasonMalformedFunctionCall)
		if response.promptBlockError() != nil {
			finishReason = ConvertGeminiFinishReasonToBifrost(FinishReasonSafety)
		}
		return createErrorResponse(response, finishReason, true), nil, true
	}

	candidate := response.Candidates[0]

	if isErrorFinishReason(candidate.FinishReason) {
		finishReason := ConvertGeminiFinishReasonToBifrost(candidate.FinishReason)
		return createErrorResponse(response, finishReason, true), nil, true
	}

	isLastChunk := candidate.FinishReason != "" && response.UsageMetadata != nil

	streamResponse := &schemas.BifrostChatResponse{
		ID:     response.ResponseID,
		Model:  response.ModelVersion,
		Object: "chat.completion.chunk",
	}

	if !response.CreateTime.IsZero() {
		streamResponse.Created = int(response.CreateTime.Unix())
	}

	delta := &schemas.ChatStreamResponseChoiceDelta{}

	if candidate.Content != nil && len(candidate.Content.Parts) > 0 {

		if candidate.Content.Role != "" {
			role := candidate.Content.Role
			if role == string(RoleModel) {
				role = string(schemas.ChatMessageRoleAssistant)
			}
			delta.Role = &role
		}

		var textContent string
		var imageDataURL string
		var toolCalls []schemas.ChatAssistantMessageToolCall
		var reasoningDetails []schemas.ChatReasoningDetails

		for _, part := range candidate.Content.Parts {
			switch {
			case part.Text != "" && part.Thought:

				reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
					Index: len(reasoningDetails),
					Type:  schemas.BifrostReasoningDetailsTypeText,
					Text:  &part.Text,
				})

			case part.Text != "":

				textContent += part.Text

			case part.FunctionCall != nil:

				jsonArgs := ""
				if len(part.FunctionCall.Args) > 0 {
					jsonArgs = string(part.FunctionCall.Args)
				}

				callID := part.FunctionCall.Name
				if part.FunctionCall.ID != "" {
					callID = part.FunctionCall.ID
				}

				if len(part.ThoughtSignature) > 0 && !strings.Contains(callID, thoughtSignatureSeparator) {
					encoded := base64.RawURLEncoding.EncodeToString(part.ThoughtSignature)
					callID = fmt.Sprintf("%s%s%s", callID, thoughtSignatureSeparator, encoded)
				}

				toolCallIdx := state.nextToolCallIndex
				state.nextToolCallIndex++

				toolCall := schemas.ChatAssistantMessageToolCall{
					Index: uint16(toolCallIdx),
					Type:  schemas.Ptr(string(schemas.ChatToolTypeFunction)),
					ID:    &callID,
					Function: schemas.ChatAssistantMessageToolCallFunction{
						Name:      &part.FunctionCall.Name,
						Arguments: jsonArgs,
					},
				}

				toolCalls = append(toolCalls, toolCall)

				if len(part.ThoughtSignature) > 0 {
					thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)

					baseCallID := callID
					if strings.Contains(callID, thoughtSignatureSeparator) {
						parts := strings.SplitN(callID, thoughtSignatureSeparator, 2)
						if len(parts) == 2 {
							baseCallID = parts[0]
						}
					}
					reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
						Index:     len(reasoningDetails),
						Type:      schemas.BifrostReasoningDetailsTypeEncrypted,
						Signature: &thoughtSig,
						ID:        schemas.Ptr(fmt.Sprintf("tool_call_%s", baseCallID)),
					})
				}

			case part.FunctionResponse != nil:

				output := extractFunctionResponseOutput(part.FunctionResponse)
				if output != "" {
					textContent += output
				}
			case part.CodeExecutionResult != nil:
				output := part.CodeExecutionResult.Output
				if part.CodeExecutionResult.Outcome != OutcomeOK {
					output = "Error: " + output
				}
				if output != "" {
					textContent += output
				}
			case part.ExecutableCode != nil:
				codeContent := "```" + part.ExecutableCode.Language + "\n" + part.ExecutableCode.Code + "\n```"
				textContent += codeContent

			case part.InlineData != nil && part.InlineData.Data != "" && strings.HasPrefix(part.InlineData.MIMEType, "audio/"):

				delta.Audio = &schemas.ChatAudioMessageAudio{
					Data: part.InlineData.Data,
				}

			case part.InlineData != nil && part.InlineData.Data != "" && strings.HasPrefix(part.InlineData.MIMEType, "image/"):

				imageDataURL = "data:" + part.InlineData.MIMEType + ";base64," + part.InlineData.Data
			}

			if len(part.ThoughtSignature) > 0 && part.FunctionCall == nil {
				thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
				reasoningDetails = append(reasoningDetails, schemas.ChatReasoningDetails{
					Index:     len(reasoningDetails),
					Type:      schemas.BifrostReasoningDetailsTypeEncrypted,
					Signature: &thoughtSig,
				})
			}
		}

		if textContent != "" {
			delta.Content = &textContent
		} else if imageDataURL != "" {
			delta.Content = &imageDataURL
		}

		if len(reasoningDetails) > 0 {
			delta.ReasoningDetails = reasoningDetails
		}

		if len(toolCalls) > 0 {
			delta.ToolCalls = toolCalls
			state.hadToolCalls = true
		}
	}

	if candidate.FinishReason != "" {
		delta.Annotations = convertGroundingMetadataToChatAnnotations(candidate.GroundingMetadata)
	}

	hasDeltaContent := delta.Role != nil || delta.Content != nil || delta.Audio != nil || len(delta.ToolCalls) > 0 || len(delta.ReasoningDetails) > 0 || len(delta.Annotations) > 0
	if !hasDeltaContent && !isLastChunk {
		return nil, nil, false
	}

	var finishReason *string
	if isLastChunk && candidate.FinishReason != "" {
		reason := ConvertGeminiFinishReasonToBifrost(candidate.FinishReason)

		if (len(delta.ToolCalls) > 0 || state.hadToolCalls) && reason == "stop" {
			reason = "tool_calls"
		}
		finishReason = &reason
	}

	choice := schemas.BifrostResponseChoice{
		Index:        int(candidate.Index),
		FinishReason: finishReason,
		LogProbs:     ConvertGeminiLogprobsResultToBifrost(candidate.LogprobsResult),
		ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
			Delta: delta,
		},
	}

	streamResponse.Choices = []schemas.BifrostResponseChoice{choice}

	if isLastChunk && response.UsageMetadata != nil {
		streamResponse.Usage = ConvertGeminiUsageMetadataToChatUsage(response.UsageMetadata)
		applyGeminiSearchQueryChatUsage(streamResponse.Usage, candidate.GroundingMetadata, response.ModelVersion)
		if t := mapGeminiTrafficTypeToBifrost(response.UsageMetadata.TrafficType); t != nil {
			streamResponse.ServiceTier = t
		} else if response.UsageMetadata.ServiceTier != "" {
			tier := mapGeminiServiceTierToBifrost(response.UsageMetadata.ServiceTier)
			streamResponse.ServiceTier = &tier
		}
	}

	return streamResponse, nil, isLastChunk
}

func convertGroundingMetadataToChatAnnotations(metadata *GroundingMetadata) []schemas.ChatAssistantMessageAnnotation {
	if metadata == nil {
		return nil
	}
	var annotations []schemas.ChatAssistantMessageAnnotation
	for _, support := range metadata.GroundingSupports {
		if support.Segment == nil {
			continue
		}
		for _, chunkIdx := range support.GroundingChunkIndices {
			if chunkIdx < 0 || int(chunkIdx) >= len(metadata.GroundingChunks) {
				continue
			}
			chunk := metadata.GroundingChunks[chunkIdx]
			if chunk.Web == nil || chunk.Web.URI == "" {
				continue
			}
			annotation := schemas.ChatAssistantMessageAnnotation{
				Type: "url_citation",
				URLCitation: schemas.ChatAssistantMessageAnnotationCitation{
					StartIndex: int(support.Segment.StartIndex),
					EndIndex:   int(support.Segment.EndIndex),
					Title:      chunk.Web.Title,
					URL:        schemas.Ptr(chunk.Web.URI),
				},
			}
			if support.Segment.Text != "" {
				annotation.URLCitation.Text = schemas.Ptr(support.Segment.Text)
			}
			annotations = append(annotations, annotation)
		}
	}
	return annotations
}

func convertGeminiInlineDataToChatContentBlock(blob *Blob) *schemas.ChatContentBlock {
	switch {
	case strings.HasPrefix(blob.MIMEType, "image/"):
		return &schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeImage,
			ImageURLStruct: &schemas.ChatInputImage{
				URL: "data:" + blob.MIMEType + ";base64," + blob.Data,
			},
		}
	case strings.HasPrefix(blob.MIMEType, "audio/"):
		format := strings.TrimPrefix(blob.MIMEType, "audio/")
		return &schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeInputAudio,
			InputAudio: &schemas.ChatInputAudio{
				Data:   blob.Data,
				Format: schemas.Ptr(format),
			},
		}
	default:
		return nil
	}
}

func isErrorFinishReason(reason FinishReason) bool {
	return reason == FinishReasonSafety ||
		reason == FinishReasonRecitation ||
		reason == FinishReasonMalformedFunctionCall ||
		reason == FinishReasonBlocklist ||
		reason == FinishReasonProhibitedContent ||
		reason == FinishReasonSPII ||
		reason == FinishReasonImageSafety ||
		reason == FinishReasonUnexpectedToolCall ||
		reason == FinishReasonMissingThoughtSignature ||
		reason == FinishReasonMalformedResponse ||
		reason == FinishReasonImageProhibitedContent ||
		reason == FinishReasonImageRecitation ||
		reason == FinishReasonTooManyToolCalls ||
		reason == FinishReasonNoImage
}

func createErrorResponse(response *GenerateContentResponse, finishReason string, isStream bool) *schemas.BifrostChatResponse {
	var choice schemas.BifrostResponseChoice
	if isStream {
		choice = schemas.BifrostResponseChoice{
			Index:        0,
			FinishReason: &finishReason,
			ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
				Delta: &schemas.ChatStreamResponseChoiceDelta{},
			},
		}
	} else {
		choice = schemas.BifrostResponseChoice{
			Index:        0,
			FinishReason: &finishReason,
			ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
				Message: &schemas.ChatMessage{
					Role:    schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{},
				},
			},
		}
	}

	objectType := "chat.completion"
	if isStream {
		objectType = "chat.completion.chunk"
	}

	errorResp := &schemas.BifrostChatResponse{
		ID:      response.ResponseID,
		Model:   response.ModelVersion,
		Object:  objectType,
		Choices: []schemas.BifrostResponseChoice{choice},
		Usage:   ConvertGeminiUsageMetadataToChatUsage(response.UsageMetadata),
	}

	if !response.CreateTime.IsZero() {
		errorResp.Created = int(response.CreateTime.Unix())
	}

	return errorResp
}

func isChatAssistantPrefillMessage(msg *schemas.ChatMessage) bool {
	if msg.Role != schemas.ChatMessageRoleAssistant {
		return false
	}
	if a := msg.ChatAssistantMessage; a != nil {
		if len(a.ToolCalls) > 0 || a.Audio != nil || a.Reasoning != nil || len(a.ReasoningDetails) > 0 {
			return false
		}
	}
	return true
}

func trimTrailingChatAssistantPrefill(messages []schemas.ChatMessage, caps schemas.ModelCaps) []schemas.ChatMessage {
	if caps.SupportsAssistantPrefill(false) {
		return messages
	}
	trimmed := len(messages)
	for trimmed > 0 && isChatAssistantPrefillMessage(&messages[trimmed-1]) {
		trimmed--
	}
	return messages[:trimmed]
}
