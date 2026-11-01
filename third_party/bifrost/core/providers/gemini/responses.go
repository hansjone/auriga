// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"

	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type GeminiServerSearchRound struct {
	CallID       string
	Queries      []string
	ImageQueries []string
}
type GeminiResponsesStreamState struct {
	HasEmittedCreated    bool
	HasEmittedInProgress bool
	HasEmittedCompleted  bool

	CurrentOutputIndex int
	TextOutputIndex    int
	ItemIDs            map[int]string
	TextItemClosed     bool

	ToolCallIDs         map[int]string
	ToolCallNames       map[int]string
	ToolArgumentBuffers map[int]string

	OutputItems map[int]*schemas.ResponsesMessage

	MessageID  *string
	Model      *string
	CreatedAt  int
	ResponseID *string

	SafetyRatings      []*SafetyRating
	AvgLogprobs        float64
	URLContextMetadata *URLContextMetadata
	ContinuationToken  string

	PendingFinishReason  FinishReason
	PendingFinishMessage string
	PromptBlockError     *schemas.ResponsesResponseError

	HasStartedText     bool
	HasStartedToolCall bool
	TextBuffer         strings.Builder

	CodeExecOutputIndex int
	CodeExecItemID      string
	CodeExecCode        string

	HasEmittedWebSearch bool

	ServerSearchRounds []GeminiServerSearchRound
}

var responsesGenerationConfigExtraParamKeys = []string{
	"top_k",
	"frequency_penalty",
	"presence_penalty",
	"stop_sequences",
	"media_resolution",
	"candidate_count",
	"response_logprobs",
	"seed",
}
var geminiResponsesStreamStatePool = sync.Pool{
	New: func() interface{} {
		return &GeminiResponsesStreamState{
			ItemIDs:              make(map[int]string),
			ToolCallIDs:          make(map[int]string),
			ToolCallNames:        make(map[int]string),
			ToolArgumentBuffers:  make(map[int]string),
			OutputItems:          make(map[int]*schemas.ResponsesMessage),
			CurrentOutputIndex:   0,
			TextOutputIndex:      -1,
			CreatedAt:            int(time.Now().Unix()),
			HasEmittedCreated:    false,
			HasEmittedInProgress: false,
			HasEmittedCompleted:  false,
			TextItemClosed:       false,
			HasStartedText:       false,
			HasStartedToolCall:   false,
			HasEmittedWebSearch:  false,
			CodeExecOutputIndex:  -1,
		}
	},
}

func thoughtSignatureFromEncryptedContent(encryptedContent *string) []byte {
	if encryptedContent == nil || *encryptedContent == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(*encryptedContent)
	if err != nil || len(decoded) == 0 {
		return nil
	}
	return decoded
}

func ToGeminiResponsesRequest(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostResponsesRequest) (*GeminiGenerationRequest, error) {
	return ToGeminiResponsesRequestWithImageURLSchemes(ctx, bifrostReq, defaultGeminiImageURLSchemes...)
}

func ToGeminiResponsesRequestWithImageURLSchemes(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostResponsesRequest, allowedImageURLSchemes ...string) (*GeminiGenerationRequest, error) {
	if bifrostReq == nil {
		return nil, nil
	}

	bifrostReq.Model = NormalizeModelName(bifrostReq.Model)

	geminiReq := &GeminiGenerationRequest{
		Model: bifrostReq.Model,
	}

	capModel := NormalizeModelName(schemas.ResolveCanonicalModel(ctx, bifrostReq.Model))

	if bifrostReq.Params != nil {
		var err error
		geminiReq.GenerationConfig, err = geminiReq.convertParamsToGenerationConfigResponses(bifrostReq.Params, bifrostReq.Provider, capModel)
		if err != nil {
			return nil, err
		}
		geminiReq.ExtraParams = responsesExtraParamsWithoutGenerationConfigKeys(bifrostReq.Params.ExtraParams)
		includeServerSideToolInvocations := bifrostReq.Params.IncludeServerSideToolInvocations != nil && *bifrostReq.Params.IncludeServerSideToolInvocations

		if len(bifrostReq.Params.Tools) > 0 {
			geminiReq.Tools, err = convertResponsesToolsToGemini(bifrostReq.Params.Tools, includeServerSideToolInvocations, bifrostReq.Provider, bifrostReq.Model)
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
					geminiReq.ToolConfig = convertResponsesToolChoiceToGemini(bifrostReq.Params.ToolChoice)
				}
			}

			hasGoogleSearch := false
			for _, tool := range geminiReq.Tools {
				if tool.GoogleSearch != nil {
					hasGoogleSearch = true
					break
				}
			}
			for _, tool := range bifrostReq.Params.Tools {
				webSearch := tool.ResponsesToolWebSearch
				if !hasGoogleSearch || tool.Type != schemas.ResponsesToolTypeWebSearch || webSearch == nil || webSearch.UserLocation == nil {
					continue
				}
				if webSearch.UserLocation.Latitude == nil && webSearch.UserLocation.Longitude == nil {
					continue
				}
				if geminiReq.ToolConfig == nil {
					geminiReq.ToolConfig = &ToolConfig{}
				}
				geminiReq.ToolConfig.RetrievalConfig = &RetrievalConfig{
					LatLng: &LatLng{
						Latitude:  webSearch.UserLocation.Latitude,
						Longitude: webSearch.UserLocation.Longitude,
					},
				}
				break
			}
		}

		if includeServerSideToolInvocations {
			applyServerSideToolInvocations(geminiReq)
		}

		if bifrostReq.Params.ServiceTier != nil {
			geminiReq.ServiceTier = mapBifrostServiceTierToGemini(*bifrostReq.Params.ServiceTier)
		}
	}

	if bifrostReq.Input != nil {
		contents, systemInstruction, err := convertResponsesMessagesToGeminiContents(bifrostReq.Input, capModel, bifrostReq.Provider, allowedImageURLSchemes...)
		if err != nil {
			return nil, err
		}
		geminiReq.Contents = contents

		if systemInstruction != nil {
			geminiReq.SystemInstruction = systemInstruction
		}
	}

	if bifrostReq.Params != nil {
		if bifrostReq.Params.Instructions != nil {

			if geminiReq.SystemInstruction == nil {
				geminiReq.SystemInstruction = &Content{
					Parts: []*Part{
						{Text: *bifrostReq.Params.Instructions},
					},
				}
			} else if *bifrostReq.Params.Instructions != "" {

				geminiReq.SystemInstruction.Parts = append([]*Part{{Text: *bifrostReq.Params.Instructions}}, geminiReq.SystemInstruction.Parts...)
			}
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
			if token, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["continuation_token"]); ok {
				delete(geminiReq.ExtraParams, "continuation_token")
				geminiReq.ContinuationToken = token
			}

			if nativeTools, ok := bifrostReq.Params.ExtraParams[geminiNativeToolsExtraParamKey]; ok {
				delete(geminiReq.ExtraParams, geminiNativeToolsExtraParamKey)
				geminiReq.Tools = append(geminiReq.Tools, extractGeminiNativeTools(nativeTools)...)
			}
		}
	}

	return geminiReq, nil
}

func (response *GenerateContentResponse) ToResponsesBifrostResponsesResponse() *schemas.BifrostResponsesResponse {
	if response == nil {
		return nil
	}

	bifrostResp := &schemas.BifrostResponsesResponse{
		ID:        schemas.Ptr("resp_" + schemas.GetRandomString(50)),
		CreatedAt: int(time.Now().Unix()),
		Model:     response.ModelVersion,
	}

	bifrostResp.Usage = ConvertGeminiUsageMetadataToResponsesUsage(response.UsageMetadata)
	if len(response.Candidates) > 0 && response.Candidates[0] != nil {
		applyGeminiSearchQueryResponsesUsage(bifrostResp.Usage, response.Candidates[0].GroundingMetadata, response.ModelVersion)
	}

	if response.UsageMetadata != nil {
		if t := mapGeminiTrafficTypeToBifrost(response.UsageMetadata.TrafficType); t != nil {
			bifrostResp.ServiceTier = t
		} else if response.UsageMetadata.ServiceTier != "" {
			tier := mapGeminiServiceTierToBifrost(response.UsageMetadata.ServiceTier)
			bifrostResp.ServiceTier = &tier
		}
	}

	if blockErr := response.promptBlockError(); blockErr != nil {
		bifrostResp.StopReason = new(ConvertGeminiFinishReasonToBifrost(FinishReasonSafety))
		bifrostResp.Status = new(schemas.ResponsesResponseStatusFailed)
		bifrostResp.Error = blockErr
		return bifrostResp
	}

	if len(response.Candidates) > 0 {
		candidate := response.Candidates[0]

		if candidate.FinishReason != "" && candidate.FinishReason != FinishReasonUnspecified {
			stopReason := ConvertGeminiFinishReasonToBifrost(candidate.FinishReason)
			bifrostResp.StopReason = &stopReason

			if isErrorFinishReason(candidate.FinishReason) {
				failedStatus := "failed"
				bifrostResp.Status = &failedStatus

				errMsg := candidate.FinishMessage
				if errMsg == "" {
					errMsg = string(candidate.FinishReason)
				}
				bifrostResp.Error = &schemas.ResponsesResponseError{
					Code:    stopReason,
					Message: errMsg,
				}

				if len(response.Candidates) > 1 {
					bifrostResp.ProviderExtraFields = map[string]interface{}{
						"additionalCandidates": response.Candidates[1:],
					}
				}

				return bifrostResp
			}

			bifrostResp.Status, bifrostResp.IncompleteDetails = geminiResponsesStatus(stopReason)
		}

		outputMessages := convertGeminiCandidatesToResponsesOutput(response.Candidates[:1])
		if len(outputMessages) > 0 {
			bifrostResp.Output = outputMessages
		}
		if bifrostResp.Status != nil && *bifrostResp.Status == schemas.ResponsesResponseStatusIncomplete {
			schemas.MarkTruncatedOutputItem(bifrostResp.Output)
		}

		extraFields := map[string]interface{}{}
		if response.ResponseID != "" {
			extraFields["responseId"] = response.ResponseID
		}
		if len(candidate.SafetyRatings) > 0 {
			extraFields["safetyRatings"] = candidate.SafetyRatings
		}
		if candidate.AvgLogprobs != 0 {
			extraFields["avgLogprobs"] = candidate.AvgLogprobs
		}
		if candidate.LogprobsResult != nil {
			extraFields["logprobsResult"] = candidate.LogprobsResult
		}
		if candidate.URLContextMetadata != nil {
			extraFields["urlContextMetadata"] = candidate.URLContextMetadata
		}
		if candidate.ContinuationToken != "" {
			extraFields["continuationToken"] = candidate.ContinuationToken
		}
		if len(response.Candidates) > 1 {
			extraFields["additionalCandidates"] = response.Candidates[1:]
		}

		if parts := serverSideToolParts(candidate); len(parts) > 0 {
			extraFields["serverSideToolParts"] = parts
		}
		if len(extraFields) > 0 {
			bifrostResp.ProviderExtraFields = extraFields
		}
	}

	return bifrostResp
}

func thoughtTextParts(reasoning *schemas.ResponsesReasoning) []*Part {
	if reasoning == nil {
		return nil
	}
	var parts []*Part
	for _, summaryBlock := range reasoning.Summary {
		if summaryBlock.Text == "" {
			continue
		}
		parts = append(parts, &Part{Text: summaryBlock.Text, Thought: true})
	}
	return parts
}

func acquireGeminiResponsesStreamState() *GeminiResponsesStreamState {
	state := geminiResponsesStreamStatePool.Get().(*GeminiResponsesStreamState)
	state.flush()
	return state
}

func releaseGeminiResponsesStreamState(state *GeminiResponsesStreamState) {
	if state != nil {
		state.flush()
		geminiResponsesStreamStatePool.Put(state)
	}
}

func (state *GeminiResponsesStreamState) flush() {

	if state.ItemIDs == nil {
		state.ItemIDs = make(map[int]string)
	} else {
		clear(state.ItemIDs)
	}
	if state.ToolCallIDs == nil {
		state.ToolCallIDs = make(map[int]string)
	} else {
		clear(state.ToolCallIDs)
	}
	if state.ToolCallNames == nil {
		state.ToolCallNames = make(map[int]string)
	} else {
		clear(state.ToolCallNames)
	}
	if state.ToolArgumentBuffers == nil {
		state.ToolArgumentBuffers = make(map[int]string)
	} else {
		clear(state.ToolArgumentBuffers)
	}
	if state.OutputItems == nil {
		state.OutputItems = make(map[int]*schemas.ResponsesMessage)
	} else {
		clear(state.OutputItems)
	}
	state.CurrentOutputIndex = 0
	state.TextOutputIndex = -1
	state.MessageID = nil
	state.Model = nil
	state.ResponseID = nil
	state.SafetyRatings = nil
	state.AvgLogprobs = 0
	state.URLContextMetadata = nil
	state.ContinuationToken = ""
	state.PendingFinishReason = ""
	state.PendingFinishMessage = ""
	state.PromptBlockError = nil
	state.CreatedAt = int(time.Now().Unix())
	state.HasEmittedCreated = false
	state.HasEmittedCompleted = false
	state.HasEmittedInProgress = false
	state.TextItemClosed = false
	state.HasStartedText = false
	state.HasStartedToolCall = false
	state.HasEmittedWebSearch = false
	state.ServerSearchRounds = nil
	state.CodeExecOutputIndex = -1
	state.CodeExecItemID = ""
	state.CodeExecCode = ""
	state.TextBuffer.Reset()
}

