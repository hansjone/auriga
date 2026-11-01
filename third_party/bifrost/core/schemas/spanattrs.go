// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"strconv"
	"strings"
)

type ToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func (d *LLMSpanData) Attributes() map[string]any {
	if d == nil {
		return map[string]any{}
	}
	attrs := make(map[string]any, 48)
	d.appendIdentity(attrs)
	d.appendFamily(attrs)
	d.appendResponseEnvelope(attrs)
	d.appendUsage(attrs)
	d.appendError(attrs)
	for k, v := range d.Extra {
		attrs[k] = v
	}
	return attrs
}

func (d *LLMSpanData) appendIdentity(attrs map[string]any) {
	attrs[AttrProviderName] = OTelProviderName(d.Provider)
	attrs[AttrBifrostProviderName] = string(d.Provider)
	attrs[AttrRequestModel] = d.RequestModel
	attrs[AttrOperationName] = OTelOperationName(d.RequestType)
	if d.Alias != "" {
		attrs[AttrBifrostAlias] = d.Alias
	}
}

func (d *LLMSpanData) appendFamily(attrs map[string]any) {
	if d.MessageCount > 0 {
		attrs[AttrMessageCount] = d.MessageCount
	}
	if len(d.InputMessages) > 0 {
		if data, err := MarshalString(d.InputMessages); err == nil {
			attrs[AttrInputMessages] = data
		}
	}
	switch {
	case d.Responses != nil:
		d.appendResponsesRequest(attrs)
	case d.TextCompletion != nil:
		d.appendTextCompletionRequest(attrs)
	case d.Embedding != nil || d.EmbeddingInput != nil:
		d.appendEmbeddingRequest(attrs)
	case d.Speech != nil || d.SpeechInput != nil:
		d.appendSpeechRequest(attrs)
	case d.Transcription != nil:
		d.appendTranscriptionRequest(attrs)
	}

	if p := d.Chat; p != nil {
		setIfNotNil(attrs, AttrMaxTokens, p.MaxCompletionTokens)
		setIfNotNil(attrs, AttrTemperature, p.Temperature)
		setIfNotNil(attrs, AttrTopP, p.TopP)
		if p.Stop != nil {
			attrs[AttrStopSequences] = append([]string(nil), p.Stop...)
			attrs[AttrBifrostStopSequencesJoined] = strings.Join(p.Stop, ",")
		}
		setIfNotNil(attrs, AttrPresencePenalty, p.PresencePenalty)
		setIfNotNil(attrs, AttrFrequencyPenalty, p.FrequencyPenalty)
		setIfNotNil(attrs, AttrParallelToolCall, p.ParallelToolCalls)
		setIfNotNil(attrs, AttrRequestUser, p.User)

		if len(p.Tools) > 0 {
			if data, err := MarshalString(chatToolSummaries(p.Tools)); err == nil {
				attrs[AttrTools] = data
			}
		}
	}
}

func (d *LLMSpanData) appendResponsesRequest(attrs map[string]any) {
	p := d.Responses
	setIfNotNil(attrs, AttrParallelToolCall, p.ParallelToolCalls)
	setIfNotNil(attrs, AttrPromptCacheKey, p.PromptCacheKey)
	if r := p.Reasoning; r != nil {
		setIfNotNil(attrs, AttrReasoningEffort, r.Effort)
		setIfNotNil(attrs, AttrReasoningSummary, r.Summary)
		setIfNotNil(attrs, AttrReasoningGenSummary, r.GenerateSummary)
	}
	setIfNotNil(attrs, AttrSafetyIdentifier, p.SafetyIdentifier)
	if p.ServiceTier != nil {
		attrs[AttrServiceTier] = string(*p.ServiceTier)
	}
	setIfNotNil(attrs, AttrStore, p.Store)
	setIfNotNil(attrs, AttrTemperature, p.Temperature)
	if t := p.Text; t != nil {
		setIfNotNil(attrs, AttrTextVerbosity, t.Verbosity)
		if t.Format != nil {
			attrs[AttrTextFormatType] = t.Format.Type
		}
	}
	setIfNotNil(attrs, AttrTopLogProbs, p.TopLogProbs)
	setIfNotNil(attrs, AttrTopP, p.TopP)
	if tc := p.ToolChoice; tc != nil {
		if tc.ResponsesToolChoiceStr != nil && *tc.ResponsesToolChoiceStr != "" {
			attrs[AttrToolChoiceType] = *tc.ResponsesToolChoiceStr
		}
		if tc.ResponsesToolChoiceStruct != nil && tc.ResponsesToolChoiceStruct.Name != nil {
			attrs[AttrToolChoiceName] = *tc.ResponsesToolChoiceStruct.Name
		}
	}
	if p.Tools != nil {
		if data, err := MarshalString(responsesToolSummaries(p.Tools)); err == nil {
			attrs[AttrTools] = data
		}
	}
	setIfNotNil(attrs, AttrTruncation, p.Truncation)
}

