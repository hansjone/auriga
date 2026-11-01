package openai

import (
	"bytes"
	"encoding/json"

	"fmt"

	"slices"
	"strings"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type OpenAITranscriptionRequest struct {
	Model    string `json:"model"`
	File     []byte `json:"file"`
	Filename string `json:"filename"`

	schemas.TranscriptionParameters
	Stream *bool `json:"stream,omitempty"`

	Fallbacks []string `json:"fallbacks,omitempty"`
}
type OpenAIResponsesRequest struct {
	Model string                      `json:"model"`
	Input OpenAIResponsesRequestInput `json:"input"`

	schemas.ResponsesParameters
	Stream *bool `json:"stream,omitempty"`

	Provider    schemas.ModelProvider  `json:"-"`
	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`

	keepDeferLoading bool
}
type OpenAIRerankResponse struct {
	ID      string                       `json:"id"`
	Results []OpenAIRerankResponseResult `json:"results"`
	Meta    *OpenAIRerankMeta            `json:"meta,omitempty"`
	Usage   *schemas.BifrostLLMUsage     `json:"usage,omitempty"`
}
type OpenAIRerankResponseResult struct {
	Index          int             `json:"index"`
	RelevanceScore float64         `json:"relevance_score"`
	Document       json.RawMessage `json:"document,omitempty"`
}
type OpenAIListModelsResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}
type OpenAIVideoEditVideoInput struct {
	ID    string `json:"id,omitempty"`
	Bytes []byte `json:"-"`
}
type OpenAIImageEditRequest struct {
	Model string                  `json:"model"`
	Input *schemas.ImageEditInput `json:"input"`

	schemas.ImageEditParameters

	Stream      *bool                  `json:"stream,omitempty"`
	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
	Created *int64 `json:"created,omitempty"`

	ShutdownDate *string `json:"shutdown_date,omitempty"`

	Active        *bool `json:"active,omitempty"`
	ContextWindow *int  `json:"context_window,omitempty"`
}
type OpenAIImageGenerationRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`

	schemas.ImageGenerationParameters

	Stream    *bool    `json:"stream,omitempty"`
	Fallbacks []string `json:"fallbacks,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIDecisionRequest struct {
	Model            string                   `json:"model"`
	Input            schemas.DecisionInput    `json:"input"`
	Questions        []OpenAIDecisionQuestion `json:"questions"`
	SafetyIdentifier *string                  `json:"safety_identifier,omitempty"`
	Fallbacks        []string                 `json:"fallbacks,omitempty"`
	ExtraParams      map[string]interface{}   `json:"-"`

	PromptCacheKey     *string                     `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions *schemas.PromptCacheOptions `json:"prompt_cache_options,omitempty"`
}
type OpenAIDecisionResponse struct {
	Model   string                 `json:"model"`
	Answers []OpenAIDecisionAnswer `json:"answers"`
	Usage   *OpenAIDecisionUsage   `json:"usage,omitempty"`
}
type OpenAIEmbeddingInput struct {
	Text       *string
	Texts      []string
	Embedding  []int
	Embeddings [][]int
}
type OpenAIVideoSize string
type OpenAITextCompletionRequest struct {
	Model  string                       `json:"model"`
	Prompt *schemas.TextCompletionInput `json:"prompt"`

	schemas.TextCompletionParameters
	Stream *bool `json:"stream,omitempty"`

	PromptCacheIsolationKey *string `json:"prompt_cache_isolation_key,omitempty"`

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIDecisionLevel struct {
	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`

	unknown []string
}
type OpenAIVideoRemixRequest struct {
	Prompt string `json:"prompt"`

	ID       string                `json:"-"`
	Provider schemas.ModelProvider `json:"-"`

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIChatRequest struct {
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`

	schemas.ChatParameters
	Stream *bool `json:"stream,omitempty"`

	PromptCacheIsolationKey *string `json:"prompt_cache_isolation_key,omitempty"`

	MaxTokens *int `json:"max_tokens,omitempty"`

	Provider schemas.ModelProvider `json:"-"`

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAISpeechRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`

	schemas.SpeechParameters
	StreamFormat *string `json:"stream_format,omitempty"`

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIDecisionChoice struct {
	Value       schemas.DecisionScalar `json:"value"`
	Description *string                `json:"description,omitempty"`

	unknown []string
}
type OpenAIDecisionQuestion struct {
	Type         schemas.DecisionType   `json:"type"`
	Name         *string                `json:"name,omitempty"`
	Instructions *schemas.DecisionText  `json:"instructions"`
	Choices      []OpenAIDecisionChoice `json:"choices,omitempty"`
	Levels       []OpenAIDecisionLevel  `json:"levels,omitempty"`

	unknown []string
}
type OpenAIEmbeddingRequest struct {
	Model string                `json:"model"`
	Input *OpenAIEmbeddingInput `json:"input"`

	schemas.EmbeddingParameters

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIRerankRequest struct {
	Model           string                   `json:"model"`
	Query           string                   `json:"query"`
	Documents       []schemas.RerankDocument `json:"documents"`
	TopN            *int                     `json:"top_n,omitempty"`
	MaxTokensPerDoc *int                     `json:"max_tokens_per_doc,omitempty"`
	Priority        *int                     `json:"priority,omitempty"`
	ExtraParams     map[string]interface{}   `json:"-"`
}
type OpenAIImageStreamResponse struct {
	Type              schemas.ImageEventType `json:"type,omitempty"`
	SequenceNumber    *int                   `json:"sequence_number,omitempty"`
	B64JSON           *string                `json:"b64_json,omitempty"`
	PartialImageIndex *int                   `json:"partial_image_index,omitempty"`
	CreatedAt         int64                  `json:"created_at,omitempty"`
	Size              string                 `json:"size,omitempty"`
	Quality           string                 `json:"quality,omitempty"`
	Background        string                 `json:"background,omitempty"`
	OutputFormat      string                 `json:"output_format,omitempty"`
	RawSSE            string                 `json:"-"`
	Usage             *schemas.ImageUsage    `json:"usage,omitempty"`

	Error *struct {
		Code    *string `json:"code,omitempty"`
		Message string  `json:"message,omitempty"`
		Param   *string `json:"param,omitempty"`
		Type    *string `json:"type,omitempty"`
	} `json:"error,omitempty"`
}
type OpenAIVideoEditRequest struct {
	Prompt string                    `json:"prompt"`
	Video  OpenAIVideoEditVideoInput `json:"video"`

	Model string `json:"model,omitempty"`

	Provider schemas.ModelProvider `json:"-"`

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIRerankMeta struct {
	BilledUnits *OpenAIRerankTokenUsage `json:"billed_units,omitempty"`
	Tokens      *OpenAIRerankTokenUsage `json:"tokens,omitempty"`
}
type OpenAIDecisionAnswer struct {
	schemas.DecisionAnswer
}
type OpenAIRerankTokenUsage struct {
	InputTokens  *int64 `json:"input_tokens,omitempty"`
	OutputTokens *int64 `json:"output_tokens,omitempty"`
	SearchUnits  *int64 `json:"search_units,omitempty"`
}
type OpenAIImageVariationRequest struct {
	Model string                       `json:"model"`
	Input *schemas.ImageVariationInput `json:"input"`

	schemas.ImageVariationParameters

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}
type OpenAIVideoGenerationRequest struct {
	Prompt         string  `json:"prompt"`
	InputReference []byte  `json:"input_reference,omitempty"`
	VideoURI       *string `json:"video_uri,omitempty"`

	Model string `json:"model"`

	schemas.VideoGenerationParameters

	Fallbacks   []string               `json:"fallbacks,omitempty"`
	ExtraParams map[string]interface{} `json:"-"`
}

var openAIDecisionRequestKnownFields = map[string]bool{
	"model":             true,
	"input":             true,
	"questions":         true,
	"safety_identifier": true,
	"fallbacks":         true,

	"prompt_cache_key":     true,
	"prompt_cache_options": true,
}

type OpenAIMessage struct {
	Name    *string                     `json:"name,omitempty"`
	Role    schemas.ChatMessageRole     `json:"role,omitempty"`
	Content *schemas.ChatMessageContent `json:"content,omitempty"`

	*schemas.ChatToolMessage
	*OpenAIChatAssistantMessage
}
type OpenAIResponsesRequestInput struct {
	OpenAIResponsesRequestInputStr   *string
	OpenAIResponsesRequestInputArray []schemas.ResponsesMessage
}
type OpenAIChatAssistantMessage struct {
	Refusal   *string `json:"refusal,omitempty"`
	Reasoning *string `json:"reasoning_content,omitempty"`

	ReasoningAlias   *string                        `json:"reasoning,omitempty"`
	ReasoningDetails []schemas.ChatReasoningDetails `json:"reasoning_details,omitempty"`

	Annotations []schemas.ChatAssistantMessageAnnotation `json:"annotations,omitempty"`
	ToolCalls   []schemas.ChatAssistantMessageToolCall   `json:"tool_calls,omitempty"`
}

var ValidOpenAIVideoSizes = map[string]bool{
	string(OpenAIVideoSize720x1280):  true,
	string(OpenAIVideoSize1280x720):  true,
	string(OpenAIVideoSize1024x1792): true,
	string(OpenAIVideoSize1792x1024): true,
}

type OpenAIDecisionUsage struct {
	InputTokens         int                                    `json:"input_tokens"`
	InputTokensDetails  *schemas.ResponsesResponseInputTokens  `json:"input_tokens_details,omitempty"`
	OutputTokens        int                                    `json:"output_tokens"`
	OutputTokensDetails *schemas.ResponsesResponseOutputTokens `json:"output_tokens_details,omitempty"`
	TotalTokens         int                                    `json:"total_tokens"`

	StateTokens        *int     `json:"state_tokens,omitempty"`
	StateTokensDropped *int     `json:"state_tokens_dropped,omitempty"`
	Truncated          *bool    `json:"truncated,omitempty"`
	TruncatedQuestions []string `json:"truncated_questions,omitempty"`
}

const MinMaxCompletionTokens = 16

const MinReasoningMaxTokens = 1

const DefaultCompletionMaxTokens = 4096

func (req *OpenAITextCompletionRequest) GetExtraParams() map[string]interface{} {
	return req.ExtraParams
}

func (e *OpenAIEmbeddingInput) MarshalJSON() ([]byte, error) {

	set := 0
	if e.Text != nil {
		set++
	}
	if e.Texts != nil {
		set++
	}
	if e.Embedding != nil {
		set++
	}
	if e.Embeddings != nil {
		set++
	}
	if set == 0 {
		return nil, fmt.Errorf("embedding input is empty")
	}
	if set > 1 {
		return nil, fmt.Errorf("embedding input must set exactly one of: text, texts, embedding, embeddings")
	}

	if e.Text != nil {
		return providerUtils.MarshalSorted(*e.Text)
	}
	if e.Texts != nil {
		return providerUtils.MarshalSorted(e.Texts)
	}
	if e.Embedding != nil {
		return providerUtils.MarshalSorted(e.Embedding)
	}
	if e.Embeddings != nil {
		return providerUtils.MarshalSorted(e.Embeddings)
	}

	return nil, fmt.Errorf("invalid embedding input")
}

func (e *OpenAIEmbeddingInput) UnmarshalJSON(data []byte) error {
	e.Text = nil
	e.Texts = nil
	e.Embedding = nil
	e.Embeddings = nil

	var s string
	if err := sonic.Unmarshal(data, &s); err == nil {
		e.Text = &s
		return nil
	}

	var ss []string
	if err := sonic.Unmarshal(data, &ss); err == nil {
		e.Texts = ss
		return nil
	}

	var i []int
	if err := sonic.Unmarshal(data, &i); err == nil {
		e.Embedding = i
		return nil
	}

	var i2 [][]int
	if err := sonic.Unmarshal(data, &i2); err == nil {
		e.Embeddings = i2
		return nil
	}

	return fmt.Errorf("unsupported embedding input shape")
}

func (r *OpenAIEmbeddingRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (q *OpenAIDecisionQuestion) UnmarshalJSON(data []byte) error {
	type alias OpenAIDecisionQuestion
	if err := sonic.Unmarshal(data, (*alias)(q)); err != nil {
		return err
	}
	q.unknown = unknownJSONFields(data, "type", "name", "instructions", "choices", "levels")
	return nil
}

func (c *OpenAIDecisionChoice) UnmarshalJSON(data []byte) error {
	type alias OpenAIDecisionChoice
	if err := sonic.Unmarshal(data, (*alias)(c)); err != nil {
		return err
	}
	c.unknown = unknownJSONFields(data, "value", "description")
	return nil
}

func (l *OpenAIDecisionLevel) UnmarshalJSON(data []byte) error {
	type alias OpenAIDecisionLevel
	if err := sonic.Unmarshal(data, (*alias)(l)); err != nil {
		return err
	}
	l.unknown = unknownJSONFields(data, "label", "description")
	return nil
}

func unknownJSONFields(data []byte, known ...string) []string {
	var unknown []string
	gjson.ParseBytes(data).ForEach(func(key, _ gjson.Result) bool {
		if !slices.Contains(known, key.String()) {
			unknown = append(unknown, key.String())
		}
		return true
	})
	return unknown
}

func (r *OpenAIDecisionRequest) UnmarshalJSON(data []byte) error {
	type alias OpenAIDecisionRequest
	if err := sonic.Unmarshal(data, (*alias)(r)); err != nil {
		return err
	}
	r.ExtraParams = nil
	gjson.ParseBytes(data).ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		if openAIDecisionRequestKnownFields[name] {
			return true
		}
		if r.ExtraParams == nil {
			r.ExtraParams = make(map[string]interface{})
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(value.Raw)); err == nil {
			r.ExtraParams[name] = json.RawMessage(compact.Bytes())
		} else {
			r.ExtraParams[name] = json.RawMessage(value.Raw)
		}
		return true
	})
	return nil
}

func (r *OpenAIDecisionRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (a OpenAIDecisionAnswer) MarshalJSON() ([]byte, error) {
	if a.Name != nil {
		return sonic.Marshal(a.DecisionAnswer)
	}
	type answer schemas.DecisionAnswer
	return sonic.Marshal(struct {
		Type schemas.DecisionType `json:"type"`
		Name *string              `json:"name"`
		answer
	}{Type: a.Type, answer: answer(a.DecisionAnswer)})
}

func (u *OpenAIDecisionUsage) ToBifrostLLMUsage() *schemas.BifrostLLMUsage {
	if u == nil {
		return nil
	}
	usage := (&schemas.ResponsesResponseUsage{
		InputTokens:         u.InputTokens,
		InputTokensDetails:  u.InputTokensDetails,
		OutputTokens:        u.OutputTokens,
		OutputTokensDetails: u.OutputTokensDetails,
		TotalTokens:         u.TotalTokens,
	}).ToBifrostLLMUsage()
	usage.StateTokens = u.StateTokens
	usage.StateTokensDropped = u.StateTokensDropped
	usage.Truncated = u.Truncated
	usage.TruncatedQuestions = u.TruncatedQuestions
	return usage
}

func (r *OpenAIRerankRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (req *OpenAIChatRequest) GetExtraParams() map[string]interface{} {
	return req.ExtraParams
}

func (req *OpenAIChatRequest) MarshalJSON() ([]byte, error) {
	if req == nil {
		return []byte("null"), nil
	}
	type Alias OpenAIChatRequest

	keepCacheControl := req.Provider == schemas.OpenRouter

	needsCopy := false
	for _, msg := range req.Messages {
		if hasFieldsToStripInChatMessage(msg, keepCacheControl) {
			needsCopy = true
			break
		}
	}

	var processedMessages []OpenAIMessage
	if needsCopy {
		processedMessages = make([]OpenAIMessage, len(req.Messages))
		for i, msg := range req.Messages {
			if !hasFieldsToStripInChatMessage(msg, keepCacheControl) {

				processedMessages[i] = msg
				continue
			}

			processedMessages[i] = msg

			if msg.OpenAIChatAssistantMessage != nil {
				needsAnnotationStrip := false
				for _, annotation := range msg.OpenAIChatAssistantMessage.Annotations {
					if annotation.URLCitation.Text != nil {
						needsAnnotationStrip = true
						break
					}
				}
				needsToolCallStrip := false
				if !keepCacheControl {
					for _, toolCall := range msg.OpenAIChatAssistantMessage.ToolCalls {
						if toolCall.CacheControl != nil {
							needsToolCallStrip = true
							break
						}
					}
				}
				if needsAnnotationStrip || needsToolCallStrip {
					assistantCopy := *msg.OpenAIChatAssistantMessage
					if needsAnnotationStrip {
						assistantCopy.Annotations = make([]schemas.ChatAssistantMessageAnnotation, len(msg.OpenAIChatAssistantMessage.Annotations))
						for j, annotation := range msg.OpenAIChatAssistantMessage.Annotations {
							annotationCopy := annotation
							annotationCopy.URLCitation.Text = nil
							assistantCopy.Annotations[j] = annotationCopy
						}
					}
					if needsToolCallStrip {
						assistantCopy.ToolCalls = slices.Clone(msg.OpenAIChatAssistantMessage.ToolCalls)
						for j := range assistantCopy.ToolCalls {
							assistantCopy.ToolCalls[j].CacheControl = nil
						}
					}
					processedMessages[i].OpenAIChatAssistantMessage = &assistantCopy
				}
			}

			if msg.Content != nil && msg.Content.ContentBlocks != nil {
				contentCopy := *msg.Content
				contentCopy.ContentBlocks = make([]schemas.ChatContentBlock, len(msg.Content.ContentBlocks))
				for j, block := range msg.Content.ContentBlocks {
					stripBlockCacheControl := block.CacheControl != nil && !keepCacheControl
					needsBlockCopy := stripBlockCacheControl || block.Citations != nil || (block.File != nil && (block.File.FileType != nil || block.File.FileURL != nil || fileDataNeedsDataURL(block.File.FileData)))
					if needsBlockCopy {
						blockCopy := block
						if stripBlockCacheControl {
							blockCopy.CacheControl = nil
						}
						blockCopy.Citations = nil

						if blockCopy.File != nil && (blockCopy.File.FileType != nil || fileDataNeedsDataURL(blockCopy.File.FileData)) {
							fileCopy := *blockCopy.File
							if fileCopy.FileData != nil {
								fileCopy.FileData = schemas.Ptr(fileDataAsDataURL(*fileCopy.FileData, fileCopy.FileType))
							}
							fileCopy.FileType = nil
							blockCopy.File = &fileCopy
						}
						contentCopy.ContentBlocks[j] = blockCopy
					} else {
						contentCopy.ContentBlocks[j] = block
					}
				}
				processedMessages[i].Content = &contentCopy
			}
		}
	} else {
		processedMessages = req.Messages
	}

	var processedTools []schemas.ChatTool
	if len(req.Tools) > 0 {
		needsToolChange := false
		for _, tool := range req.Tools {
			stripToolCacheControl := tool.CacheControl != nil && !keepCacheControl
			if stripToolCacheControl || isAnthropicServerToolShape(tool) || hasAnthropicOnlyToolFlags(tool) {
				needsToolChange = true
				break
			}
		}

		if needsToolChange {
			processedTools = make([]schemas.ChatTool, 0, len(req.Tools))
			for _, tool := range req.Tools {

				if isAnthropicServerToolShape(tool) {
					continue
				}
				stripToolCacheControl := tool.CacheControl != nil && !keepCacheControl
				if !stripToolCacheControl && !hasAnthropicOnlyToolFlags(tool) {
					processedTools = append(processedTools, tool)
					continue
				}
				toolCopy := tool
				if stripToolCacheControl {
					toolCopy.CacheControl = nil
				}
				toolCopy.DeferLoading = nil
				toolCopy.AllowedCallers = nil
				toolCopy.InputExamples = nil
				toolCopy.EagerInputStreaming = nil
				processedTools = append(processedTools, toolCopy)
			}
		} else {
			processedTools = req.Tools
		}
	} else {
		processedTools = req.Tools
	}

	aux := struct {
		*Alias

		Messages []OpenAIMessage `json:"messages"`

		Tools []schemas.ChatTool `json:"tools,omitempty"`

		Reasoning       *schemas.ChatReasoning `json:"reasoning,omitempty"`
		ReasoningEffort *string                `json:"reasoning_effort,omitempty"`

		WebSearchOptions *schemas.ChatWebSearchOptions `json:"web_search_options,omitempty"`
	}{
		Alias:    (*Alias)(req),
		Messages: processedMessages,
		Tools:    processedTools,
	}

	if req.Reasoning != nil && req.Reasoning.Effort != nil {
		aux.ReasoningEffort = req.Reasoning.Effort
	}

	if req.ChatParameters.WebSearchOptions != nil {
		aux.WebSearchOptions = req.ChatParameters.WebSearchOptions
		if aux.WebSearchOptions.Filters != nil {
			optionsCopy := *req.ChatParameters.WebSearchOptions
			optionsCopy.Filters = nil
			aux.WebSearchOptions = &optionsCopy
		}
	}

	return providerUtils.MarshalSorted(aux)
}

func (req *OpenAIChatRequest) UnmarshalJSON(data []byte) error {

	type baseFields struct {
		Model                   string          `json:"model"`
		Messages                []OpenAIMessage `json:"messages"`
		Stream                  *bool           `json:"stream,omitempty"`
		MaxTokens               *int            `json:"max_tokens,omitempty"`
		PromptCacheIsolationKey *string         `json:"prompt_cache_isolation_key,omitempty"`
		Fallbacks               []string        `json:"fallbacks,omitempty"`
	}
	var base baseFields
	if err := sonic.Unmarshal(data, &base); err != nil {
		return err
	}
	req.Model = base.Model
	req.Messages = base.Messages
	req.Stream = base.Stream
	req.MaxTokens = base.MaxTokens
	req.PromptCacheIsolationKey = base.PromptCacheIsolationKey
	req.Fallbacks = base.Fallbacks

	var params schemas.ChatParameters
	if err := sonic.Unmarshal(data, &params); err != nil {
		return err
	}
	req.ChatParameters = params

	return nil
}

func (r *OpenAIResponsesRequestInput) UnmarshalJSON(data []byte) error {
	var str string
	if err := sonic.Unmarshal(data, &str); err == nil {
		r.OpenAIResponsesRequestInputStr = &str
		r.OpenAIResponsesRequestInputArray = nil
		return nil
	}
	var array []schemas.ResponsesMessage
	if err := sonic.Unmarshal(data, &array); err == nil {
		r.OpenAIResponsesRequestInputStr = nil
		r.OpenAIResponsesRequestInputArray = array
		return nil
	}
	return fmt.Errorf("openai responses request input is neither a string nor an array of responses messages")
}

func (r *OpenAIResponsesRequestInput) MarshalJSON() ([]byte, error) {
	if r.OpenAIResponsesRequestInputStr != nil {
		return providerUtils.MarshalSorted(*r.OpenAIResponsesRequestInputStr)
	}
	if r.OpenAIResponsesRequestInputArray != nil {

		needsCopy := false
		for _, msg := range r.OpenAIResponsesRequestInputArray {
			if hasFieldsToStripInResponsesMessage(msg) {
				needsCopy = true
				break
			}
		}

		if !needsCopy {
			data, err := providerUtils.MarshalSorted(r.OpenAIResponsesRequestInputArray)
			if err != nil {
				return nil, err
			}
			return stripCompactionItemSummary(data, r.OpenAIResponsesRequestInputArray), nil
		}

		messagesCopy := make([]schemas.ResponsesMessage, len(r.OpenAIResponsesRequestInputArray))
		for i, msg := range r.OpenAIResponsesRequestInputArray {
			if !hasFieldsToStripInResponsesMessage(msg) {

				messagesCopy[i] = msg
				continue
			}

			messagesCopy[i] = msg

			messagesCopy[i].CacheControl = nil

			if msg.Content != nil && msg.Content.ContentBlocks != nil {
				contentCopy := *msg.Content
				contentCopy.ContentBlocks = make([]schemas.ResponsesMessageContentBlock, 0, len(msg.Content.ContentBlocks))
				hasContentModification := false
				for _, block := range msg.Content.ContentBlocks {

					if block.Type == schemas.ResponsesOutputMessageContentTypeRenderedContent {
						hasContentModification = true
						continue
					}

					needsBlockCopy := block.CacheControl != nil || block.Citations != nil || block.MediaResolution != nil || (block.ResponsesInputMessageContentBlockFile != nil && (block.ResponsesInputMessageContentBlockFile.FileType != nil || fileDataNeedsDataURL(block.ResponsesInputMessageContentBlockFile.FileData))) || (block.ResponsesOutputMessageContentText != nil && len(block.ResponsesOutputMessageContentText.Annotations) > 0)
					if needsBlockCopy {
						hasContentModification = true
						blockCopy := block
						blockCopy.CacheControl = nil
						blockCopy.Citations = nil
						blockCopy.MediaResolution = nil

						if blockCopy.ResponsesOutputMessageContentText != nil && len(blockCopy.ResponsesOutputMessageContentText.Annotations) > 0 {
							textCopy := *blockCopy.ResponsesOutputMessageContentText
							filteredAnnotations := filterSupportedAnnotations(textCopy.Annotations)
							if len(filteredAnnotations) > 0 {
								textCopy.Annotations = filteredAnnotations
								blockCopy.ResponsesOutputMessageContentText = &textCopy
							} else {

								textCopy.Annotations = nil
								blockCopy.ResponsesOutputMessageContentText = &textCopy
							}
						}

						if blockCopy.ResponsesInputMessageContentBlockFile != nil && (blockCopy.ResponsesInputMessageContentBlockFile.FileType != nil || fileDataNeedsDataURL(blockCopy.ResponsesInputMessageContentBlockFile.FileData)) {
							fileCopy := *blockCopy.ResponsesInputMessageContentBlockFile
							if fileCopy.FileData != nil {
								fileCopy.FileData = schemas.Ptr(fileDataAsDataURL(*fileCopy.FileData, fileCopy.FileType))
							}
							fileCopy.FileType = nil
							blockCopy.ResponsesInputMessageContentBlockFile = &fileCopy
						}
						contentCopy.ContentBlocks = append(contentCopy.ContentBlocks, blockCopy)
					} else {
						contentCopy.ContentBlocks = append(contentCopy.ContentBlocks, block)
					}
				}
				if hasContentModification {
					messagesCopy[i].Content = &contentCopy
				}
			}

			if msg.ResponsesToolMessage != nil {
				toolMsgCopy := *msg.ResponsesToolMessage
				toolMsgModified := false

				if msg.ResponsesToolMessage.Action != nil && msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction != nil {
					sources := msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction.Sources
					if len(sources) > 0 {
						needsSourceCopy := false
						for _, source := range sources {
							if source.Title != nil || source.EncryptedContent != nil || source.PageAge != nil {
								needsSourceCopy = true
								break
							}
						}

						if needsSourceCopy {
							actionCopy := *msg.ResponsesToolMessage.Action
							webSearchActionCopy := *msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction
							strippedSources := make([]schemas.ResponsesWebSearchToolCallActionSearchSource, len(sources))
							for j, source := range sources {

								strippedSources[j] = schemas.ResponsesWebSearchToolCallActionSearchSource{
									Type: source.Type,
									URL:  source.URL,
									Name: source.Name,
								}
							}
							webSearchActionCopy.Sources = strippedSources
							actionCopy.ResponsesWebSearchToolCallAction = &webSearchActionCopy
							toolMsgCopy.Action = &actionCopy
							toolMsgModified = true
						}
					}
				}

				var outputBlocks []schemas.ResponsesMessageContentBlock
				hadToolReferences := false
				if msg.ResponsesToolMessage.Output != nil {
					outputBlocks, hadToolReferences = withoutToolReferenceBlocks(msg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks)
				}
				if hadToolReferences && len(outputBlocks) == 0 {
					empty := ""
					outputCopy := *msg.ResponsesToolMessage.Output
					outputCopy.ResponsesToolCallOutputStr = &empty
					outputCopy.ResponsesFunctionToolCallOutputBlocks = nil
					toolMsgCopy.Output = &outputCopy
					toolMsgModified = true
				} else if outputBlocks != nil && isFunctionCallOutputBlocksFlattenable(outputBlocks) {
					flattened := flattenFunctionCallOutputBlocks(outputBlocks)
					outputCopy := *msg.ResponsesToolMessage.Output
					outputCopy.ResponsesToolCallOutputStr = &flattened
					outputCopy.ResponsesFunctionToolCallOutputBlocks = nil
					toolMsgCopy.Output = &outputCopy
					toolMsgModified = true
				} else if outputBlocks != nil {

					hasToolModification := hadToolReferences
					for _, block := range outputBlocks {
						if block.CacheControl != nil || block.Citations != nil || block.MediaResolution != nil || (block.ResponsesInputMessageContentBlockFile != nil && (block.ResponsesInputMessageContentBlockFile.FileType != nil || fileDataNeedsDataURL(block.ResponsesInputMessageContentBlockFile.FileData))) {
							hasToolModification = true
							break
						}
					}

					if hasToolModification {
						outputCopy := *msg.ResponsesToolMessage.Output
						outputCopy.ResponsesFunctionToolCallOutputBlocks = make([]schemas.ResponsesMessageContentBlock, len(outputBlocks))
						for j, block := range outputBlocks {
							needsBlockCopy := block.CacheControl != nil || block.Citations != nil || block.MediaResolution != nil || (block.ResponsesInputMessageContentBlockFile != nil && (block.ResponsesInputMessageContentBlockFile.FileType != nil || fileDataNeedsDataURL(block.ResponsesInputMessageContentBlockFile.FileData)))
							if needsBlockCopy {
								blockCopy := block
								blockCopy.CacheControl = nil
								blockCopy.Citations = nil
								blockCopy.MediaResolution = nil

								if blockCopy.ResponsesInputMessageContentBlockFile != nil && (blockCopy.ResponsesInputMessageContentBlockFile.FileType != nil || fileDataNeedsDataURL(blockCopy.ResponsesInputMessageContentBlockFile.FileData)) {
									fileCopy := *blockCopy.ResponsesInputMessageContentBlockFile
									if fileCopy.FileData != nil {
										fileCopy.FileData = schemas.Ptr(fileDataAsDataURL(*fileCopy.FileData, fileCopy.FileType))
									}
									fileCopy.FileType = nil
									blockCopy.ResponsesInputMessageContentBlockFile = &fileCopy
								}
								outputCopy.ResponsesFunctionToolCallOutputBlocks[j] = blockCopy
							} else {
								outputCopy.ResponsesFunctionToolCallOutputBlocks[j] = block
							}
						}
						toolMsgCopy.Output = &outputCopy
						toolMsgModified = true
					}
				}

				if toolMsgModified {
					messagesCopy[i].ResponsesToolMessage = &toolMsgCopy
				}
			}
		}
		data, err := providerUtils.MarshalSorted(messagesCopy)
		if err != nil {
			return nil, err
		}
		return stripCompactionItemSummary(data, messagesCopy), nil
	}
	return providerUtils.MarshalSorted(nil)
}

func stripCompactionItemSummary(data []byte, items []schemas.ResponsesMessage) []byte {

	parsed := gjson.ParseBytes(data)
	if !parsed.IsArray() {
		return data
	}

	var rebuilt [][]byte
	changed := false
	index := 0
	parsed.ForEach(func(_, element gjson.Result) bool {
		raw := []byte(element.Raw)
		if index < len(items) {
			if msg := items[index]; msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeCompaction {
				if updated, err := sjson.DeleteBytes(raw, "summary"); err == nil {
					raw = updated
					changed = true
				}
			}
		}
		rebuilt = append(rebuilt, raw)
		index++
		return true
	})
	if !changed {
		return data
	}

	var joined bytes.Buffer
	joined.Grow(len(data))
	joined.WriteByte('[')
	for i, element := range rebuilt {
		if i > 0 {
			joined.WriteByte(',')
		}
		joined.Write(element)
	}
	joined.WriteByte(']')
	return joined.Bytes()
}

func isAnthropicServerToolShape(t schemas.ChatTool) bool {
	return t.Function == nil && t.Custom == nil
}

func hasAnthropicOnlyToolFlags(t schemas.ChatTool) bool {
	return t.DeferLoading != nil ||
		len(t.AllowedCallers) > 0 ||
		len(t.InputExamples) > 0 ||
		t.EagerInputStreaming != nil
}

func hasAnthropicOnlyResponsesToolFlags(t schemas.ResponsesTool, keepDeferLoading bool) bool {
	return (t.DeferLoading != nil && !keepDeferLoading) ||
		responsesToolCallersNeedRewrite(t) ||
		len(t.InputExamples) > 0 ||
		t.EagerInputStreaming != nil ||
		(t.ResponsesToolCodeInterpreter != nil && t.ResponsesToolCodeInterpreter.Version != nil) ||
		hasAnthropicMCPToolsetConfig(t)
}

func hasAnthropicMCPToolsetConfig(t schemas.ResponsesTool) bool {
	return t.ResponsesToolMCP != nil && (t.ResponsesToolMCP.DefaultConfig != nil || len(t.ResponsesToolMCP.ToolConfigs) > 0)
}

func responsesToolCallersNeedRewrite(t schemas.ResponsesTool) bool {
	if len(t.AllowedCallers) == 0 {
		return false
	}
	if !responsesToolSupportsAllowedCallers(t.Type) {
		return true
	}
	mapped := openAIAllowedCallers(t.AllowedCallers)
	if len(mapped) != len(t.AllowedCallers) {
		return true
	}
	for i := range mapped {
		if mapped[i] != t.AllowedCallers[i] {
			return true
		}
	}
	return false
}

func openAIAllowedCallers(callers []string) []string {
	mapped := make([]string, 0, len(callers))
	seen := make(map[string]bool, len(callers))
	for _, caller := range callers {
		if strings.HasPrefix(caller, schemas.ResponsesToolCallerCodeExecutionPrefix) {
			caller = schemas.ResponsesToolCallerProgrammatic
		}
		if seen[caller] {
			continue
		}
		seen[caller] = true
		mapped = append(mapped, caller)
	}
	return mapped
}

func responsesToolSupportsAllowedCallers(t schemas.ResponsesToolType) bool {
	switch t {
	case schemas.ResponsesToolTypeFunction,
		schemas.ResponsesToolTypeCustom,
		schemas.ResponsesToolTypeShell,
		schemas.ResponsesToolTypeApplyPatch,
		schemas.ResponsesToolTypeMCP,
		schemas.ResponsesToolTypeCodeInterpreter:
		return true
	default:
		return false
	}
}

func isAnthropicOnlyResponsesToolType(t schemas.ResponsesTool) bool {
	return t.Type == schemas.ResponsesToolTypeWebFetch ||
		t.Type == schemas.ResponsesToolTypeMemory
}

func hasFieldsToStripInChatMessage(msg OpenAIMessage, keepCacheControl bool) bool {
	if msg.Content != nil && msg.Content.ContentBlocks != nil {
		for _, block := range msg.Content.ContentBlocks {
			if block.CacheControl != nil && !keepCacheControl {
				return true
			}
			if block.Citations != nil {
				return true
			}
			if block.File != nil && (block.File.FileType != nil || fileDataNeedsDataURL(block.File.FileData)) {
				return true
			}
		}
	}
	if msg.OpenAIChatAssistantMessage != nil {
		for _, annotation := range msg.OpenAIChatAssistantMessage.Annotations {
			if annotation.URLCitation.Text != nil {
				return true
			}
		}
		if !keepCacheControl {
			for _, toolCall := range msg.OpenAIChatAssistantMessage.ToolCalls {
				if toolCall.CacheControl != nil {
					return true
				}
			}
		}
	}
	return false
}

func hasFieldsToStripInResponsesMessage(msg schemas.ResponsesMessage) bool {

	if msg.CacheControl != nil {
		return true
	}
	if msg.Content != nil && msg.Content.ContentBlocks != nil {
		for _, block := range msg.Content.ContentBlocks {
			if block.CacheControl != nil {
				return true
			}
			if block.Citations != nil {
				return true
			}

			if block.MediaResolution != nil {
				return true
			}
			if block.ResponsesInputMessageContentBlockFile != nil && (block.ResponsesInputMessageContentBlockFile.FileType != nil || fileDataNeedsDataURL(block.ResponsesInputMessageContentBlockFile.FileData)) {
				return true
			}
			if block.ResponsesOutputMessageContentText != nil && len(block.ResponsesOutputMessageContentText.Annotations) > 0 {
				return true
			}

			if block.Type == schemas.ResponsesOutputMessageContentTypeRenderedContent {
				return true
			}
		}
	}
	if msg.ResponsesToolMessage != nil {

		if msg.ResponsesToolMessage.Action != nil && msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction != nil {
			for _, source := range msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction.Sources {
				if source.Title != nil || source.EncryptedContent != nil || source.PageAge != nil {
					return true
				}
			}
		}

		if msg.ResponsesToolMessage.Output != nil && msg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks != nil {

			if isFunctionCallOutputBlocksFlattenable(msg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks) {
				return true
			}
			for _, block := range msg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks {
				if block.Type == schemas.ResponsesInputMessageContentBlockTypeToolReference {
					return true
				}
				if block.CacheControl != nil {
					return true
				}

				if block.Citations != nil {
					return true
				}
				if block.MediaResolution != nil {
					return true
				}
				if block.ResponsesInputMessageContentBlockFile != nil && (block.ResponsesInputMessageContentBlockFile.FileType != nil || fileDataNeedsDataURL(block.ResponsesInputMessageContentBlockFile.FileData)) {
					return true
				}
			}
		}
	}
	return false
}

func isFunctionCallOutputBlocksFlattenable(blocks []schemas.ResponsesMessageContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Type != schemas.ResponsesInputMessageContentBlockTypeText &&
			block.Type != schemas.ResponsesOutputMessageContentTypeText {
			return false
		}
		if block.Text == nil {
			return false
		}

		if block.PromptCacheBreakpoint != nil {
			return false
		}
	}
	return true
}

func withoutToolReferenceBlocks(blocks []schemas.ResponsesMessageContentBlock) ([]schemas.ResponsesMessageContentBlock, bool) {
	if !slices.ContainsFunc(blocks, func(b schemas.ResponsesMessageContentBlock) bool {
		return b.Type == schemas.ResponsesInputMessageContentBlockTypeToolReference
	}) {
		return blocks, false
	}
	filtered := make([]schemas.ResponsesMessageContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != schemas.ResponsesInputMessageContentBlockTypeToolReference {
			filtered = append(filtered, block)
		}
	}
	return filtered, true
}

func flattenFunctionCallOutputBlocks(blocks []schemas.ResponsesMessageContentBlock) string {
	var b strings.Builder
	for i, block := range blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		if block.Text != nil {
			b.WriteString(*block.Text)
		}
	}
	return b.String()
}

func filterSupportedAnnotations(annotations []schemas.ResponsesOutputMessageContentTextAnnotation) []schemas.ResponsesOutputMessageContentTextAnnotation {
	if len(annotations) == 0 {
		return annotations
	}

	supportedAnnotations := make([]schemas.ResponsesOutputMessageContentTextAnnotation, 0, len(annotations))
	for _, annotation := range annotations {
		switch annotation.Type {
		case "url_citation":
			supportedAnnotations = append(supportedAnnotations, schemas.ResponsesOutputMessageContentTextAnnotation{
				Type:       "url_citation",
				URL:        annotation.URL,
				Title:      annotation.Title,
				StartIndex: annotation.StartIndex,
				EndIndex:   annotation.EndIndex,
			})
		case "file_citation", "container_file_citation", "file_path", "text_annotation":

			supportedAnnotations = append(supportedAnnotations, annotation)
		default:
			continue
		}
	}

	return supportedAnnotations
}

func (resp *OpenAIResponsesRequest) GetExtraParams() map[string]interface{} {
	return resp.ExtraParams
}

func (resp *OpenAIResponsesRequest) MarshalJSON() ([]byte, error) {
	type Alias OpenAIResponsesRequest

	inputBytes, err := resp.Input.MarshalJSON()
	if err != nil {
		return nil, err
	}

	var processedTools []schemas.ResponsesTool
	if len(resp.Tools) > 0 {
		needsReshape := false
		for _, tool := range resp.Tools {
			if isAnthropicOnlyResponsesToolType(tool) ||
				tool.CacheControl != nil ||
				hasAnthropicOnlyResponsesToolFlags(tool, resp.keepDeferLoading) {
				needsReshape = true
				break
			}
		}

		if needsReshape {
			processedTools = make([]schemas.ResponsesTool, 0, len(resp.Tools))
			for _, tool := range resp.Tools {
				if isAnthropicOnlyResponsesToolType(tool) {

					continue
				}
				if tool.CacheControl == nil && !hasAnthropicOnlyResponsesToolFlags(tool, resp.keepDeferLoading) {
					processedTools = append(processedTools, tool)
					continue
				}
				toolCopy := tool
				toolCopy.CacheControl = nil
				if !resp.keepDeferLoading {
					toolCopy.DeferLoading = nil
				}
				if !responsesToolSupportsAllowedCallers(toolCopy.Type) {
					toolCopy.AllowedCallers = nil
				} else if len(toolCopy.AllowedCallers) > 0 {
					toolCopy.AllowedCallers = openAIAllowedCallers(toolCopy.AllowedCallers)
				}
				toolCopy.InputExamples = nil
				toolCopy.EagerInputStreaming = nil
				if hasAnthropicMCPToolsetConfig(toolCopy) {
					mcpCopy := *toolCopy.ResponsesToolMCP
					mcpCopy.DefaultConfig = nil
					mcpCopy.ToolConfigs = nil
					toolCopy.ResponsesToolMCP = &mcpCopy
				}
				if toolCopy.ResponsesToolCodeInterpreter != nil && toolCopy.ResponsesToolCodeInterpreter.Version != nil {
					ciCopy := *toolCopy.ResponsesToolCodeInterpreter
					ciCopy.Version = nil
					toolCopy.ResponsesToolCodeInterpreter = &ciCopy
				}
				processedTools = append(processedTools, toolCopy)
			}
		} else {
			processedTools = resp.Tools
		}
	} else {
		processedTools = resp.Tools
	}

	aux := struct {
		*Alias

		Input json.RawMessage `json:"input"`

		Reasoning *schemas.ResponsesParametersReasoning `json:"reasoning,omitempty"`

		Tools []schemas.ResponsesTool `json:"tools,omitempty"`
	}{
		Alias: (*Alias)(resp),
		Input: json.RawMessage(inputBytes),
		Tools: processedTools,
	}

	if resp.Reasoning != nil {
		aux.Reasoning = &schemas.ResponsesParametersReasoning{
			Effort:          resp.Reasoning.Effort,
			GenerateSummary: resp.Reasoning.GenerateSummary,
			Summary:         resp.Reasoning.Summary,
			Context:         resp.Reasoning.Context,
			Mode:            resp.Reasoning.Mode,
			MaxTokens:       nil,
		}
	}

	data, err := providerUtils.MarshalSorted(aux)
	if err != nil || resp.Provider != schemas.BytePlus || resp.Reasoning == nil {
		return data, err
	}

	return providerUtils.DeleteJSONField(data, "reasoning.summary")
}

func (r *OpenAISpeechRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (response *OpenAIListModelsResponse) UnmarshalJSON(data []byte) error {
	type envelope OpenAIListModelsResponse

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return sonic.Unmarshal(trimmed, (*envelope)(response))
	}

	var models []struct {
		OpenAIModel
		Organization  string `json:"organization"`
		ContextLength *int   `json:"context_length,omitempty"`
	}
	if err := sonic.Unmarshal(trimmed, &models); err != nil {
		return err
	}

	response.Data = make([]OpenAIModel, len(models))
	for i, model := range models {
		response.Data[i] = model.OpenAIModel
		if response.Data[i].OwnedBy == "" {
			response.Data[i].OwnedBy = model.Organization
		}
		if response.Data[i].ContextWindow == nil {
			response.Data[i].ContextWindow = model.ContextLength
		}
	}
	return nil
}

func (r *OpenAIImageGenerationRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (r *OpenAIImageEditRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (r *OpenAIImageVariationRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

const (
	OpenAIVideoSize720x1280  OpenAIVideoSize = "720x1280"
	OpenAIVideoSize1280x720  OpenAIVideoSize = "1280x720"
	OpenAIVideoSize1024x1792 OpenAIVideoSize = "1024x1792"
	OpenAIVideoSize1792x1024 OpenAIVideoSize = "1792x1024"
)

const DefaultOpenAIVideoSize = OpenAIVideoSize720x1280

func (req *OpenAIVideoGenerationRequest) GetExtraParams() map[string]interface{} {
	return req.ExtraParams
}

func (r *OpenAIVideoEditRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (r *OpenAIVideoRemixRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}