func (state *GeminiResponsesStreamState) closeTextItemIfOpen(sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	if state.HasStartedText && !state.TextItemClosed {
		return closeGeminiTextItem(state, sequenceNumber)
	}
	return nil
}

func (state *GeminiResponsesStreamState) nextOutputIndex() int {
	index := state.CurrentOutputIndex
	state.CurrentOutputIndex++
	return index
}

func (state *GeminiResponsesStreamState) generateItemID(suffix string, outputIndex int) string {
	if state.MessageID != nil {
		return fmt.Sprintf("msg_%s_%s_%d", *state.MessageID, suffix, outputIndex)
	}
	return fmt.Sprintf("%s_%d", suffix, outputIndex)
}

func recordGeminiOutputItems(state *GeminiResponsesStreamState, responses []*schemas.BifrostResponsesStreamResponse) {
	if state == nil {
		return
	}
	for _, response := range responses {
		if response == nil || response.Type != schemas.ResponsesStreamResponseTypeOutputItemDone {
			continue
		}
		if response.Item == nil || response.OutputIndex == nil {
			continue
		}
		item := *response.Item
		state.OutputItems[*response.OutputIndex] = &item
	}
}

func (response *GenerateContentResponse) ToBifrostResponsesStream(sequenceNumber int, state *GeminiResponsesStreamState) ([]*schemas.BifrostResponsesStreamResponse, *schemas.BifrostError) {
	responses, bifrostErr := response.toBifrostResponsesStream(sequenceNumber, state)
	recordGeminiOutputItems(state, responses)
	return responses, bifrostErr
}

func (response *GenerateContentResponse) toBifrostResponsesStream(sequenceNumber int, state *GeminiResponsesStreamState) ([]*schemas.BifrostResponsesStreamResponse, *schemas.BifrostError) {
	var responses []*schemas.BifrostResponsesStreamResponse

	if !state.HasEmittedCreated {

		if response.ModelVersion != "" && state.Model == nil {
			state.Model = &response.ModelVersion
		}
		if response.ResponseID != "" && state.ResponseID == nil {
			state.ResponseID = &response.ResponseID
		}
		responses = append(responses, state.startEvents(sequenceNumber)...)
	}

	if blockErr := response.promptBlockError(); blockErr != nil {
		state.PromptBlockError = blockErr
		return append(responses, closeGeminiOpenItems(state, nil, response.UsageMetadata, sequenceNumber+len(responses), "", "")...), nil
	}

	if len(response.Candidates) > 0 {
		candidate := response.Candidates[0]

		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			for _, part := range candidate.Content.Parts {
				partResponses := processGeminiPart(part, state, sequenceNumber+len(responses))
				responses = append(responses, partResponses...)
			}
		}

		if candidate.FinishReason != "" {
			state.PendingFinishReason = candidate.FinishReason
			state.PendingFinishMessage = candidate.FinishMessage
		}

		if len(candidate.SafetyRatings) > 0 {
			state.SafetyRatings = candidate.SafetyRatings
		}
		if candidate.AvgLogprobs != 0 {
			state.AvgLogprobs = candidate.AvgLogprobs
		}
		if candidate.URLContextMetadata != nil {
			state.URLContextMetadata = candidate.URLContextMetadata
		}
		if candidate.ContinuationToken != "" {
			state.ContinuationToken = candidate.ContinuationToken
		}

		if candidate.FinishReason != "" && len(state.ItemIDs) > 0 {

			if (candidate.GroundingMetadata != nil || len(state.ServerSearchRounds) > 0) && !state.HasEmittedWebSearch {

				webSearchResponses := emitWebSearchFromGroundingMetadata(
					candidate.GroundingMetadata,
					state,
					sequenceNumber+len(responses),
				)
				responses = append(responses, webSearchResponses...)
			}

			recordGeminiOutputItems(state, responses)

			closeResponses := closeGeminiOpenItems(state, candidate.GroundingMetadata, response.UsageMetadata, sequenceNumber+len(responses), candidate.FinishReason, candidate.FinishMessage)
			responses = append(responses, closeResponses...)
		}
	}

	return responses, nil
}

func processGeminiPart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	switch {
	case part.Thought && part.Text != "":

		responses = append(responses, processGeminiThoughtPart(part, state, sequenceNumber)...)
	case part.Text != "" && !part.Thought:

		responses = append(responses, processGeminiTextPart(part, state, sequenceNumber)...)

	case part.FunctionCall != nil:

		responses = append(responses, processGeminiFunctionCallPart(part, state, sequenceNumber)...)

	case part.ToolCall != nil:

		if isSearchToolType(part.ToolCall.ToolType) {
			queries := toolCallSearchQueries(part.ToolCall)
			round := GeminiServerSearchRound{CallID: part.ToolCall.ID, Queries: queries}
			if strings.EqualFold(part.ToolCall.ToolType, "GOOGLE_SEARCH_IMAGE") {
				round.ImageQueries = queries
			}
			state.ServerSearchRounds = append(state.ServerSearchRounds, round)
		}
		responses = append(responses, processGeminiThoughtSignaturePart(part, state, sequenceNumber)...)

	case part.ToolResponse != nil:

		responses = append(responses, processGeminiThoughtSignaturePart(part, state, sequenceNumber)...)

	case part.ExecutableCode != nil:

		responses = append(responses, processGeminiExecutableCodePart(part, state, sequenceNumber)...)

	case part.CodeExecutionResult != nil:

		responses = append(responses, processGeminiCodeExecutionResultPart(part, state, sequenceNumber)...)

	case part.FunctionResponse != nil:

		responses = append(responses, processGeminiFunctionResponsePart(part, state, sequenceNumber)...)
	case part.InlineData != nil:

		responses = append(responses, processGeminiInlineDataPart(part, state, sequenceNumber)...)
	case part.FileData != nil:

		responses = append(responses, processGeminiFileDataPart(part, state, sequenceNumber)...)
	case part.ThoughtSignature != nil:

		responses = append(responses, processGeminiThoughtSignaturePart(part, state, sequenceNumber)...)
	}

	return responses
}

func processGeminiTextPart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	var outputIndex int

	if !state.HasStartedText || state.TextItemClosed {
		state.TextItemClosed = false
		state.TextBuffer.Reset()
		outputIndex = state.nextOutputIndex()
		state.TextOutputIndex = outputIndex
		itemID := state.generateItemID("item", outputIndex)
		state.ItemIDs[outputIndex] = itemID

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			Item: &schemas.ResponsesMessage{
				ID:     &itemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				Status: schemas.Ptr("in_progress"),
				Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: []schemas.ResponsesMessageContentBlock{},
				},
			},
		})

		contentIndex := 0
		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeContentPartAdded,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ContentIndex:   &contentIndex,
			ItemID:         &itemID,
			Part: &schemas.ResponsesMessageContentBlock{
				Type: schemas.ResponsesOutputMessageContentTypeText,
				Text: schemas.Ptr(""),
				ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
					LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
					Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
				},
			},
		})

		state.HasStartedText = true
	} else {

		outputIndex = state.TextOutputIndex
	}

	if part.Text != "" {
		itemID := state.ItemIDs[outputIndex]
		contentIndex := 0
		text := part.Text

		state.TextBuffer.WriteString(text)

		streamResponse := &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputTextDelta,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ContentIndex:   &contentIndex,
			ItemID:         &itemID,
			Delta:          &text,
			LogProbs:       []schemas.ResponsesOutputMessageContentTextLogProb{},
		}
		if len(part.ThoughtSignature) > 0 {
			thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
			streamResponse.Signature = &thoughtSig
		}

		responses = append(responses, streamResponse)
	}

	return responses
}

func processGeminiThoughtPart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	outputIndex := state.nextOutputIndex()
	itemID := state.generateItemID("reasoning", outputIndex)
	state.ItemIDs[outputIndex] = itemID

	summaryIndex := 0
	thoughtText := part.Text

	var thoughtSignature *string
	if len(part.ThoughtSignature) > 0 {
		thoughtSignature = schemas.Ptr(base64.StdEncoding.EncodeToString(part.ThoughtSignature))
	}

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:   &itemID,
			Type: schemas.Ptr(schemas.ResponsesMessageTypeReasoning),
			Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			ResponsesReasoning: &schemas.ResponsesReasoning{
				Summary: []schemas.ResponsesReasoningSummary{},
			},
		},
	})

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeReasoningSummaryPartAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		SummaryIndex:   &summaryIndex,
		Part: &schemas.ResponsesMessageContentBlock{
			Type: schemas.ResponsesOutputMessageContentTypeSummaryText,
			Text: schemas.Ptr(""),
		},
	})

	if thoughtText != "" {
		text := thoughtText
		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeReasoningSummaryTextDelta,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			SummaryIndex:   &summaryIndex,
			Delta:          &text,
			Signature:      thoughtSignature,
		})
	}

	doneText := thoughtText
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeReasoningSummaryTextDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		SummaryIndex:   &summaryIndex,
		Text:           &doneText,
	})

	partText := thoughtText
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeReasoningSummaryPartDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		SummaryIndex:   &summaryIndex,
		Part: &schemas.ResponsesMessageContentBlock{
			Type: schemas.ResponsesOutputMessageContentTypeSummaryText,
			Text: &partText,
		},
	})

	statusCompleted := "completed"
	itemSummary := []schemas.ResponsesReasoningSummary{}
	if thoughtText != "" {
		itemSummary = append(itemSummary, schemas.ResponsesReasoningSummary{
			Type: schemas.ResponsesReasoningContentBlockTypeSummaryText,
			Text: thoughtText,
		})
	}
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:     &itemID,
			Type:   schemas.Ptr(schemas.ResponsesMessageTypeReasoning),
			Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			Status: &statusCompleted,
			ResponsesReasoning: &schemas.ResponsesReasoning{
				Summary:          itemSummary,
				EncryptedContent: thoughtSignature,
			},
		},
	})

	return responses
}

func processGeminiThoughtSignaturePart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	outputIndex := state.nextOutputIndex()
	itemID := state.generateItemID("reasoning", outputIndex)
	state.ItemIDs[outputIndex] = itemID

	thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)

	nativeParts := nativePartPayload(part)

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:   &itemID,
			Type: schemas.Ptr(schemas.ResponsesMessageTypeReasoning),
			Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			ResponsesReasoning: &schemas.ResponsesReasoning{
				Summary:          []schemas.ResponsesReasoningSummary{},
				EncryptedContent: &thoughtSig,
			},
			ProviderNativeParts: nativeParts,
		},
	})

	statusCompleted := "completed"
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:     &itemID,
			Type:   schemas.Ptr(schemas.ResponsesMessageTypeReasoning),
			Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			Status: &statusCompleted,
			ResponsesReasoning: &schemas.ResponsesReasoning{
				Summary:          []schemas.ResponsesReasoningSummary{},
				EncryptedContent: &thoughtSig,
			},
			ProviderNativeParts: nativeParts,
		},
	})

	return responses
}

func nativePartPayload(part *Part) json.RawMessage {
	if part == nil || (part.ToolCall == nil && part.ToolResponse == nil) {
		return nil
	}
	encoded, err := sonic.Marshal([]*Part{part})
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

func processGeminiFunctionCallPart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	outputIndex := state.nextOutputIndex()

	toolUseID := part.FunctionCall.ID
	if toolUseID == "" {
		toolUseID = part.FunctionCall.Name
	}

	state.ItemIDs[outputIndex] = toolUseID
	state.ToolCallIDs[outputIndex] = toolUseID
	state.ToolCallNames[outputIndex] = part.FunctionCall.Name

	argsJSON := ""
	if len(part.FunctionCall.Args) > 0 {
		argsJSON = string(part.FunctionCall.Args)
	}
	state.ToolArgumentBuffers[outputIndex] = argsJSON

	if len(part.ThoughtSignature) > 0 && !strings.Contains(toolUseID, thoughtSignatureSeparator) {
		encoded := base64.RawURLEncoding.EncodeToString(part.ThoughtSignature)
		toolUseID = fmt.Sprintf("%s%s%s", toolUseID, thoughtSignatureSeparator, encoded)
	}

	status := "in_progress"
	addedEvent := &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &toolUseID,
		Item: &schemas.ResponsesMessage{
			ID:     &toolUseID,
			Type:   schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
			Status: &status,
			ResponsesToolMessage: &schemas.ResponsesToolMessage{
				CallID:    &toolUseID,
				Name:      &part.FunctionCall.Name,
				Arguments: schemas.Ptr(""),
			},
		},
	}

	responses = append(responses, addedEvent)

	if argsJSON != "" {
		deltaEvents := generateSyntheticFunctionCallArgumentDeltas(
			argsJSON,
			&outputIndex,
			&toolUseID,
			sequenceNumber+len(responses),
		)
		responses = append(responses, deltaEvents...)
	}

	doneEvent := &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &toolUseID,
		Arguments:      &argsJSON,
	}

	responses = append(responses, doneEvent)

	outputItemDone := &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &toolUseID,
		Item: &schemas.ResponsesMessage{
			ID:     &toolUseID,
			Type:   schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
			Status: schemas.Ptr("completed"),
			ResponsesToolMessage: &schemas.ResponsesToolMessage{
				CallID:    &toolUseID,
				Name:      &part.FunctionCall.Name,
				Arguments: &argsJSON,
			},
		},
	}

	responses = append(responses, outputItemDone)

	delete(state.ToolArgumentBuffers, outputIndex)

	state.HasStartedToolCall = true

	return responses
}