func (d *LLMSpanData) appendTextCompletionRequest(attrs map[string]any) {
	p := d.TextCompletion
	setIfNotNil(attrs, AttrMaxTokens, p.MaxTokens)
	setIfNotNil(attrs, AttrTemperature, p.Temperature)
	setIfNotNil(attrs, AttrTopP, p.TopP)
	if p.Stop != nil {
		attrs[AttrStopSequences] = append([]string(nil), p.Stop...)
		attrs[AttrBifrostStopSequencesJoined] = strings.Join(p.Stop, ",")
	}
	setIfNotNil(attrs, AttrPresencePenalty, p.PresencePenalty)
	setIfNotNil(attrs, AttrFrequencyPenalty, p.FrequencyPenalty)
	setIfNotNil(attrs, AttrBestOf, p.BestOf)
	setIfNotNil(attrs, AttrEcho, p.Echo)
	if p.LogitBias != nil {
		if data, err := MarshalString(p.LogitBias); err == nil {
			attrs[AttrLogitBias] = data
		}
	}
	setIfNotNil(attrs, AttrLogProbs, p.LogProbs)
	setIfNotNil(attrs, AttrChoiceCount, p.N)
	setIfNotNil(attrs, AttrSeed, p.Seed)
	setIfNotNil(attrs, AttrSuffix, p.Suffix)
	setIfNotNil(attrs, AttrRequestUser, p.User)

	if in := d.TextPrompt; in != nil {
		if in.PromptStr != nil {
			attrs[AttrInputText] = *in.PromptStr
		} else if in.PromptArray != nil {
			attrs[AttrInputText] = strings.Join(in.PromptArray, ",")
		}
	}
}

func (d *LLMSpanData) appendEmbeddingRequest(attrs map[string]any) {
	if p := d.Embedding; p != nil {
		setIfNotNil(attrs, AttrEmbeddingsDimensionCount, p.Dimensions)
		if p.EncodingFormat != nil {
			attrs[AttrEncodingFormats] = []string{*p.EncodingFormat}
		}
	}
	in := d.EmbeddingInput
	if in == nil {
		return
	}

	if texts := in.Texts(); len(texts) > 0 {
		attrs[AttrInputText] = strings.Join(texts, ",")
		return
	}
	if ids := in.TokenIDs(); len(ids) > 0 {
		var groups []string
		for _, group := range ids {
			parts := make([]string, len(group))
			for i, v := range group {
				parts[i] = strconv.Itoa(v)
			}
			groups = append(groups, strings.Join(parts, ","))
		}
		attrs[AttrInputEmbedding] = strings.Join(groups, ",")
	}
}

func (d *LLMSpanData) appendSpeechRequest(attrs map[string]any) {
	if p := d.Speech; p != nil {
		if vc := p.VoiceConfig; vc != nil {
			setIfNotNil(attrs, AttrVoice, vc.Voice)
			if len(vc.MultiVoiceConfig) > 0 {
				voices := make([]string, len(vc.MultiVoiceConfig))
				for i, v := range vc.MultiVoiceConfig {
					voices[i] = v.Voice
				}
				attrs[AttrMultiVoiceConfig] = strings.Join(voices, ",")
			}
		}
		setIfNotEmpty(attrs, AttrInstructions, p.Instructions)
		setIfNotEmpty(attrs, AttrResponseFormat, p.ResponseFormat)
		setIfNotNil(attrs, AttrSpeed, p.Speed)
	}
	if in := d.SpeechInput; in != nil {
		setIfNotEmpty(attrs, AttrInputSpeech, in.Input)
	}
}

func (d *LLMSpanData) appendTranscriptionRequest(attrs map[string]any) {
	p := d.Transcription
	setIfNotNil(attrs, AttrLanguage, p.Language)
	setIfNotNil(attrs, AttrPrompt, p.Prompt)
	setIfNotNil(attrs, AttrResponseFormat, p.ResponseFormat)
	setIfNotNil(attrs, AttrFormat, p.Format)
}

