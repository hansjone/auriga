package gemini

import (
	"bytes"

	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"

	"sort"
	"strconv"
	"strings"

	"unsafe"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var geminiFinishReasonToBifrost = map[FinishReason]string{
	FinishReasonStop:                    "stop",
	FinishReasonMaxTokens:               "length",
	FinishReasonSafety:                  "content_filter",
	FinishReasonRecitation:              "content_filter",
	FinishReasonLanguage:                "content_filter",
	FinishReasonOther:                   "stop",
	FinishReasonBlocklist:               "content_filter",
	FinishReasonProhibitedContent:       "content_filter",
	FinishReasonSPII:                    "content_filter",
	FinishReasonMalformedFunctionCall:   "stop",
	FinishReasonImageSafety:             "content_filter",
	FinishReasonImageProhibitedContent:  "content_filter",
	FinishReasonImageOther:              "stop",
	FinishReasonNoImage:                 "stop",
	FinishReasonImageRecitation:         "content_filter",
	FinishReasonUnexpectedToolCall:      "stop",
	FinishReasonTooManyToolCalls:        "stop",
	FinishReasonMissingThoughtSignature: "stop",
	FinishReasonMalformedResponse:       "stop",
	FinishReasonContinuation:            "continuation",
}
var defaultGemini3ThinkingLevels = []string{"low", "medium", "high"}
var geminiThinkingLevelSupport = []struct {
	prefix string
	levels []string
}{
	{"gemini-4-argon", []string{"low", "medium", "high"}},
	{"gemini-3.1-flash-lite-image", []string{"minimal", "high"}},
	{"gemini-3.1-flash-lite", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.7-flash", []string{"low", "medium", "high"}},
	{"gemini-3.6-flash", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.5-flash-lite", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.5-flash", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3.1-pro", []string{"low", "medium", "high"}},
	{"gemini-3-flash", []string{"minimal", "low", "medium", "high"}},
	{"gemini-3-pro", []string{"low", "high"}},
}
var defaultGeminiImageURLSchemes = []string{"http", "https"}

type rawThinkingTarget struct {
	ctx      *schemas.BifrostContext
	provider schemas.ModelProvider
	model    string
	caps     schemas.ModelCaps
	resolved bool
}

var rawThinkingBudgetKeys = [...]struct{ budget, level string }{
	{"thinkingBudget", "thinkingLevel"},
	{"thinking_budget", "thinking_level"},
}
var uriExtensionMIMETypes = map[string]string{
	".pdf":  "application/pdf",
	".txt":  "text/plain",
	".csv":  "text/csv",
	".md":   "text/markdown",
	".html": "text/html",
	".json": "application/json",
	".xml":  "application/xml",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
	".heic": "image/heic",
	".heif": "image/heif",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".ogg":  "audio/ogg",
	".flac": "audio/flac",
	".aac":  "audio/aac",
	".mp4":  "video/mp4",
	".mov":  "video/quicktime",
	".webm": "video/webm",
	".avi":  "video/x-msvideo",
	".mpeg": "video/mpeg",
}

func isGemini3Plus(model string) bool {
	model = strings.ToLower(model)
	idx := strings.Index(model, "gemini-")
	if idx == -1 {
		return false
	}
	afterPrefix := model[idx+7:]
	if len(afterPrefix) == 0 {
		return false
	}
	firstChar := afterPrefix[0]
	if firstChar < '0' || firstChar > '9' {
		return false
	}
	return firstChar >= '3'
}

func isGemini4Plus(model string) bool {
	model = strings.ToLower(model)
	idx := strings.Index(model, "gemini-")
	if idx == -1 {
		return false
	}
	afterPrefix := model[idx+7:]
	end := 0
	for end < len(afterPrefix) && afterPrefix[end] >= '0' && afterPrefix[end] <= '9' {
		end++
	}
	if end == 0 {
		return false
	}
	major, err := strconv.Atoi(afterPrefix[:end])
	return err == nil && major >= 4
}

func defaultSupportsReasoning(model string) bool {
	modelLower := strings.ToLower(model)
	if strings.Contains(modelLower, "thinking") || strings.Contains(modelLower, "gemini-2.5") {
		return true
	}
	return isGemini3Plus(model)
}

func defaultCanDisableReasoning(model string) bool {
	return !strings.Contains(strings.ToLower(model), "gemini-2.5-pro")
}

func defaultSupportsReasoningBudget(model string) bool {
	return !isGemini4Plus(model)
}

func defaultEffortControl(model string) *schemas.EffortControl {
	if !isGemini3Plus(model) {
		return nil
	}

	return &schemas.EffortControl{Levels: supportedThinkingLevels(model)}
}

func NormalizeRawGenerateContentRequestForCompatibility(ctx *schemas.BifrostContext, jsonBody []byte, providerName schemas.ModelProvider, model string) []byte {
	if len(jsonBody) == 0 {
		return jsonBody
	}

	out := jsonBody
	for _, path := range []string{
		"generationConfig.presencePenalty",
		"generationConfig.frequencyPenalty",
		"fallbacks",
	} {
		if providerUtils.JSONFieldExists(out, path) {
			if updated, err := providerUtils.DeleteJSONField(out, path); err == nil {
				out = updated
			}
		}
	}
	out = rewriteRawThinkingBudgetToLevel(ctx, out, providerName, model)

	contents := gjson.GetBytes(out, "contents")
	if !contents.IsArray() {
		return out
	}

	var rebuiltContents bytes.Buffer
	rebuiltContents.WriteByte('[')
	keptContents := 0
	removedAny := false
	for _, content := range contents.Array() {
		contentRaw := content.Raw
		parts := content.Get("parts")
		if !parts.IsArray() {
			if keptContents > 0 {
				rebuiltContents.WriteByte(',')
			}
			rebuiltContents.WriteString(contentRaw)
			keptContents++
			continue
		}
		var rebuiltParts bytes.Buffer
		rebuiltParts.WriteByte('[')
		keptParts := 0
		contentRemovedAny := false
		parts.ForEach(func(_, part gjson.Result) bool {
			inlineData := part.Get("inlineData")
			removePart := false
			if inlineData.Exists() {
				mimeType := strings.ToLower(inlineData.Get("mimeType").String())
				data := inlineData.Get("data").String()
				if strings.HasPrefix(mimeType, "audio/") && !isValidAudioBase64Payload(data) {
					removePart = true
					removedAny = true
					contentRemovedAny = true
				}
			}
			if !removePart {
				if keptParts > 0 {
					rebuiltParts.WriteByte(',')
				}
				rebuiltParts.WriteString(part.Raw)
				keptParts++
			}
			return true
		})
		rebuiltParts.WriteByte(']')
		if keptParts == 0 {
			if contentRemovedAny {
				continue
			}
		} else if contentRemovedAny {
			if updated, err := sjson.SetRawBytes([]byte(contentRaw), "parts", rebuiltParts.Bytes()); err == nil {
				contentRaw = string(updated)
			}
		}
		if keptContents > 0 {
			rebuiltContents.WriteByte(',')
		}
		rebuiltContents.WriteString(contentRaw)
		keptContents++
	}
	rebuiltContents.WriteByte(']')
	if removedAny {
		if updated, err := sjson.SetRawBytes(out, "contents", rebuiltContents.Bytes()); err == nil {
			out = updated
		}
	}

	return out
}

func (t *rawThinkingTarget) resolve() schemas.ModelCaps {
	if !t.resolved {
		t.caps = schemas.ResolveModelCaps(t.provider, schemas.ResolveCanonicalModel(t.ctx, t.model))
		t.resolved = true
	}
	return t.caps
}

func rewriteRawThinkingBudgetToLevel(ctx *schemas.BifrostContext, body []byte, providerName schemas.ModelProvider, model string) []byte {
	if model == "" {
		return body
	}
	view := rawJSONView(body)
	for _, genKey := range [...]string{"generationConfig", "generation_config"} {
		genConfig := gjson.Get(view, genKey)
		if !genConfig.IsObject() {
			continue
		}
		target := rawThinkingTarget{ctx: ctx, provider: providerName, model: model}
		if updated, changed := rewriteGenerationConfigThinkingBudget(genConfig.Raw, &target); changed {
			if rewritten, err := providerUtils.SetRawJSONField(body, genKey, updated); err == nil {
				body = rewritten
			}
		}
		return body
	}
	return rewriteRawBatchThinkingBudgets(ctx, body, view, providerName, model)
}

func rawJSONView(body []byte) string {
	return unsafe.String(unsafe.SliceData(body), len(body))
}

func rewriteRawBatchThinkingBudgets(ctx *schemas.BifrostContext, body []byte, view string, providerName schemas.ModelProvider, model string) []byte {
	const itemsPath = "batch.inputConfig.requests.requests"
	items := gjson.Get(view, itemsPath)
	if !items.IsArray() || items.Index <= 0 || view[items.Index] != '[' {
		return body
	}
	target := rawThinkingTarget{ctx: ctx, provider: providerName, model: model}
	var rebuilt *bytes.Buffer
	items.ForEach(func(_, item gjson.Result) bool {
		var updatedItem []byte
		for _, genKey := range [...]string{"request.generationConfig", "request.generation_config"} {
			genConfig := item.Get(genKey)
			if !genConfig.IsObject() {
				continue
			}
			if updated, changed := rewriteGenerationConfigThinkingBudget(genConfig.Raw, &target); changed {
				if rewritten, err := providerUtils.SetRawJSONField([]byte(item.Raw), genKey, updated); err == nil {
					updatedItem = rewritten
				}
			}
			break
		}
		if rebuilt == nil {
			if updatedItem == nil {
				return true
			}

			rebuilt = bytes.NewBuffer(make([]byte, 0, len(items.Raw)+64))
			rebuilt.WriteString(view[items.Index:item.Index])
		} else {
			rebuilt.WriteByte(',')
		}
		if updatedItem != nil {
			rebuilt.Write(updatedItem)
		} else {
			rebuilt.WriteString(item.Raw)
		}
		return true
	})
	if rebuilt == nil {
		return body
	}
	rebuilt.WriteByte(']')
	if rewritten, err := providerUtils.SetRawJSONField(body, itemsPath, rebuilt.Bytes()); err == nil {
		return rewritten
	}
	return body
}

func rawThinkingBudgetValue(budget gjson.Result) (int, bool) {
	var text string
	switch budget.Type {
	case gjson.Number:
		text = budget.Raw
	case gjson.String:
		text = budget.Str
	default:
		return 0, false
	}
	value, err := strconv.ParseInt(text, 10, 32)
	if err != nil {
		return 0, false
	}
	return int(value), true
}

func rewriteGenerationConfigThinkingBudget(genConfig string, target *rawThinkingTarget) ([]byte, bool) {
	for _, configKey := range [...]string{"thinkingConfig", "thinking_config"} {
		thinking := gjson.Get(genConfig, configKey)
		if !thinking.IsObject() {
			continue
		}
		for _, keys := range rawThinkingBudgetKeys {
			budget := thinking.Get(keys.budget)
			if !budget.Exists() {
				continue
			}
			value, ok := rawThinkingBudgetValue(budget)
			if !ok || value < DynamicReasoningBudget {
				return nil, false
			}
			caps := target.resolve()
			if caps.SupportsReasoningBudget(defaultSupportsReasoningBudget(caps.Model())) {
				return nil, false
			}
			updated, err := providerUtils.DeleteJSONField([]byte(genConfig), configKey+"."+keys.budget)
			if err != nil {
				return nil, false
			}
			hasLevel := thinking.Get("thinkingLevel").Exists() || thinking.Get("thinking_level").Exists()
			if !hasLevel && value != DynamicReasoningBudget {
				level := lowestThinkingLevel(caps.Model())
				if value != 0 {
					level = effortToThinkingLevel(caps, thinkingLevelFromBudget(value))
				}
				if updated, err = providerUtils.SetJSONField(updated, configKey+"."+keys.level, level); err != nil {
					return nil, false
				}
			}
			return updated, true
		}
	}
	return nil, false
}

func isValidAudioBase64Payload(data string) bool {
	decoded, err := decodeBase64StringToBytes(data)
	return err == nil && len(decoded) > 0
}

func supportedThinkingLevels(model string) []string {
	modelLower := strings.ToLower(model)
	for _, entry := range geminiThinkingLevelSupport {
		if strings.Contains(modelLower, entry.prefix) {
			return entry.levels
		}
	}
	return defaultGemini3ThinkingLevels
}

func lowestThinkingLevel(model string) string {
	levels := supportedThinkingLevels(model)
	if len(levels) == 0 {
		return "low"
	}
	return levels[0]
}

func setThinkingBudgetZeroIfSupported(config *GenerationConfig, caps schemas.ModelCaps) {
	model := caps.Model()

	if caps.SupportsReasoningEffort(isGemini3Plus(model)) {
		if config.ThinkingConfig == nil {
			config.ThinkingConfig = &GenerationConfigThinkingConfig{}
		}
		config.ThinkingConfig.IncludeThoughts = false
		config.ThinkingConfig.ThinkingBudget = nil
		config.ThinkingConfig.ThinkingLevel = schemas.Ptr(lowestThinkingLevel(model))
		return
	}
	if !caps.CanDisableReasoning(defaultCanDisableReasoning(model)) {
		config.ThinkingConfig = nil
		return
	}
	if config.ThinkingConfig == nil {
		config.ThinkingConfig = &GenerationConfigThinkingConfig{}
	}
	config.ThinkingConfig.IncludeThoughts = false
	config.ThinkingConfig.ThinkingBudget = schemas.Ptr(int32(0))
}

func effortToThinkingLevel(caps schemas.ModelCaps, effort string) string {
	if effort == schemas.ReasoningEffortNone {
		return ""
	}
	return caps.NormalizeReasoningEffort(effort, defaultEffortControl(caps.Model()))
}

func thinkingLevelFromBudget(budget int) string {
	switch {
	case budget <= 1024:
		return schemas.ReasoningEffortLow
	case budget <= 8192:
		return schemas.ReasoningEffortMedium
	default:
		return schemas.ReasoningEffortHigh
	}
}

func applyThinkingBudget(config *GenerationConfig, caps schemas.ModelCaps, budget int) error {
	if budget == 0 {
		setThinkingBudgetZeroIfSupported(config, caps)
		return nil
	}
	if budget < DynamicReasoningBudget {
		return validateThinkingBudget(caps, budget)
	}
	if !caps.SupportsReasoningBudget(defaultSupportsReasoningBudget(caps.Model())) {
		if budget != DynamicReasoningBudget {
			if level := effortToThinkingLevel(caps, thinkingLevelFromBudget(budget)); level != "" {
				config.ThinkingConfig.ThinkingLevel = schemas.Ptr(level)
			}
		}
		return nil
	}
	if budget == DynamicReasoningBudget {
		config.ThinkingConfig.ThinkingBudget = schemas.Ptr(int32(DynamicReasoningBudget))
		return nil
	}
	if err := validateThinkingBudget(caps, budget); err != nil {
		return err
	}
	config.ThinkingConfig.ThinkingBudget = schemas.Ptr(int32(budget))
	return nil
}

func getThinkingBudgetRange(caps schemas.ModelCaps, defaultMaxTokens int) thinkingBudgetRange {
	min, max, _ := caps.ReasoningBudgetRange(defaultMaxTokens, providerUtils.KnownGeminiReasoningBudget(caps.Model()))
	return thinkingBudgetRange{Min: min, Max: max}
}

func validateThinkingBudget(caps schemas.ModelCaps, budget int) error {
	if budget == 0 {
		return nil
	}
	if budget == DynamicReasoningBudget {
		if caps.SupportsDynamicReasoningBudget(true) {
			return nil
		}
		return fmt.Errorf("thinking budget -1 (dynamic) is not supported for model %s", caps.Model())
	}
	if budget < 0 {
		return fmt.Errorf("thinking budget %d is invalid; only 0 and -1 are supported special values", budget)
	}
	min, max, ok := caps.ReasoningBudgetRange(0, providerUtils.KnownGeminiReasoningBudget(caps.Model()))
	if !ok {
		return nil
	}
	if budget < min {
		return fmt.Errorf("thinking budget %d is below the minimum of %d for model %s", budget, min, caps.Model())
	}
	if budget > max {
		return fmt.Errorf("thinking budget %d exceeds the maximum of %d for model %s", budget, max, caps.Model())
	}
	return nil
}

func mapGeminiServiceTierToBifrost(tier ServiceTier) schemas.BifrostServiceTier {
	switch tier {
	case ServiceTierStandard:
		return schemas.BifrostServiceTierDefault
	case ServiceTierFlex:
		return schemas.BifrostServiceTierFlex
	case ServiceTierPriority:
		return schemas.BifrostServiceTierPriority
	default:
		return schemas.BifrostServiceTierAuto
	}
}

func mapGeminiTrafficTypeToBifrost(trafficType TrafficType) *schemas.BifrostServiceTier {
	var tier schemas.BifrostServiceTier
	switch trafficType {
	case TrafficTypeOnDemand:
		tier = schemas.BifrostServiceTierDefault
	case TrafficTypeOnDemandPriority:
		tier = schemas.BifrostServiceTierPriority
	case TrafficTypeOnDemandFlex:
		tier = schemas.BifrostServiceTierFlex
	case TrafficTypeProvisionedThroughput:
		tier = schemas.BifrostServiceTierProvisioned
	default:
		return nil
	}
	return &tier
}

func isImageMimeType(mimeType string) bool {
	if mimeType == "" {
		return false
	}

	mimeType = strings.ToLower(mimeType)

	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}

	if strings.HasPrefix(mimeType, "image/") {
		return true
	}

	commonImageTypes := []string{
		"jpeg",
		"jpg",
		"png",
		"gif",
		"webp",
		"bmp",
		"svg",
		"tiff",
		"ico",
		"avif",
	}

	for _, imageType := range commonImageTypes {
		if strings.Contains(mimeType, imageType) {
			return true
		}
	}

	return false
}

func convertFileDataToBytes(fileData string) ([]byte, string) {
	var dataBytes []byte
	var mimeType string

	if strings.HasPrefix(fileData, "data:") {
		urlInfo := schemas.ExtractURLTypeInfo(fileData)

		if urlInfo.DataURLWithoutPrefix != nil {

			decoded, err := base64.StdEncoding.DecodeString(*urlInfo.DataURLWithoutPrefix)
			if err == nil {
				dataBytes = decoded
				if urlInfo.MediaType != nil {
					mimeType = *urlInfo.MediaType
				}
			}
		}
	} else {

		decoded, err := base64.StdEncoding.DecodeString(fileData)
		if err == nil {
			dataBytes = decoded
		} else {

			dataBytes = []byte(fileData)
		}
	}

	return dataBytes, mimeType
}

func ConvertGeminiFinishReasonToBifrost(providerReason FinishReason) string {
	if bifrostReason, ok := geminiFinishReasonToBifrost[providerReason]; ok {
		return bifrostReason
	}
	return string(providerReason)
}

func geminiResponsesStatus(stopReason string) (*string, *schemas.ResponsesResponseIncompleteDetails) {
	status, details, mapped := schemas.ResponsesStatusFromFinishReason(stopReason)
	if !mapped {

		return schemas.Ptr(schemas.ResponsesResponseStatusIncomplete), nil
	}
	return &status, details
}

func (response *GenerateContentResponse) promptBlockError() *schemas.ResponsesResponseError {
	feedback := response.PromptFeedback
	if feedback == nil || len(response.Candidates) > 0 || feedback.BlockReason == "" || feedback.BlockReason == "BLOCK_REASON_UNSPECIFIED" {
		return nil
	}
	message := feedback.BlockReasonMessage
	if message == "" {
		message = feedback.BlockReason
	}
	return &schemas.ResponsesResponseError{
		Code:    "invalid_prompt",
		Message: message,
	}
}

func ConvertGeminiUsageMetadataToChatUsage(metadata *GenerateContentResponseUsageMetadata) *schemas.BifrostLLMUsage {
	if metadata == nil {
		return nil
	}

	usage := &schemas.BifrostLLMUsage{
		PromptTokens:     int(metadata.PromptTokenCount),
		CompletionTokens: int(metadata.CandidatesTokenCount),
		TotalTokens:      int(metadata.TotalTokenCount),
	}

	if len(metadata.PromptTokensDetails) > 0 || metadata.CachedContentTokenCount > 0 {
		if usage.PromptTokensDetails == nil {
			usage.PromptTokensDetails = &schemas.ChatPromptTokensDetails{}
		}

		for _, detail := range metadata.PromptTokensDetails {
			switch detail.Modality {
			case ModalityText:
				usage.PromptTokensDetails.TextTokens = int(detail.TokenCount)
			case ModalityAudio:
				usage.PromptTokensDetails.AudioTokens = int(detail.TokenCount)
			case ModalityImage:
				usage.PromptTokensDetails.ImageTokens = int(detail.TokenCount)
			}
		}

		if metadata.CachedContentTokenCount > 0 {
			usage.PromptTokensDetails.CachedReadTokens = int(metadata.CachedContentTokenCount)
		}
	}

	if len(metadata.CandidatesTokensDetails) > 0 || metadata.ThoughtsTokenCount > 0 {
		if usage.CompletionTokensDetails == nil {
			usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
		}

		for _, detail := range metadata.CandidatesTokensDetails {
			switch detail.Modality {
			case ModalityText:
				usage.CompletionTokensDetails.TextTokens = int(detail.TokenCount)
			case ModalityAudio:
				usage.CompletionTokensDetails.AudioTokens = int(detail.TokenCount)
			case ModalityImage:
				usage.CompletionTokensDetails.ImageTokens = schemas.Ptr(int(detail.TokenCount))
			}
		}

		if metadata.ThoughtsTokenCount > 0 {
			usage.CompletionTokensDetails.ReasoningTokens = int(metadata.ThoughtsTokenCount)
			usage.CompletionTokens = usage.CompletionTokens + int(metadata.ThoughtsTokenCount)
		}
	}

	return usage
}

func convertGeminiUsageMetadataToSpeechUsage(metadata *GenerateContentResponseUsageMetadata) *schemas.SpeechUsage {
	if metadata == nil {
		return nil
	}

	usage := &schemas.SpeechUsage{
		InputTokens:  int(metadata.PromptTokenCount),
		OutputTokens: int(metadata.CandidatesTokenCount),
		TotalTokens:  int(metadata.TotalTokenCount),
	}

	if len(metadata.PromptTokensDetails) > 0 {
		inputDetails := &schemas.SpeechUsageInputTokenDetails{}
		for _, detail := range metadata.PromptTokensDetails {
			switch detail.Modality {
			case ModalityText:
				inputDetails.TextTokens = int(detail.TokenCount)
			case ModalityAudio:
				inputDetails.AudioTokens = int(detail.TokenCount)
			}
		}
		usage.InputTokenDetails = inputDetails
	}

	return usage
}

func convertGeminiUsageMetadataToTranscriptionUsage(metadata *GenerateContentResponseUsageMetadata) *schemas.TranscriptionUsage {
	if metadata == nil {
		return nil
	}

	usage := &schemas.TranscriptionUsage{
		Type:         "tokens",
		InputTokens:  schemas.Ptr(int(metadata.PromptTokenCount)),
		OutputTokens: schemas.Ptr(int(metadata.CandidatesTokenCount)),
		TotalTokens:  schemas.Ptr(int(metadata.TotalTokenCount)),
	}

	if len(metadata.PromptTokensDetails) > 0 {
		inputDetails := &schemas.TranscriptionUsageInputTokenDetails{}
		for _, detail := range metadata.PromptTokensDetails {
			switch detail.Modality {
			case ModalityText:
				inputDetails.TextTokens = int(detail.TokenCount)
			case ModalityAudio:
				inputDetails.AudioTokens = int(detail.TokenCount)
			}
		}
		usage.InputTokenDetails = inputDetails
	}

	return usage
}

func convertGeminiUsageMetadataToImageUsage(metadata *GenerateContentResponseUsageMetadata) *schemas.ImageUsage {
	if metadata == nil {
		return nil
	}

	usage := &schemas.ImageUsage{
		InputTokens:  int(metadata.PromptTokenCount),
		OutputTokens: int(metadata.CandidatesTokenCount),
		TotalTokens:  int(metadata.TotalTokenCount),
	}

	if len(metadata.PromptTokensDetails) > 0 {
		inputDetails := &schemas.ImageTokenDetails{}
		for _, detail := range metadata.PromptTokensDetails {
			switch detail.Modality {
			case ModalityText:
				inputDetails.TextTokens = int(detail.TokenCount)
			case ModalityImage:
				inputDetails.ImageTokens = int(detail.TokenCount)
			}
		}
		usage.InputTokensDetails = inputDetails
	}

	if len(metadata.CandidatesTokensDetails) > 0 {
		outputDetails := &schemas.ImageTokenDetails{}
		for _, detail := range metadata.CandidatesTokensDetails {
			switch detail.Modality {
			case ModalityText:
				outputDetails.TextTokens = int(detail.TokenCount)
			case ModalityImage:
				outputDetails.ImageTokens = int(detail.TokenCount)
			}
		}
		usage.OutputTokensDetails = outputDetails
	}

	if metadata.ThoughtsTokenCount > 0 {
		if usage.OutputTokensDetails == nil {
			usage.OutputTokensDetails = &schemas.ImageTokenDetails{}
		}
		usage.OutputTokensDetails.ReasoningTokens = int(metadata.ThoughtsTokenCount)
		usage.OutputTokens += int(metadata.ThoughtsTokenCount)
	}

	return usage
}

func ConvertGeminiUsageMetadataToResponsesUsage(metadata *GenerateContentResponseUsageMetadata) *schemas.ResponsesResponseUsage {
	if metadata == nil {
		return nil
	}

	usage := &schemas.ResponsesResponseUsage{
		TotalTokens:         int(metadata.TotalTokenCount),
		InputTokens:         int(metadata.PromptTokenCount),
		OutputTokens:        int(metadata.CandidatesTokenCount),
		OutputTokensDetails: &schemas.ResponsesResponseOutputTokens{},
		InputTokensDetails:  &schemas.ResponsesResponseInputTokens{},
	}

	if len(metadata.PromptTokensDetails) > 0 {
		for _, detail := range metadata.PromptTokensDetails {
			switch detail.Modality {
			case ModalityText:
				usage.InputTokensDetails.TextTokens = int(detail.TokenCount)
			case ModalityAudio:
				usage.InputTokensDetails.AudioTokens = int(detail.TokenCount)
			case ModalityImage:
				usage.InputTokensDetails.ImageTokens = int(detail.TokenCount)
			}
		}
	}

	if metadata.CachedContentTokenCount > 0 {
		usage.InputTokensDetails.CachedReadTokens = int(metadata.CachedContentTokenCount)
	}

	if len(metadata.CandidatesTokensDetails) > 0 {
		for _, detail := range metadata.CandidatesTokensDetails {
			switch detail.Modality {
			case ModalityText:
				usage.OutputTokensDetails.TextTokens = int(detail.TokenCount)
			case ModalityAudio:
				usage.OutputTokensDetails.AudioTokens = int(detail.TokenCount)
			case ModalityImage:
				usage.OutputTokensDetails.ImageTokens = schemas.Ptr(int(detail.TokenCount))
			}
		}
	}

	if metadata.ThoughtsTokenCount > 0 {
		usage.OutputTokensDetails.ReasoningTokens = int(metadata.ThoughtsTokenCount)
		usage.OutputTokens = usage.OutputTokens + int(metadata.ThoughtsTokenCount)
	}

	return usage
}

func convertParamsToGenerationConfig(params *schemas.ChatParameters, responseModalities []string, provider schemas.ModelProvider, model string) (GenerationConfig, error) {
	config := GenerationConfig{}

	if len(responseModalities) > 0 {
		var modalities []Modality
		for _, mod := range responseModalities {
			modalities = append(modalities, Modality(mod))
		}
		config.ResponseModalities = modalities
	}

	if params.N != nil {
		if *params.N < 1 || *params.N > math.MaxInt32 {
			return config, fmt.Errorf("n must be between 1 and %d for Gemini candidateCount, got %d", math.MaxInt32, *params.N)
		}
		config.CandidateCount = int32(*params.N)
	}
	if params.Stop != nil {
		config.StopSequences = params.Stop
	}
	if params.MaxCompletionTokens != nil {
		config.MaxOutputTokens = int32(*params.MaxCompletionTokens)
	}
	if params.Temperature != nil {
		temp := float64(*params.Temperature)
		config.Temperature = &temp
	}
	if params.TopP != nil {
		topP := float64(*params.TopP)
		config.TopP = &topP
	}
	if params.PresencePenalty != nil {
		penalty := float64(*params.PresencePenalty)
		config.PresencePenalty = &penalty
	}
	if params.FrequencyPenalty != nil {
		penalty := float64(*params.FrequencyPenalty)
		config.FrequencyPenalty = &penalty
	}

	caps := schemas.ResolveModelCaps(provider, model)
	if params.Reasoning != nil && caps.SupportsReasoning(defaultSupportsReasoning(model)) {
		config.ThinkingConfig = &GenerationConfigThinkingConfig{
			IncludeThoughts: true,
		}

		hasMaxTokens := params.Reasoning.MaxTokens != nil
		hasEffort := params.Reasoning.Effort != nil
		supportsLevel := caps.SupportsReasoningEffort(isGemini3Plus(model))

		if !hasMaxTokens && hasEffort && *params.Reasoning.Effort == "none" {
			setThinkingBudgetZeroIfSupported(&config, caps)
		} else if hasMaxTokens {

			if err := applyThinkingBudget(&config, caps, *params.Reasoning.MaxTokens); err != nil {
				return config, err
			}
		} else if hasEffort {

			if supportsLevel {

				if level := effortToThinkingLevel(caps, *params.Reasoning.Effort); level != "" {
					config.ThinkingConfig.ThinkingLevel = &level
				}
			} else {
				maxTokens := providerUtils.GetMaxOutputTokensOrDefault(provider, model, DefaultCompletionMaxTokens)
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

	if rf, ok := schemas.ParseChatResponseFormat(params.ResponseFormat); ok {
		switch rf.Type {
		case "json_schema":

			if schemaMap := extractSchemaMapFromResponseFormat(params.ResponseFormat); schemaMap != nil {
				config.ResponseMIMEType = "application/json"
				config.ResponseJSONSchema = schemaMap
			}
		case "json_object":

			config.ResponseMIMEType = "application/json"
		}
	}
	if params.ExtraParams != nil {
		if topK, ok := params.ExtraParams["top_k"]; ok {
			if val, success := schemas.SafeExtractInt(topK); success {
				config.TopK = schemas.Ptr(val)
			}
		}
		if responseMimeType, ok := schemas.SafeExtractString(params.ExtraParams["response_mime_type"]); ok {
			config.ResponseMIMEType = responseMimeType
		}

		if responseJsonSchema, ok := params.ExtraParams["response_json_schema"]; ok {
			config.ResponseJSONSchema = responseJsonSchema
		}
	}

	if params.LogProbs != nil {
		config.ResponseLogprobs = *params.LogProbs
	}

	if params.TopLogProbs != nil {
		topLogProbs := *params.TopLogProbs
		if topLogProbs > 20 {
			topLogProbs = 20
		}
		if topLogProbs > 0 {
			config.ResponseLogprobs = true
			config.Logprobs = schemas.Ptr(int32(topLogProbs))
		}
	}
	dropJSONResponseFormatWithToolsIfUnsupported(&config, len(params.Tools) > 0, caps, model)
	return config, nil
}

func dropJSONResponseFormatWithToolsIfUnsupported(config *GenerationConfig, hasTools bool, caps schemas.ModelCaps, model string) {
	if !hasTools || config.ResponseMIMEType != "application/json" {
		return
	}
	if caps.SupportsResponseSchemaWithTools(isGemini3Plus(model)) {
		return
	}
	config.ResponseMIMEType = ""
	config.ResponseJSONSchema = nil
}

func mapBifrostServiceTierToGemini(tier schemas.BifrostServiceTier) ServiceTier {
	switch tier {
	case schemas.BifrostServiceTierDefault:
		return ServiceTierStandard
	case schemas.BifrostServiceTierFlex:
		return ServiceTierFlex
	case schemas.BifrostServiceTierPriority:
		return ServiceTierPriority
	default:
		return ServiceTierUnspecified
	}
}

func convertBifrostToolsToGemini(bifrostTools []schemas.ChatTool) ([]Tool, error) {
	geminiTool := Tool{}

	for _, tool := range bifrostTools {
		if tool.Type == "" {
			continue
		}
		if tool.Type == "function" && tool.Function != nil {
			fd := &FunctionDeclaration{
				Name: tool.Function.Name,
			}
			if tool.Function.Parameters != nil {
				raw, err := providerUtils.MarshalSorted(tool.Function.Parameters)
				if err != nil {
					return nil, fmt.Errorf("marshal tool %q parameters: %w", tool.Function.Name, err)
				}
				fd.ParametersJSONSchema = json.RawMessage(raw)
			}
			if tool.Function.Description != nil {
				fd.Description = *tool.Function.Description
			}
			geminiTool.FunctionDeclarations = append(geminiTool.FunctionDeclarations, fd)
		}
	}

	if len(geminiTool.FunctionDeclarations) > 0 {
		return []Tool{geminiTool}, nil
	}
	return []Tool{}, nil
}

func convertToolChoiceToToolConfig(toolChoice *schemas.ChatToolChoice) *ToolConfig {
	if toolChoice == nil || (toolChoice.ChatToolChoiceStr == nil && toolChoice.ChatToolChoiceStruct == nil) {
		return nil
	}
	config := &ToolConfig{}
	functionCallingConfig := FunctionCallingConfig{}

	if toolChoice.ChatToolChoiceStr != nil {

		switch *toolChoice.ChatToolChoiceStr {
		case "none":
			functionCallingConfig.Mode = FunctionCallingConfigModeNone
		case "auto":
			functionCallingConfig.Mode = FunctionCallingConfigModeAuto
		case "any", "required":
			functionCallingConfig.Mode = FunctionCallingConfigModeAny
		default:
			functionCallingConfig.Mode = FunctionCallingConfigModeAuto
		}
	} else if toolChoice.ChatToolChoiceStruct != nil {
		switch toolChoice.ChatToolChoiceStruct.Type {
		case schemas.ChatToolChoiceTypeNone:
			functionCallingConfig.Mode = FunctionCallingConfigModeNone
		case schemas.ChatToolChoiceTypeFunction:
			functionCallingConfig.Mode = FunctionCallingConfigModeAny
		case schemas.ChatToolChoiceTypeRequired:
			functionCallingConfig.Mode = FunctionCallingConfigModeAny
		default:
			functionCallingConfig.Mode = FunctionCallingConfigModeAuto
		}

		if toolChoice.ChatToolChoiceStruct.Function != nil && toolChoice.ChatToolChoiceStruct.Function.Name != "" {
			functionCallingConfig.AllowedFunctionNames = []string{toolChoice.ChatToolChoiceStruct.Function.Name}
		}
	}

	config.FunctionCallingConfig = &functionCallingConfig
	return config
}

func countGeminiSearchQueries(metadata *GroundingMetadata, model string) *int {
	if metadata == nil {
		return nil
	}
	unique := make(map[string]struct{}, len(metadata.WebSearchQueries))
	for _, query := range metadata.WebSearchQueries {
		if trimmed := strings.TrimSpace(query); trimmed != "" {
			unique[trimmed] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return nil
	}
	if !isGemini3Plus(model) {
		return new(1)
	}
	return new(len(unique))
}

func applyGeminiSearchQueryChatUsage(usage *schemas.BifrostLLMUsage, metadata *GroundingMetadata, model string) {
	count := countGeminiSearchQueries(metadata, model)
	if usage == nil || count == nil {
		return
	}
	if usage.CompletionTokensDetails == nil {
		usage.CompletionTokensDetails = &schemas.ChatCompletionTokensDetails{}
	}
	usage.CompletionTokensDetails.NumSearchQueries = count
	usage.ToolUsage = &schemas.ToolUsage{WebSearch: &schemas.WebSearchToolUsage{NumRequests: *count}}
}

func applyGeminiSearchQueryResponsesUsage(usage *schemas.ResponsesResponseUsage, metadata *GroundingMetadata, model string) {
	count := countGeminiSearchQueries(metadata, model)
	if usage == nil || count == nil {
		return
	}
	if usage.OutputTokensDetails == nil {
		usage.OutputTokensDetails = &schemas.ResponsesResponseOutputTokens{}
	}
	usage.OutputTokensDetails.NumSearchQueries = count
	usage.ToolUsage = &schemas.ToolUsage{WebSearch: &schemas.WebSearchToolUsage{NumRequests: *count}}
}

func applyServerSideToolInvocations(req *GeminiGenerationRequest) {
	if req == nil {
		return
	}
	if req.ToolConfig == nil {
		req.ToolConfig = &ToolConfig{}
	}
	req.ToolConfig.IncludeServerSideToolInvocations = schemas.Ptr(true)
	if fc := req.ToolConfig.FunctionCallingConfig; fc != nil {
		if fc.Mode == FunctionCallingConfigModeAuto || fc.Mode == "" {
			fc.Mode = FunctionCallingConfigModeValidated
		}
	}
}

func addSpeechConfigToGenerationConfig(config *GenerationConfig, voiceConfig *schemas.SpeechVoiceInput) {
	speechConfig := SpeechConfig{}

	if voiceConfig != nil && voiceConfig.Voice != nil {
		speechConfig.VoiceConfig = &VoiceConfig{
			PrebuiltVoiceConfig: &PrebuiltVoiceConfig{
				VoiceName: *voiceConfig.Voice,
			},
		}
	}

	if voiceConfig != nil && len(voiceConfig.MultiVoiceConfig) > 0 {
		var speakerVoiceConfigs []*SpeakerVoiceConfig
		for _, vc := range voiceConfig.MultiVoiceConfig {
			speakerVoiceConfigs = append(speakerVoiceConfigs, &SpeakerVoiceConfig{
				Speaker: vc.Speaker,
				VoiceConfig: &VoiceConfig{
					PrebuiltVoiceConfig: &PrebuiltVoiceConfig{
						VoiceName: vc.Voice,
					},
				},
			})
		}

		speechConfig.MultiSpeakerVoiceConfig = &MultiSpeakerVoiceConfig{
			SpeakerVoiceConfigs: speakerVoiceConfigs,
		}
	}

	config.SpeechConfig = &speechConfig
}

func inlineGeminiChatSystemReminder(message schemas.ChatMessage) *Content {
	if message.Content == nil {
		return nil
	}
	wrap := func(text string) *Part {
		return &Part{Text: "<system-reminder>\n" + text + "\n</system-reminder>\n"}
	}
	content := &Content{Role: "user"}
	if message.Content.ContentStr != nil {
		if *message.Content.ContentStr != "" {
			content.Parts = append(content.Parts, wrap(*message.Content.ContentStr))
		}
	} else if message.Content.ContentBlocks != nil {
		for _, block := range message.Content.ContentBlocks {
			if block.Text != nil && *block.Text != "" {
				content.Parts = append(content.Parts, wrap(*block.Text))
			}
		}
	}
	if len(content.Parts) == 0 {
		return nil
	}
	return content
}

func convertBifrostMessagesToGemini(messages []schemas.ChatMessage, allowedImageURLSchemes ...string) ([]Content, *Content, error) {
	if len(allowedImageURLSchemes) == 0 {
		allowedImageURLSchemes = defaultGeminiImageURLSchemes
	}

	if len(messages) == 1 && (messages[0].Role == schemas.ChatMessageRoleSystem || messages[0].Role == schemas.ChatMessageRoleDeveloper) {
		content := convertSystemChatMessageToGeminiUserContent(messages[0])
		if len(content.Parts) > 0 {
			return []Content{content}, nil, nil
		}
	}

	var contents []Content
	var systemInstruction *Content

	var pendingToolResponseParts []*Part

	callIDToFunctionName := make(map[string]string)

	seenNonSystemMessage := false

	for i, message := range messages {
		isSystemMessage := message.Role == schemas.ChatMessageRoleSystem || message.Role == schemas.ChatMessageRoleDeveloper
		if !isSystemMessage {
			seenNonSystemMessage = true
		}
		if isSystemMessage && seenNonSystemMessage {

			if len(pendingToolResponseParts) > 0 {
				contents = append(contents, Content{Parts: pendingToolResponseParts, Role: "user"})
				pendingToolResponseParts = nil
			}
			if inlined := inlineGeminiChatSystemReminder(message); inlined != nil {
				contents = append(contents, *inlined)
			}
			continue
		}

		if isSystemMessage {
			if systemInstruction == nil {
				systemInstruction = &Content{}
			}

			if message.Content != nil {
				if message.Content.ContentStr != nil && *message.Content.ContentStr != "" {
					systemInstruction.Parts = append(systemInstruction.Parts, &Part{
						Text: *message.Content.ContentStr,
					})
				} else if message.Content.ContentBlocks != nil {
					for _, block := range message.Content.ContentBlocks {
						if block.Text != nil && *block.Text != "" {
							systemInstruction.Parts = append(systemInstruction.Parts, &Part{
								Text: *block.Text,
							})
						}
					}
				}
			}
			continue
		}

		isToolResponse := message.Role == schemas.ChatMessageRoleTool && message.ChatToolMessage != nil

		if len(pendingToolResponseParts) > 0 && !isToolResponse {
			contents = append(contents, Content{
				Parts: pendingToolResponseParts,
				Role:  "user",
			})
			pendingToolResponseParts = nil
		}

		if isToolResponse {

			var responseData json.RawMessage
			var contentStr string

			if content := schemas.FlattenToolResultMedia(message.Content, nil); content != nil {

				if content.ContentStr != nil && *content.ContentStr != "" {
					contentStr = *content.ContentStr
				} else if content.ContentBlocks != nil {

					var textParts []string
					for _, block := range content.ContentBlocks {
						if block.Text != nil && *block.Text != "" {
							textParts = append(textParts, *block.Text)
						}
					}
					if len(textParts) > 0 {
						contentStr = strings.Join(textParts, "\n")
					}
				}
			}

			if contentStr != "" {
				var buf bytes.Buffer
				if err := json.Compact(&buf, []byte(contentStr)); err == nil && buf.Len() > 0 && buf.Bytes()[0] == '{' && !containsJSONRefKey(buf.Bytes()) {

					responseData = json.RawMessage(buf.Bytes())
				} else {

					responseData, _ = providerUtils.MarshalSorted(map[string]any{
						"content": contentStr,
					})
				}
			} else {

				responseData = json.RawMessage(`{}`)
			}

			callID := ""
			if message.ChatToolMessage.ToolCallID != nil {
				callID = *message.ChatToolMessage.ToolCallID
			}

			functionName := callID
			if mappedName, ok := callIDToFunctionName[callID]; ok {
				functionName = mappedName
			}

			pendingToolResponseParts = append(pendingToolResponseParts, &Part{
				FunctionResponse: &FunctionResponse{

					ID:       providerUtils.StripThoughtSignature(callID),
					Name:     functionName,
					Response: responseData,
				},
			})

			if i == len(messages)-1 && len(pendingToolResponseParts) > 0 {
				contents = append(contents, Content{
					Parts: pendingToolResponseParts,
					Role:  "user",
				})
				pendingToolResponseParts = nil
			}

			continue
		}

		var parts []*Part

		if message.Content != nil {
			if message.Content.ContentStr != nil && *message.Content.ContentStr != "" {
				parts = append(parts, &Part{
					Text: *message.Content.ContentStr,
				})
			} else if message.Content.ContentBlocks != nil {
				for _, block := range message.Content.ContentBlocks {
					if block.Text != nil {
						parts = append(parts, &Part{
							Text: *block.Text,
						})
					} else if block.File != nil {

						if block.File.FileURL != nil && *block.File.FileURL != "" {

							fileData := &FileData{FileURI: *block.File.FileURL}
							if block.File.FileType != nil {
								fileData.MIMEType = *block.File.FileType
							} else {
								fileData.MIMEType = mimeTypeFromURI(*block.File.FileURL)
							}
							parts = append(parts, &Part{FileData: fileData})
						} else if block.File.FileData != nil {

							fileData := *block.File.FileData
							mimeType := "application/pdf"
							if block.File.FileType != nil {
								mimeType = *block.File.FileType
							}

							dataBytes, extractedMimeType := convertFileDataToBytes(fileData)
							if extractedMimeType != "" {
								mimeType = extractedMimeType
							}

							if len(dataBytes) > 0 {
								parts = append(parts, &Part{
									InlineData: &Blob{
										MIMEType: mimeType,
										Data:     encodeBytesToBase64String(dataBytes),
									},
								})
							}
						}
					} else if block.ImageURLStruct != nil {

						imageURL := block.ImageURLStruct.URL

						sanitizedURL, err := schemas.SanitizeImageURLWithAllowedSchemes(imageURL, allowedImageURLSchemes...)
						if err != nil {
							return nil, nil, fmt.Errorf("failed to sanitize image URL: %w", err)
						}

						urlInfo := schemas.ExtractURLTypeInfo(sanitizedURL)

						mimeType := "image/jpeg"
						if urlInfo.MediaType != nil {
							mimeType = *urlInfo.MediaType
						}

						if urlInfo.Type == schemas.ImageContentTypeBase64 {

							if urlInfo.DataURLWithoutPrefix != nil {
								decodedData, err := base64.StdEncoding.DecodeString(*urlInfo.DataURLWithoutPrefix)
								if err == nil && len(decodedData) > 0 {
									parts = append(parts, &Part{
										InlineData: &Blob{
											MIMEType: mimeType,
											Data:     encodeBytesToBase64String(decodedData),
										},
									})
								}
							}
						} else {

							parts = append(parts, &Part{
								FileData: &FileData{
									MIMEType: mimeType,
									FileURI:  sanitizedURL,
								},
							})
						}
					} else if block.InputAudio != nil {

						decodedData, err := decodeBase64StringToBytes(block.InputAudio.Data)
						if err != nil || len(decodedData) == 0 {
							continue
						}

						mimeType := "audio/mpeg"
						if block.InputAudio.Format != nil {
							format := strings.ToLower(strings.TrimSpace(*block.InputAudio.Format))
							if format != "" {
								if strings.HasPrefix(format, "audio/") {
									mimeType = format
								} else {
									mimeType = "audio/" + format
								}
							}
						}

						parts = append(parts, &Part{
							InlineData: &Blob{
								MIMEType: mimeType,
								Data:     encodeBytesToBase64String(decodedData),
							},
						})
					}
				}
			}
		}

		if message.ChatAssistantMessage != nil && message.ChatAssistantMessage.ToolCalls != nil {
			for _, toolCall := range message.ChatAssistantMessage.ToolCalls {

				if toolCall.Function.Name != nil {

					var argsRaw json.RawMessage
					if toolCall.Function.Arguments != "" {
						var buf bytes.Buffer
						if err := json.Compact(&buf, []byte(toolCall.Function.Arguments)); err == nil {
							argsRaw = buf.Bytes()
						} else {
							argsRaw = json.RawMessage("{}")
						}
					} else {
						argsRaw = json.RawMessage("{}")
					}

					callID := *toolCall.Function.Name
					if toolCall.ID != nil && strings.TrimSpace(*toolCall.ID) != "" {
						callID = *toolCall.ID
					}

					var thoughtSig string
					if strings.Contains(callID, thoughtSignatureSeparator) {
						parts := strings.SplitN(callID, thoughtSignatureSeparator, 2)
						if len(parts) == 2 {
							thoughtSig = parts[1]
						}
					}

					part := &Part{
						FunctionCall: &FunctionCall{

							ID:   providerUtils.StripThoughtSignature(callID),
							Name: *toolCall.Function.Name,
							Args: argsRaw,
						},
					}

					callIDToFunctionName[callID] = *toolCall.Function.Name

					if thoughtSig != "" {
						decoded, err := base64.RawURLEncoding.DecodeString(thoughtSig)
						if err == nil {
							part.ThoughtSignature = decoded
						}
					}

					if part.ThoughtSignature == nil && len(message.ChatAssistantMessage.ReasoningDetails) > 0 {

						baseCallID := callID
						if strings.Contains(callID, thoughtSignatureSeparator) {
							splitParts := strings.SplitN(callID, thoughtSignatureSeparator, 2)
							if len(splitParts) == 2 {
								baseCallID = splitParts[0]
							}
						}
						lookupID := fmt.Sprintf("tool_call_%s", baseCallID)
						for _, reasoningDetail := range message.ChatAssistantMessage.ReasoningDetails {
							if reasoningDetail.ID != nil && *reasoningDetail.ID == lookupID &&
								reasoningDetail.Type == schemas.BifrostReasoningDetailsTypeEncrypted &&
								reasoningDetail.Signature != nil {

								decoded, err := base64.StdEncoding.DecodeString(*reasoningDetail.Signature)
								if err == nil {
									part.ThoughtSignature = decoded
								}
								break
							}
						}
					}

					if part.ThoughtSignature == nil {
						part.ThoughtSignature = []byte(skipThoughtSignatureValidator)
					}

					parts = append(parts, part)
				}
			}
		}

		if len(parts) > 0 {
			content := Content{
				Parts: parts,
				Role:  string(message.Role),
			}
			if message.Role == schemas.ChatMessageRoleUser {
				content.Role = "user"
			} else {
				content.Role = "model"
			}
			contents = append(contents, content)
		}
	}

	return contents, systemInstruction, nil
}

func convertSystemChatMessageToGeminiUserContent(message schemas.ChatMessage) Content {
	content := Content{Role: "user"}

	if message.Content == nil {
		return content
	}

	if message.Content.ContentStr != nil && *message.Content.ContentStr != "" {
		content.Parts = append(content.Parts, &Part{
			Text: *message.Content.ContentStr,
		})
		return content
	}

	if message.Content.ContentBlocks != nil {
		for _, block := range message.Content.ContentBlocks {
			if block.Text != nil && *block.Text != "" {
				content.Parts = append(content.Parts, &Part{
					Text: *block.Text,
				})
			}
		}
	}

	return content
}

func NormalizeModelName(model string) string {
	model = strings.TrimSpace(model)
	if len(model) >= len("google/") && strings.EqualFold(model[:len("google/")], "google/") {
		strippedModel := model[len("google/"):]
		if schemas.IsGeminiModel(strippedModel) || schemas.IsVeoModel(strippedModel) || schemas.IsImagenModel(strippedModel) || schemas.IsGemmaModel(strippedModel) {
			return strippedModel
		}
	}
	return model
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

func normalizeSchemaForGemini(schema map[string]interface{}) map[string]interface{} {
	if schema == nil {
		return nil
	}

	normalized := make(map[string]interface{})
	for k, v := range schema {
		normalized[k] = v
	}

	if typeVal, exists := normalized["type"]; exists {
		types := extractTypesFromValue(typeVal)
		if len(types) > 1 {

			nonNullTypes := make([]string, 0, len(types))
			hasNull := false
			for _, t := range types {
				if t != "null" {
					nonNullTypes = append(nonNullTypes, t)
				} else {
					hasNull = true
				}
			}

			if len(nonNullTypes) > 1 {

				delete(normalized, "type")

				anyOfSchemas := make([]interface{}, 0, len(types))
				for _, t := range nonNullTypes {
					typeSchema := map[string]interface{}{"type": t}
					anyOfSchemas = append(anyOfSchemas, typeSchema)
				}

				if hasNull {
					anyOfSchemas = append(anyOfSchemas, map[string]interface{}{"type": "null"})
				}

				normalized["anyOf"] = anyOfSchemas

				delete(normalized, "enum")
			} else if len(nonNullTypes) == 1 && hasNull {

				normalized["type"] = []interface{}{nonNullTypes[0], "null"}
			} else if len(nonNullTypes) == 1 && !hasNull {

				normalized["type"] = nonNullTypes[0]
			} else if len(nonNullTypes) == 0 && hasNull {

				normalized["type"] = "null"
			}
		}
	}

	switch properties := schema["properties"].(type) {
	case map[string]interface{}:
		newProps := make(map[string]interface{})
		for key, prop := range properties {
			newProps[key] = normalizeSchemaValueForGemini(prop)
		}
		normalized["properties"] = newProps
	case *schemas.OrderedMap:
		newProps := schemas.NewOrderedMapWithCapacity(properties.Len())
		properties.Range(func(key string, prop interface{}) bool {
			newProps.Set(key, normalizeSchemaValueForGemini(prop))
			return true
		})
		normalized["properties"] = newProps
	case schemas.OrderedMap:
		newProps := schemas.NewOrderedMapWithCapacity(properties.Len())
		properties.Range(func(key string, prop interface{}) bool {
			newProps.Set(key, normalizeSchemaValueForGemini(prop))
			return true
		})
		normalized["properties"] = newProps
	}

	switch schema["items"].(type) {
	case map[string]interface{}, *schemas.OrderedMap, schemas.OrderedMap:
		normalized["items"] = normalizeSchemaValueForGemini(schema["items"])
	}

	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		switch schema[key].(type) {
		case []interface{}, []schemas.OrderedMap:
			normalized[key] = normalizeSchemaValueForGemini(schema[key])
		}
	}

	return normalized
}

func normalizeSchemaValueForGemini(v interface{}) interface{} {
	switch tv := v.(type) {
	case []interface{}:
		out := make([]interface{}, len(tv))
		for i, item := range tv {
			out[i] = normalizeSchemaValueForGemini(item)
		}
		return out
	case []schemas.OrderedMap:
		out := make([]schemas.OrderedMap, len(tv))
		for i := range tv {
			if normalized := normalizeOrderedSchemaForGemini(&tv[i]); normalized != nil {
				out[i] = *normalized
			} else {
				out[i] = tv[i]
			}
		}
		return out
	case map[string]interface{}:
		return normalizeSchemaForGemini(tv)
	case *schemas.OrderedMap:
		return normalizeOrderedSchemaForGemini(tv)
	case schemas.OrderedMap:
		if normalized := normalizeOrderedSchemaForGemini(&tv); normalized != nil {
			return *normalized
		}
		return tv
	}
	return v
}

func normalizeOrderedSchemaForGemini(om *schemas.OrderedMap) *schemas.OrderedMap {
	if om == nil {
		return nil
	}
	normalized := normalizeSchemaForGemini(om.ToMap())
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

func extractSchemaMapFromResponseFormat(responseFormat *interface{}) interface{} {
	rf, ok := schemas.ParseChatResponseFormat(responseFormat)
	if !ok || rf.Type != "json_schema" {
		return nil
	}

	if raw := rf.RawSchema(); len(raw) > 0 && !schemaNeedsGeminiNormalization(raw) {
		return raw
	}

	schemaObj, ok := rf.Schema()
	if !ok {
		return nil
	}

	switch schemaObj.(type) {
	case map[string]interface{}, *schemas.OrderedMap, schemas.OrderedMap:

		return normalizeSchemaValueForGemini(schemaObj)
	}
	return nil
}

func containsJSONRefKey(raw []byte) bool {
	found := false
	var walk func(v gjson.Result)
	walk = func(v gjson.Result) {
		isObject := v.IsObject()
		v.ForEach(func(key, value gjson.Result) bool {
			if isObject && key.String() == "$ref" {
				found = true
				return false
			}
			if value.IsObject() || value.IsArray() {
				walk(value)
			}
			return !found
		})
	}
	walk(gjson.ParseBytes(raw))
	return found
}

func geminiFunctionOutputValue(output string) any {
	if json.Valid([]byte(output)) && !containsJSONRefKey([]byte(output)) {
		return json.RawMessage(output)
	}
	return output
}

func extractFunctionResponseOutput(funcResp *FunctionResponse) string {
	if funcResp == nil || funcResp.Response == nil {
		return ""
	}

	var respMap map[string]json.RawMessage
	if err := sonic.Unmarshal(funcResp.Response, &respMap); err == nil {
		if outputVal, ok := respMap["output"]; ok {
			var outputStr string
			if err := sonic.Unmarshal(outputVal, &outputStr); err == nil {
				return outputStr
			}
			return string(outputVal)
		}
	}

	return string(funcResp.Response)
}

func decodeBase64StringToBytes(b64 string) ([]byte, error) {

	standardBase64 := strings.ReplaceAll(strings.ReplaceAll(b64, "_", "/"), "-", "+")

	switch len(standardBase64) % 4 {
	case 2:
		standardBase64 += "=="
	case 3:
		standardBase64 += "="
	}

	decoded, err := base64.StdEncoding.DecodeString(standardBase64)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func encodeBytesToBase64String(bytes []byte) string {
	var base64str string

	if len(bytes) > 0 {

		base64str = base64.StdEncoding.EncodeToString(bytes)
	}

	return base64str
}

func tokenToBytes(token string) []int {
	bytes := []byte(token)
	result := make([]int, len(bytes))
	for i, b := range bytes {
		result[i] = int(b)
	}
	return result
}

func ConvertGeminiLogprobsResultToBifrost(result *LogprobsResult) *schemas.BifrostLogProbs {
	if result == nil || len(result.ChosenCandidates) == 0 {
		return nil
	}

	content := make([]schemas.ContentLogProb, len(result.ChosenCandidates))
	for i, chosen := range result.ChosenCandidates {
		content[i] = schemas.ContentLogProb{
			Token:   chosen.Token,
			LogProb: float64(chosen.LogProbability),
			Bytes:   tokenToBytes(chosen.Token),
		}
		if i < len(result.TopCandidates) && result.TopCandidates[i] != nil {
			for _, tc := range result.TopCandidates[i].Candidates {
				content[i].TopLogProbs = append(content[i].TopLogProbs, schemas.LogProb{
					Token:   tc.Token,
					LogProb: float64(tc.LogProbability),
					Bytes:   tokenToBytes(tc.Token),
				})
			}
		}
	}
	return &schemas.BifrostLogProbs{Content: content}
}

func mimeTypeFromURI(uri string) string {
	if uri == "" {
		return ""
	}

	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}

	if i := strings.LastIndex(uri, "/"); i >= 0 {
		uri = uri[i+1:]
	}
	dot := strings.LastIndex(uri, ".")
	if dot < 0 {
		return ""
	}
	return uriExtensionMIMETypes[strings.ToLower(uri[dot:])]
}

func schemaNeedsGeminiNormalization(raw []byte) bool {
	return resultNeedsGeminiNormalization(gjson.ParseBytes(raw))
}

func geminiTypeArrayNeedsRewrite(value gjson.Result) bool {
	stringEntries, nonNull, foreign := 0, 0, 0
	value.ForEach(func(_, entry gjson.Result) bool {
		if entry.Type != gjson.String {
			foreign++
			return true
		}
		stringEntries++
		if entry.String() != "null" {
			nonNull++
		}
		return true
	})

	if foreign > 0 {

		return true
	}
	if stringEntries <= 1 {

		return false
	}
	if nonNull > 1 {
		return true
	}
	if nonNull == 0 {
		return true
	}

	return stringEntries != 2
}

func resultNeedsGeminiNormalization(result gjson.Result) bool {
	needs := false
	switch {
	case result.IsObject():
		result.ForEach(func(key, value gjson.Result) bool {
			if key.String() == "type" && value.IsArray() && geminiTypeArrayNeedsRewrite(value) {
				needs = true
				return false
			}
			if resultNeedsGeminiNormalization(value) {
				needs = true
				return false
			}
			return true
		})
	case result.IsArray():
		result.ForEach(func(_, value gjson.Result) bool {
			if resultNeedsGeminiNormalization(value) {
				needs = true
				return false
			}
			return true
		})
	}
	return needs
}