func geminiCodeInterpreterItem(state *GeminiResponsesStreamState, result *CodeExecutionResult) *schemas.ResponsesMessage {
	ci := &schemas.ResponsesCodeInterpreterToolCall{Code: schemas.Ptr(state.CodeExecCode)}
	if result != nil {
		ci.Outputs = geminiCodeExecutionOutputs(result)
	}
	return &schemas.ResponsesMessage{
		ID:     schemas.Ptr(state.CodeExecItemID),
		Type:   schemas.Ptr(schemas.ResponsesMessageTypeCodeInterpreterCall),
		Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
		Status: schemas.Ptr(geminiCodeExecutionStatus(result)),
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			CallID:                           schemas.Ptr(state.CodeExecItemID),
			ResponsesCodeInterpreterToolCall: ci,
		},
	}
}

func geminiOpenCodeInterpreterItem(state *GeminiResponsesStreamState) *schemas.ResponsesMessage {
	item := geminiCodeInterpreterItem(state, nil)
	item.Status = schemas.Ptr("in_progress")
	return item
}

func (state *GeminiResponsesStreamState) closeCodeExecItemIfOpen(result *CodeExecutionResult, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	if state.CodeExecOutputIndex < 0 {
		return nil
	}
	outputIndex := state.CodeExecOutputIndex
	itemID := state.CodeExecItemID
	done := &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber,
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item:           geminiCodeInterpreterItem(state, result),
	}
	state.CodeExecOutputIndex = -1
	state.CodeExecItemID = ""
	state.CodeExecCode = ""
	return []*schemas.BifrostResponsesStreamResponse{done}
}

func processGeminiExecutableCodePart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	if closeResponses := state.closeCodeExecItemIfOpen(nil, sequenceNumber+len(responses)); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	outputIndex := state.nextOutputIndex()
	itemID := geminiCodeExecutionCallID()
	state.ItemIDs[outputIndex] = itemID
	state.CodeExecOutputIndex = outputIndex
	state.CodeExecItemID = itemID
	state.CodeExecCode = part.ExecutableCode.Code

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item:           geminiOpenCodeInterpreterItem(state),
	})

	return responses
}

func processGeminiCodeExecutionResultPart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if state.CodeExecOutputIndex < 0 {
		if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
			responses = append(responses, closeResponses...)
		}
		outputIndex := state.nextOutputIndex()
		itemID := geminiCodeExecutionCallID()
		state.ItemIDs[outputIndex] = itemID
		state.CodeExecOutputIndex = outputIndex
		state.CodeExecItemID = itemID
		state.CodeExecCode = ""

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			Item:           geminiOpenCodeInterpreterItem(state),
		})
	}

	responses = append(responses, state.closeCodeExecItemIfOpen(part.CodeExecutionResult, sequenceNumber+len(responses))...)
	return responses
}

func processGeminiFunctionResponsePart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	output := extractFunctionResponseOutput(part.FunctionResponse)

	outputIndex := state.nextOutputIndex()

	responseID := part.FunctionResponse.ID
	if responseID == "" {
		responseID = part.FunctionResponse.Name
	}

	itemID := fmt.Sprintf("func_resp_%s", responseID)
	state.ItemIDs[outputIndex] = itemID

	status := "completed"
	item := &schemas.ResponsesMessage{
		ID:     &itemID,
		Type:   schemas.Ptr(schemas.ResponsesMessageTypeFunctionCallOutput),
		Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
		Status: &status,
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			CallID: &responseID,
			Output: &schemas.ResponsesToolMessageOutputStruct{
				ResponsesToolCallOutputStr: &output,
			},
		},
	}

	if name := strings.TrimSpace(part.FunctionResponse.Name); name != "" {
		item.ResponsesToolMessage.Name = schemas.Ptr(name)
	}

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item:           item,
	})

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:     &itemID,
			Type:   schemas.Ptr(schemas.ResponsesMessageTypeFunctionCallOutput),
			Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			Status: &status,
			ResponsesToolMessage: &schemas.ResponsesToolMessage{
				CallID: &responseID,
				Output: &schemas.ResponsesToolMessageOutputStruct{
					ResponsesToolCallOutputStr: &output,
				},
			},
		},
	})

	if name := strings.TrimSpace(part.FunctionResponse.Name); name != "" {
		last := responses[len(responses)-1]
		if last.Item != nil && last.Item.ResponsesToolMessage != nil {
			last.Item.ResponsesToolMessage.Name = schemas.Ptr(name)
		}
	}

	return responses
}

func processGeminiInlineDataPart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	block := convertGeminiInlineDataToContentBlock(part.InlineData)
	if block == nil {
		return responses
	}
	applyGeminiThoughtSignatureToContentBlock(block, part)

	outputIndex := state.nextOutputIndex()
	itemID := state.generateItemID("item", outputIndex)
	state.ItemIDs[outputIndex] = itemID

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:   &itemID,
			Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			Content: &schemas.ResponsesMessageContent{
				ContentBlocks: []schemas.ResponsesMessageContentBlock{*block},
			},
		},
	})

	contentIndex := 0
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeContentPartAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ContentIndex:   &contentIndex,
		ItemID:         &itemID,
		Part:           block,
	})

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeContentPartDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ContentIndex:   &contentIndex,
		ItemID:         &itemID,
		Part:           block,
	})

	statusCompleted := "completed"
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:     &itemID,
			Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			Status: &statusCompleted,
			Content: &schemas.ResponsesMessageContent{
				ContentBlocks: []schemas.ResponsesMessageContentBlock{},
			},
		},
	})

	return responses
}

func processGeminiFileDataPart(part *Part, state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	block := convertGeminiFileDataToContentBlock(part.FileData)
	if block == nil {
		return responses
	}
	applyGeminiThoughtSignatureToContentBlock(block, part)

	outputIndex := state.nextOutputIndex()
	itemID := state.generateItemID("item", outputIndex)
	state.ItemIDs[outputIndex] = itemID

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:   &itemID,
			Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			Content: &schemas.ResponsesMessageContent{
				ContentBlocks: []schemas.ResponsesMessageContentBlock{*block},
			},
		},
	})

	contentIndex := 0
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeContentPartAdded,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ContentIndex:   &contentIndex,
		ItemID:         &itemID,
		Part:           block,
	})

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeContentPartDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ContentIndex:   &contentIndex,
		ItemID:         &itemID,
		Part:           block,
	})

	statusCompleted := "completed"
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item: &schemas.ResponsesMessage{
			ID:     &itemID,
			Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
			Status: &statusCompleted,
			Content: &schemas.ResponsesMessageContent{
				ContentBlocks: []schemas.ResponsesMessageContentBlock{},
			},
		},
	})

	return responses
}

func closeGeminiTextItem(state *GeminiResponsesStreamState, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	outputIndex := state.TextOutputIndex
	itemID := state.ItemIDs[outputIndex]
	contentIndex := 0

	fullText := state.TextBuffer.String()
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputTextDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ContentIndex:   &contentIndex,
		ItemID:         &itemID,
		Text:           &fullText,
		LogProbs:       []schemas.ResponsesOutputMessageContentTextLogProb{},
	})

	partText := fullText
	part := &schemas.ResponsesMessageContentBlock{
		Type: schemas.ResponsesOutputMessageContentTypeText,
		Text: &partText,
		ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
			LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
			Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
		},
	}
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeContentPartDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ContentIndex:   &contentIndex,
		ItemID:         &itemID,
		Part:           part,
	})

	itemText := fullText
	doneItem := &schemas.ResponsesMessage{
		Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
		Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
		Status: schemas.Ptr("completed"),
		Content: &schemas.ResponsesMessageContent{
			ContentBlocks: []schemas.ResponsesMessageContentBlock{
				{
					Type: schemas.ResponsesOutputMessageContentTypeText,
					Text: &itemText,
					ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
						Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
						LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
					},
				},
			},
		},
	}
	if itemID != "" {
		doneItem.ID = &itemID
	}
	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
		SequenceNumber: sequenceNumber + len(responses),
		OutputIndex:    &outputIndex,
		ItemID:         &itemID,
		Item:           doneItem,
	})

	state.TextItemClosed = true

	return responses
}

func closeGeminiOpenItems(state *GeminiResponsesStreamState, groundingMetadata *GroundingMetadata, usage *GenerateContentResponseUsageMetadata, sequenceNumber int, finishReason FinishReason, finishMessage string) []*schemas.BifrostResponsesStreamResponse {
	if state.HasEmittedCompleted {
		return nil
	}

	var responses []*schemas.BifrostResponsesStreamResponse

	if closeResponses := state.closeTextItemIfOpen(sequenceNumber); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	if groundingMetadata != nil && len(groundingMetadata.GroundingSupports) > 0 && state.TextOutputIndex >= 0 {
		annotationResponses := emitAnnotationsFromGroundingSupports(
			groundingMetadata,
			state,
			sequenceNumber+len(responses),
		)
		responses = append(responses, annotationResponses...)
	}

	if closeResponses := state.closeCodeExecItemIfOpen(nil, sequenceNumber+len(responses)); closeResponses != nil {
		responses = append(responses, closeResponses...)
	}

	for outputIndex := range state.ToolArgumentBuffers {
		itemID := state.ItemIDs[outputIndex]
		toolCallID := state.ToolCallIDs[outputIndex]
		toolName := state.ToolCallNames[outputIndex]
		toolArgs := state.ToolArgumentBuffers[outputIndex]
		if strings.TrimSpace(toolName) == "" {
			toolName = toolCallID
		}

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			Item: &schemas.ResponsesMessage{
				ID:     &itemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
				Status: schemas.Ptr("completed"),
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					CallID:    &toolCallID,
					Name:      &toolName,
					Arguments: &toolArgs,
				},
			},
		})
	}

	if isErrorFinishReason(finishReason) && finishMessage != "" {
		errorText := fmt.Sprintf("Error: %s - %s", finishReason, finishMessage)
		outputIndex := state.nextOutputIndex()
		itemID := state.generateItemID("error", outputIndex)
		state.ItemIDs[outputIndex] = itemID
		contentIndex := 0

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			Item: &schemas.ResponsesMessage{
				ID:     &itemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				Status: schemas.Ptr("in_progress"),
				Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: []schemas.ResponsesMessageContentBlock{},
				},
			},
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeContentPartAdded,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ContentIndex:   &contentIndex,
			ItemID:         &itemID,
			Part: &schemas.ResponsesMessageContentBlock{
				Type: schemas.ResponsesOutputMessageContentTypeText,
				Text: schemas.Ptr(""),
			},
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputTextDelta,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ContentIndex:   &contentIndex,
			ItemID:         &itemID,
			Delta:          &errorText,
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputTextDone,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ContentIndex:   &contentIndex,
			ItemID:         &itemID,
			Text:           &errorText,
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeContentPartDone,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ContentIndex:   &contentIndex,
			ItemID:         &itemID,
			Part: &schemas.ResponsesMessageContentBlock{
				Type: schemas.ResponsesOutputMessageContentTypeText,
				Text: &errorText,
			},
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			Item: &schemas.ResponsesMessage{
				ID:     &itemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				Status: schemas.Ptr("completed"),
				Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: []schemas.ResponsesMessageContentBlock{
						{
							Type: schemas.ResponsesOutputMessageContentTypeText,
							Text: &errorText,
						},
					},
				},
			},
		})
	}

	bifrostUsage := ConvertGeminiUsageMetadataToResponsesUsage(usage)
	if state.Model != nil {
		applyGeminiSearchQueryResponsesUsage(bifrostUsage, groundingMetadata, *state.Model)
	}

	recordGeminiOutputItems(state, responses)

	completedResp := &schemas.BifrostResponsesResponse{
		ID:        state.MessageID,
		CreatedAt: state.CreatedAt,
		Usage:     bifrostUsage,
	}

	if len(state.OutputItems) > 0 {
		completedResp.Output = make([]schemas.ResponsesMessage, 0, len(state.OutputItems))
		for i := 0; i < state.CurrentOutputIndex; i++ {
			if item, exists := state.OutputItems[i]; exists && item != nil {
				completedResp.Output = append(completedResp.Output, *item)
			}
		}
	}
	if usage != nil {
		if t := mapGeminiTrafficTypeToBifrost(usage.TrafficType); t != nil {
			completedResp.ServiceTier = t
		} else if usage.ServiceTier != "" {
			tier := mapGeminiServiceTierToBifrost(usage.ServiceTier)
			completedResp.ServiceTier = &tier
		}
	}
	if state.Model != nil {
		completedResp.Model = *state.Model
	}

	if state.PromptBlockError != nil {
		completedResp.StopReason = new(ConvertGeminiFinishReasonToBifrost(FinishReasonSafety))
		completedResp.Status = new(schemas.ResponsesResponseStatusFailed)
		completedResp.Error = state.PromptBlockError
	} else if finishReason != "" {
		stopReason := ConvertGeminiFinishReasonToBifrost(finishReason)
		completedResp.StopReason = &stopReason

		if isErrorFinishReason(finishReason) {
			failedStatus := "failed"
			completedResp.Status = &failedStatus
		} else {
			completedResp.Status, completedResp.IncompleteDetails = geminiResponsesStatus(stopReason)
		}
	} else {

		completedResp.Status = schemas.Ptr(schemas.ResponsesResponseStatusIncomplete)
	}

	terminalType := schemas.ResponsesStreamResponseTypeCompleted
	if completedResp.Status != nil && *completedResp.Status == schemas.ResponsesResponseStatusIncomplete {
		terminalType = schemas.ResponsesStreamResponseTypeIncomplete
		schemas.MarkTruncatedOutputItem(completedResp.Output)
	} else if state.PromptBlockError != nil {

		terminalType = schemas.ResponsesStreamResponseTypeFailed
	}

	extraFields := map[string]interface{}{}
	if state.ResponseID != nil && *state.ResponseID != "" {
		extraFields["responseId"] = *state.ResponseID
	}
	if len(state.SafetyRatings) > 0 {
		extraFields["safetyRatings"] = state.SafetyRatings
	}
	if state.AvgLogprobs != 0 {
		extraFields["avgLogprobs"] = state.AvgLogprobs
	}
	if state.URLContextMetadata != nil {
		extraFields["urlContextMetadata"] = state.URLContextMetadata
	}
	if state.ContinuationToken != "" {
		extraFields["continuationToken"] = state.ContinuationToken
	}
	if len(extraFields) > 0 {
		completedResp.ProviderExtraFields = extraFields
	}

	responses = append(responses, &schemas.BifrostResponsesStreamResponse{
		Type:           terminalType,
		SequenceNumber: sequenceNumber + len(responses),
		Response:       completedResp,
	})

	state.HasEmittedCompleted = true

	return responses
}