func chatToolSummaries(tools []ChatTool) []ToolSummary {
	out := make([]ToolSummary, 0, len(tools))
	for _, tool := range tools {
		if tool.Function == nil || tool.Function.Name == "" {
			out = append(out, ToolSummary{Name: string(tool.Type)})
			continue
		}
		info := ToolSummary{Name: tool.Function.Name}
		if tool.Function.Description != nil {
			info.Description = *tool.Function.Description
		}
		out = append(out, info)
	}
	return out
}

func responsesToolSummaries(tools []ResponsesTool) []ToolSummary {
	out := make([]ToolSummary, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == nil {
			out = append(out, ToolSummary{Name: string(tool.Type)})
			continue
		}
		info := ToolSummary{Name: *tool.Name}
		if tool.Description != nil {
			info.Description = *tool.Description
		}
		out = append(out, info)
	}
	return out
}

func (d *LLMSpanData) appendResponseEnvelope(attrs map[string]any) {
	if !d.hasResponse() {
		return
	}
	switch d.RequestType {
	case ResponsesRequest, ResponsesStreamRequest:
		setIfNotEmpty(attrs, AttrResponseID, d.ResponseID)
		setIfNotEmpty(attrs, AttrResponseModel, d.ResponseModel)
		setIfNotEmpty(attrs, AttrServiceTier, d.ServiceTier)
		d.appendOutputMessagesJSON(attrs, AttrOutputMessages)
		d.appendResponsesEcho(attrs)
		if len(d.FinishReasons) > 0 {
			attrs[AttrFinishReasons] = d.FinishReasons
		}

	case TextCompletionRequest, TextCompletionStreamRequest:
		attrs[AttrResponseID] = d.ResponseID
		attrs[AttrResponseModel] = d.ResponseModel
		setIfNotEmpty(attrs, AttrObject, d.Object)
		setIfNotEmpty(attrs, AttrSystemFprint, d.SystemFingerprint)
		if outputs := d.outputContents(); len(outputs) > 0 {
			attrs[AttrOutputMessages] = outputs
		}
		if len(d.FinishReasons) > 0 {
			attrs[AttrFinishReasons] = d.FinishReasons
		}

	case TranscriptionRequest, TranscriptionStreamRequest:
		if len(d.OutputMessages) > 0 {
			attrs[AttrOutputMessages] = d.OutputMessages[0].Content
		}

	case EmbeddingRequest, SpeechRequest, SpeechStreamRequest:

	default:
		attrs[AttrResponseID] = d.ResponseID
		attrs[AttrResponseModel] = d.ResponseModel
		setIfNotEmpty(attrs, AttrObject, d.Object)
		setIfNotEmpty(attrs, AttrSystemFprint, d.SystemFingerprint)
		attrs[AttrCreated] = int(d.Created)
		setIfNotEmpty(attrs, AttrServiceTier, d.ServiceTier)
		d.appendOutputMessagesJSON(attrs, AttrOutputMessages)
		if len(d.FinishReasons) > 0 {
			attrs[AttrFinishReasons] = d.FinishReasons
		}
	}
}

func (d *LLMSpanData) hasResponse() bool {
	return d.ResponseID != "" || d.ResponseModel != "" || len(d.OutputMessages) > 0 ||
		d.Created != 0 || d.ResponsesEcho != nil
}

func (d *LLMSpanData) appendOutputMessagesJSON(attrs map[string]any, key string) {
	if len(d.OutputMessages) == 0 {
		return
	}
	if data, err := MarshalString(d.OutputMessages); err == nil {
		attrs[key] = data
	}
}

func (d *LLMSpanData) outputContents() []string {
	if len(d.OutputMessages) == 0 {
		return nil
	}
	out := make([]string, 0, len(d.OutputMessages))
	for _, m := range d.OutputMessages {
		out = append(out, m.Content)
	}
	return out
}