func (state *GeminiResponsesStreamState) startEvents(sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	if state.HasEmittedCreated {
		return nil
	}
	if state.MessageID == nil {
		messageID := fmt.Sprintf("msg_%d", state.CreatedAt)
		state.MessageID = &messageID
	}
	var responses []*schemas.BifrostResponsesStreamResponse
	for _, eventType := range []schemas.ResponsesStreamResponseType{
		schemas.ResponsesStreamResponseTypeCreated,
		schemas.ResponsesStreamResponseTypeInProgress,
	} {
		resp := &schemas.BifrostResponsesResponse{
			ID:        state.MessageID,
			CreatedAt: state.CreatedAt,
		}
		if state.Model != nil {
			resp.Model = *state.Model
		}
		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           eventType,
			SequenceNumber: sequenceNumber + len(responses),
			Response:       resp,
		})
	}
	state.HasEmittedCreated = true
	state.HasEmittedInProgress = true
	return responses
}

func FinalizeGeminiResponsesStream(state *GeminiResponsesStreamState, usage *GenerateContentResponseUsageMetadata, sequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse
	if !state.HasEmittedCompleted {
		responses = append(responses, state.startEvents(sequenceNumber)...)
	}
	return append(responses, closeGeminiOpenItems(state, nil, usage, sequenceNumber+len(responses), state.PendingFinishReason, state.PendingFinishMessage)...)
}

func convertGeminiInlineDataToContentBlock(blob *Blob) *schemas.ResponsesMessageContentBlock {
	if blob == nil {
		return nil
	}

	mimeType := blob.MIMEType
	if mimeType == "" {
		return nil
	}

	if isImageMimeType(mimeType) {

		imageURL := fmt.Sprintf("data:%s;base64,%s", mimeType, blob.Data)
		return &schemas.ResponsesMessageContentBlock{
			Type: schemas.ResponsesInputMessageContentBlockTypeImage,
			ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
				ImageURL: &imageURL,
			},
		}
	}

	if strings.HasPrefix(mimeType, "audio/") {
		encodedData := blob.Data
		format := mimeType
		if strings.HasPrefix(mimeType, "audio/") {
			format = mimeType[6:]
		}

		return &schemas.ResponsesMessageContentBlock{
			Type: schemas.ResponsesInputMessageContentBlockTypeAudio,
			Audio: &schemas.ResponsesInputMessageContentBlockAudio{
				Format: format,
				Data:   encodedData,
			},
		}
	}

	mimeTypeForFile := mimeType
	if mimeTypeForFile == "" {
		mimeTypeForFile = "application/pdf"
	}

	filename := blob.DisplayName
	if filename == "" {
		filename = "unnamed_file"
	}

	fileDataURL := blob.Data
	if !strings.HasPrefix(fileDataURL, "data:") {
		fileDataURL = fmt.Sprintf("data:%s;base64,%s", mimeTypeForFile, fileDataURL)
	}
	return &schemas.ResponsesMessageContentBlock{
		Type: schemas.ResponsesInputMessageContentBlockTypeFile,
		ResponsesInputMessageContentBlockFile: &schemas.ResponsesInputMessageContentBlockFile{
			FileData: &fileDataURL,
			FileType: &mimeTypeForFile,
			Filename: &filename,
		},
	}
}

func applyGeminiThoughtSignatureToContentBlock(block *schemas.ResponsesMessageContentBlock, part *Part) {
	if block == nil || part == nil || len(part.ThoughtSignature) == 0 {
		return
	}
	signature := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
	block.Signature = &signature
}

func convertGeminiFileDataToContentBlock(fileData *FileData) *schemas.ResponsesMessageContentBlock {
	if fileData == nil || fileData.FileURI == "" {
		return nil
	}

	mimeType := fileData.MIMEType

	if isImageMimeType(mimeType) {
		return &schemas.ResponsesMessageContentBlock{
			Type: schemas.ResponsesInputMessageContentBlockTypeImage,
			ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
				ImageURL: &fileData.FileURI,
			},
		}
	}

	block := &schemas.ResponsesMessageContentBlock{
		Type: schemas.ResponsesInputMessageContentBlockTypeFile,
		ResponsesInputMessageContentBlockFile: &schemas.ResponsesInputMessageContentBlockFile{
			FileURL: &fileData.FileURI,
		},
	}

	if mimeType != "" {
		block.ResponsesInputMessageContentBlockFile.FileType = &mimeType
	}

	return block
}

const (
	geminiSearchContentTypeText  = "text"
	geminiSearchContentTypeImage = "image"
)

const geminiNativeToolsExtraParamKey = "gemini_native_tools"

func extractGeminiNativeTools(v interface{}) []Tool {
	if v == nil {
		return nil
	}
	if tools, ok := v.([]Tool); ok {
		return tools
	}
	b, err := providerUtils.MarshalSorted(v)
	if err != nil {
		return nil
	}
	var tools []Tool
	if err := sonic.Unmarshal(b, &tools); err != nil {
		return nil
	}
	return tools
}

func serverSideToolParts(candidate *Candidate) []*Part {
	if candidate == nil || candidate.Content == nil {
		return nil
	}
	hasToolPart := false
	for _, part := range candidate.Content.Parts {
		if part != nil && (part.ToolCall != nil || part.ToolResponse != nil) {
			hasToolPart = true
			break
		}
	}
	if !hasToolPart {
		return nil
	}
	parts := make([]*Part, 0, len(candidate.Content.Parts))
	for _, part := range candidate.Content.Parts {
		if part != nil {
			parts = append(parts, part)
		}
	}
	return parts
}

func firstSearchCallIndex(indices []int) (int, bool) {
	first, found := 0, false
	for _, idx := range indices {
		if !found || idx < first {
			first, found = idx, true
		}
	}
	return first, found
}

func reasoningFromThoughtSignature(part *Part) (schemas.ResponsesMessage, bool) {
	if part == nil || len(part.ThoughtSignature) == 0 {
		return schemas.ResponsesMessage{}, false
	}
	thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
	return schemas.ResponsesMessage{
		Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
		Type: schemas.Ptr(schemas.ResponsesMessageTypeReasoning),
		ResponsesReasoning: &schemas.ResponsesReasoning{
			Summary:          []schemas.ResponsesReasoningSummary{},
			EncryptedContent: &thoughtSig,
		},
	}, true
}

func toolCallSearchQueries(call *ToolCall) []string {
	if call == nil || call.Args == nil {
		return nil
	}
	raw, ok := call.Args["queries"].([]any)
	if !ok {
		return nil
	}
	queries := make([]string, 0, len(raw))
	for _, q := range raw {
		if s, ok := q.(string); ok && s != "" {
			queries = append(queries, s)
		}
	}
	if len(queries) == 0 {
		return nil
	}
	return queries
}

func newWebSearchActionFromToolCall(call *ToolCall) *schemas.ResponsesWebSearchToolCallAction {
	action := &schemas.ResponsesWebSearchToolCallAction{Type: "search"}
	queries := toolCallSearchQueries(call)
	if len(queries) > 0 {
		action.Queries = queries
		action.Query = schemas.Ptr(queries[0])
	}
	if call != nil && strings.EqualFold(call.ToolType, "GOOGLE_SEARCH_IMAGE") {
		action.ImageQueries = queries
	}
	return action
}

func textBlockOf(messages []schemas.ResponsesMessage, idx int) *schemas.ResponsesOutputMessageContentText {
	if idx < 0 || idx >= len(messages) || messages[idx].Content == nil {
		return nil
	}
	for i := range messages[idx].Content.ContentBlocks {
		block := &messages[idx].Content.ContentBlocks[i]
		if block.Type == schemas.ResponsesOutputMessageContentTypeText && block.ResponsesOutputMessageContentText != nil {
			return block.ResponsesOutputMessageContentText
		}
	}
	return nil
}

func groundingChunkSource(chunk *GroundingChunk) (schemas.ResponsesWebSearchToolCallActionSearchSource, bool) {
	if chunk == nil {
		return schemas.ResponsesWebSearchToolCallActionSearchSource{}, false
	}
	source := schemas.ResponsesWebSearchToolCallActionSearchSource{Type: "url"}

	switch {
	case chunk.Web != nil && chunk.Web.URI != "":
		source.URL = chunk.Web.URI
		if chunk.Web.Title != "" {
			source.Title = schemas.Ptr(chunk.Web.Title)
		}
	case chunk.Image != nil && chunk.Image.SourceURI != "":
		source.URL = chunk.Image.SourceURI
		if chunk.Image.Title != "" {
			source.Title = schemas.Ptr(chunk.Image.Title)
		}
		if chunk.Image.ImageURI != "" {
			source.ImageURL = schemas.Ptr(chunk.Image.ImageURI)
		}
		if chunk.Image.Domain != "" {
			source.Domain = schemas.Ptr(chunk.Image.Domain)
		}
	default:
		return source, false
	}

	return source, true
}

const geminiCodeExecutionErrorPrefix = "Error: "

func geminiCodeExecutionStatus(result *CodeExecutionResult) string {
	if result != nil && result.Outcome != OutcomeOK {
		return "failed"
	}
	return "completed"
}

func geminiCodeExecutionOutputs(result *CodeExecutionResult) []schemas.ResponsesCodeInterpreterOutput {
	return []schemas.ResponsesCodeInterpreterOutput{{
		ResponsesCodeInterpreterOutputLogs: &schemas.ResponsesCodeInterpreterOutputLogs{
			Type: "logs",
			Logs: result.Output,
		},
	}}
}

func geminiPartsFromCodeInterpreterCall(msg *schemas.ResponsesMessage) []*Part {
	ci := msg.ResponsesToolMessage.ResponsesCodeInterpreterToolCall
	if ci == nil {
		return nil
	}
	var logs strings.Builder
	hasResult := false
	for _, out := range ci.Outputs {
		if out.ResponsesCodeInterpreterOutputLogs != nil {
			hasResult = true
			logs.WriteString(out.ResponsesCodeInterpreterOutputLogs.Logs)
		}
	}
	failed := msg.Status != nil && *msg.Status == "failed"
	if ci.Code == nil || *ci.Code == "" {
		if logs.Len() == 0 {
			return nil
		}
		if failed {
			return []*Part{{Text: geminiCodeExecutionErrorPrefix + logs.String()}}
		}
		return []*Part{{Text: logs.String()}}
	}
	parts := []*Part{{ExecutableCode: &ExecutableCode{Language: "PYTHON", Code: *ci.Code}}}
	if !hasResult && !failed {
		return parts
	}
	outcome := OutcomeOK
	if failed {
		outcome = OutcomeFailed
	}
	return append(parts, &Part{CodeExecutionResult: &CodeExecutionResult{Outcome: outcome, Output: logs.String()}})
}

func newGeminiCodeInterpreterCall(code *ExecutableCode, result *CodeExecutionResult) schemas.ResponsesMessage {
	callID := geminiCodeExecutionCallID()
	ci := &schemas.ResponsesCodeInterpreterToolCall{}
	if code != nil {
		ci.Code = schemas.Ptr(code.Code)
	}
	if result != nil {
		ci.Outputs = geminiCodeExecutionOutputs(result)
	}
	return schemas.ResponsesMessage{
		ID:     schemas.Ptr(callID),
		Type:   schemas.Ptr(schemas.ResponsesMessageTypeCodeInterpreterCall),
		Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
		Status: schemas.Ptr(geminiCodeExecutionStatus(result)),
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			CallID:                           schemas.Ptr(callID),
			ResponsesCodeInterpreterToolCall: ci,
		},
	}
}

func geminiCodeExecutionCallID() string {
	return "ci_" + schemas.GetRandomString(24)
}

func attachGeminiCodeExecutionResult(msgs []schemas.ResponsesMessage, result *CodeExecutionResult) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := &msgs[i]
		if msg.Type == nil || *msg.Type != schemas.ResponsesMessageTypeCodeInterpreterCall {
			continue
		}
		if msg.ResponsesToolMessage == nil || msg.ResponsesToolMessage.ResponsesCodeInterpreterToolCall == nil {
			continue
		}
		ci := msg.ResponsesToolMessage.ResponsesCodeInterpreterToolCall
		if len(ci.Outputs) > 0 {
			return false
		}
		ci.Outputs = geminiCodeExecutionOutputs(result)
		msg.Status = schemas.Ptr(geminiCodeExecutionStatus(result))
		return true
	}
	return false
}

func convertGeminiCandidatesToResponsesOutput(candidates []*Candidate) []schemas.ResponsesMessage {
	var messages []schemas.ResponsesMessage

	for _, candidate := range candidates {
		if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
			continue
		}

		textMessageIdx := -1

		searchCallIndexByID := map[string]int{}

		var anonymousSearchCalls []int

		var searchCallIndices []int

		for _, part := range candidate.Content.Parts {

			switch {
			case part.Thought:

				if part.Text != "" || len(part.ThoughtSignature) > 0 {
					text := part.Text
					msg := schemas.ResponsesMessage{
						Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
						Content: &schemas.ResponsesMessageContent{
							ContentBlocks: []schemas.ResponsesMessageContentBlock{
								{
									Type: schemas.ResponsesOutputMessageContentTypeReasoning,
									Text: &text,
								},
							},
						},
						Type: schemas.Ptr(schemas.ResponsesMessageTypeReasoning),
					}
					if len(part.ThoughtSignature) > 0 {

						encoded := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
						msg.ResponsesReasoning = &schemas.ResponsesReasoning{
							Summary:          []schemas.ResponsesReasoningSummary{},
							EncryptedContent: &encoded,
						}
						msg.Content.ContentBlocks[0].Signature = &encoded
					}
					messages = append(messages, msg)
				}

			case part.Text != "":

				msg := schemas.ResponsesMessage{
					ID:     schemas.Ptr("msg_" + schemas.GetRandomString(50)),
					Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Status: schemas.Ptr("completed"),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{
							{
								Type: schemas.ResponsesOutputMessageContentTypeText,
								Text: &part.Text,
								ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
									LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
									Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
								},
							},
						},
					},
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				}

				if len(part.ThoughtSignature) > 0 {
					thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
					msg.Content.ContentBlocks[len(msg.Content.ContentBlocks)-1].Signature = &thoughtSig
				}
				messages = append(messages, msg)
				if textMessageIdx < 0 {
					textMessageIdx = len(messages) - 1
				}

			case part.FunctionCall != nil:

				argumentsStr := ""
				if len(part.FunctionCall.Args) > 0 {
					argumentsStr = string(part.FunctionCall.Args)
				}

				callID := part.FunctionCall.ID
				if strings.TrimSpace(callID) == "" {
					callID = part.FunctionCall.Name
				}

				if len(part.ThoughtSignature) > 0 && !strings.Contains(callID, thoughtSignatureSeparator) {
					thoughtSig := base64.RawURLEncoding.EncodeToString(part.ThoughtSignature)
					callID = fmt.Sprintf("%s%s%s", callID, thoughtSignatureSeparator, thoughtSig)
				}

				name := part.FunctionCall.Name
				toolMsg := &schemas.ResponsesToolMessage{
					CallID:    &callID,
					Name:      &name,
					Arguments: &argumentsStr,
				}
				msg := schemas.ResponsesMessage{
					ID:                   schemas.Ptr("fc_" + schemas.GetRandomString(50)),
					Role:                 schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Type:                 schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
					Status:               schemas.Ptr("completed"),
					ResponsesToolMessage: toolMsg,
				}
				messages = append(messages, msg)

			case part.FunctionResponse != nil:

				output := extractFunctionResponseOutput(part.FunctionResponse)

				msg := schemas.ResponsesMessage{
					Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Type: schemas.Ptr(schemas.ResponsesMessageTypeFunctionCallOutput),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID: schemas.Ptr(part.FunctionResponse.ID),
						Output: &schemas.ResponsesToolMessageOutputStruct{
							ResponsesToolCallOutputStr: &output,
						},
					},
				}

				if name := strings.TrimSpace(part.FunctionResponse.Name); name != "" {
					msg.ResponsesToolMessage.Name = schemas.Ptr(name)
				} else {

					if strings.Contains(part.FunctionResponse.ID, thoughtSignatureSeparator) {
						parts := strings.SplitN(part.FunctionResponse.ID, thoughtSignatureSeparator, 2)
						if len(parts) == 2 {
							name := parts[0]
							msg.ResponsesToolMessage.Name = schemas.Ptr(name)
						}
					} else {
						msg.ResponsesToolMessage.Name = schemas.Ptr(part.FunctionResponse.ID)
					}
				}
				messages = append(messages, msg)

			case part.InlineData != nil:

				contentBlocks := []schemas.ResponsesMessageContentBlock{
					{
						Type: func() schemas.ResponsesMessageContentBlockType {
							if strings.HasPrefix(part.InlineData.MIMEType, "image/") {
								return schemas.ResponsesInputMessageContentBlockTypeImage
							} else if strings.HasPrefix(part.InlineData.MIMEType, "audio/") {
								return schemas.ResponsesInputMessageContentBlockTypeAudio
							}
							return schemas.ResponsesInputMessageContentBlockTypeText
						}(),
						ResponsesInputMessageContentBlockImage: func() *schemas.ResponsesInputMessageContentBlockImage {
							if strings.HasPrefix(part.InlineData.MIMEType, "image/") {
								return &schemas.ResponsesInputMessageContentBlockImage{
									ImageURL: schemas.Ptr("data:" + part.InlineData.MIMEType + ";base64," + part.InlineData.Data),
								}
							}
							return nil
						}(),
						Audio: func() *schemas.ResponsesInputMessageContentBlockAudio {
							if strings.HasPrefix(part.InlineData.MIMEType, "audio/") {

								format := strings.TrimPrefix(part.InlineData.MIMEType, "audio/")
								return &schemas.ResponsesInputMessageContentBlockAudio{
									Format: format,
									Data:   part.InlineData.Data,
								}
							}
							return nil
						}(),
					},
				}

				msg := schemas.ResponsesMessage{
					Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: contentBlocks,
					},
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				}
				messages = append(messages, msg)

			case part.FileData != nil:

				block := schemas.ResponsesMessageContentBlock{
					Type: schemas.ResponsesInputMessageContentBlockTypeFile,
					ResponsesInputMessageContentBlockFile: &schemas.ResponsesInputMessageContentBlockFile{
						FileURL: schemas.Ptr(part.FileData.FileURI),
					},
				}
				if strings.HasPrefix(part.FileData.MIMEType, "image/") {
					block.Type = schemas.ResponsesInputMessageContentBlockTypeImage
					block.ResponsesInputMessageContentBlockImage = &schemas.ResponsesInputMessageContentBlockImage{
						ImageURL: schemas.Ptr(part.FileData.FileURI),
					}
				}
				contentBlocks := []schemas.ResponsesMessageContentBlock{block}

				msg := schemas.ResponsesMessage{
					Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: contentBlocks,
					},
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				}
				messages = append(messages, msg)

			case part.CodeExecutionResult != nil:

				if !attachGeminiCodeExecutionResult(messages, part.CodeExecutionResult) {
					messages = append(messages, newGeminiCodeInterpreterCall(nil, part.CodeExecutionResult))
				}

			case part.ExecutableCode != nil:
				messages = append(messages, newGeminiCodeInterpreterCall(part.ExecutableCode, nil))
			case part.ToolCall != nil:

				if msg, ok := reasoningFromThoughtSignature(part); ok {
					messages = append(messages, msg)
				}
				if !isSearchToolType(part.ToolCall.ToolType) {

					break
				}

				itemID := part.ToolCall.ID
				if itemID == "" {
					itemID = fmt.Sprintf("ws_%d", len(messages))
					anonymousSearchCalls = append(anonymousSearchCalls, len(messages))
				} else {
					searchCallIndexByID[itemID] = len(messages)
				}
				searchCallIndices = append(searchCallIndices, len(messages))
				messages = append(messages, schemas.ResponsesMessage{
					ID:     schemas.Ptr(itemID),
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
					Status: schemas.Ptr("in_progress"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						Action: &schemas.ResponsesToolMessageActionStruct{
							ResponsesWebSearchToolCallAction: newWebSearchActionFromToolCall(part.ToolCall),
						},
					},
				})

			case part.ToolResponse != nil:

				if msg, ok := reasoningFromThoughtSignature(part); ok {
					messages = append(messages, msg)
				}
				if part.ToolResponse.ID != "" {
					if idx, ok := searchCallIndexByID[part.ToolResponse.ID]; ok && idx < len(messages) {
						messages[idx].Status = schemas.Ptr("completed")
					}
				} else if len(anonymousSearchCalls) > 0 {

					idx := anonymousSearchCalls[0]
					anonymousSearchCalls = anonymousSearchCalls[1:]
					if idx < len(messages) {
						messages[idx].Status = schemas.Ptr("completed")
					}
				}

			case part.ThoughtSignature != nil:

				thoughtSig := base64.StdEncoding.EncodeToString(part.ThoughtSignature)
				msg := schemas.ResponsesMessage{
					Role: schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Type: schemas.Ptr(schemas.ResponsesMessageTypeReasoning),
					ResponsesReasoning: &schemas.ResponsesReasoning{
						Summary:          []schemas.ResponsesReasoningSummary{},
						EncryptedContent: &thoughtSig,
					},
				}
				messages = append(messages, msg)
			}
		}

		if candidate.GroundingMetadata != nil {
			webSearchmessage := schemas.ResponsesMessage{
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
				Status: schemas.Ptr("completed"),
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					Action: &schemas.ResponsesToolMessageActionStruct{
						ResponsesWebSearchToolCallAction: &schemas.ResponsesWebSearchToolCallAction{
							Type:    "search",
							Queries: candidate.GroundingMetadata.WebSearchQueries,
						},
					},
				},
			}
			if len(candidate.GroundingMetadata.WebSearchQueries) > 0 {
				webSearchmessage.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction.Query = schemas.Ptr(candidate.GroundingMetadata.WebSearchQueries[0])
			}
			if len(candidate.GroundingMetadata.ImageSearchQueries) > 0 {
				webSearchmessage.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction.ImageQueries = candidate.GroundingMetadata.ImageSearchQueries
			}

			sources := []schemas.ResponsesWebSearchToolCallActionSearchSource{}
			for _, chunk := range candidate.GroundingMetadata.GroundingChunks {
				if source, ok := groundingChunkSource(chunk); ok {
					sources = append(sources, source)
				}
			}

			if len(sources) > 0 {
				webSearchmessage.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction.Sources = sources
			}

			if idx, ok := firstSearchCallIndex(searchCallIndices); ok && idx < len(messages) {
				if existing := messages[idx].ResponsesToolMessage; existing != nil && existing.Action != nil {
					if action := existing.Action.ResponsesWebSearchToolCallAction; action != nil {
						if len(sources) > 0 {
							action.Sources = sources
						}
						if len(action.Queries) == 0 {
							action.Queries = candidate.GroundingMetadata.WebSearchQueries
							if len(action.Queries) > 0 {
								action.Query = schemas.Ptr(action.Queries[0])
							}
						}
						if len(action.ImageQueries) == 0 {
							action.ImageQueries = candidate.GroundingMetadata.ImageSearchQueries
						}
					}
				}
			} else {
				messages = append(messages, webSearchmessage)
			}

			if len(candidate.GroundingMetadata.GroundingSupports) > 0 {
				annotations := []schemas.ResponsesOutputMessageContentTextAnnotation{}
				for _, support := range candidate.GroundingMetadata.GroundingSupports {
					if support.Segment == nil {
						continue
					}

					for _, chunkIdx := range support.GroundingChunkIndices {
						if chunkIdx < 0 || int(chunkIdx) >= len(candidate.GroundingMetadata.GroundingChunks) {
							continue
						}
						source, ok := groundingChunkSource(candidate.GroundingMetadata.GroundingChunks[chunkIdx])
						if !ok {
							continue
						}
						annotation := schemas.ResponsesOutputMessageContentTextAnnotation{
							Type:       "url_citation",
							Text:       schemas.Ptr(support.Segment.Text),
							StartIndex: schemas.Ptr(int(support.Segment.StartIndex)),
							EndIndex:   schemas.Ptr(int(support.Segment.EndIndex)),
							URL:        schemas.Ptr(source.URL),
						}
						annotation.Title = source.Title
						annotations = append(annotations, annotation)
					}
				}

				if block := textBlockOf(messages, textMessageIdx); block != nil {
					block.Annotations = append(block.Annotations, annotations...)
				} else if len(annotations) > 0 {
					messages = append(messages, schemas.ResponsesMessage{
						Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
						Status: schemas.Ptr("completed"),
						Content: &schemas.ResponsesMessageContent{
							ContentBlocks: []schemas.ResponsesMessageContentBlock{
								{
									Type: schemas.ResponsesOutputMessageContentTypeText,
									Text: schemas.Ptr(""),
									ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
										LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
										Annotations: annotations,
									},
								},
							},
						},
					})
				}
			}

			if candidate.GroundingMetadata.SearchEntryPoint != nil &&
				candidate.GroundingMetadata.SearchEntryPoint.RenderedContent != "" {
				renderedContentMessage := schemas.ResponsesMessage{
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Status: schemas.Ptr("completed"),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{
							{
								Type: schemas.ResponsesOutputMessageContentTypeRenderedContent,
								ResponsesOutputMessageContentRenderedContent: &schemas.ResponsesOutputMessageContentRenderedContent{
									RenderedContent: candidate.GroundingMetadata.SearchEntryPoint.RenderedContent,
								},
							},
						},
					},
				}
				messages = append(messages, renderedContentMessage)
			}
		}
	}

	return messages
}