func (d *LLMSpanData) appendResponsesEcho(attrs map[string]any) {
	p := d.ResponsesEcho
	if p == nil {
		return
	}
	if p.Include != nil {
		attrs[AttrRespInclude] = strings.Join(p.Include, ",")
	}
	setIfNotNil(attrs, AttrRespMaxOutputTokens, p.MaxOutputTokens)
	setIfNotNil(attrs, AttrRespMaxToolCalls, p.MaxToolCalls)
	if p.Metadata != nil {
		if data, err := MarshalString(p.Metadata); err == nil {
			attrs[AttrRespMetadata] = data
		}
	}
	setIfNotNil(attrs, AttrRespPreviousRespID, p.PreviousResponseID)
	setIfNotNil(attrs, AttrRespPromptCacheKey, p.PromptCacheKey)
	if r := p.Reasoning; r != nil {
		setIfNotNil(attrs, AttrRespReasoningText, r.Summary)
		setIfNotNil(attrs, AttrRespReasoningEffort, r.Effort)
		setIfNotNil(attrs, AttrRespReasoningGenSum, r.GenerateSummary)
	}
	setIfNotNil(attrs, AttrRespSafetyIdentifier, p.SafetyIdentifier)
	setIfNotNil(attrs, AttrRespStore, p.Store)
	setIfNotNil(attrs, AttrRespTemperature, p.Temperature)
	if t := p.Text; t != nil {
		setIfNotNil(attrs, AttrRespTextVerbosity, t.Verbosity)
		if t.Format != nil {
			attrs[AttrRespTextFormatType] = t.Format.Type
		}
	}
	setIfNotNil(attrs, AttrRespTopLogProbs, p.TopLogProbs)
	setIfNotNil(attrs, AttrRespTopP, p.TopP)
	if tc := p.ToolChoice; tc != nil {
		setIfNotNil(attrs, AttrRespToolChoiceType, tc.ResponsesToolChoiceStr)
		if tc.ResponsesToolChoiceStruct != nil {
			setIfNotNil(attrs, AttrRespToolChoiceName, tc.ResponsesToolChoiceStruct.Name)
		}
	}
	setIfNotNil(attrs, AttrRespTruncation, p.Truncation)
	if p.Tools != nil {
		if data, err := MarshalString(responsesToolSummaries(p.Tools)); err == nil {
			attrs[AttrRespTools] = data
		}
	}
}

func (d *LLMSpanData) appendUsage(attrs map[string]any) {
	u := d.Usage
	if u == nil {
		return
	}
	attrs[AttrTotalTokens] = u.TotalTokens
	attrs[AttrInputTokens] = u.PromptTokens
	attrs[AttrOutputTokens] = u.CompletionTokens

	responsesNS := usesInputTokenDetailsNamespace(d.RequestType)

	if pd := u.PromptTokensDetails; pd != nil {
		textKey, audioKey, imageKey := AttrPromptTokenDetailsText, AttrPromptTokenDetailsAudio, AttrPromptTokenDetailsImage
		write5m, write1h := AttrPromptTokenDetailsCachedWrite5m, AttrPromptTokenDetailsCachedWrite1h
		if responsesNS {
			textKey, audioKey, imageKey = AttrInputTokenDetailsText, AttrInputTokenDetailsAudio, AttrInputTokenDetailsImage
			write5m, write1h = AttrInputTokenDetailsCachedWrite5m, AttrInputTokenDetailsCachedWrite1h
		}
		setIfPositive(attrs, textKey, pd.TextTokens)
		setIfPositive(attrs, audioKey, pd.AudioTokens)
		setIfPositive(attrs, imageKey, pd.ImageTokens)
		setIfPositive(attrs, AttrUsageCacheReadInputTokens, pd.CachedReadTokens)
		setIfPositive(attrs, AttrUsageCacheCreationInputTokens, pd.CachedWriteTokens)
		if wd := pd.CachedWriteTokenDetails; wd != nil {
			setIfPositive(attrs, write5m, wd.CachedWriteTokens5m)
			setIfPositive(attrs, write1h, wd.CachedWriteTokens1h)
		}
	}

	if cd := u.CompletionTokensDetails; cd != nil {
		textKey, audioKey, imageKey := AttrCompletionTokenDetailsText, AttrCompletionTokenDetailsAudio, AttrCompletionTokenDetailsImage
		acceptKey, rejectKey := AttrCompletionTokenDetailsAccept, AttrCompletionTokenDetailsReject
		citeKey, searchKey := AttrCompletionTokenDetailsCite, AttrCompletionTokenDetailsSearch
		if responsesNS {
			textKey, audioKey, imageKey = AttrOutputTokenDetailsText, AttrOutputTokenDetailsAudio, AttrOutputTokenDetailsImage
			acceptKey, rejectKey = AttrOutputTokenDetailsAccept, AttrOutputTokenDetailsReject
			citeKey, searchKey = AttrOutputTokenDetailsCite, AttrOutputTokenDetailsSearch
		}
		setIfPositive(attrs, textKey, cd.TextTokens)
		setIfPositive(attrs, audioKey, cd.AudioTokens)
		setIfPositivePtr(attrs, imageKey, cd.ImageTokens)
		setIfPositive(attrs, AttrUsageReasoningOutputTokens, cd.ReasoningTokens)
		setIfPositive(attrs, acceptKey, cd.AcceptedPredictionTokens)
		setIfPositive(attrs, rejectKey, cd.RejectedPredictionTokens)
		setIfPositivePtr(attrs, citeKey, cd.CitationTokens)
		setIfPositivePtr(attrs, searchKey, cd.NumSearchQueries)
	}

	appendCostAttributes(attrs, u.Cost)
	appendCostAttributes(attrs, d.Cost)
}