func convertTextConfigToGenerationConfig(textConfig *schemas.ResponsesTextConfig, config *GenerationConfig) error {
	if textConfig == nil || config == nil {
		return nil
	}

	if textConfig.Format == nil {
		return nil
	}

	switch textConfig.Format.Type {
	case "json_schema":
		config.ResponseMIMEType = "application/json"
		if textConfig.Format.JSONSchema != nil {
			schema, err := reconstructSchemaFromJSONSchema(textConfig.Format.JSONSchema)
			if err != nil {
				return err
			}
			if schema != nil {
				config.ResponseJSONSchema = schema
			}

		}

	case "json_object":
		config.ResponseMIMEType = "application/json"

	case "text":
		config.ResponseMIMEType = "text/plain"
	}
	return nil
}

func reconstructSchemaFromJSONSchema(jsonSchema *schemas.ResponsesTextConfigFormatJSONSchema) (interface{}, error) {
	composite, acceptAll, err := jsonSchema.CompositeSchema()
	if err != nil {
		return nil, err
	}
	if composite != nil {

		return normalizeSchemaValueForGemini(composite), nil
	}
	if acceptAll {

		return map[string]interface{}{"type": "object"}, nil
	}

	schema := make(map[string]interface{})

	if jsonSchema.Defs != nil {
		schema["$defs"] = *jsonSchema.Defs
	}

	if jsonSchema.Type != nil {
		schema["type"] = *jsonSchema.Type
	}

	if jsonSchema.Properties != nil {
		schema["properties"] = *jsonSchema.Properties
	}

	if len(jsonSchema.Required) > 0 {
		schema["required"] = jsonSchema.Required
	}

	if jsonSchema.Description != nil {
		schema["description"] = *jsonSchema.Description
	}

	if jsonSchema.AdditionalProperties != nil {
		schema["additionalProperties"] = *jsonSchema.AdditionalProperties
	}

	if jsonSchema.Name != nil {
		schema["title"] = *jsonSchema.Name
	}

	if len(jsonSchema.PropertyOrdering) > 0 {
		schema["propertyOrdering"] = jsonSchema.PropertyOrdering
	}

	if len(schema) == 0 {
		return nil, nil
	}

	return normalizeSchemaForGemini(schema), nil
}

func (r *GeminiGenerationRequest) convertParamsToGenerationConfigResponses(params *schemas.ResponsesParameters, provider schemas.ModelProvider, capModel string) (GenerationConfig, error) {
	config := GenerationConfig{}

	if params.Temperature != nil {
		config.Temperature = schemas.Ptr(float64(*params.Temperature))
	}
	if params.TopP != nil {
		config.TopP = schemas.Ptr(float64(*params.TopP))
	}
	if params.MaxOutputTokens != nil {
		config.MaxOutputTokens = int32(*params.MaxOutputTokens)
	}

	if params.TopLogProbs != nil {
		config.Logprobs = schemas.Ptr(int32(*params.TopLogProbs))
		if *params.TopLogProbs > 0 {
			config.ResponseLogprobs = true
		}
	}

	caps := schemas.ResolveModelCaps(provider, capModel)
	if params.Reasoning != nil && caps.SupportsReasoning(defaultSupportsReasoning(capModel)) {
		config.ThinkingConfig = &GenerationConfigThinkingConfig{
			IncludeThoughts: true,
		}

		hasMaxTokens := params.Reasoning.MaxTokens != nil
		hasEffort := params.Reasoning.Effort != nil
		supportsLevel := caps.SupportsReasoningEffort(isGemini3Plus(capModel))

		if !hasMaxTokens && hasEffort && *params.Reasoning.Effort == "none" {
			setThinkingBudgetZeroIfSupported(&config, caps)
		} else if hasMaxTokens {

			if err := applyThinkingBudget(&config, caps, *params.Reasoning.MaxTokens); err != nil {
				return config, err
			}
		} else if hasEffort {

			if supportsLevel {

				if level := effortToThinkingLevel(caps, *params.Reasoning.Effort); level != "" {
					config.ThinkingConfig.ThinkingLevel = schemas.Ptr(level)
				}
			} else {
				maxTokens := providerUtils.GetMaxOutputTokensOrDefault(provider, capModel, DefaultCompletionMaxTokens)
				if config.MaxOutputTokens > 0 {
					maxTokens = int(config.MaxOutputTokens)
				}
				budgetRange := getThinkingBudgetRange(caps, maxTokens)

				budgetTokens, err := providerUtils.GetBudgetTokensFromReasoningEffort(
					*params.Reasoning.Effort,
					budgetRange.Min,
					budgetRange.Max,
				)
				if err == nil {
					config.ThinkingConfig.ThinkingBudget = schemas.Ptr(int32(budgetTokens))
				}
			}
		}
	}
	if params.Text != nil {
		if err := convertTextConfigToGenerationConfig(params.Text, &config); err != nil {
			return config, err
		}
		dropJSONResponseFormatWithToolsIfUnsupported(&config, len(params.Tools) > 0, caps, capModel)
	}

	if params.ExtraParams != nil {
		if topK, ok := params.ExtraParams["top_k"]; ok {
			if val, success := schemas.SafeExtractInt(topK); success {
				config.TopK = schemas.Ptr(val)
			}
		}
		if frequencyPenalty, ok := params.ExtraParams["frequency_penalty"]; ok {
			if val, success := schemas.SafeExtractFloat64(frequencyPenalty); success {
				config.FrequencyPenalty = schemas.Ptr(val)
			}
		}
		if presencePenalty, ok := params.ExtraParams["presence_penalty"]; ok {
			if val, success := schemas.SafeExtractFloat64(presencePenalty); success {
				config.PresencePenalty = schemas.Ptr(val)
			}
		}
		if stopSequences, ok := params.ExtraParams["stop_sequences"]; ok {
			if val, success := schemas.SafeExtractStringSlice(stopSequences); success {
				config.StopSequences = val
			}
		}
		if mediaResolution, ok := params.ExtraParams["media_resolution"]; ok {
			if val, success := schemas.SafeExtractString(mediaResolution); success {
				config.MediaResolution = val
			}
		}
		if candidateCount, ok := params.ExtraParams["candidate_count"]; ok {
			if val, success := schemas.SafeExtractInt(candidateCount); success {
				config.CandidateCount = int32(val)
			}
		}
		if responseLogprobs, ok := params.ExtraParams["response_logprobs"]; ok {
			if val, success := schemas.SafeExtractBool(responseLogprobs); success && val {
				config.ResponseLogprobs = true
			}
		}
		if seed, ok := params.ExtraParams["seed"]; ok {
			if val, success := schemas.SafeExtractInt(seed); success {
				config.Seed = schemas.Ptr(int32(val))
			}
		}
	}

	if config.Logprobs != nil && *config.Logprobs == 0 && !config.ResponseLogprobs {
		config.Logprobs = nil
	}

	return config, nil
}

func responsesExtraParamsWithoutGenerationConfigKeys(extraParams map[string]interface{}) map[string]interface{} {
	filtered := maps.Clone(extraParams)
	maps.DeleteFunc(filtered, func(key string, _ interface{}) bool {
		return slices.Contains(responsesGenerationConfigExtraParamKeys, key)
	})
	return filtered
}

func modelSupportsToolCombination(model string) bool {
	name := strings.ToLower(model)

	if idx := strings.LastIndex(name, "/"); idx != -1 {
		name = name[idx+1:]
	}
	const prefix = "gemini-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}

	rest := name[len(prefix):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return false
	}
	major, err := strconv.Atoi(rest[:end])
	if err != nil {
		return false
	}
	return major >= 3
}

func convertResponsesToolsToGemini(tools []schemas.ResponsesTool, includeServerSideToolInvocations bool, provider schemas.ModelProvider, model string) ([]Tool, error) {
	var functionDeclarations []*FunctionDeclaration
	var googleSearch *GoogleSearch
	var codeExecution *ToolCodeExecution

	allowsMixedTools := includeServerSideToolInvocations ||
		(provider == schemas.Vertex && modelSupportsToolCombination(model))

	hasFunctionTool := false

	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeFunction && tool.Name != nil {
			hasFunctionTool = true
			break
		}
	}

	dropServerSideTools := hasFunctionTool && !allowsMixedTools

	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeFunction {

			if tool.Name != nil {
				funcDecl := &FunctionDeclaration{
					Name: *tool.Name,
					Description: func() string {
						if tool.Description != nil {
							return *tool.Description
						}
						return ""
					}(),
				}
				if tool.ResponsesToolFunction != nil && tool.ResponsesToolFunction.Parameters != nil {
					raw, err := providerUtils.MarshalSorted(tool.ResponsesToolFunction.Parameters)
					if err != nil {
						return []Tool{}, fmt.Errorf("marshal tool %q parameters: %w", *tool.Name, err)
					}
					funcDecl.ParametersJSONSchema = json.RawMessage(raw)
				}
				functionDeclarations = append(functionDeclarations, funcDecl)
			}
		}
		if tool.Type == schemas.ResponsesToolTypeCodeInterpreter && !dropServerSideTools {

			codeExecution = &ToolCodeExecution{}
		}
		if tool.Type == schemas.ResponsesToolTypeWebSearch && !dropServerSideTools {
			googleSearch = &GoogleSearch{}
			if tool.ResponsesToolWebSearch != nil && tool.ResponsesToolWebSearch.Filters != nil {
				if tool.ResponsesToolWebSearch.Filters.TimeRangeFilter != nil {
					googleSearch.TimeRangeFilter = &Interval{
						StartTime: tool.ResponsesToolWebSearch.Filters.TimeRangeFilter.StartTime,
						EndTime:   tool.ResponsesToolWebSearch.Filters.TimeRangeFilter.EndTime,
					}
				}
				if len(tool.ResponsesToolWebSearch.Filters.BlockedDomains) > 0 {
					googleSearch.ExcludeDomains = tool.ResponsesToolWebSearch.Filters.BlockedDomains
				}
			}
			if tool.ResponsesToolWebSearch != nil && len(tool.ResponsesToolWebSearch.SearchContentTypes) > 0 {
				searchTypes := &SearchTypes{}
				for _, contentType := range tool.ResponsesToolWebSearch.SearchContentTypes {
					switch contentType {
					case geminiSearchContentTypeText:
						searchTypes.WebSearch = &WebSearch{}
					case geminiSearchContentTypeImage:
						searchTypes.ImageSearch = &ImageSearch{}
					}
				}
				if searchTypes.WebSearch != nil || searchTypes.ImageSearch != nil {
					googleSearch.SearchTypes = searchTypes
				}
			}
		}
	}

	var geminiTools []Tool
	if len(functionDeclarations) > 0 {
		geminiTools = append(geminiTools, Tool{FunctionDeclarations: functionDeclarations})
	}
	if googleSearch != nil {
		geminiTools = append(geminiTools, Tool{GoogleSearch: googleSearch})
	}
	if codeExecution != nil {
		geminiTools = append(geminiTools, Tool{CodeExecution: codeExecution})
	}
	if len(geminiTools) == 0 {
		return []Tool{}, nil
	}
	return geminiTools, nil
}

func convertResponsesToolChoiceToGemini(toolChoice *schemas.ResponsesToolChoice) *ToolConfig {
	config := &ToolConfig{}

	if toolChoice.ResponsesToolChoiceStruct != nil {
		funcConfig := &FunctionCallingConfig{}
		ext := toolChoice.ResponsesToolChoiceStruct

		if ext.Mode != nil {
			switch *ext.Mode {
			case "auto":
				funcConfig.Mode = FunctionCallingConfigModeAuto
			case "required":
				funcConfig.Mode = FunctionCallingConfigModeAny
			case "none":
				funcConfig.Mode = FunctionCallingConfigModeNone
			}
		}

		seen := make(map[string]struct{}, len(ext.Tools)+1)
		appendAllowed := func(name string) {
			if _, dup := seen[name]; dup {
				return
			}
			seen[name] = struct{}{}
			funcConfig.AllowedFunctionNames = append(funcConfig.AllowedFunctionNames, name)
		}

		if ext.Name != nil {
			if ext.Mode == nil {
				funcConfig.Mode = FunctionCallingConfigModeAny
			}
			appendAllowed(*ext.Name)
		}

		if len(ext.Tools) > 0 {
			if ext.Mode == nil {
				funcConfig.Mode = FunctionCallingConfigModeAny
			}
			for _, tool := range ext.Tools {
				if tool.Name != nil {
					appendAllowed(*tool.Name)
				}
			}
		}

		config.FunctionCallingConfig = funcConfig
		return config
	}

	if toolChoice.ResponsesToolChoiceStr != nil {
		funcConfig := &FunctionCallingConfig{}
		switch *toolChoice.ResponsesToolChoiceStr {
		case "none":
			funcConfig.Mode = FunctionCallingConfigModeNone
		case "required", "any":
			funcConfig.Mode = FunctionCallingConfigModeAny
		default:
			funcConfig.Mode = FunctionCallingConfigModeAuto
		}
		config.FunctionCallingConfig = funcConfig
	}

	return config
}

func isAssistantPrefillMessage(msg *schemas.ResponsesMessage) bool {
	if msg.Role == nil || *msg.Role != schemas.ResponsesInputMessageRoleAssistant {
		return false
	}
	if msg.ResponsesToolMessage != nil || msg.ResponsesReasoning != nil {
		return false
	}
	if msg.Type != nil && *msg.Type != schemas.ResponsesMessageTypeMessage {
		return false
	}

	if msg.Content != nil {
		for _, block := range msg.Content.ContentBlocks {
			if block.Signature != nil {
				return false
			}
			switch block.Type {
			case schemas.ResponsesInputMessageContentBlockTypeText,
				schemas.ResponsesOutputMessageContentTypeText:
			default:
				return false
			}
		}
	}
	return true
}

func trimTrailingAssistantPrefill(messages []schemas.ResponsesMessage, caps schemas.ModelCaps) []schemas.ResponsesMessage {
	if caps.SupportsAssistantPrefill(false) {
		return messages
	}
	trimmed := len(messages)
	for trimmed > 0 && isAssistantPrefillMessage(&messages[trimmed-1]) {
		trimmed--
	}
	return messages[:trimmed]
}

func inlineGeminiSystemReminder(msg *schemas.ResponsesMessage, allowedImageURLSchemes ...string) (*Content, error) {
	if msg.Content == nil {
		return nil, nil
	}

	wrap := func(text string) string {
		return "<system-reminder>\n" + text + "\n</system-reminder>\n"
	}

	content := &Content{Role: "user"}

	if msg.Content.ContentStr != nil {
		if *msg.Content.ContentStr != "" {
			content.Parts = append(content.Parts, &Part{Text: wrap(*msg.Content.ContentStr)})
		}
	} else {
		for _, block := range msg.Content.ContentBlocks {
			part, err := convertContentBlockToGeminiPart(block, allowedImageURLSchemes...)
			if err != nil {
				return nil, fmt.Errorf("failed to convert system message content block: %w", err)
			}
			if part == nil {
				continue
			}

			if part.Text != "" {
				part.Text = wrap(part.Text)
			}
			content.Parts = append(content.Parts, part)
		}
	}

	if len(content.Parts) == 0 {
		return nil, nil
	}
	return content, nil
}

func convertResponsesMessagesToGeminiContents(messages []schemas.ResponsesMessage, model string, provider schemas.ModelProvider, allowedImageURLSchemes ...string) ([]Content, *Content, error) {
	if len(allowedImageURLSchemes) == 0 {
		allowedImageURLSchemes = defaultGeminiImageURLSchemes
	}

	isVertex := provider == schemas.Vertex
	caps := schemas.ResolveModelCaps(provider, model)

	messages = trimTrailingAssistantPrefill(messages, caps)

	if len(messages) == 1 && messages[0].Role != nil && (*messages[0].Role == schemas.ResponsesInputMessageRoleSystem || *messages[0].Role == schemas.ResponsesInputMessageRoleDeveloper) {
		content := Content{Role: "user"}
		if messages[0].Content != nil {
			if messages[0].Content.ContentStr != nil && *messages[0].Content.ContentStr != "" {
				content.Parts = append(content.Parts, &Part{
					Text: *messages[0].Content.ContentStr,
				})
			}
			if messages[0].Content.ContentBlocks != nil {
				for _, block := range messages[0].Content.ContentBlocks {
					part, err := convertContentBlockToGeminiPart(block, allowedImageURLSchemes...)
					if err != nil {
						return nil, nil, fmt.Errorf("failed to convert system message content block: %w", err)
					}
					if part != nil {
						content.Parts = append(content.Parts, part)
					}
				}
			}
		}
		if len(content.Parts) > 0 {
			return []Content{content}, nil, nil
		}
	}

	var contents []Content
	var systemInstruction *Content

	callIDToName := make(map[string]string)
	for i := range messages {
		m := &messages[i]
		if m.Type != nil && *m.Type == schemas.ResponsesMessageTypeFunctionCall &&
			m.ResponsesToolMessage != nil &&
			m.ResponsesToolMessage.CallID != nil &&
			m.ResponsesToolMessage.Name != nil {
			if name := strings.TrimSpace(*m.ResponsesToolMessage.Name); name != "" {
				callIDToName[*m.ResponsesToolMessage.CallID] = name
			}
		}
	}

	var pendingFunctionResponseParts []*Part

	var pendingFunctionCallIDs []string
	flushFunctionResponses := func() {
		contents = append(contents, Content{
			Parts: orderFunctionResponsesByCallID(pendingFunctionResponseParts, pendingFunctionCallIDs),
			Role:  "user",
		})
		pendingFunctionResponseParts = nil
		pendingFunctionCallIDs = nil
	}

	seenNonSystemMessage := false

	for i, msg := range messages {
		isSystemMessage := msg.Role != nil &&
			(*msg.Role == schemas.ResponsesInputMessageRoleSystem || *msg.Role == schemas.ResponsesInputMessageRoleDeveloper)

		if !isSystemMessage {
			seenNonSystemMessage = true
		}

		if msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeReasoning && msg.ResponsesReasoning != nil {
			parts := thoughtTextParts(msg.ResponsesReasoning)

			consumedByLookAhead := i > 0 &&
				messages[i-1].Type != nil &&
				*messages[i-1].Type == schemas.ResponsesMessageTypeFunctionCall
			if !consumedByLookAhead {
				if sig := thoughtSignatureFromEncryptedContent(msg.ResponsesReasoning.EncryptedContent); sig != nil {
					parts = append(parts, &Part{ThoughtSignature: sig})
				}
			}

			if len(parts) > 0 {
				contents = append(contents, Content{
					Parts: parts,
					Role:  "model",
				})
			}
			continue
		}

		if isSystemMessage && seenNonSystemMessage {
			inlined, err := inlineGeminiSystemReminder(&msg, allowedImageURLSchemes...)
			if err != nil {
				return nil, nil, err
			}
			if inlined != nil {

				if len(pendingFunctionResponseParts) > 0 {
					flushFunctionResponses()
				}
				contents = append(contents, *inlined)
			}
			continue
		}

		if isSystemMessage {
			if systemInstruction == nil {
				systemInstruction = &Content{}
			}

			if msg.Content != nil {
				if msg.Content.ContentStr != nil {
					systemInstruction.Parts = append(systemInstruction.Parts, &Part{
						Text: *msg.Content.ContentStr,
					})
				}
				if msg.Content.ContentBlocks != nil {
					for _, block := range msg.Content.ContentBlocks {
						part, err := convertContentBlockToGeminiPart(block, allowedImageURLSchemes...)
						if err != nil {
							return nil, nil, fmt.Errorf("failed to convert system message content block: %w", err)
						}
						if part != nil {
							systemInstruction.Parts = append(systemInstruction.Parts, part)
						}
					}
				}
			}

			continue
		}

		isFunctionOutput := msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeFunctionCallOutput && msg.ResponsesToolMessage != nil

		if len(pendingFunctionResponseParts) > 0 && !isFunctionOutput {
			flushFunctionResponses()
		}

		content := Content{}

		if msg.Role != nil {

			switch *msg.Role {
			case schemas.ResponsesInputMessageRoleAssistant:
				content.Role = "model"
			case schemas.ResponsesInputMessageRoleUser, schemas.ResponsesInputMessageRoleDeveloper:
				content.Role = "user"
			default:

				content.Role = "user"
			}
		}

		if msg.ResponsesToolMessage != nil && msg.Type != nil {
			switch *msg.Type {
			case schemas.ResponsesMessageTypeFunctionCall:

				if msg.ResponsesToolMessage.Name != nil {
					var argsRaw json.RawMessage
					if msg.ResponsesToolMessage.Arguments != nil {
						rawArgs := strings.TrimSpace(*msg.ResponsesToolMessage.Arguments)
						if rawArgs == "" {
							rawArgs = "{}"
						}
						var buf bytes.Buffer
						if err := json.Compact(&buf, []byte(rawArgs)); err != nil {
							return nil, nil, fmt.Errorf("failed to decode function call arguments: %w", err)
						}
						argsRaw = buf.Bytes()
					}
					if argsRaw == nil {
						argsRaw = json.RawMessage("{}")
					}

					var thoughtSig string
					part := &Part{
						FunctionCall: &FunctionCall{
							Name: *msg.ResponsesToolMessage.Name,
							Args: argsRaw,
						},
					}
					if msg.ResponsesToolMessage.CallID != nil {
						if strings.Contains(*msg.ResponsesToolMessage.CallID, thoughtSignatureSeparator) {
							parts := strings.SplitN(*msg.ResponsesToolMessage.CallID, thoughtSignatureSeparator, 2)
							if len(parts) == 2 {
								thoughtSig = parts[1]
							}
						}

						part.FunctionCall.ID = providerUtils.StripThoughtSignature(*msg.ResponsesToolMessage.CallID)
						pendingFunctionCallIDs = append(pendingFunctionCallIDs, part.FunctionCall.ID)
					}
					if thoughtSig != "" {
						var err error
						part.ThoughtSignature, err = base64.RawURLEncoding.DecodeString(thoughtSig)
						if err != nil {

							thoughtSig = ""
						}
					}

					if i+1 < len(messages) {
						nextMsg := messages[i+1]
						if nextMsg.Type != nil && *nextMsg.Type == schemas.ResponsesMessageTypeReasoning &&
							nextMsg.ResponsesReasoning != nil && nextMsg.ResponsesReasoning.EncryptedContent != nil {
							decodedSig, err := base64.StdEncoding.DecodeString(*nextMsg.ResponsesReasoning.EncryptedContent)
							if err == nil {
								part.ThoughtSignature = decodedSig
							}
						}
					}

					if part.ThoughtSignature == nil {
						part.ThoughtSignature = []byte(skipThoughtSignatureValidator)
					}

					content.Parts = append(content.Parts, part)
				}

			case schemas.ResponsesMessageTypeCodeInterpreterCall:

				content.Role = "model"
				content.Parts = append(content.Parts, geminiPartsFromCodeInterpreterCall(&msg)...)

			case schemas.ResponsesMessageTypeFunctionCallOutput:

				if msg.ResponsesToolMessage.CallID != nil {
					responseMap := make(map[string]any)

					var funcMediaParts []*Part

					if msg.ResponsesToolMessage.Output != nil && msg.ResponsesToolMessage.Output.ResponsesToolCallOutputStr != nil {
						responseMap["output"] = geminiFunctionOutputValue(*msg.ResponsesToolMessage.Output.ResponsesToolCallOutputStr)
					} else if msg.ResponsesToolMessage.Output != nil && msg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks != nil {

						supportsMultimodalToolOutput := caps.SupportsMultimodalToolOutput(isGemini3Plus(model))
						var textParts []string
						for _, block := range msg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks {
							if block.Text != nil && *block.Text != "" {
								textParts = append(textParts, *block.Text)
								continue
							}
							if !supportsMultimodalToolOutput {

								if placeholder, isMedia := schemas.ResponsesToolResultMediaPlaceholder(block); isMedia {
									textParts = append(textParts, placeholder)
								}
								continue
							}
							mediaPart, err := convertContentBlockToGeminiPart(block, allowedImageURLSchemes...)
							if err != nil {
								return nil, nil, fmt.Errorf("failed to convert function output content block: %w", err)
							}
							if mediaPart == nil {
								continue
							}
							displayName := fmt.Sprintf("media_%d", len(funcMediaParts))
							if mediaPart.InlineData != nil {
								mediaPart.InlineData.DisplayName = displayName
							} else if mediaPart.FileData != nil {
								mediaPart.FileData.DisplayName = displayName
							}
							funcMediaParts = append(funcMediaParts, mediaPart)
							if isVertex {
								responseMap[displayName+"_ref"] = map[string]string{"$ref": displayName}
							}
						}
						if len(textParts) > 0 {
							responseMap["output"] = geminiFunctionOutputValue(strings.Join(textParts, "\n"))
						} else if len(funcMediaParts) > 0 {

							responseMap["output"] = ""
						}
					} else if msg.Content != nil && msg.Content.ContentStr != nil {

						responseMap["output"] = geminiFunctionOutputValue(*msg.Content.ContentStr)
					}

					funcName := ""
					if msg.ResponsesToolMessage.Name != nil && strings.TrimSpace(*msg.ResponsesToolMessage.Name) != "" {
						funcName = *msg.ResponsesToolMessage.Name
					} else if name, ok := callIDToName[*msg.ResponsesToolMessage.CallID]; ok && strings.TrimSpace(name) != "" {
						funcName = name
					} else {
						funcName = *msg.ResponsesToolMessage.CallID
					}

					responseBytes, _ := providerUtils.MarshalSorted(responseMap)
					part := &Part{
						FunctionResponse: &FunctionResponse{
							Name:     funcName,
							Response: json.RawMessage(responseBytes),

							ID:    providerUtils.StripThoughtSignature(*msg.ResponsesToolMessage.CallID),
							Parts: funcMediaParts,
						},
					}
					pendingFunctionResponseParts = append(pendingFunctionResponseParts, part)

					if i == len(messages)-1 && len(pendingFunctionResponseParts) > 0 {
						flushFunctionResponses()
					}

					continue
				}
			}
		}

		if !isFunctionOutput {

			if msg.Content != nil {
				if msg.Content.ContentStr != nil {
					content.Parts = append(content.Parts, &Part{
						Text: *msg.Content.ContentStr,
					})
				}

				if msg.Content.ContentBlocks != nil {
					for _, block := range msg.Content.ContentBlocks {
						part, err := convertContentBlockToGeminiPart(block, allowedImageURLSchemes...)
						if err != nil {
							return nil, nil, fmt.Errorf("failed to convert message content block: %w", err)
						}
						if part != nil {
							content.Parts = append(content.Parts, part)
						}
					}
				}
			}
		}

		if len(content.Parts) > 0 {
			contents = append(contents, content)
		}
	}

	return contents, systemInstruction, nil
}