func (d *LLMSpanData) appendError(attrs map[string]any) {
	AppendSpanErrorAttributes(attrs, d.Error)
}

func AppendSpanErrorAttributes(attrs map[string]any, e *SpanError) {
	if e == nil {
		return
	}
	if e.Detail != nil {
		attrs[AttrError] = e.Detail.Message
		setIfNotNil(attrs, AttrErrorTypeSpec, e.Detail.Type)
		setIfNotNil(attrs, AttrErrorCode, e.Detail.Code)
	}
	setIfNotNil(attrs, AttrHTTPResponseStatusCode, e.StatusCode)
	if e.Type != "" {
		attrs[AttrBifrostErrorType] = e.Type
	}
}

func usesInputTokenDetailsNamespace(rt RequestType) bool {
	switch rt {
	case ResponsesRequest, ResponsesStreamRequest,
		TranscriptionRequest, TranscriptionStreamRequest:
		return true
	}
	return false
}

func setIfNotEmpty(attrs map[string]any, key, value string) {
	if value != "" {
		attrs[key] = value
	}
}

func setIfPositive(attrs map[string]any, key string, value int) {
	if value > 0 {
		attrs[key] = value
	}
}

func setIfPositivePtr(attrs map[string]any, key string, value *int) {
	if value != nil && *value > 0 {
		attrs[key] = *value
	}
}

func setIfNotNil[T any](attrs map[string]any, key string, value *T) {
	if value != nil {
		attrs[key] = *value
	}
}

func appendCostAttributes(attrs map[string]any, cost *BifrostCost) {
	if cost == nil {
		return
	}
	attrs[AttrUsageCost] = cost.TotalCost
	setIfNonZero(attrs, AttrBifrostCostInput, cost.InputCost)
	setIfNonZero(attrs, AttrBifrostCostOutput, cost.OutputCost)
	setIfNonZero(attrs, AttrBifrostCostAdditional, cost.AdditionalCost)

	if d := cost.InputCostDetails; d != nil {
		setIfNonZero(attrs, AttrBifrostCostInputText, d.TextCost)
		setIfNonZero(attrs, AttrBifrostCostInputAudio, d.AudioCost)
		setIfNonZero(attrs, AttrBifrostCostInputImage, d.ImageCost)
		setIfNonZero(attrs, AttrBifrostCostInputCachedRead, d.CachedReadCost)
		setIfNonZero(attrs, AttrBifrostCostInputCachedWrite, d.CachedWriteCost)
		setIfNonZero(attrs, AttrBifrostCostInputRequest, d.RequestCost)
	}
	if d := cost.OutputCostDetails; d != nil {
		setIfNonZero(attrs, AttrBifrostCostOutputText, d.TextCost)
		setIfNonZero(attrs, AttrBifrostCostOutputAudio, d.AudioCost)
		setIfNonZero(attrs, AttrBifrostCostOutputImage, d.ImageCost)
		setIfNonZero(attrs, AttrBifrostCostOutputReasoning, d.ReasoningCost)
		setIfNonZero(attrs, AttrBifrostCostOutputCitation, d.CitationCost)
		setIfNonZero(attrs, AttrBifrostCostOutputSearch, d.SearchQueriesCost)
	}
	if d := cost.AdditionalCostDetails; d != nil {
		setIfNonZero(attrs, AttrBifrostCostGuardrail, d.GuardrailCost)
		setIfNonZero(attrs, AttrBifrostCostMCP, d.MCPCost)
		setIfNonZero(attrs, AttrBifrostCostSemanticCache, d.SemanticCacheCost)
		setIfNonZero(attrs, AttrBifrostCostRouting, d.RoutingCost)
	}
}

func setIfNonZero(attrs map[string]any, key string, value float64) {
	if value != 0 {
		attrs[key] = value
	}
}