func orderFunctionResponsesByCallID(parts []*Part, callIDs []string) []*Part {
	if len(parts) < 2 || len(callIDs) == 0 {
		return parts
	}
	rank := make(map[string]int, len(callIDs))
	for i, id := range callIDs {
		if _, seen := rank[id]; !seen {
			rank[id] = i
		}
	}
	rankOf := func(p *Part) int {
		if p != nil && p.FunctionResponse != nil {
			if r, ok := rank[p.FunctionResponse.ID]; ok {
				return r
			}
		}
		return len(callIDs)
	}
	ordered := slices.Clone(parts)
	slices.SortStableFunc(ordered, func(a, b *Part) int { return rankOf(a) - rankOf(b) })
	return ordered
}

func convertContentBlockToGeminiPart(block schemas.ResponsesMessageContentBlock, allowedImageURLSchemes ...string) (*Part, error) {
	part, err := buildGeminiPartFromContentBlock(block, allowedImageURLSchemes...)
	if err != nil || part == nil {
		return part, err
	}
	if block.Signature != nil && len(part.ThoughtSignature) == 0 {
		if signature, decodeErr := base64.StdEncoding.DecodeString(*block.Signature); decodeErr == nil {
			part.ThoughtSignature = signature
		}
	}

	if block.MediaResolution != nil && (part.InlineData != nil || part.FileData != nil) {
		part.MediaResolution = &PartMediaResolution{Level: block.MediaResolution.Level}
		if n := block.MediaResolution.NumTokens; n != nil {
			part.MediaResolution.NumTokens = new(*n)
		}
	}

	return part, nil
}

func buildGeminiPartFromContentBlock(block schemas.ResponsesMessageContentBlock, allowedImageURLSchemes ...string) (*Part, error) {
	if len(allowedImageURLSchemes) == 0 {
		allowedImageURLSchemes = defaultGeminiImageURLSchemes
	}

	switch block.Type {
	case schemas.ResponsesInputMessageContentBlockTypeText,
		schemas.ResponsesOutputMessageContentTypeText:
		if block.Text != nil && *block.Text != "" {
			part := &Part{
				Text: *block.Text,
			}
			if block.Signature != nil {
				decodedSig, err := base64.StdEncoding.DecodeString(*block.Signature)
				if err == nil {
					part.ThoughtSignature = decodedSig
				}
			}
			return part, nil
		}

	case schemas.ResponsesOutputMessageContentTypeReasoning:
		if block.Text != nil && *block.Text != "" {
			return &Part{
				Text:    *block.Text,
				Thought: true,
			}, nil
		}

	case schemas.ResponsesOutputMessageContentTypeRefusal:

		if block.ResponsesOutputMessageContentRefusal != nil {
			return &Part{
				Text: block.ResponsesOutputMessageContentRefusal.Refusal,
			}, nil
		}

	case schemas.ResponsesOutputMessageContentTypeCompaction:

		if block.ResponsesOutputMessageContentCompaction != nil {
			if summary := strings.TrimSpace(block.ResponsesOutputMessageContentCompaction.Summary); summary != "" {
				return &Part{Text: summary}, nil
			}
		}

	case schemas.ResponsesOutputMessageContentTypeFallback:

		return nil, nil

	case schemas.ResponsesInputMessageContentBlockTypeImage:
		if block.ResponsesInputMessageContentBlockImage != nil && block.ResponsesInputMessageContentBlockImage.ImageURL != nil {
			imageURL := *block.ResponsesInputMessageContentBlockImage.ImageURL

			sanitizedURL, err := schemas.SanitizeImageURLWithAllowedSchemes(imageURL, allowedImageURLSchemes...)
			if err != nil {
				return nil, fmt.Errorf("failed to sanitize image URL: %w", err)
			}

			urlInfo := schemas.ExtractURLTypeInfo(sanitizedURL)
			mimeType := "image/jpeg"
			if urlInfo.MediaType != nil {
				mimeType = *urlInfo.MediaType
			}

			if urlInfo.Type == schemas.ImageContentTypeBase64 {
				data := ""
				if urlInfo.DataURLWithoutPrefix != nil {
					data = *urlInfo.DataURLWithoutPrefix
				}

				decodedData, err := decodeBase64StringToBytes(data)
				if err != nil {
					return nil, fmt.Errorf("failed to decode base64 image data: %w", err)
				}

				return &Part{
					InlineData: &Blob{
						MIMEType: mimeType,
						Data:     encodeBytesToBase64String(decodedData),
					},
				}, nil
			} else {
				return &Part{
					FileData: &FileData{
						MIMEType: mimeType,
						FileURI:  sanitizedURL,
					},
				}, nil
			}
		}

	case schemas.ResponsesInputMessageContentBlockTypeAudio:
		if block.Audio != nil {

			decodedData, err := decodeBase64StringToBytes(block.Audio.Data)
			if err != nil {
				return nil, fmt.Errorf("failed to decode base64 audio data: %w", err)
			}

			return &Part{
				InlineData: &Blob{
					MIMEType: func() string {
						f := strings.ToLower(strings.TrimSpace(block.Audio.Format))
						if f == "" {
							return "audio/mpeg"
						}
						if strings.HasPrefix(f, "audio/") {
							return f
						}
						return "audio/" + f
					}(),
					Data: encodeBytesToBase64String(decodedData),
				},
			}, nil
		}

	case schemas.ResponsesInputMessageContentBlockTypeFile:
		if block.ResponsesInputMessageContentBlockFile != nil {
			fileBlock := block.ResponsesInputMessageContentBlockFile

			if fileBlock.FileURL != nil {

				fileData := &FileData{FileURI: *fileBlock.FileURL}
				if fileBlock.FileType != nil {
					fileData.MIMEType = *fileBlock.FileType
				} else {
					fileData.MIMEType = mimeTypeFromURI(*fileBlock.FileURL)
				}
				return &Part{FileData: fileData}, nil
			}

			if fileBlock.FileData != nil {
				mimeType := "application/pdf"
				if fileBlock.FileType != nil {
					mimeType = *fileBlock.FileType
				}

				dataBytes, extractedMimeType := convertFileDataToBytes(*fileBlock.FileData)
				if extractedMimeType != "" {
					mimeType = extractedMimeType
				}

				if len(dataBytes) > 0 {
					part := &Part{
						InlineData: &Blob{
							MIMEType: mimeType,
							Data:     encodeBytesToBase64String(dataBytes),
						},
					}

					return part, nil
				}
			}
		}
	}

	return nil, nil
}

func emitWebSearchFromGroundingMetadata(
	metadata *GroundingMetadata,
	state *GeminiResponsesStreamState,
	sequenceNumber int,
) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	hasGroundingQueries := metadata != nil && (len(metadata.WebSearchQueries) > 0 || len(metadata.ImageSearchQueries) > 0)
	if !hasGroundingQueries && len(state.ServerSearchRounds) == 0 {
		return responses
	}

	var sources []schemas.ResponsesWebSearchToolCallActionSearchSource
	if metadata != nil {
		for _, chunk := range metadata.GroundingChunks {
			if source, ok := groundingChunkSource(chunk); ok {
				sources = append(sources, source)
			}
		}
	}

	rounds := state.ServerSearchRounds
	if len(rounds) == 0 {
		rounds = []GeminiServerSearchRound{{}}
	}

	for i, round := range rounds {
		outputIndex := state.nextOutputIndex()

		itemID := round.CallID
		if itemID == "" {
			itemID = state.generateItemID("ws", outputIndex)
		}
		state.ItemIDs[outputIndex] = itemID

		action := &schemas.ResponsesWebSearchToolCallAction{
			Type:         "search",
			Queries:      round.Queries,
			ImageQueries: round.ImageQueries,
		}
		if i == 0 && metadata != nil {
			if len(action.Queries) == 0 {
				action.Queries = metadata.WebSearchQueries
			}
			if len(action.ImageQueries) == 0 {
				action.ImageQueries = metadata.ImageSearchQueries
			}
			action.Sources = sources
		}
		if len(action.Queries) > 0 {
			action.Query = &action.Queries[0]
		}

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			Item: &schemas.ResponsesMessage{
				ID:     &itemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
				Status: schemas.Ptr("in_progress"),
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					Action: &schemas.ResponsesToolMessageActionStruct{
						ResponsesWebSearchToolCallAction: &schemas.ResponsesWebSearchToolCallAction{
							Type: "search",
						},
					},
				},
			},
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeWebSearchCallInProgress,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeWebSearchCallSearching,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeWebSearchCallCompleted,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &outputIndex,
			ItemID:         &itemID,
			Item: &schemas.ResponsesMessage{
				ID:     &itemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
				Status: schemas.Ptr("completed"),
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					Action: &schemas.ResponsesToolMessageActionStruct{
						ResponsesWebSearchToolCallAction: action,
					},
				},
			},
		})
	}

	state.HasEmittedWebSearch = true

	if metadata != nil && metadata.SearchEntryPoint != nil && metadata.SearchEntryPoint.RenderedContent != "" {
		renderedIndex := state.nextOutputIndex()
		renderedItemID := state.generateItemID("rc", renderedIndex)
		state.ItemIDs[renderedIndex] = renderedItemID

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &renderedIndex,
			Item: &schemas.ResponsesMessage{
				ID:     &renderedItemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				Status: schemas.Ptr("completed"),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: []schemas.ResponsesMessageContentBlock{
						{
							Type: schemas.ResponsesOutputMessageContentTypeRenderedContent,
							ResponsesOutputMessageContentRenderedContent: &schemas.ResponsesOutputMessageContentRenderedContent{
								RenderedContent: metadata.SearchEntryPoint.RenderedContent,
							},
						},
					},
				},
			},
		})

		responses = append(responses, &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
			SequenceNumber: sequenceNumber + len(responses),
			OutputIndex:    &renderedIndex,
			ItemID:         &renderedItemID,
			Item: &schemas.ResponsesMessage{
				ID:     &renderedItemID,
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				Status: schemas.Ptr("completed"),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: []schemas.ResponsesMessageContentBlock{
						{
							Type: schemas.ResponsesOutputMessageContentTypeRenderedContent,
							ResponsesOutputMessageContentRenderedContent: &schemas.ResponsesOutputMessageContentRenderedContent{
								RenderedContent: metadata.SearchEntryPoint.RenderedContent,
							},
						},
					},
				},
			},
		})
	}

	return responses
}

func emitAnnotationsFromGroundingSupports(
	metadata *GroundingMetadata,
	state *GeminiResponsesStreamState,
	sequenceNumber int,
) []*schemas.BifrostResponsesStreamResponse {
	var responses []*schemas.BifrostResponsesStreamResponse

	if metadata == nil || len(metadata.GroundingSupports) == 0 || state.TextOutputIndex < 0 {
		return responses
	}

	itemID := state.ItemIDs[state.TextOutputIndex]

	emmitedIndex := 0

	for _, support := range metadata.GroundingSupports {
		if support.Segment == nil {
			continue
		}

		for _, chunkIdx := range support.GroundingChunkIndices {
			if chunkIdx < 0 || int(chunkIdx) >= len(metadata.GroundingChunks) {
				continue
			}
			source, ok := groundingChunkSource(metadata.GroundingChunks[chunkIdx])
			if !ok {
				continue
			}

			annotation := schemas.ResponsesOutputMessageContentTextAnnotation{
				Type: "url_citation",
				URL:  &source.URL,
			}
			if support.Segment.Text != "" {
				annotation.Text = &support.Segment.Text
			}
			annotation.StartIndex = schemas.Ptr(int(support.Segment.StartIndex))
			annotation.EndIndex = schemas.Ptr(int(support.Segment.EndIndex))
			annotation.Title = source.Title

			responses = append(responses, &schemas.BifrostResponsesStreamResponse{
				Type:            schemas.ResponsesStreamResponseTypeOutputTextAnnotationAdded,
				SequenceNumber:  sequenceNumber + len(responses),
				OutputIndex:     &state.TextOutputIndex,
				ItemID:          &itemID,
				ContentIndex:    schemas.Ptr(0),
				Annotation:      &annotation,
				AnnotationIndex: schemas.Ptr(emmitedIndex),
			})
			emmitedIndex++
		}
	}

	return responses
}

func generateSyntheticFunctionCallArgumentDeltas(argumentsJSON string, outputIndex *int, itemID *string, baseSequenceNumber int) []*schemas.BifrostResponsesStreamResponse {
	var events []*schemas.BifrostResponsesStreamResponse

	chunkSize := 8

	runes := []rune(argumentsJSON)
	for i := 0; i < len(runes); i += chunkSize {
		end := min(i+chunkSize, len(runes))

		chunk := string(runes[i:end])
		deltaEvent := &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta,
			SequenceNumber: baseSequenceNumber + len(events),
			OutputIndex:    outputIndex,
			ItemID:         itemID,
			Delta:          &chunk,
		}
		events = append(events, deltaEvent)
	}

	return events
}
