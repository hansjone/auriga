// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"context"

	"encoding/json"
	"fmt"
	"hash"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
)

type anthropicToResponsesStreamState struct {
	webSearchItemIDs map[string]bool

	nextBlockIndex   int
	blockIndexByItem map[string]int

	emittedTextByBlock map[int]*emittedTextProgress

	blockIndexMisses []string

	passthrough bool

	pendingHostedToolSearches []hostedToolSearchCall
	emittedToolUse            bool

	codeExecToolNameByItem map[string]string

	blockTypes []AnthropicContentBlockType

	reasoningPayloadSentByItem map[string]bool

	reasoningSignaturesByItem map[string]string

	codeExecServerClosedByItem map[string]bool
}

var anthropicResponsesStreamStatePool = sync.Pool{
	New: func() interface{} {
		return &AnthropicResponsesStreamState{
			InputJSONBuffers:          make(map[int]string),
			InputJSONPurposes:         make(map[int]anthropicInputJSONBufferKind),
			ContentIndexToOutputIndex: make(map[int]int),
			ToolArgumentBuffers:       make(map[int]string),
			MCPCallOutputIndices:      make(map[int]bool),
			ItemIDs:                   make(map[int]string),
			ReasoningSignatures:       make(map[int]string),
			TextContentIndices:        make(map[int]bool),
			ReasoningContentIndices:   make(map[int]bool),
			CompactionContentIndices:  make(map[int]*schemas.CacheControl),
			CompactionToolChanges:     make(map[int][]schemas.ResponsesMessageContentBlock),
			OutputItems:               make(map[int]*schemas.ResponsesMessage),
			TextBuffers:               make(map[int]*strings.Builder),
			ReasoningTextBuffers:      make(map[int]*strings.Builder),
			CurrentOutputIndex:        0,
			CreatedAt:                 int(time.Now().Unix()),
			HasEmittedCreated:         false,
			HasEmittedInProgress:      false,
		}
	},
}

type anthropicNativeRequestSurface struct {
	Thinking *AnthropicThinking

	MaxTokens int

	NeutralReasoning *schemas.ResponsesParametersReasoning
}

var anthropicClientToolSearchKey = anthropicClientToolSearchKeyType{}

type anthropicNativeEffortKeyType struct{}
type AnthropicResponsesStreamState struct {
	InputJSONBuffers  map[int]string
	InputJSONPurposes map[int]anthropicInputJSONBufferKind

	ComputerToolID *string

	WebSearchToolID      *string
	WebSearchOutputIndex *int
	WebSearchResult      *AnthropicContentBlock
	WebSearchQuery       *string
	WebSearchCaller      *AnthropicToolCaller

	WebFetchToolID      *string
	WebFetchOutputIndex *int
	WebFetchURL         *string
	WebFetchResult      *AnthropicContentBlock

	AdvisorToolID      *string
	AdvisorOutputIndex *int
	AdvisorResult      *AnthropicContentBlock

	ToolSearchToolID      *string
	ToolSearchToolName    *string
	ToolSearchOutputIndex *int
	ToolSearchResult      *AnthropicContentBlock
	ToolSearchInput       *string

	CodeExecToolID      *string
	CodeExecToolName    *string
	CodeExecOutputIndex *int
	CodeExecResult      *AnthropicContentBlock
	CodeExecInput       string
	Container           *AnthropicResponseContainer

	ContentIndexToOutputIndex map[int]int
	ContentIndexToBlockType   map[int]AnthropicContentBlockType
	ToolArgumentBuffers       map[int]string
	MCPCallOutputIndices      map[int]bool
	ItemIDs                   map[int]string
	OutputItems               map[int]*schemas.ResponsesMessage
	ReasoningSignatures       map[int]string
	TextContentIndices        map[int]bool
	ReasoningContentIndices   map[int]bool
	TextBuffers               map[int]*strings.Builder
	ReasoningTextBuffers      map[int]*strings.Builder
	CompactionContentIndices  map[int]*schemas.CacheControl
	CurrentOutputIndex        int
	MessageID                 *string
	Model                     *string

	CompactionToolChanges    map[int][]schemas.ResponsesMessageContentBlock
	StopReason               *string
	StopDetails              *schemas.ResponsesStopDetails
	StopSequence             *string
	SafeguardResults         json.RawMessage
	CreatedAt                int
	HasEmittedCreated        bool
	HasEmittedInProgress     bool
	HasEmittedMessageDelta   bool
	StructuredOutputToolName string
	StructuredOutputIndex    *int
	UsedStructuredOutputTool bool
	SeenRealToolCall         bool
}
type anthropicClientToolSearchKeyType struct{}
type emittedTextProgress struct {
	emittedBytes int
	prefixHasher hash.Hash
}
type anthropicInputJSONBufferKind string
type anthropicNativeEffort struct {
	Effort string

	ThinkingOmitted bool
}

var anthropicNativeEffortKey = anthropicNativeEffortKeyType{}

type hostedToolSearchCall struct {
	toolUseID *string
	callID    *string
}

const (
	anthropicInputJSONBufferComputer   anthropicInputJSONBufferKind = "computer"
	anthropicInputJSONBufferWebSearch  anthropicInputJSONBufferKind = "web_search"
	anthropicInputJSONBufferWebFetch   anthropicInputJSONBufferKind = "web_fetch"
	anthropicInputJSONBufferAdvisor    anthropicInputJSONBufferKind = "advisor"
	anthropicInputJSONBufferCodeExec   anthropicInputJSONBufferKind = "code_exec"
	anthropicInputJSONBufferToolSearch anthropicInputJSONBufferKind = "tool_search"
)

const anthropicReasoningSummaryIndex = 0

func (state *AnthropicResponsesStreamState) beginInputJSONBuffer(index *int, kind anthropicInputJSONBufferKind) {
	if index == nil {
		return
	}
	if state.InputJSONBuffers == nil {
		state.InputJSONBuffers = make(map[int]string)
	}
	if state.InputJSONPurposes == nil {
		state.InputJSONPurposes = make(map[int]anthropicInputJSONBufferKind)
	}
	state.InputJSONBuffers[*index] = ""
	state.InputJSONPurposes[*index] = kind
}

func (state *AnthropicResponsesStreamState) appendInputJSON(index *int, partial string) bool {
	if index == nil || state.InputJSONPurposes == nil {
		return false
	}
	if _, ok := state.InputJSONPurposes[*index]; !ok {
		return false
	}
	state.InputJSONBuffers[*index] += partial
	return true
}

func (state *AnthropicResponsesStreamState) finishInputJSONBuffer(index *int) (string, anthropicInputJSONBufferKind, bool) {
	if index == nil || state.InputJSONPurposes == nil {
		return "", "", false
	}
	kind, ok := state.InputJSONPurposes[*index]
	if !ok {
		return "", "", false
	}
	input := state.InputJSONBuffers[*index]
	delete(state.InputJSONBuffers, *index)
	delete(state.InputJSONPurposes, *index)
	return input, kind, true
}

func (s *anthropicToResponsesStreamState) blockType(index int) AnthropicContentBlockType {
	if index < 0 || index >= len(s.blockTypes) {
		return ""
	}
	return s.blockTypes[index]
}

func anthropicClientToolSearchFrom(ctx *schemas.BifrostContext) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(anthropicClientToolSearchKey).(string)
	return name
}

func anthropicNativeEffortFrom(ctx *schemas.BifrostContext) (anthropicNativeEffort, bool) {
	if ctx == nil {
		return anthropicNativeEffort{}, false
	}
	native, ok := ctx.Value(anthropicNativeEffortKey).(anthropicNativeEffort)
	return native, ok
}

const anthropicNativeRequestSurfaceKey = schemas.BifrostContextKeyAnthropicNativeRequestSurface

func preserveNativeThinking(caps schemas.ModelCaps, thinkingType string) bool {
	switch thinkingType {
	case "between_tools":
		return caps.SupportsBetweenToolsThinking(DefaultSupportsBetweenToolsThinking(caps.Model()))
	case "enabled":
		return PreservesCallerRequestSurface(caps.Model())
	}
	return false
}

func neutralReasoningUnchanged(recorded, current *schemas.ResponsesParametersReasoning) bool {
	if current == nil {
		return true
	}
	if recorded == nil {
		return false
	}
	return eqPtr(recorded.Context, current.Context) &&
		eqPtr(recorded.Effort, current.Effort) &&
		eqPtr(recorded.GenerateSummary, current.GenerateSummary) &&
		eqPtr(recorded.Mode, current.Mode) &&
		eqPtr(recorded.Summary, current.Summary) &&
		eqPtr(recorded.Type, current.Type) &&
		eqPtr(recorded.MaxTokens, current.MaxTokens)
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func anthropicNativeRequestSurfaceFrom(ctx *schemas.BifrostContext) (anthropicNativeRequestSurface, bool) {
	if ctx == nil {
		return anthropicNativeRequestSurface{}, false
	}
	surface, ok := ctx.Value(anthropicNativeRequestSurfaceKey).(anthropicNativeRequestSurface)
	return surface, ok
}

func compatDroppedParams(ctx *schemas.BifrostContext) map[string]bool {
	if ctx == nil {
		return nil
	}
	dropped, ok := ctx.Value(schemas.BifrostContextKeyCompatDroppedParams).([]string)
	if !ok || len(dropped) == 0 {
		return nil
	}
	set := make(map[string]bool, len(dropped))
	for _, name := range dropped {
		set[name] = true
	}
	return set
}

func restoreNativeAnthropicRequestSurface(ctx *schemas.BifrostContext, req *AnthropicMessageRequest, params *schemas.ResponsesParameters, provider schemas.ModelProvider, capModel string) {
	if req == nil {
		return
	}
	surface, ok := anthropicNativeRequestSurfaceFrom(ctx)
	if !ok {

		return
	}
	caps := schemas.ResolveModelCaps(provider, capModel)

	neutral := params
	if neutral == nil {
		neutral = &schemas.ResponsesParameters{}
	}

	dropped := compatDroppedParams(ctx)

	if surface.Thinking != nil && preserveNativeThinking(caps, surface.Thinking.Type) &&
		!dropped["reasoning"] &&
		neutralReasoningUnchanged(surface.NeutralReasoning, neutral.Reasoning) {
		restored := *surface.Thinking
		req.Thinking = &restored

		if _, callerSentEffort := anthropicNativeEffortFrom(ctx); !callerSentEffort &&
			req.OutputConfig != nil && req.OutputConfig.Effort != nil {
			req.OutputConfig.Effort = nil
			if req.OutputConfig.Format == nil && req.OutputConfig.TaskBudget == nil {
				req.OutputConfig = nil
			}
		}

		if restored.Type == "enabled" && restored.BudgetTokens != nil &&
			surface.MaxTokens > 0 && req.MaxTokens < surface.MaxTokens {

			var effort *string
			if req.OutputConfig != nil {
				effort = req.OutputConfig.Effort
			}
			if fitted, ok := fitThinkingBudget(restored.BudgetTokens, effort, req.MaxTokens); ok {
				req.Thinking.BudgetTokens = schemas.Ptr(fitted)
			} else {

				req.Thinking = nil
			}
		}
	}

}

func AcquireAnthropicResponsesStreamState() *AnthropicResponsesStreamState {
	state := anthropicResponsesStreamStatePool.Get().(*AnthropicResponsesStreamState)

	if state.ContentIndexToOutputIndex == nil {
		state.ContentIndexToOutputIndex = make(map[int]int)
	} else {
		clear(state.ContentIndexToOutputIndex)
	}
	if state.InputJSONBuffers == nil {
		state.InputJSONBuffers = make(map[int]string)
	} else {
		clear(state.InputJSONBuffers)
	}
	if state.InputJSONPurposes == nil {
		state.InputJSONPurposes = make(map[int]anthropicInputJSONBufferKind)
	} else {
		clear(state.InputJSONPurposes)
	}
	if state.ContentIndexToBlockType == nil {
		state.ContentIndexToBlockType = make(map[int]AnthropicContentBlockType)
	} else {
		clear(state.ContentIndexToBlockType)
	}
	if state.ToolArgumentBuffers == nil {
		state.ToolArgumentBuffers = make(map[int]string)
	} else {
		clear(state.ToolArgumentBuffers)
	}
	if state.MCPCallOutputIndices == nil {
		state.MCPCallOutputIndices = make(map[int]bool)
	} else {
		clear(state.MCPCallOutputIndices)
	}
	if state.ItemIDs == nil {
		state.ItemIDs = make(map[int]string)
	} else {
		clear(state.ItemIDs)
	}
	if state.ReasoningSignatures == nil {
		state.ReasoningSignatures = make(map[int]string)
	} else {
		clear(state.ReasoningSignatures)
	}
	if state.TextContentIndices == nil {
		state.TextContentIndices = make(map[int]bool)
	} else {
		clear(state.TextContentIndices)
	}
	if state.ReasoningContentIndices == nil {
		state.ReasoningContentIndices = make(map[int]bool)
	} else {
		clear(state.ReasoningContentIndices)
	}
	if state.TextBuffers == nil {
		state.TextBuffers = make(map[int]*strings.Builder)
	} else {
		clear(state.TextBuffers)
	}
	if state.ReasoningTextBuffers == nil {
		state.ReasoningTextBuffers = make(map[int]*strings.Builder)
	} else {
		clear(state.ReasoningTextBuffers)
	}
	if state.CompactionContentIndices == nil {
		state.CompactionContentIndices = make(map[int]*schemas.CacheControl)
	} else {
		clear(state.CompactionContentIndices)
	}
	if state.CompactionToolChanges == nil {
		state.CompactionToolChanges = make(map[int][]schemas.ResponsesMessageContentBlock)
	} else {
		clear(state.CompactionToolChanges)
	}
	if state.OutputItems == nil {
		state.OutputItems = make(map[int]*schemas.ResponsesMessage)
	} else {
		clear(state.OutputItems)
	}

	state.ComputerToolID = nil
	state.WebSearchToolID = nil
	state.WebSearchOutputIndex = nil
	state.WebSearchResult = nil
	state.WebSearchQuery = nil
	state.WebSearchCaller = nil
	state.WebFetchToolID = nil
	state.WebFetchOutputIndex = nil
	state.WebFetchURL = nil
	state.WebFetchResult = nil
	state.AdvisorToolID = nil
	state.AdvisorOutputIndex = nil
	state.AdvisorResult = nil
	state.ToolSearchToolID = nil
	state.ToolSearchToolName = nil
	state.ToolSearchOutputIndex = nil
	state.ToolSearchResult = nil
	state.ToolSearchInput = nil
	state.CodeExecToolID = nil
	state.CodeExecToolName = nil
	state.CodeExecOutputIndex = nil
	state.CodeExecResult = nil
	state.CodeExecInput = ""
	state.Container = nil
	state.CurrentOutputIndex = 0
	state.MessageID = nil
	state.StopReason = nil
	state.StopDetails = nil
	state.StopSequence = nil
	state.SafeguardResults = nil
	state.Model = nil
	state.CreatedAt = int(time.Now().Unix())
	state.HasEmittedCreated = false
	state.HasEmittedInProgress = false
	state.HasEmittedMessageDelta = false
	state.StructuredOutputToolName = ""
	state.StructuredOutputIndex = nil
	state.UsedStructuredOutputTool = false
	state.SeenRealToolCall = false
	return state
}

func ReleaseAnthropicResponsesStreamState(state *AnthropicResponsesStreamState) {
	if state != nil {
		state.flush()
		anthropicResponsesStreamStatePool.Put(state)
	}
}

func (state *AnthropicResponsesStreamState) flush() {
	state.InputJSONBuffers = nil
	state.InputJSONPurposes = nil
	state.ComputerToolID = nil
	state.WebSearchToolID = nil
	state.WebSearchOutputIndex = nil
	state.WebSearchResult = nil
	state.WebSearchQuery = nil
	state.WebSearchCaller = nil
	state.WebFetchToolID = nil
	state.WebFetchOutputIndex = nil
	state.WebFetchURL = nil
	state.WebFetchResult = nil
	state.AdvisorToolID = nil
	state.AdvisorOutputIndex = nil
	state.AdvisorResult = nil
	state.ToolSearchToolID = nil
	state.ToolSearchToolName = nil
	state.ToolSearchOutputIndex = nil
	state.ToolSearchResult = nil
	state.ToolSearchInput = nil
	state.CodeExecToolID = nil
	state.CodeExecToolName = nil
	state.CodeExecOutputIndex = nil
	state.CodeExecResult = nil
	state.CodeExecInput = ""
	state.Container = nil
	state.ContentIndexToOutputIndex = nil
	state.ContentIndexToBlockType = nil
	state.ToolArgumentBuffers = nil
	state.MCPCallOutputIndices = nil
	state.ItemIDs = nil
	state.ReasoningSignatures = nil
	state.TextContentIndices = nil
	state.ReasoningContentIndices = nil
	state.TextBuffers = nil
	state.ReasoningTextBuffers = nil
	state.CompactionContentIndices = nil
	state.CompactionToolChanges = nil
	state.OutputItems = nil
	state.CurrentOutputIndex = 0
	state.MessageID = nil
	state.StopReason = nil
	state.StopDetails = nil
	state.StopSequence = nil
	state.SafeguardResults = nil
	state.Model = nil
	state.CreatedAt = int(time.Now().Unix())
	state.HasEmittedCreated = false
	state.HasEmittedInProgress = false
	state.HasEmittedMessageDelta = false
	state.StructuredOutputToolName = ""
	state.StructuredOutputIndex = nil
	state.UsedStructuredOutputTool = false
	state.SeenRealToolCall = false
}

func (state *AnthropicResponsesStreamState) getOrCreateOutputIndex(contentIndex *int) int {
	if contentIndex == nil {

		outputIndex := state.CurrentOutputIndex
		state.CurrentOutputIndex++
		return outputIndex
	}

	if outputIndex, exists := state.ContentIndexToOutputIndex[*contentIndex]; exists {
		return outputIndex
	}

	outputIndex := state.CurrentOutputIndex
	state.CurrentOutputIndex++
	state.ContentIndexToOutputIndex[*contentIndex] = outputIndex
	return outputIndex
}

func (chunk *AnthropicStreamEvent) ToBifrostResponsesStream(ctx context.Context, sequenceNumber int, state *AnthropicResponsesStreamState) ([]*schemas.BifrostResponsesStreamResponse, *schemas.BifrostError, bool) {
	switch chunk.Type {
	case AnthropicStreamEventTypeSafeguardsUpdate:
		return []*schemas.BifrostResponsesStreamResponse{{
			Type:             schemas.ResponsesStreamResponseTypeSafeguardsUpdate,
			SequenceNumber:   sequenceNumber,
			SafeguardResults: chunk.SafeguardResults,
		}}, nil, false
	case AnthropicStreamEventTypeMessageStart:

		if chunk.Message != nil {
			state.MessageID = &chunk.Message.ID
			state.Model = &chunk.Message.Model

			if state.CreatedAt == 0 {
				state.CreatedAt = int(time.Now().Unix())
			}

			var responses []*schemas.BifrostResponsesStreamResponse

			if !state.HasEmittedCreated {
				response := &schemas.BifrostResponsesResponse{
					ID:        state.MessageID,
					CreatedAt: state.CreatedAt,
				}
				if state.Model != nil {
					response.Model = *state.Model
				}

				if chunk.Message.Diagnostics != nil {
					response.Diagnostics = chunk.Message.Diagnostics
				}

				if len(chunk.Message.SafeguardResults) > 0 {
					response.SafeguardResults = chunk.Message.SafeguardResults
				}

				if chunk.Message.Usage != nil {
					response.Usage = &schemas.ResponsesResponseUsage{
						InputTokens:  chunk.Message.Usage.InputTokens,
						OutputTokens: chunk.Message.Usage.OutputTokens,
						TotalTokens:  chunk.Message.Usage.InputTokens + chunk.Message.Usage.OutputTokens,
					}
					if chunk.Message.Usage.CacheReadInputTokens > 0 || chunk.Message.Usage.CacheCreationInputTokens > 0 {
						inputTokensDetails := &schemas.ResponsesResponseInputTokens{
							CachedReadTokens:  chunk.Message.Usage.CacheReadInputTokens,
							CachedWriteTokens: chunk.Message.Usage.CacheCreationInputTokens,
						}
						if chunk.Message.Usage.CacheCreation.Ephemeral5mInputTokens > 0 || chunk.Message.Usage.CacheCreation.Ephemeral1hInputTokens > 0 {
							inputTokensDetails.CachedWriteTokenDetails = &schemas.ChatCachedWriteTokenDetails{
								CachedWriteTokens5m: chunk.Message.Usage.CacheCreation.Ephemeral5mInputTokens,
								CachedWriteTokens1h: chunk.Message.Usage.CacheCreation.Ephemeral1hInputTokens,
							}
						}
						response.Usage.InputTokensDetails = inputTokensDetails

						response.Usage.InputTokens += chunk.Message.Usage.CacheReadInputTokens + chunk.Message.Usage.CacheCreationInputTokens
						response.Usage.TotalTokens += chunk.Message.Usage.CacheReadInputTokens + chunk.Message.Usage.CacheCreationInputTokens
					}
				}
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeCreated,
					SequenceNumber: sequenceNumber,
					Response:       response,
				})
				state.HasEmittedCreated = true
			}

			if !state.HasEmittedInProgress {
				response := &schemas.BifrostResponsesResponse{
					ID:        state.MessageID,
					CreatedAt: state.CreatedAt,
				}
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeInProgress,
					SequenceNumber: sequenceNumber + len(responses),
					Response:       response,
				})
				state.HasEmittedInProgress = true
			}

			if len(responses) > 0 {
				return responses, nil, false
			}
		}

	case AnthropicStreamEventTypeContentBlockStart:

		if chunk.ContentBlock != nil && chunk.Index != nil {

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeRedactedThinking &&
				(chunk.ContentBlock.Data == nil || *chunk.ContentBlock.Data == "") {
				state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeRedactedThinking
				return nil, nil, false
			}

			outputIndex := state.getOrCreateOutputIndex(chunk.Index)

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeToolUse &&
				chunk.ContentBlock.Name != nil &&
				*chunk.ContentBlock.Name == string(AnthropicToolNameComputer) &&
				chunk.ContentBlock.ID != nil {

				state.SeenRealToolCall = true

				state.ComputerToolID = chunk.ContentBlock.ID
				state.beginInputJSONBuffer(chunk.Index, anthropicInputJSONBufferComputer)

				item := &schemas.ResponsesMessage{
					ID:   chunk.ContentBlock.ID,
					Type: schemas.Ptr(schemas.ResponsesMessageTypeComputerCall),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID: chunk.ContentBlock.ID,
					},
				}

				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeServerToolUse &&
				chunk.ContentBlock.Name != nil &&
				*chunk.ContentBlock.Name == string(AnthropicToolNameWebSearch) &&
				chunk.ContentBlock.ID != nil {

				state.SeenRealToolCall = true

				state.beginInputJSONBuffer(chunk.Index, anthropicInputJSONBufferWebSearch)
				state.WebSearchToolID = chunk.ContentBlock.ID
				state.WebSearchOutputIndex = schemas.Ptr(outputIndex)
				state.WebSearchQuery = nil
				if q := providerUtils.GetJSONField(chunk.ContentBlock.Input, "query"); q.Exists() && q.Type == gjson.String {
					state.WebSearchQuery = schemas.Ptr(q.Str)
				}
				state.WebSearchCaller = chunk.ContentBlock.Caller

				state.ItemIDs[outputIndex] = *chunk.ContentBlock.ID

				wsAction := &schemas.ResponsesWebSearchToolCallAction{Type: "search"}
				if state.WebSearchQuery != nil {
					wsAction.Query = state.WebSearchQuery
					wsAction.Queries = []string{*state.WebSearchQuery}
				}
				toolMsg := &schemas.ResponsesToolMessage{
					CallID: chunk.ContentBlock.ID,
					Action: &schemas.ResponsesToolMessageActionStruct{ResponsesWebSearchToolCallAction: wsAction},
				}
				if state.WebSearchCaller != nil {
					toolMsg.Caller = &schemas.ResponsesToolCaller{
						Type:   string(state.WebSearchCaller.Type),
						ToolID: state.WebSearchCaller.ToolID,
					}
				}

				item := &schemas.ResponsesMessage{
					ID:                   chunk.ContentBlock.ID,
					Type:                 schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
					Status:               schemas.Ptr("in_progress"),
					ResponsesToolMessage: toolMsg,
				}

				var responses []*schemas.BifrostResponsesStreamResponse

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				})

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeWebSearchCallInProgress,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ItemID:         chunk.ContentBlock.ID,
				})

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeWebSearchCallSearching,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ItemID:         chunk.ContentBlock.ID,
				})

				return responses, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeWebSearchToolResult &&
				chunk.ContentBlock.ToolUseID != nil {

				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeWebSearchToolResult
				}

				if state.WebSearchToolID != nil && *state.WebSearchToolID == *chunk.ContentBlock.ToolUseID {

					state.WebSearchResult = chunk.ContentBlock

					if chunk.Index != nil {
						delete(state.ContentIndexToBlockType, *chunk.Index)
					}

					return []*schemas.BifrostResponsesStreamResponse{{
						Type:           schemas.ResponsesStreamResponseTypeWebSearchCallCompleted,
						SequenceNumber: sequenceNumber,
						OutputIndex:    state.WebSearchOutputIndex,
						ItemID:         chunk.ContentBlock.ToolUseID,
					}}, nil, false
				}

				return nil, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeServerToolUse &&
				chunk.ContentBlock.Name != nil &&
				isAnthropicToolSearchToolName(*chunk.ContentBlock.Name) &&
				chunk.ContentBlock.ID != nil {

				state.SeenRealToolCall = true

				state.beginInputJSONBuffer(chunk.Index, anthropicInputJSONBufferToolSearch)
				state.ToolSearchToolID = chunk.ContentBlock.ID
				state.ToolSearchToolName = chunk.ContentBlock.Name
				state.ToolSearchOutputIndex = schemas.Ptr(outputIndex)
				state.ItemIDs[outputIndex] = *chunk.ContentBlock.ID

				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeServerToolUse
					state.TextContentIndices[*chunk.Index] = false
				}

				item := &schemas.ResponsesMessage{
					ID:     chunk.ContentBlock.ID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeToolSearchCall),
					Status: schemas.Ptr("in_progress"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID: chunk.ContentBlock.ID,
						Name:   chunk.ContentBlock.Name,
					},
				}
				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeToolSearchToolResult &&
				chunk.ContentBlock.ToolUseID != nil {

				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeToolSearchToolResult
				}

				if state.ToolSearchToolID != nil && *state.ToolSearchToolID == *chunk.ContentBlock.ToolUseID {

					state.ToolSearchResult = chunk.ContentBlock
				}

				return nil, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeServerToolUse &&
				chunk.ContentBlock.Name != nil &&
				*chunk.ContentBlock.Name == string(AnthropicToolNameWebFetch) &&
				chunk.ContentBlock.ID != nil {

				state.SeenRealToolCall = true
				state.beginInputJSONBuffer(chunk.Index, anthropicInputJSONBufferWebFetch)
				state.WebFetchToolID = chunk.ContentBlock.ID
				state.WebFetchOutputIndex = schemas.Ptr(outputIndex)
				state.WebFetchURL = nil
				if u := providerUtils.GetJSONField(chunk.ContentBlock.Input, "url"); u.Exists() && u.Type == gjson.String {
					state.WebFetchURL = schemas.Ptr(u.Str)
				}

				state.ItemIDs[outputIndex] = *chunk.ContentBlock.ID

				toolMsg := &schemas.ResponsesToolMessage{
					CallID: chunk.ContentBlock.ID,
				}
				if state.WebFetchURL != nil {
					toolMsg.Action = &schemas.ResponsesToolMessageActionStruct{
						ResponsesWebFetchToolCallAction: &schemas.ResponsesWebFetchToolCallAction{
							Type: "fetch",
							URL:  *state.WebFetchURL,
						},
					}
				}

				if chunk.ContentBlock.Caller != nil {
					toolMsg.Caller = &schemas.ResponsesToolCaller{
						Type:   string(chunk.ContentBlock.Caller.Type),
						ToolID: chunk.ContentBlock.Caller.ToolID,
					}
				}
				item := &schemas.ResponsesMessage{
					ID:                   chunk.ContentBlock.ID,
					Type:                 schemas.Ptr(schemas.ResponsesMessageTypeWebFetchCall),
					Status:               schemas.Ptr("in_progress"),
					ResponsesToolMessage: toolMsg,
				}

				var responses []*schemas.BifrostResponsesStreamResponse

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				})

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeWebFetchCallInProgress,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ItemID:         chunk.ContentBlock.ID,
				})

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeWebFetchCallFetching,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ItemID:         chunk.ContentBlock.ID,
				})

				return responses, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeWebFetchToolResult &&
				chunk.ContentBlock.ToolUseID != nil {

				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeWebFetchToolResult
				}

				if state.WebFetchToolID != nil && *state.WebFetchToolID == *chunk.ContentBlock.ToolUseID {
					if chunk.Index != nil {
						delete(state.ContentIndexToBlockType, *chunk.Index)
					}

					state.WebFetchResult = chunk.ContentBlock

					return []*schemas.BifrostResponsesStreamResponse{{
						Type:           schemas.ResponsesStreamResponseTypeWebFetchCallCompleted,
						SequenceNumber: sequenceNumber,
						OutputIndex:    state.WebFetchOutputIndex,
						ItemID:         chunk.ContentBlock.ToolUseID,
					}}, nil, false
				}

				return nil, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeServerToolUse &&
				chunk.ContentBlock.Name != nil &&
				*chunk.ContentBlock.Name == string(AnthropicToolNameAdvisor) &&
				chunk.ContentBlock.ID != nil {

				state.SeenRealToolCall = true
				state.beginInputJSONBuffer(chunk.Index, anthropicInputJSONBufferAdvisor)
				state.AdvisorToolID = chunk.ContentBlock.ID
				state.AdvisorOutputIndex = schemas.Ptr(outputIndex)
				state.ItemIDs[outputIndex] = *chunk.ContentBlock.ID

				item := &schemas.ResponsesMessage{
					ID:     chunk.ContentBlock.ID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeAdvisorCall),
					Status: schemas.Ptr("in_progress"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID: chunk.ContentBlock.ID,
						Name:   schemas.Ptr(string(AnthropicToolNameAdvisor)),
					},
				}

				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeAdvisorToolResult &&
				chunk.ContentBlock.ToolUseID != nil {

				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeAdvisorToolResult
				}
				if state.AdvisorToolID != nil && *state.AdvisorToolID == *chunk.ContentBlock.ToolUseID {
					state.AdvisorResult = chunk.ContentBlock
				}
				return nil, nil, false
			}

			if chunk.ContentBlock.Type == AnthropicContentBlockTypeServerToolUse &&
				chunk.ContentBlock.Name != nil &&
				isAnthropicCodeExecutionToolName(*chunk.ContentBlock.Name) &&
				chunk.ContentBlock.ID != nil {

				state.SeenRealToolCall = true
				state.beginInputJSONBuffer(chunk.Index, anthropicInputJSONBufferCodeExec)
				state.CodeExecToolID = chunk.ContentBlock.ID
				state.CodeExecToolName = chunk.ContentBlock.Name
				state.CodeExecOutputIndex = schemas.Ptr(outputIndex)
				state.ItemIDs[outputIndex] = *chunk.ContentBlock.ID

				item := &schemas.ResponsesMessage{
					ID:     chunk.ContentBlock.ID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeCodeInterpreterCall),
					Status: schemas.Ptr("in_progress"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:                           chunk.ContentBlock.ID,
						ResponsesCodeInterpreterToolCall: &schemas.ResponsesCodeInterpreterToolCall{},

						ResponsesCodeExecutionCall: &schemas.ResponsesCodeExecutionCall{ToolName: *chunk.ContentBlock.Name},
					},
				}

				return []*schemas.BifrostResponsesStreamResponse{
					{
						Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
						SequenceNumber: sequenceNumber,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Item:           item,
					},
					{
						Type:           schemas.ResponsesStreamResponseTypeCodeInterpreterCallInProgress,
						SequenceNumber: sequenceNumber + 1,
						OutputIndex:    schemas.Ptr(outputIndex),
						ItemID:         chunk.ContentBlock.ID,
					},
				}, nil, false
			}

			if (chunk.ContentBlock.Type == AnthropicContentBlockTypeCodeExecutionToolResult ||
				chunk.ContentBlock.Type == AnthropicContentBlockTypeBashCodeExecutionToolResult ||
				chunk.ContentBlock.Type == AnthropicContentBlockTypeTextEditorCodeExecutionToolResult) &&
				chunk.ContentBlock.ToolUseID != nil {

				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = chunk.ContentBlock.Type
				}
				if state.CodeExecToolID != nil && *state.CodeExecToolID == *chunk.ContentBlock.ToolUseID {
					state.CodeExecResult = chunk.ContentBlock
				}
				return nil, nil, false
			}

			switch chunk.ContentBlock.Type {
			case AnthropicContentBlockTypeCompaction:

				itemID := fmt.Sprintf("cmp_%d", outputIndex)
				state.ItemIDs[outputIndex] = itemID

				state.CompactionContentIndices[outputIndex] = chunk.ContentBlock.CacheControl
				if changes := convertAnthropicToolChangesToBifrost(chunk.ContentBlock.ToolChanges); len(changes) > 0 {
					if state.CompactionToolChanges == nil {
						state.CompactionToolChanges = make(map[int][]schemas.ResponsesMessageContentBlock)
					}
					state.CompactionToolChanges[outputIndex] = changes
				}

				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeCompaction
				}

				return nil, nil, false
			case AnthropicContentBlockTypeFallback:

				itemID := fmt.Sprintf("fb_%d", outputIndex)
				state.ItemIDs[outputIndex] = itemID
				if chunk.Index != nil {
					state.ContentIndexToBlockType[*chunk.Index] = AnthropicContentBlockTypeFallback
				}
				messageType := schemas.ResponsesMessageTypeMessage
				fbRole := schemas.ResponsesInputMessageRoleAssistant
				fallback := &schemas.ResponsesOutputMessageContentFallback{}
				if chunk.ContentBlock.From != nil {
					fallback.FromModel = chunk.ContentBlock.From.Model
				}
				if chunk.ContentBlock.To != nil {
					fallback.ToModel = chunk.ContentBlock.To.Model
				}
				if chunk.ContentBlock.Trigger != nil {
					fallback.TriggerType = chunk.ContentBlock.Trigger.Type
					fallback.TriggerCategory = chunk.ContentBlock.Trigger.Category
				}
				item := &schemas.ResponsesMessage{
					ID:     schemas.Ptr(itemID),
					Status: schemas.Ptr("completed"),
					Type:   &messageType,
					Role:   &fbRole,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{{
							Type:                                  schemas.ResponsesOutputMessageContentTypeFallback,
							ResponsesOutputMessageContentFallback: fallback,
						}},
					},
				}
				return []*schemas.BifrostResponsesStreamResponse{
					{
						Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
						SequenceNumber: sequenceNumber,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Item:           item,
					},
					{
						Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
						SequenceNumber: sequenceNumber + 1,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Item:           item,
					},
				}, nil, false
			case AnthropicContentBlockTypeText:

				messageType := schemas.ResponsesMessageTypeMessage
				role := schemas.ResponsesInputMessageRoleAssistant

				var itemID string
				if state.MessageID == nil {
					itemID = fmt.Sprintf("item_%d", outputIndex)
				} else {
					itemID = fmt.Sprintf("msg_%s_item_%d", *state.MessageID, outputIndex)
				}
				state.ItemIDs[outputIndex] = itemID

				item := &schemas.ResponsesMessage{
					ID:     schemas.Ptr(itemID),
					Status: schemas.Ptr("in_progress"),
					Type:   &messageType,
					Role:   &role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{},
					},
				}

				if chunk.Index != nil {
					state.TextContentIndices[*chunk.Index] = true
				}

				var responses []*schemas.BifrostResponsesStreamResponse

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				})

				emptyText := ""
				part := &schemas.ResponsesMessageContentBlock{
					Type: schemas.ResponsesOutputMessageContentTypeText,
					Text: &emptyText,
					ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
						LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
						Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
					},
				}
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeContentPartAdded,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					ItemID:         &itemID,
					Part:           part,
				})

				return responses, nil, false
			case AnthropicContentBlockTypeToolUse:

				if state.StructuredOutputToolName != "" && chunk.ContentBlock.Name != nil && *chunk.ContentBlock.Name == state.StructuredOutputToolName {

					state.StructuredOutputIndex = &outputIndex

					state.ToolArgumentBuffers[outputIndex] = ""

					if chunk.Index != nil {
						state.TextContentIndices[*chunk.Index] = false
					}

					if chunk.ContentBlock.ID != nil {
						state.ItemIDs[outputIndex] = *chunk.ContentBlock.ID
					}

					return nil, nil, false
				}

				state.SeenRealToolCall = true

				statusInProgress := "in_progress"
				itemID := ""
				if chunk.ContentBlock.ID != nil {
					itemID = *chunk.ContentBlock.ID
					state.ItemIDs[outputIndex] = itemID
				}
				item := &schemas.ResponsesMessage{
					ID:     chunk.ContentBlock.ID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
					Status: &statusInProgress,
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:      chunk.ContentBlock.ID,
						Name:        chunk.ContentBlock.Name,
						ToolsetName: chunk.ContentBlock.ToolsetName,
						Arguments:   schemas.Ptr(""),
					},
				}

				state.ToolArgumentBuffers[outputIndex] = ""

				clonedItem := *item
				clonedToolMsg := *item.ResponsesToolMessage
				clonedItem.ResponsesToolMessage = &clonedToolMsg
				state.OutputItems[outputIndex] = &clonedItem

				if chunk.Index != nil {
					state.TextContentIndices[*chunk.Index] = false
				}

				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			case AnthropicContentBlockTypeMCPToolUse:
				state.SeenRealToolCall = true

				itemID := ""
				if chunk.ContentBlock.ID != nil {
					itemID = *chunk.ContentBlock.ID
					state.ItemIDs[outputIndex] = itemID
				}
				item := &schemas.ResponsesMessage{
					ID:   chunk.ContentBlock.ID,
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMCPCall),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						Name:      chunk.ContentBlock.Name,
						Arguments: schemas.Ptr(""),
					},
				}

				if chunk.ContentBlock.ServerName != nil {
					item.ResponsesToolMessage.ServerLabel = schemas.Ptr(*chunk.ContentBlock.ServerName)
				}

				state.ToolArgumentBuffers[outputIndex] = ""
				state.MCPCallOutputIndices[outputIndex] = true

				if chunk.Index != nil {
					state.TextContentIndices[*chunk.Index] = false
				}

				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			case AnthropicContentBlockTypeThinking:

				messageType := schemas.ResponsesMessageTypeReasoning
				role := schemas.ResponsesInputMessageRoleAssistant

				var itemID string
				if state.MessageID == nil {
					itemID = fmt.Sprintf("reasoning_%d", outputIndex)
				} else {
					itemID = fmt.Sprintf("msg_%s_reasoning_%d", *state.MessageID, outputIndex)
				}
				state.ItemIDs[outputIndex] = itemID

				item := &schemas.ResponsesMessage{
					ID:   &itemID,
					Type: &messageType,
					Role: &role,
					ResponsesReasoning: &schemas.ResponsesReasoning{
						Summary: []schemas.ResponsesReasoningSummary{},
					},
				}

				if chunk.Index != nil {
					state.ReasoningContentIndices[*chunk.Index] = true
				}

				itemCopy := *item
				state.OutputItems[outputIndex] = &itemCopy

				var responses []*schemas.BifrostResponsesStreamResponse

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				})

				emptyText := ""
				part := &schemas.ResponsesMessageContentBlock{
					Type: schemas.ResponsesOutputMessageContentTypeReasoning,
					Text: &emptyText,
				}

				if chunk.ContentBlock.Signature != nil {
					part.Signature = chunk.ContentBlock.Signature
				}
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeContentPartAdded,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					ItemID:         &itemID,
					Part:           part,
				})

				return responses, nil, false
			case AnthropicContentBlockTypeRedactedThinking:

				messageType := schemas.ResponsesMessageTypeReasoning
				role := schemas.ResponsesInputMessageRoleAssistant

				var itemID string
				if state.MessageID == nil {
					itemID = fmt.Sprintf("reasoning_%d", outputIndex)
				} else {
					itemID = fmt.Sprintf("msg_%s_reasoning_%d", *state.MessageID, outputIndex)
				}
				state.ItemIDs[outputIndex] = itemID

				item := &schemas.ResponsesMessage{
					ID:   &itemID,
					Type: &messageType,
					Role: &role,
					ResponsesReasoning: &schemas.ResponsesReasoning{
						Summary:          []schemas.ResponsesReasoningSummary{},
						EncryptedContent: chunk.ContentBlock.Data,
					},
				}

				itemCopy := *item
				state.OutputItems[outputIndex] = &itemCopy

				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber,
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			default:

				if ctx.Value(schemas.BifrostContextKeyIntegrationType) == "anthropic" {
					return []*schemas.BifrostResponsesStreamResponse{{
						Type:           "",
						SequenceNumber: sequenceNumber,
					}}, nil, false
				}
				return nil, nil, false
			}
		}

	case AnthropicStreamEventTypeContentBlockDelta:
		if chunk.Index != nil && chunk.Delta != nil {
			outputIndex := state.getOrCreateOutputIndex(chunk.Index)

			switch chunk.Delta.Type {
			case AnthropicStreamDeltaTypeCompaction:
				if chunk.Delta.Content != nil {

					itemID := state.ItemIDs[outputIndex]
					messageType := schemas.ResponsesMessageTypeMessage
					role := schemas.ResponsesInputMessageRoleAssistant

					cacheControl := state.CompactionContentIndices[outputIndex]

					item := &schemas.ResponsesMessage{
						ID:     &itemID,
						Status: schemas.Ptr("completed"),
						Type:   &messageType,
						Role:   &role,
						Content: &schemas.ResponsesMessageContent{
							ContentBlocks: []schemas.ResponsesMessageContentBlock{
								{
									Type: schemas.ResponsesOutputMessageContentTypeCompaction,
									ResponsesOutputMessageContentCompaction: &schemas.ResponsesOutputMessageContentCompaction{
										Summary:     *chunk.Delta.Content,
										ToolChanges: state.CompactionToolChanges[outputIndex],
									},
									CacheControl: cacheControl,
								},
							},
						},
					}

					return []*schemas.BifrostResponsesStreamResponse{
						{
							Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
							SequenceNumber: sequenceNumber,
							OutputIndex:    schemas.Ptr(outputIndex),
							ContentIndex:   chunk.Index,
							Item:           item,
						},
						{
							Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
							SequenceNumber: sequenceNumber + 1,
							OutputIndex:    schemas.Ptr(outputIndex),
							ItemID:         schemas.Ptr(itemID),
							Item:           item,
						},
					}, nil, false
				}
			case AnthropicStreamDeltaTypeText:
				if chunk.Delta.Text != nil && *chunk.Delta.Text != "" {

					if state.TextBuffers[outputIndex] == nil {
						state.TextBuffers[outputIndex] = &strings.Builder{}
					}
					state.TextBuffers[outputIndex].WriteString(*chunk.Delta.Text)

					itemID := state.ItemIDs[outputIndex]
					response := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeOutputTextDelta,
						SequenceNumber: sequenceNumber,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Delta:          chunk.Delta.Text,
					}
					if itemID != "" {
						response.ItemID = &itemID
					}
					return []*schemas.BifrostResponsesStreamResponse{response}, nil, false
				}

			case AnthropicStreamDeltaTypeInputJSON:

				if chunk.Delta.PartialJSON != nil {
					if state.appendInputJSON(chunk.Index, *chunk.Delta.PartialJSON) {
						return nil, nil, false
					}

					if _, exists := state.ToolArgumentBuffers[outputIndex]; !exists {
						state.ToolArgumentBuffers[outputIndex] = ""
					}
					state.ToolArgumentBuffers[outputIndex] += *chunk.Delta.PartialJSON

					if state.StructuredOutputIndex != nil && *state.StructuredOutputIndex == outputIndex {

						return nil, nil, false
					}

					var deltaType schemas.ResponsesStreamResponseType
					if state.MCPCallOutputIndices[outputIndex] {
						deltaType = schemas.ResponsesStreamResponseTypeMCPCallArgumentsDelta
					} else {
						deltaType = schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta
					}

					itemID := state.ItemIDs[outputIndex]
					response := &schemas.BifrostResponsesStreamResponse{
						Type:           deltaType,
						SequenceNumber: sequenceNumber,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Delta:          chunk.Delta.PartialJSON,
					}
					if itemID != "" {
						response.ItemID = &itemID
					}
					return []*schemas.BifrostResponsesStreamResponse{response}, nil, false
				}

			case AnthropicStreamDeltaTypeThinking:

				if chunk.Delta.Thinking != nil && *chunk.Delta.Thinking != "" {

					if state.ReasoningTextBuffers == nil {
						state.ReasoningTextBuffers = make(map[int]*strings.Builder)
					}
					if state.ReasoningTextBuffers[outputIndex] == nil {
						state.ReasoningTextBuffers[outputIndex] = &strings.Builder{}
					}
					state.ReasoningTextBuffers[outputIndex].WriteString(*chunk.Delta.Thinking)

					itemID := state.ItemIDs[outputIndex]
					response := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeReasoningSummaryTextDelta,
						SequenceNumber: sequenceNumber,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						SummaryIndex:   schemas.Ptr(anthropicReasoningSummaryIndex),
						Delta:          chunk.Delta.Thinking,
					}
					if itemID != "" {
						response.ItemID = &itemID
					}
					return []*schemas.BifrostResponsesStreamResponse{response}, nil, false
				}

			case AnthropicStreamDeltaTypeSignature:

				if chunk.Delta.Signature != nil && *chunk.Delta.Signature != "" {
					state.ReasoningSignatures[outputIndex] = *chunk.Delta.Signature

					itemID := state.ItemIDs[outputIndex]
					response := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeReasoningSummaryTextDelta,
						SequenceNumber: sequenceNumber,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						SummaryIndex:   schemas.Ptr(anthropicReasoningSummaryIndex),
						Signature:      chunk.Delta.Signature,
					}
					if itemID != "" {
						response.ItemID = &itemID
					}
					return []*schemas.BifrostResponsesStreamResponse{response}, nil, false
				}
				return nil, nil, false

			case AnthropicStreamDeltaTypeCitations:

				if chunk.Delta.Citation != nil {

					annotation := convertAnthropicCitationToAnnotation(*chunk.Delta.Citation, "")

					itemID := state.ItemIDs[outputIndex]
					response := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeOutputTextAnnotationAdded,
						SequenceNumber: sequenceNumber,
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Annotation:     &annotation,
					}
					if itemID != "" {
						response.ItemID = &itemID
					}
					return []*schemas.BifrostResponsesStreamResponse{response}, nil, false
				}
				return nil, nil, false
			}
		}

	case AnthropicStreamEventTypeContentBlockStop:

		if chunk.Index != nil {

			if blockType, exists := state.ContentIndexToBlockType[*chunk.Index]; exists &&
				blockType == AnthropicContentBlockTypeRedactedThinking {
				delete(state.ContentIndexToBlockType, *chunk.Index)
				return nil, nil, false
			}

			outputIndex := state.getOrCreateOutputIndex(chunk.Index)

			if inputJSON, inputKind, hasInputBuffer := state.finishInputJSONBuffer(chunk.Index); hasInputBuffer {
				switch inputKind {
				case anthropicInputJSONBufferComputer:
					if state.ComputerToolID == nil {
						return nil, nil, false
					}

					var inputMap map[string]interface{}
					var action *schemas.ResponsesComputerToolCallAction

					if inputJSON != "" {
						if err := sonic.Unmarshal([]byte(inputJSON), &inputMap); err == nil {
							action = convertAnthropicToResponsesComputerAction(inputMap)
						}
					}

					statusCompleted := "completed"
					item := &schemas.ResponsesMessage{
						ID:     state.ComputerToolID,
						Type:   schemas.Ptr(schemas.ResponsesMessageTypeComputerCall),
						Status: &statusCompleted,
						ResponsesToolMessage: &schemas.ResponsesToolMessage{
							CallID: state.ComputerToolID,
							ResponsesComputerToolCall: &schemas.ResponsesComputerToolCall{
								PendingSafetyChecks: []schemas.ResponsesComputerToolCallPendingSafetyCheck{},
							},
						},
					}

					if action != nil {
						setResponsesComputerCallAction(item.ResponsesToolMessage, action)
					}

					state.ComputerToolID = nil

					return []*schemas.BifrostResponsesStreamResponse{
						{
							Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
							SequenceNumber: sequenceNumber,
							OutputIndex:    schemas.Ptr(outputIndex),
							ContentIndex:   chunk.Index,
							Item:           item,
						},
					}, nil, false

				case anthropicInputJSONBufferWebSearch:
					if state.WebSearchToolID == nil {
						return nil, nil, false
					}
					if state.WebSearchQuery == nil && inputJSON != "" {
						if q := providerUtils.GetJSONField([]byte(inputJSON), "query"); q.Exists() && q.Type == gjson.String {
							state.WebSearchQuery = schemas.Ptr(q.Str)
						}
					}
					return nil, nil, false

				case anthropicInputJSONBufferWebFetch:

					if state.WebFetchURL == nil && inputJSON != "" {
						if u := providerUtils.GetJSONField([]byte(inputJSON), "url"); u.Exists() && u.Type == gjson.String {
							state.WebFetchURL = schemas.Ptr(u.Str)
						}
					}
					return nil, nil, false

				case anthropicInputJSONBufferAdvisor:
					return nil, nil, false

				case anthropicInputJSONBufferCodeExec:
					if state.CodeExecToolID == nil {
						return nil, nil, false
					}

					state.CodeExecInput = inputJSON
					code := ""
					if inputJSON != "" {
						key := "code"
						if state.CodeExecToolName != nil &&
							AnthropicToolName(*state.CodeExecToolName) == AnthropicToolNameBashCodeExecution {
							key = "command"
						}
						if c := providerUtils.GetJSONField([]byte(inputJSON), key); c.Exists() && c.Type == gjson.String {
							code = c.Str
						}
					}
					outIdx := state.CodeExecOutputIndex
					itemID := state.CodeExecToolID

					var responses []*schemas.BifrostResponsesStreamResponse
					if code != "" {
						responses = append(responses, &schemas.BifrostResponsesStreamResponse{
							Type:           schemas.ResponsesStreamResponseTypeCodeInterpreterCallCodeDelta,
							SequenceNumber: sequenceNumber + len(responses),
							OutputIndex:    outIdx,
							ItemID:         itemID,
							Delta:          schemas.Ptr(code),
						})
					}

					codeDoneIdx := sequenceNumber + len(responses)
					responses = append(responses,
						&schemas.BifrostResponsesStreamResponse{
							Type:           schemas.ResponsesStreamResponseTypeCodeInterpreterCallCodeDone,
							SequenceNumber: codeDoneIdx,
							OutputIndex:    outIdx,
							ItemID:         itemID,
							Code:           schemas.Ptr(code),
						},
						&schemas.BifrostResponsesStreamResponse{
							Type:           schemas.ResponsesStreamResponseTypeCodeInterpreterCallInterpreting,
							SequenceNumber: codeDoneIdx + 1,
							OutputIndex:    outIdx,
							ItemID:         itemID,
						},
					)
					return responses, nil, false

				case anthropicInputJSONBufferToolSearch:

					if inputJSON != "" {
						state.ToolSearchInput = schemas.Ptr(inputJSON)
					}
					return nil, nil, false
				}
			}

			if state.WebSearchResult != nil && state.WebSearchToolID != nil {

				var query string
				var queries []string
				if state.WebSearchQuery != nil {
					query = *state.WebSearchQuery
					queries = []string{query}
				}

				var sources []schemas.ResponsesWebSearchToolCallActionSearchSource
				if state.WebSearchResult.Content != nil && len(state.WebSearchResult.Content.ContentBlocks) > 0 {
					for _, resultBlock := range state.WebSearchResult.Content.ContentBlocks {
						if resultBlock.Type == AnthropicContentBlockTypeWebSearchResult && resultBlock.URL != nil {
							sources = append(sources, schemas.ResponsesWebSearchToolCallActionSearchSource{
								Type:             "url",
								URL:              *resultBlock.URL,
								Title:            resultBlock.Title,
								EncryptedContent: resultBlock.EncryptedContent,
								PageAge:          resultBlock.PageAge,
							})
						}
					}
				}

				statusCompleted := "completed"
				action := &schemas.ResponsesWebSearchToolCallAction{
					Type:    "search",
					Sources: sources,
				}

				if query != "" {
					action.Query = &query
					action.Queries = queries
				}

				toolMsg := &schemas.ResponsesToolMessage{
					CallID: state.WebSearchToolID,
					Action: &schemas.ResponsesToolMessageActionStruct{ResponsesWebSearchToolCallAction: action},
				}
				if state.WebSearchCaller != nil {
					toolMsg.Caller = &schemas.ResponsesToolCaller{
						Type:   string(state.WebSearchCaller.Type),
						ToolID: state.WebSearchCaller.ToolID,
					}
				}
				item := &schemas.ResponsesMessage{
					ID:                   state.WebSearchToolID,
					Type:                 schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
					Status:               &statusCompleted,
					ResponsesToolMessage: toolMsg,
				}

				outputIdx := state.WebSearchOutputIndex

				state.WebSearchToolID = nil
				state.WebSearchOutputIndex = nil
				state.WebSearchResult = nil
				state.WebSearchQuery = nil
				state.WebSearchCaller = nil

				if chunk.Index != nil {
					delete(state.ContentIndexToBlockType, *chunk.Index)
				}

				return []*schemas.BifrostResponsesStreamResponse{
					{
						Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
						SequenceNumber: sequenceNumber,
						OutputIndex:    outputIdx,
						ContentIndex:   chunk.Index,
						Item:           item,
					},
				}, nil, false
			}

			if state.WebFetchResult != nil && state.WebFetchToolID != nil {
				toolMsg := &schemas.ResponsesToolMessage{CallID: state.WebFetchToolID}
				if state.WebFetchURL != nil {
					toolMsg.Action = &schemas.ResponsesToolMessageActionStruct{
						ResponsesWebFetchToolCallAction: &schemas.ResponsesWebFetchToolCallAction{
							Type: "fetch",
							URL:  *state.WebFetchURL,
						},
					}
				}
				toolMsg.ResponsesWebFetchCall = convertAnthropicWebFetchResultToBifrost(state.WebFetchResult)
				item := &schemas.ResponsesMessage{
					ID:                   state.WebFetchToolID,
					Type:                 schemas.Ptr(schemas.ResponsesMessageTypeWebFetchCall),
					Status:               schemas.Ptr("completed"),
					ResponsesToolMessage: toolMsg,
				}
				outputIdx := state.WebFetchOutputIndex

				state.WebFetchToolID = nil
				state.WebFetchOutputIndex = nil
				state.WebFetchURL = nil
				state.WebFetchResult = nil

				if chunk.Index != nil {
					delete(state.ContentIndexToBlockType, *chunk.Index)
				}

				return []*schemas.BifrostResponsesStreamResponse{
					{
						Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
						SequenceNumber: sequenceNumber,
						OutputIndex:    outputIdx,
						ContentIndex:   chunk.Index,
						Item:           item,
					},
				}, nil, false
			}

			if state.AdvisorResult != nil && state.AdvisorToolID != nil {
				advisor := &schemas.ResponsesAdvisorCall{}

				if c := state.AdvisorResult.Content; c != nil {
					inner := c.ContentObj
					if inner == nil && len(c.ContentBlocks) > 0 {
						inner = &c.ContentBlocks[0]
					}
					if inner != nil {
						advisor.ResultType = string(inner.Type)
						advisor.Text = inner.Text
						advisor.EncryptedContent = inner.EncryptedContent
						advisor.ErrorCode = inner.ErrorCode
						advisor.StopReason = inner.StopReason
					}
				}
				item := &schemas.ResponsesMessage{
					ID:     state.AdvisorToolID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeAdvisorCall),
					Status: schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:               state.AdvisorToolID,
						Name:                 schemas.Ptr(string(AnthropicToolNameAdvisor)),
						ResponsesAdvisorCall: advisor,
					},
				}
				outputIdx := state.AdvisorOutputIndex

				if outputIdx != nil {
					cloned := *item
					clonedToolMsg := *item.ResponsesToolMessage
					cloned.ResponsesToolMessage = &clonedToolMsg
					state.OutputItems[*outputIdx] = &cloned
				}
				state.AdvisorToolID = nil
				state.AdvisorOutputIndex = nil
				state.AdvisorResult = nil
				if chunk.Index != nil {
					delete(state.ContentIndexToBlockType, *chunk.Index)
				}
				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
					SequenceNumber: sequenceNumber,
					OutputIndex:    outputIdx,
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			}

			if state.ToolSearchResult != nil && state.ToolSearchToolID != nil {
				var toolRefs []string
				for _, ref := range state.ToolSearchResult.DiscoveredToolReferences() {
					if ref.ToolName != nil {
						toolRefs = append(toolRefs, *ref.ToolName)
					} else if ref.Name != nil {
						toolRefs = append(toolRefs, *ref.Name)
					}
				}
				item := &schemas.ResponsesMessage{
					ID:     state.ToolSearchToolID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeToolSearchCall),
					Status: schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:                  state.ToolSearchToolID,
						Name:                    state.ToolSearchToolName,
						Arguments:               state.ToolSearchInput,
						ResponsesToolSearchCall: &schemas.ResponsesToolSearchCall{ToolReferences: toolRefs},
					},
				}
				outputIdx := state.ToolSearchOutputIndex

				if outputIdx != nil {
					cloned := *item
					clonedToolMsg := *item.ResponsesToolMessage
					cloned.ResponsesToolMessage = &clonedToolMsg
					state.OutputItems[*outputIdx] = &cloned
				}
				state.ToolSearchToolID = nil
				state.ToolSearchToolName = nil
				state.ToolSearchOutputIndex = nil
				state.ToolSearchResult = nil
				state.ToolSearchInput = nil
				if chunk.Index != nil {
					delete(state.ContentIndexToBlockType, *chunk.Index)
				}
				return []*schemas.BifrostResponsesStreamResponse{{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
					SequenceNumber: sequenceNumber,
					OutputIndex:    outputIdx,
					ContentIndex:   chunk.Index,
					Item:           item,
				}}, nil, false
			}

			if state.CodeExecResult != nil && state.CodeExecToolID != nil {

				serverBlock := AnthropicContentBlock{
					Type: AnthropicContentBlockTypeServerToolUse,
					ID:   state.CodeExecToolID,
					Name: state.CodeExecToolName,
				}

				if state.CodeExecInput != "" {
					serverBlock.Input = json.RawMessage(state.CodeExecInput)
				}
				msgs := []schemas.ResponsesMessage{buildBifrostCodeExecutionCall(serverBlock)}
				attachAnthropicCodeExecutionResult(msgs, *state.CodeExecToolID, *state.CodeExecResult)
				item := &msgs[0]

				outputIdx := state.CodeExecOutputIndex
				itemID := state.CodeExecToolID

				if outputIdx != nil {
					cloned := *item
					if item.ResponsesToolMessage != nil {
						clonedTM := *item.ResponsesToolMessage
						cloned.ResponsesToolMessage = &clonedTM
					}
					state.OutputItems[*outputIdx] = &cloned
				}

				state.CodeExecToolID = nil
				state.CodeExecToolName = nil
				state.CodeExecOutputIndex = nil
				state.CodeExecResult = nil
				state.CodeExecInput = ""
				if chunk.Index != nil {
					delete(state.ContentIndexToBlockType, *chunk.Index)
				}

				return []*schemas.BifrostResponsesStreamResponse{
					{
						Type:           schemas.ResponsesStreamResponseTypeCodeInterpreterCallCompleted,
						SequenceNumber: sequenceNumber,
						OutputIndex:    outputIdx,
						ItemID:         itemID,
					},
					{
						Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
						SequenceNumber: sequenceNumber + 1,
						OutputIndex:    outputIdx,
						ContentIndex:   chunk.Index,
						Item:           item,
					},
				}, nil, false
			}

			if chunk.Index != nil {
				if blockType, exists := state.ContentIndexToBlockType[*chunk.Index]; exists {
					if blockType == AnthropicContentBlockTypeWebSearchToolResult ||
						blockType == AnthropicContentBlockTypeWebFetchToolResult ||
						blockType == AnthropicContentBlockTypeAdvisorToolResult ||
						blockType == AnthropicContentBlockTypeToolSearchToolResult ||
						blockType == AnthropicContentBlockTypeServerToolUse ||
						blockType == AnthropicContentBlockTypeCodeExecutionToolResult ||
						blockType == AnthropicContentBlockTypeBashCodeExecutionToolResult ||
						blockType == AnthropicContentBlockTypeTextEditorCodeExecutionToolResult {
						delete(state.ContentIndexToBlockType, *chunk.Index)
						return nil, nil, false
					}
					if blockType == AnthropicContentBlockTypeCompaction {

						delete(state.ContentIndexToBlockType, *chunk.Index)
						return nil, nil, false
					}
					if blockType == AnthropicContentBlockTypeFallback {

						delete(state.ContentIndexToBlockType, *chunk.Index)
						return nil, nil, false
					}
				}
			}

			var responses []*schemas.BifrostResponsesStreamResponse
			itemID := state.ItemIDs[outputIndex]

			accText := ""
			if buf := state.TextBuffers[outputIndex]; buf != nil {
				accText = buf.String()
			}

			if chunk.Index != nil {
				if state.TextContentIndices[*chunk.Index] {

					textDoneResponse := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeOutputTextDone,
						SequenceNumber: sequenceNumber + len(responses),
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Text:           &accText,
					}
					if itemID != "" {
						textDoneResponse.ItemID = &itemID
					}
					responses = append(responses, textDoneResponse)

					partText := accText
					part := &schemas.ResponsesMessageContentBlock{
						Type: schemas.ResponsesOutputMessageContentTypeText,
						Text: &partText,
						ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
							Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
							LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
						},
					}
					partDoneResponse := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeContentPartDone,
						SequenceNumber: sequenceNumber + len(responses),
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Part:           part,
					}
					if itemID != "" {
						partDoneResponse.ItemID = &itemID
					}
					responses = append(responses, partDoneResponse)

					delete(state.TextContentIndices, *chunk.Index)
				}

				if state.ReasoningContentIndices[*chunk.Index] {

					accReasoning := ""
					if buf := state.ReasoningTextBuffers[outputIndex]; buf != nil {
						accReasoning = buf.String()
						delete(state.ReasoningTextBuffers, outputIndex)
					}
					var signature *string
					if sig, ok := state.ReasoningSignatures[outputIndex]; ok && sig != "" {
						sigCopy := sig
						signature = &sigCopy
						delete(state.ReasoningSignatures, outputIndex)
					}

					if storedItem, exists := state.OutputItems[outputIndex]; exists {
						textCopy := accReasoning
						storedItem.Content = &schemas.ResponsesMessageContent{
							ContentBlocks: []schemas.ResponsesMessageContentBlock{
								{
									Type:      schemas.ResponsesOutputMessageContentTypeReasoning,
									Text:      &textCopy,
									Signature: signature,
								},
							},
						}
						storedItem.ResponsesReasoning = nil
					}

					doneText := accReasoning
					reasoningDoneResponse := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeReasoningSummaryTextDone,
						SequenceNumber: sequenceNumber + len(responses),
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						SummaryIndex:   schemas.Ptr(anthropicReasoningSummaryIndex),
						Text:           &doneText,
					}
					if itemID != "" {
						reasoningDoneResponse.ItemID = &itemID
					}
					responses = append(responses, reasoningDoneResponse)

					partText := accReasoning
					partDoneResponse := &schemas.BifrostResponsesStreamResponse{
						Type:           schemas.ResponsesStreamResponseTypeContentPartDone,
						SequenceNumber: sequenceNumber + len(responses),
						OutputIndex:    schemas.Ptr(outputIndex),
						ContentIndex:   chunk.Index,
						Part: &schemas.ResponsesMessageContentBlock{
							Type:      schemas.ResponsesOutputMessageContentTypeReasoning,
							Text:      &partText,
							Signature: signature,
						},
					}
					if itemID != "" {
						partDoneResponse.ItemID = &itemID
					}
					responses = append(responses, partDoneResponse)

					delete(state.ReasoningContentIndices, *chunk.Index)
				}
			}

			if accumulatedArgs, hasArgs := state.ToolArgumentBuffers[outputIndex]; hasArgs && state.StructuredOutputIndex != nil && *state.StructuredOutputIndex == outputIndex {

				textContent := accumulatedArgs
				if textContent == "" {
					textContent = "{}"
				}

				contentBlock := schemas.ResponsesMessageContentBlock{
					Type: schemas.ResponsesOutputMessageContentTypeText,
					Text: &textContent,
					ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
						Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
						LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
					},
				}

				item := &schemas.ResponsesMessage{
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Status: schemas.Ptr("completed"),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{contentBlock},
					},
				}
				if itemID != "" {
					item.ID = &itemID
				}
				var itemIDPtr *string
				if itemID != "" {
					itemIDPtr = &itemID
				}

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				})

				emptyText := ""
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeContentPartAdded,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					ItemID:         itemIDPtr,
					Part: &schemas.ResponsesMessageContentBlock{
						Type: schemas.ResponsesOutputMessageContentTypeText,
						Text: &emptyText,
						ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
							Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
							LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
						},
					},
				})

				deltaText := textContent
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputTextDelta,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					ItemID:         itemIDPtr,
					Delta:          &deltaText,
				})

				doneText := textContent
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputTextDone,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					ItemID:         itemIDPtr,
					Text:           &doneText,
				})

				partText := textContent
				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeContentPartDone,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					ItemID:         itemIDPtr,
					Part: &schemas.ResponsesMessageContentBlock{
						Type: schemas.ResponsesOutputMessageContentTypeText,
						Text: &partText,
						ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
							Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
							LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
						},
					},
				})

				responses = append(responses, &schemas.BifrostResponsesStreamResponse{
					Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Item:           item,
				})

				delete(state.ToolArgumentBuffers, outputIndex)
				state.StructuredOutputIndex = nil
				state.UsedStructuredOutputTool = true

				return responses, nil, false
			}

			if accumulatedArgs, hasArgs := state.ToolArgumentBuffers[outputIndex]; hasArgs {

				if storedItem, exists := state.OutputItems[outputIndex]; exists && storedItem.ResponsesToolMessage != nil {
					storedItem.ResponsesToolMessage.Arguments = &accumulatedArgs
					storedItem.Status = schemas.Ptr("completed")
				}

				var doneType schemas.ResponsesStreamResponseType
				if state.MCPCallOutputIndices[outputIndex] {
					doneType = schemas.ResponsesStreamResponseTypeMCPCallArgumentsDone
				} else {
					doneType = schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDone
				}

				response := &schemas.BifrostResponsesStreamResponse{
					Type:           doneType,
					SequenceNumber: sequenceNumber + len(responses),
					OutputIndex:    schemas.Ptr(outputIndex),
					ContentIndex:   chunk.Index,
					Arguments:      &accumulatedArgs,
				}
				if itemID != "" {
					response.ItemID = &itemID
				}
				responses = append(responses, response)

				delete(state.ToolArgumentBuffers, outputIndex)
				delete(state.MCPCallOutputIndices, outputIndex)
			}

			statusCompleted := "completed"
			doneItemID := state.ItemIDs[outputIndex]
			var doneItem *schemas.ResponsesMessage
			if storedItem, exists := state.OutputItems[outputIndex]; exists {
				copied := *storedItem
				if storedItem.ResponsesToolMessage != nil {
					toolMsgCopy := *storedItem.ResponsesToolMessage
					copied.ResponsesToolMessage = &toolMsgCopy
				}
				doneItem = &copied
			} else {

				contentBlocks := []schemas.ResponsesMessageContentBlock{}
				if accText != "" {
					textCopy := accText
					contentBlocks = []schemas.ResponsesMessageContentBlock{
						{
							Type: schemas.ResponsesOutputMessageContentTypeText,
							Text: &textCopy,
							ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
								Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
								LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
							},
						},
					}
				}
				delete(state.TextBuffers, outputIndex)
				doneItem = &schemas.ResponsesMessage{
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role:   schemas.Ptr(schemas.ResponsesInputMessageRoleAssistant),
					Status: &statusCompleted,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: contentBlocks,
					},
				}
				if doneItemID != "" {
					doneItem.ID = &doneItemID
				}

				if len(contentBlocks) > 0 {
					cloned := *doneItem
					clonedContent := *doneItem.Content
					cloned.Content = &clonedContent
					state.OutputItems[outputIndex] = &cloned
				}
			}
			responses = append(responses, &schemas.BifrostResponsesStreamResponse{
				Type:           schemas.ResponsesStreamResponseTypeOutputItemDone,
				SequenceNumber: sequenceNumber + len(responses),
				OutputIndex:    schemas.Ptr(outputIndex),
				ContentIndex:   chunk.Index,
				Item:           doneItem,
			})

			return responses, nil, false
		}

	case AnthropicStreamEventTypeMessageDelta:

		if chunk.Delta != nil {

			if chunk.Delta.Container != nil {
				state.Container = chunk.Delta.Container
			}
			if chunk.Delta.StopReason != nil {
				mapped := ConvertAnthropicFinishReasonToBifrost(*chunk.Delta.StopReason)
				if state.UsedStructuredOutputTool && !state.SeenRealToolCall &&
					mapped == string(schemas.BifrostFinishReasonToolCalls) {
					mapped = string(schemas.BifrostFinishReasonStop)
				}
				state.StopReason = &mapped
				if *chunk.Delta.StopReason == AnthropicStopReasonStopSequence {
					state.StopSequence = chunk.Delta.StopSequence
				}
			}
			if chunk.Delta.StopDetails != nil {
				state.StopDetails = stopDetailsToBifrost(chunk.Delta.StopDetails)
			}
			if len(chunk.Delta.SafeguardResults) > 0 {
				state.SafeguardResults = chunk.Delta.SafeguardResults
			}
		}

		if ctx.Value(schemas.BifrostContextKeyIntegrationType) == "anthropic" {

			bifrostUsage := ConvertAnthropicUsageToBifrostUsage(chunk.Usage)

			var stopReason *string
			if state.StopReason != nil {
				stopReason = state.StopReason
			}

			response := &schemas.BifrostResponsesResponse{
				CreatedAt: state.CreatedAt,
			}
			if state.MessageID != nil {
				response.ID = state.MessageID
			}
			if state.Model != nil {
				response.Model = *state.Model
			}
			if stopReason != nil {
				response.StopReason = stopReason
			}
			response.StopDetails = state.StopDetails
			response.StopSequence = state.StopSequence
			if bifrostUsage != nil {
				response.Usage = bifrostUsage
				response.Speed = chunk.Usage.Speed
				response.InferenceGeo = chunk.Usage.InferenceGeo
			}

			if state.Container != nil {
				response.Container = &schemas.ResponsesResponseContainer{
					ID:        state.Container.ID,
					ExpiresAt: state.Container.ExpiresAt,
				}
			}

			response.SafeguardResults = state.SafeguardResults

			state.HasEmittedMessageDelta = true

			return []*schemas.BifrostResponsesStreamResponse{{
				Type:           "message_delta",
				SequenceNumber: sequenceNumber,
				Response:       response,
			}}, nil, false
		}

		return nil, nil, false

	case AnthropicStreamEventTypeMessageStop:

		response := &schemas.BifrostResponsesResponse{
			CreatedAt: state.CreatedAt,
		}
		if state.MessageID != nil {
			response.ID = state.MessageID
		}
		if state.Model != nil {
			response.Model = *state.Model
		}
		if state.StopReason != nil {
			response.StopReason = state.StopReason
		}
		response.StopDetails = state.StopDetails
		response.StopSequence = state.StopSequence
		response.SafeguardResults = state.SafeguardResults

		if state.Container != nil {
			response.Container = &schemas.ResponsesResponseContainer{
				ID:        state.Container.ID,
				ExpiresAt: state.Container.ExpiresAt,
			}
			for _, item := range state.OutputItems {
				if item == nil || item.Type == nil ||
					*item.Type != schemas.ResponsesMessageTypeCodeInterpreterCall ||
					item.ResponsesToolMessage == nil {
					continue
				}
				if item.ResponsesToolMessage.ResponsesCodeInterpreterToolCall == nil {
					item.ResponsesToolMessage.ResponsesCodeInterpreterToolCall = &schemas.ResponsesCodeInterpreterToolCall{}
				}
				item.ResponsesToolMessage.ResponsesCodeInterpreterToolCall.ContainerID = state.Container.ID
				if state.Container.ExpiresAt != nil {
					if item.ResponsesToolMessage.ResponsesCodeExecutionCall == nil {
						item.ResponsesToolMessage.ResponsesCodeExecutionCall = &schemas.ResponsesCodeExecutionCall{}
					}
					item.ResponsesToolMessage.ResponsesCodeExecutionCall.ContainerExpiresAt = state.Container.ExpiresAt
				}
			}
		}

		if len(state.OutputItems) > 0 {

			response.Output = make([]schemas.ResponsesMessage, 0, len(state.OutputItems))
			for i := 0; i < state.CurrentOutputIndex; i++ {
				if item, exists := state.OutputItems[i]; exists {
					response.Output = append(response.Output, *item)
				}
			}
		}

		terminalType := schemas.ResponsesStreamResponseTypeCompleted
		response.Status, response.IncompleteDetails = anthropicResponsesStatus(response.StopReason)
		if response.Status != nil && *response.Status == schemas.ResponsesResponseStatusIncomplete {
			terminalType = schemas.ResponsesStreamResponseTypeIncomplete
			schemas.MarkTruncatedOutputItem(response.Output)
		}

		return []*schemas.BifrostResponsesStreamResponse{{
			Type:           terminalType,
			SequenceNumber: sequenceNumber,
			Response:       response,
		}}, nil, true

	case AnthropicStreamEventTypePing:
		return []*schemas.BifrostResponsesStreamResponse{{
			Type:           schemas.ResponsesStreamResponseTypePing,
			SequenceNumber: sequenceNumber,
		}}, nil, false

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

			return []*schemas.BifrostResponsesStreamResponse{{
				Type:           schemas.ResponsesStreamResponseTypeError,
				SequenceNumber: sequenceNumber,
				Message:        &chunk.Error.Message,
			}}, bifrostErr, false
		}
	}

	return nil, nil, false
}

func ToAnthropicResponsesRequest(ctx *schemas.BifrostContext, bifrostReq *schemas.BifrostResponsesRequest) (*AnthropicMessageRequest, error) {
	if bifrostReq == nil {
		return nil, fmt.Errorf("bifrost request is nil")
	}

	capModel := schemas.ResolveCanonicalModel(ctx, bifrostReq.Model)
	caps := schemas.ResolveModelCaps(bifrostReq.Provider, capModel)
	var requestTools []schemas.ResponsesTool
	if bifrostReq.Params != nil {
		requestTools = bifrostReq.Params.Tools
	}
	input, err := prepareInlineToolDefinitions(bifrostReq.Input, requestTools)
	if err != nil {
		return nil, err
	}

	forcedToolChoiceSupported := caps.SupportsForcedToolChoice(schemas.DefaultSupportsForcedToolChoice(capModel))

	anthropicReq := &AnthropicMessageRequest{
		Model:     bifrostReq.Model,
		MaxTokens: providerUtils.GetMaxOutputTokensOrDefault(bifrostReq.Provider, capModel, AnthropicDefaultMaxTokens),
	}

	if bifrostReq.Params != nil {
		if bifrostReq.Params.MaxOutputTokens != nil {
			anthropicReq.MaxTokens = clampToModelOutputCeiling(caps, *bifrostReq.Params.MaxOutputTokens)
		}

		if !caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {

			if bifrostReq.Params.Temperature != nil {
				anthropicReq.Temperature = bifrostReq.Params.Temperature
			} else if bifrostReq.Params.TopP != nil {
				anthropicReq.TopP = bifrostReq.Params.TopP
			}
		}
		if bifrostReq.Params.User != nil {
			anthropicReq.Metadata = &AnthropicMetaData{
				UserID: bifrostReq.Params.User,
			}
		}
		if bifrostReq.Params.Text != nil {

			if ProviderRequiresSyntheticStructuredOutput(bifrostReq.Provider) {
				if bifrostReq.Params.Text.Format != nil {
					responseFormatTool, err := convertResponsesTextFormatToTool(ctx, bifrostReq.Params.Text)
					if err != nil {
						return nil, err
					}
					if responseFormatTool != nil {
						if anthropicReq.Tools == nil {
							anthropicReq.Tools = []AnthropicTool{}
						}
						anthropicReq.Tools = append(anthropicReq.Tools, *responseFormatTool)
						thinkingEnabled := bifrostReq.Params.Reasoning != nil &&
							(bifrostReq.Params.Reasoning.MaxTokens != nil ||
								(bifrostReq.Params.Reasoning.Effort != nil && *bifrostReq.Params.Reasoning.Effort != "none"))
						if !thinkingEnabled && forcedToolChoiceSupported {
							anthropicReq.ToolChoice = &AnthropicToolChoice{
								Type: "tool",
								Name: responseFormatTool.Name,
							}
						}
					}
				}
			} else {

				hasCitationsEnabled := false

				for _, message := range bifrostReq.Input {
					if message.Content == nil || message.Content.ContentBlocks == nil {
						continue
					}
					if message.Content.ContentBlocks != nil {
						for _, block := range message.Content.ContentBlocks {
							if block.Type == schemas.ResponsesInputMessageContentBlockTypeFile &&
								block.Citations != nil &&
								block.Citations.Enabled != nil &&
								*block.Citations.Enabled {
								hasCitationsEnabled = true
								break
							}
						}
					}
					if hasCitationsEnabled {
						break
					}
				}
				if !hasCitationsEnabled {

					outputFormat := convertResponsesTextConfigToAnthropicOutputFormat(bifrostReq.Params.Text)
					if outputFormat != nil {
						anthropicReq.OutputConfig = &AnthropicOutputConfig{
							Format: outputFormat,
						}
					}
				}
			}
		}
		if bifrostReq.Params.Reasoning != nil {
			if bifrostReq.Params.Reasoning.Type != nil && *bifrostReq.Params.Reasoning.Type == "between_tools" &&
				schemas.IsAnthropicModelFamily(ctx, bifrostReq.Model) {

				anthropicReq.Thinking = BetweenToolsThinking(caps, bifrostReq.Params.Reasoning.Effort)
				if bifrostReq.Params.Reasoning.Effort != nil && *bifrostReq.Params.Reasoning.Effort != "none" &&
					caps.SupportsNativeEffort(DefaultSupportsNativeEffort(caps.Model())) {
					setEffortOnOutputConfig(anthropicReq, MapBifrostEffortToAnthropic(*bifrostReq.Params.Reasoning.Effort))
				}
			} else if bifrostReq.Params.Reasoning.MaxTokens != nil {
				if caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {

					anthropicReq.Thinking = &AnthropicThinking{Type: "adaptive"}

					if bifrostReq.Params.Reasoning.Effort != nil && *bifrostReq.Params.Reasoning.Effort != "none" {
						setEffortOnOutputConfig(anthropicReq, MapBifrostEffortToAnthropic(*bifrostReq.Params.Reasoning.Effort))
					}
				} else {
					budgetTokens := *bifrostReq.Params.Reasoning.MaxTokens
					if *bifrostReq.Params.Reasoning.MaxTokens == -1 {

						budgetTokens = MinimumReasoningMaxTokens
					}
					if budgetTokens < MinimumReasoningMaxTokens {
						return nil, fmt.Errorf("reasoning.max_tokens must be >= %d for anthropic: %w", MinimumReasoningMaxTokens, ErrReasoningMaxTokensTooLow)
					}

					if requested := bifrostReq.Params.MaxOutputTokens; requested != nil && *requested > anthropicReq.MaxTokens {
						if fitted, ok := fitThinkingBudget(&budgetTokens, bifrostReq.Params.Reasoning.Effort, anthropicReq.MaxTokens); ok {
							budgetTokens = fitted
						}
					}
					anthropicReq.Thinking = &AnthropicThinking{
						Type:         "enabled",
						BudgetTokens: schemas.Ptr(budgetTokens),
					}
				}
			} else if native, ok := anthropicNativeEffortFrom(ctx); ok && native.ThinkingOmitted {

				if caps.SupportsNativeEffort(DefaultSupportsNativeEffort(caps.Model())) {
					setEffortOnOutputConfig(anthropicReq, MapBifrostEffortToAnthropic(native.Effort))
				}
			} else {
				if bifrostReq.Params.Reasoning.Effort != nil {
					if *bifrostReq.Params.Reasoning.Effort != "none" {
						effort := MapBifrostEffortToAnthropic(*bifrostReq.Params.Reasoning.Effort)

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

							budgetTokens, err := providerUtils.GetBudgetTokensFromReasoningEffort(effort, MinimumReasoningMaxTokens, anthropicReq.MaxTokens)
							if err != nil {
								return nil, fmt.Errorf("%w: %w", ErrReasoningMaxTokensTooLow, err)
							}
							anthropicReq.Thinking = &AnthropicThinking{
								Type:         "enabled",
								BudgetTokens: schemas.Ptr(budgetTokens),
							}
						}
					} else {
						if caps.CanDisableReasoning(DefaultCanDisableReasoning(capModel)) {

							anthropicReq.Thinking = &AnthropicThinking{
								Type: "disabled",
							}
						}

						if native, ok := anthropicNativeEffortFrom(ctx); ok && native.Effort != "" &&
							caps.SupportsNativeEffort(DefaultSupportsNativeEffort(caps.Model())) {
							setEffortOnOutputConfig(anthropicReq, MapBifrostEffortToAnthropic(native.Effort))
						}
					}
				}
			}

			if anthropicReq.Thinking != nil && anthropicReq.Thinking.Type != "disabled" && anthropicReq.Thinking.Type != "between_tools" {
				if bifrostReq.Params.Reasoning != nil &&
					bifrostReq.Params.Reasoning.Summary != nil {
					if *bifrostReq.Params.Reasoning.Summary == "none" {
						anthropicReq.Thinking.Display = schemas.Ptr("omitted")
					} else {
						anthropicReq.Thinking.Display = schemas.Ptr("summarized")
					}
				} else if caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {
					anthropicReq.Thinking.Display = schemas.Ptr("summarized")
				}
			}
		}

		if bifrostReq.Params.ServiceTier != nil {
			mapped := MapBifrostServiceTierToAnthropicRequest(*bifrostReq.Params.ServiceTier)
			anthropicReq.ServiceTier = &mapped
		}

		if bifrostReq.Params.ExtraParams != nil {
			anthropicReq.ExtraParams = make(map[string]any, len(bifrostReq.Params.ExtraParams))
			maps.Copy(anthropicReq.ExtraParams, bifrostReq.Params.ExtraParams)
			if cacheControlRaw, exists := anthropicReq.ExtraParams["cache_control"]; exists {
				parsed := false
				switch v := cacheControlRaw.(type) {
				case *schemas.CacheControl:
					anthropicReq.CacheControl = v
					parsed = true
				case schemas.CacheControl:
					anthropicReq.CacheControl = &v
					parsed = true
				default:
					if data, err := providerUtils.MarshalSorted(v); err == nil {
						var cc schemas.CacheControl
						if sonic.Unmarshal(data, &cc) == nil {
							anthropicReq.CacheControl = &cc
							parsed = true
						}
					}
				}
				if parsed {
					delete(anthropicReq.ExtraParams, "cache_control")
				}
			}
			if diagnosticsRaw, exists := anthropicReq.ExtraParams["diagnostics"]; exists {
				parsed := false
				switch v := diagnosticsRaw.(type) {
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
			if safeguardsRaw, exists := anthropicReq.ExtraParams["safeguards"]; exists {

				delete(anthropicReq.ExtraParams, "safeguards")
				switch v := safeguardsRaw.(type) {
				case json.RawMessage:
					anthropicReq.Safeguards = v
				case []byte:
					anthropicReq.Safeguards = json.RawMessage(v)
				default:
					if data, err := providerUtils.MarshalSorted(v); err == nil {
						anthropicReq.Safeguards = json.RawMessage(data)
					}
				}
			}
			if containerRaw, exists := anthropicReq.ExtraParams["container"]; exists {
				parsed := false
				switch v := containerRaw.(type) {
				case *AnthropicContainer:
					anthropicReq.Container = v
					parsed = true
				case AnthropicContainer:
					anthropicReq.Container = &v
					parsed = true
				default:

					if data, err := providerUtils.MarshalSorted(v); err == nil {
						var c AnthropicContainer
						if sonic.Unmarshal(data, &c) == nil {
							anthropicReq.Container = &c
							parsed = true
						}
					}
				}
				if parsed {
					delete(anthropicReq.ExtraParams, "container")
				}
			}
			topK, ok := schemas.SafeExtractIntPointer(bifrostReq.Params.ExtraParams["top_k"])
			if ok {
				delete(anthropicReq.ExtraParams, "top_k")
				if !caps.AdaptiveOnlyThinking(DefaultAdaptiveOnlyThinking(caps.Model())) {
					anthropicReq.TopK = topK
				}
			}
			if speed, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["speed"]); ok {
				delete(anthropicReq.ExtraParams, "speed")
				if caps.SupportsFastMode(DefaultSupportsFastMode(capModel)) {
					anthropicReq.Speed = speed
				}
			}
			if stop, ok := schemas.SafeExtractStringSlice(bifrostReq.Params.ExtraParams["stop"]); ok {
				delete(anthropicReq.ExtraParams, "stop")
				anthropicReq.StopSequences = stop
			}
			if inferenceGeo, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["inference_geo"]); ok {
				delete(anthropicReq.ExtraParams, "inference_geo")
				anthropicReq.InferenceGeo = inferenceGeo
			}
			if tbVal, exists := bifrostReq.Params.ExtraParams["task_budget"]; exists {

				delete(anthropicReq.ExtraParams, "task_budget")
				var taskBudget *AnthropicTaskBudget
				switch v := tbVal.(type) {
				case *AnthropicTaskBudget:
					taskBudget = v
				case AnthropicTaskBudget:
					taskBudget = &v
				default:
					if data, err := providerUtils.MarshalSorted(v); err == nil {
						var tb AnthropicTaskBudget
						if sonic.Unmarshal(data, &tb) == nil {
							taskBudget = &tb
						}
					}
				}
				if taskBudget == nil {
					return nil, fmt.Errorf("invalid task_budget format for anthropic")
				}
				if anthropicReq.OutputConfig == nil {
					anthropicReq.OutputConfig = &AnthropicOutputConfig{}
				}
				anthropicReq.OutputConfig.TaskBudget = taskBudget
			}

			if fbVal, exists := bifrostReq.Params.ExtraParams["fallbacks"]; exists {
				delete(anthropicReq.ExtraParams, "fallbacks")
				var natives []AnthropicNativeFallback
				switch v := fbVal.(type) {
				case string:

					anthropicReq.Fallbacks = &AnthropicFallbacks{Preset: v}
				case []AnthropicNativeFallback:
					natives = v
				default:
					if data, err := providerUtils.MarshalSorted(v); err == nil {
						_ = sonic.Unmarshal(data, &natives)
					}
				}
				if len(natives) > 0 {
					entries := make([]AnthropicFallbackEntry, len(natives))
					for i := range natives {
						n := natives[i]
						entries[i] = AnthropicFallbackEntry{Native: &n}
					}
					anthropicReq.Fallbacks = &AnthropicFallbacks{Entries: entries}
				}
			}

			if tokenVal, exists := bifrostReq.Params.ExtraParams["fallback_credit_token"]; exists {
				delete(anthropicReq.ExtraParams, "fallback_credit_token")
				if token, ok := tokenVal.(string); ok && token != "" {
					anthropicReq.FallbackCreditToken = &token
				}
			}
		}

		extraContextManagement := bifrostReq.Params.ExtraParams["context_management"]
		delete(anthropicReq.ExtraParams, "context_management")

		if features, ok := ProviderFeatures[bifrostReq.Provider]; !ok || features.ContextManagementField {
			if len(bifrostReq.Params.ContextManagement) > 0 {
				var cm ContextManagement
				if err := sonic.Unmarshal(bifrostReq.Params.ContextManagement, &cm); err == nil {
					anthropicReq.ContextManagement = &cm
				}
			}

			if anthropicReq.ContextManagement == nil && extraContextManagement != nil {
				if cm, ok := extraContextManagement.(*ContextManagement); ok && cm != nil {
					anthropicReq.ContextManagement = cm
				} else if data, err := providerUtils.MarshalSorted(extraContextManagement); err == nil {
					var cm ContextManagement
					if sonic.Unmarshal(data, &cm) == nil {
						anthropicReq.ContextManagement = &cm
					}
				}
			}
		}

		if bifrostReq.Params.Tools != nil {
			anthropicTools, mcpServers, err := convertBifrostToolsToAnthropic(caps, bifrostReq.Params.Tools, bifrostReq.Provider)
			if err != nil {
				return nil, err
			}
			if len(anthropicTools) > 0 {
				if anthropicReq.Tools == nil {
					anthropicReq.Tools = anthropicTools
				} else {
					anthropicReq.Tools = append(anthropicReq.Tools, anthropicTools...)
				}
			}
			if len(mcpServers) > 0 {
				anthropicReq.MCPServers = mcpServers
			}
		}

		forcedToolChoiceRejected := !forcedToolChoiceSupported && bifrostReq.Params.ToolChoice.IsForced()

		if bifrostReq.Params.ToolChoice != nil && !forcedToolChoiceRejected {
			anthropicToolChoice := convertResponsesToolChoiceToAnthropic(bifrostReq.Params.ToolChoice)
			if anthropicToolChoice != nil {
				anthropicReq.ToolChoice = anthropicToolChoice
			}
		}

		if anthropicReq.ToolChoice != nil {
			applyParallelToolUse(anthropicReq.ToolChoice, bifrostReq.Params.ParallelToolCalls)
		} else if len(anthropicReq.Tools) > 0 &&
			bifrostReq.Params.ParallelToolCalls != nil && !*bifrostReq.Params.ParallelToolCalls {
			anthropicReq.ToolChoice = &AnthropicToolChoice{Type: "auto"}
			applyParallelToolUse(anthropicReq.ToolChoice, bifrostReq.Params.ParallelToolCalls)
		}

		if bifrostReq.Provider == schemas.DeepSeek && anthropicReq.ToolChoice != nil &&
			anthropicReq.ToolChoice.Type == "tool" {
			anthropicReq.Thinking = &AnthropicThinking{Type: "disabled"}
		}
	}

	if bifrostReq.Input != nil {
		anthropicMessages, systemContent := ConvertBifrostMessagesToAnthropicMessages(ctx, input, true, caps)

		if bifrostReq.Params != nil && bifrostReq.Params.Instructions != nil && *bifrostReq.Params.Instructions != "" {
			instructions := &AnthropicContent{
				ContentBlocks: []AnthropicContentBlock{
					{
						Type: AnthropicContentBlockTypeText,
						Text: bifrostReq.Params.Instructions,
					},
				},
			}
			if systemContent != nil {
				instructions = appendToSystemContent(instructions, *systemContent)
			}
			systemContent = instructions
		}

		if systemContent != nil {
			anthropicReq.System = systemContent
		}

		anthropicReq.Messages = anthropicMessages
	}

	clampAnthropicCacheBreakpoints(anthropicReq)

	return anthropicReq, nil
}

func ConvertAnthropicUsageToBifrostUsage(anthropicUsage *AnthropicUsage) *schemas.ResponsesResponseUsage {
	if anthropicUsage == nil {
		return nil
	}

	billable := billableAnthropicUsage(anthropicUsage)

	bifrostUsage := &schemas.ResponsesResponseUsage{
		Type:         anthropicUsage.Type,
		Model:        anthropicUsage.Model,
		InputTokens:  billable.InputTokens,
		OutputTokens: billable.OutputTokens,
		TotalTokens:  billable.InputTokens + billable.OutputTokens,
	}

	if billable.CacheReadInputTokens > 0 {
		if bifrostUsage.InputTokensDetails == nil {
			bifrostUsage.InputTokensDetails = &schemas.ResponsesResponseInputTokens{}
		}
		bifrostUsage.InputTokensDetails.CachedReadTokens = billable.CacheReadInputTokens
		bifrostUsage.InputTokens = bifrostUsage.InputTokens + billable.CacheReadInputTokens
		bifrostUsage.TotalTokens = bifrostUsage.TotalTokens + billable.CacheReadInputTokens
	}

	if billable.CacheCreationInputTokens > 0 {
		if bifrostUsage.InputTokensDetails == nil {
			bifrostUsage.InputTokensDetails = &schemas.ResponsesResponseInputTokens{}
		}
		bifrostUsage.InputTokensDetails.CachedWriteTokens = billable.CacheCreationInputTokens
		if billable.CacheCreation.Ephemeral5mInputTokens > 0 || billable.CacheCreation.Ephemeral1hInputTokens > 0 {
			bifrostUsage.InputTokensDetails.CachedWriteTokenDetails = &schemas.ChatCachedWriteTokenDetails{
				CachedWriteTokens5m: billable.CacheCreation.Ephemeral5mInputTokens,
				CachedWriteTokens1h: billable.CacheCreation.Ephemeral1hInputTokens,
			}
		}
		bifrostUsage.InputTokens = bifrostUsage.InputTokens + billable.CacheCreationInputTokens
		bifrostUsage.TotalTokens = bifrostUsage.TotalTokens + billable.CacheCreationInputTokens
	}

	if billable.ServerToolUse != nil && billable.ServerToolUse.WebSearchRequests > 0 {
		if bifrostUsage.OutputTokensDetails == nil {
			bifrostUsage.OutputTokensDetails = &schemas.ResponsesResponseOutputTokens{}
		}
		bifrostUsage.OutputTokensDetails.NumSearchQueries = schemas.Ptr(billable.ServerToolUse.WebSearchRequests)
		bifrostUsage.ToolUsage = &schemas.ToolUsage{WebSearch: &schemas.WebSearchToolUsage{NumRequests: billable.ServerToolUse.WebSearchRequests}}
	}

	if billable.OutputTokensDetails != nil {
		if bifrostUsage.OutputTokensDetails == nil {
			bifrostUsage.OutputTokensDetails = &schemas.ResponsesResponseOutputTokens{}
		}
		bifrostUsage.OutputTokensDetails.ReasoningTokens = billable.OutputTokensDetails.ThinkingTokens
	}

	if len(anthropicUsage.Iterations) > 0 {
		bifrostUsage.Iterations = make([]schemas.ResponsesResponseUsage, len(anthropicUsage.Iterations))
		for i, iteration := range anthropicUsage.Iterations {
			if converted := ConvertAnthropicUsageToBifrostUsage(&iteration); converted != nil {
				bifrostUsage.Iterations[i] = *converted
			}
		}
	}

	return bifrostUsage
}

func stopDetailsToBifrost(d *AnthropicStopDetails) *schemas.ResponsesStopDetails {
	if d == nil {
		return nil
	}
	return &schemas.ResponsesStopDetails{
		Type:                    d.Type,
		Category:                d.Category,
		Explanation:             d.Explanation,
		RecommendedModel:        d.RecommendedModel,
		FallbackCreditToken:     d.FallbackCreditToken,
		FallbackHasPrefillClaim: d.FallbackHasPrefillClaim,
	}
}

func (response *AnthropicMessageResponse) ToBifrostResponsesResponse(ctx *schemas.BifrostContext) *schemas.BifrostResponsesResponse {
	if response == nil {
		return nil
	}

	bifrostResp := &schemas.BifrostResponsesResponse{
		ID:        schemas.Ptr(response.ID),
		CreatedAt: int(time.Now().Unix()),
	}

	bifrostResp.Usage = ConvertAnthropicUsageToBifrostUsage(response.Usage)

	bifrostResp.ExtraFields.RoutingInfo.ServerSideFallbackModel = response.Usage.ServerSideFallbackModel()

	if len(response.Content) > 0 {

		tempMsg := AnthropicMessage{
			Role: AnthropicMessageRoleAssistant,
			Content: AnthropicContent{
				ContentBlocks: response.Content,
			},
		}
		outputMessages := ConvertAnthropicMessagesToBifrostMessages(ctx, []AnthropicMessage{tempMsg}, nil, true, false)
		if len(outputMessages) > 0 {

			if response.Container != nil {
				for i := range outputMessages {
					m := &outputMessages[i]
					if m.Type == nil || *m.Type != schemas.ResponsesMessageTypeCodeInterpreterCall || m.ResponsesToolMessage == nil {
						continue
					}
					if m.ResponsesToolMessage.ResponsesCodeInterpreterToolCall == nil {
						m.ResponsesToolMessage.ResponsesCodeInterpreterToolCall = &schemas.ResponsesCodeInterpreterToolCall{}
					}
					m.ResponsesToolMessage.ResponsesCodeInterpreterToolCall.ContainerID = response.Container.ID
					if response.Container.ExpiresAt != nil {
						if m.ResponsesToolMessage.ResponsesCodeExecutionCall == nil {
							m.ResponsesToolMessage.ResponsesCodeExecutionCall = &schemas.ResponsesCodeExecutionCall{}
						}
						m.ResponsesToolMessage.ResponsesCodeExecutionCall.ContainerExpiresAt = response.Container.ExpiresAt
					}
				}
			}
			bifrostResp.Output = outputMessages
		}
	}

	bifrostResp.Model = response.Model

	if response.StopReason != "" {
		mapped := ConvertAnthropicFinishReasonToBifrost(response.StopReason)
		if mapped == string(schemas.BifrostFinishReasonToolCalls) {
			if soToolName, ok := ctx.Value(schemas.BifrostContextKeyStructuredOutputToolName).(string); ok && soToolName != "" {
				hasRealToolUse := false
				for _, block := range response.Content {
					if block.Type == AnthropicContentBlockTypeServerToolUse ||
						block.Type == AnthropicContentBlockTypeMCPToolUse ||
						(block.Type == AnthropicContentBlockTypeToolUse && block.Name != nil && *block.Name != soToolName) {
						hasRealToolUse = true
						break
					}
				}
				if !hasRealToolUse {
					mapped = string(schemas.BifrostFinishReasonStop)
				}
			}
		}
		bifrostResp.StopReason = &mapped
		if response.StopReason == AnthropicStopReasonStopSequence {
			bifrostResp.StopSequence = response.StopSequence
		}
	}

	bifrostResp.Status, bifrostResp.IncompleteDetails = anthropicResponsesStatus(bifrostResp.StopReason)
	if bifrostResp.Status != nil && *bifrostResp.Status == schemas.ResponsesResponseStatusIncomplete {
		schemas.MarkTruncatedOutputItem(bifrostResp.Output)
	}

	bifrostResp.Status, bifrostResp.IncompleteDetails = anthropicResponsesStatus(bifrostResp.StopReason)
	if bifrostResp.Status != nil && *bifrostResp.Status == schemas.ResponsesResponseStatusIncomplete {
		schemas.MarkTruncatedOutputItem(bifrostResp.Output)
	}
	bifrostResp.StopDetails = stopDetailsToBifrost(response.StopDetails)

	if response.Usage != nil && response.Usage.ServiceTier != nil {
		mapped := MapAnthropicServiceTierToBifrost(*response.Usage.ServiceTier)
		bifrostResp.ServiceTier = &mapped
	}

	if response.Usage != nil && response.Usage.Speed != nil {
		bifrostResp.Speed = response.Usage.Speed
	}

	if response.Usage != nil && response.Usage.InferenceGeo != nil {
		bifrostResp.InferenceGeo = response.Usage.InferenceGeo
	}

	if response.Diagnostics != nil {
		bifrostResp.Diagnostics = response.Diagnostics
	}

	if len(response.SafeguardResults) > 0 {
		bifrostResp.SafeguardResults = response.SafeguardResults
	}

	return bifrostResp
}

func ConvertAnthropicMessagesToBifrostMessages(ctx *schemas.BifrostContext, anthropicMessages []AnthropicMessage, systemContent *AnthropicContent, isOutputMessage bool, keepToolsGrouped bool) []schemas.ResponsesMessage {
	return convertAnthropicMessagesToBifrostMessages(ctx, anthropicMessages, systemContent, isOutputMessage, keepToolsGrouped, false)
}

func convertAnthropicMessagesToBifrostMessages(ctx *schemas.BifrostContext, anthropicMessages []AnthropicMessage, systemContent *AnthropicContent, isOutputMessage bool, keepToolsGrouped bool, preserveThinkingOrder bool) []schemas.ResponsesMessage {
	var bifrostMessages []schemas.ResponsesMessage

	var structuredOutputToolName string
	if ctx != nil {
		if toolName, ok := ctx.Value(schemas.BifrostContextKeyStructuredOutputToolName).(string); ok {
			structuredOutputToolName = toolName
		}
	}

	if systemContent != nil {
		systemMessages := convertAnthropicSystemToBifrostMessages(systemContent)
		bifrostMessages = append(bifrostMessages, systemMessages...)
	}

	for _, msg := range anthropicMessages {
		var convertedMessages []schemas.ResponsesMessage
		if keepToolsGrouped {
			convertedMessages = convertSingleAnthropicMessageToBifrostMessagesGrouped(&msg, isOutputMessage, structuredOutputToolName)
		} else {
			convertedMessages = convertSingleAnthropicMessageToBifrostMessages(ctx, &msg, isOutputMessage, structuredOutputToolName, preserveThinkingOrder)
		}
		bifrostMessages = append(bifrostMessages, convertedMessages...)
	}

	return bifrostMessages
}

func ConvertBifrostMessagesToAnthropicMessages(ctx *schemas.BifrostContext, bifrostMessages []schemas.ResponsesMessage, isRequestMessage bool, caps schemas.ModelCaps) ([]AnthropicMessage, *AnthropicContent) {
	return convertBifrostMessagesToAnthropicMessages(ctx, bifrostMessages, isRequestMessage, caps, false)
}

func convertBifrostMessagesToAnthropicMessages(ctx *schemas.BifrostContext, bifrostMessages []schemas.ResponsesMessage, isRequestMessage bool, caps schemas.ModelCaps, bridgedFromChat bool) ([]AnthropicMessage, *AnthropicContent) {
	bifrostMessages = normalizeOpenAIToolSearchItems(ctx, bifrostMessages, isRequestMessage)

	if len(bifrostMessages) == 1 && bifrostMessages[0].Role != nil && (*bifrostMessages[0].Role == schemas.ResponsesInputMessageRoleSystem || *bifrostMessages[0].Role == schemas.ResponsesInputMessageRoleDeveloper) {
		if systemContent := convertBifrostMessageToAnthropicSystemContent(caps, &bifrostMessages[0]); systemContent != nil {
			return []AnthropicMessage{{
				Role:    AnthropicMessageRoleUser,
				Content: *systemContent,
			}}, nil
		}
	}

	seenConversation := false
	midConvSystemSupported := isRequestMessage && caps.SupportsMidConversationSystem(
		DefaultSupportsMidConversationSystem(caps.Provider(), caps.Model()))

	midConvOutputConfigSupported := isRequestMessage && supportsPerMessageOutputConfig(caps)

	inlineMidConvSystem := isRequestMessage

	var anthropicMessages []AnthropicMessage
	var systemContent *AnthropicContent
	var pendingToolCalls []AnthropicContentBlock
	var pendingToolResultBlocks []AnthropicContentBlock
	var pendingToolOutputTexts []AnthropicMessage
	var pendingReasoningContentBlocks []AnthropicContentBlock
	var currentAssistantMessage *AnthropicMessage

	type toolCallGroup struct {
		toolCallIDs map[string]bool
		flushed     bool
	}
	var toolCallGroups []toolCallGroup
	var currentToolCallIDs map[string]bool

	knownToolUseIDs := make(map[string]bool)

	toolsetNameByToolUseID := make(map[string]string)

	computerCallFillerIDs := make(map[string][]string)

	midConvPlacementOK := func(i int) bool {
		prev := len(anthropicMessages) - 1
		for prev >= 0 && anthropicMessages[prev].Role == AnthropicMessageRoleSystem {
			prev--
		}
		if prev < 0 {
			return false
		}
		switch anthropicMessages[prev].Role {
		case AnthropicMessageRoleUser:
		case AnthropicMessageRoleAssistant:

			if !endsInServerToolResult(anthropicMessages[prev].Content) {
				return false
			}
		default:
			return false
		}

		next := i + 1
		for next < len(bifrostMessages) && bifrostMessages[next].Role != nil &&
			(*bifrostMessages[next].Role == schemas.ResponsesInputMessageRoleSystem ||
				*bifrostMessages[next].Role == schemas.ResponsesInputMessageRoleDeveloper) {
			next++
		}
		if next == len(bifrostMessages) {
			return true
		}
		return bifrostMessages[next].Role != nil && *bifrostMessages[next].Role == schemas.ResponsesInputMessageRoleAssistant
	}

	hasConversationTurn := func() bool {
		for _, m := range anthropicMessages {
			if m.Role != AnthropicMessageRoleSystem {
				return true
			}
		}
		return false
	}

	appendOrphanToolResultsAsUserText := func(blocks []AnthropicContentBlock) {
		if len(blocks) == 0 {
			return
		}
		var textBlocks []AnthropicContentBlock
		for i := range blocks {
			text := toolResultBlockToText(&blocks[i])
			if text == "" {
				continue
			}
			textBlocks = append(textBlocks, AnthropicContentBlock{
				Type: AnthropicContentBlockTypeText,
				Text: schemas.Ptr(text),
			})
		}
		if len(textBlocks) > 0 {
			anthropicMessages = append(anthropicMessages, AnthropicMessage{
				Role:    AnthropicMessageRoleUser,
				Content: AnthropicContent{ContentBlocks: textBlocks},
			})
		}
	}

	flushPendingToolResults := func() {

		defer func() {
			anthropicMessages = append(anthropicMessages, pendingToolOutputTexts...)
			pendingToolOutputTexts = nil
		}()
		if len(pendingToolResultBlocks) == 0 {
			return
		}

		var matched []AnthropicContentBlock
		var orphaned []AnthropicContentBlock
		for _, block := range pendingToolResultBlocks {
			if block.ToolUseID != nil && knownToolUseIDs[*block.ToolUseID] {
				if block.ToolsetName == nil {
					if name, ok := toolsetNameByToolUseID[*block.ToolUseID]; ok {
						block.ToolsetName = &name
					}
				}
				matched = append(matched, block)
			} else {
				orphaned = append(orphaned, block)
			}
		}
		pendingToolResultBlocks = nil

		remaining := matched
		for i := range toolCallGroups {
			if toolCallGroups[i].flushed {
				continue
			}

			var groupResults []AnthropicContentBlock
			var rest []AnthropicContentBlock
			for _, block := range remaining {
				if block.ToolUseID != nil && toolCallGroups[i].toolCallIDs[*block.ToolUseID] {
					groupResults = append(groupResults, block)
				} else {
					rest = append(rest, block)
				}
			}

			if len(groupResults) > 0 {
				anthropicMessages = append(anthropicMessages, AnthropicMessage{
					Role:    AnthropicMessageRoleUser,
					Content: AnthropicContent{ContentBlocks: groupResults},
				})
				toolCallGroups[i].flushed = true
				remaining = rest
			}
		}

		if len(remaining) > 0 {
			anthropicMessages = append(anthropicMessages, AnthropicMessage{
				Role:    AnthropicMessageRoleUser,
				Content: AnthropicContent{ContentBlocks: remaining},
			})
		}

		appendOrphanToolResultsAsUserText(orphaned)
	}

	flushPendingToolCallsWithTracking := func() {
		if len(pendingToolCalls) > 0 && currentAssistantMessage != nil {

			copied := make([]AnthropicContentBlock, len(pendingToolCalls))
			copy(copied, pendingToolCalls)
			currentAssistantMessage.Content = AnthropicContent{
				ContentBlocks: copied,
			}
			anthropicMessages = append(anthropicMessages, *currentAssistantMessage)

			if len(currentToolCallIDs) > 0 {
				toolCallGroups = append(toolCallGroups, toolCallGroup{
					toolCallIDs: currentToolCallIDs,
					flushed:     false,
				})

				for id := range currentToolCallIDs {
					knownToolUseIDs[id] = true
				}
				currentToolCallIDs = nil
			}

			pendingToolCalls = nil
			currentAssistantMessage = nil
		}
	}

	trimIndex := len(bifrostMessages)
	if isRequestMessage && ctx.Value(schemas.BifrostContextKeySupportsAssistantPrefill) == false {
		for trimIndex > 0 {
			m := bifrostMessages[trimIndex-1]
			if m.Role != nil && *m.Role == schemas.ResponsesInputMessageRoleAssistant {
				trimIndex--
			} else {
				break
			}
		}
	}

	for i, msg := range bifrostMessages {

		msgType := schemas.ResponsesMessageTypeMessage
		if msg.Type != nil {
			msgType = *msg.Type
		}

		if isRequestMessage && i >= trimIndex {
			continue
		}

		switch msgType {
		case schemas.ResponsesMessageTypeMessage:

			flushPendingToolResults()

			flushPendingToolCallsWithTracking()

			if msg.Role != nil && (*msg.Role == schemas.ResponsesInputMessageRoleSystem || *msg.Role == schemas.ResponsesInputMessageRoleDeveloper) {

				if len(pendingReasoningContentBlocks) > 0 {
					copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
					copy(copied, pendingReasoningContentBlocks)
					anthropicMessages = append(anthropicMessages, AnthropicMessage{
						Role:    AnthropicMessageRoleAssistant,
						Content: AnthropicContent{ContentBlocks: copied},
					})
					pendingReasoningContentBlocks = nil
				}
				if !seenConversation && (hasConversationTurn() ||
					len(pendingToolCalls) > 0 ||
					len(pendingToolResultBlocks) > 0 ||
					currentAssistantMessage != nil) {
					seenConversation = true
				}
				content := convertBifrostMessageToAnthropicSystemContent(caps, &msg)
				if msg.IsEffortOnlySystemItem() {

					content = nil
				}

				perMessageOutputConfig := perMessageOutputConfigFor(&msg, midConvOutputConfigSupported)
				appendEffortOnly := func() {
					if perMessageOutputConfig != nil {
						anthropicMessages = append(anthropicMessages, AnthropicMessage{
							Role:         AnthropicMessageRoleSystem,
							Content:      AnthropicContent{ContentBlocks: []AnthropicContentBlock{}},
							OutputConfig: perMessageOutputConfig,
						})
					}
				}
				if content == nil {
					appendEffortOnly()
					continue
				}
				switch {
				case seenConversation && midConvSystemSupported && midConvPlacementOK(i):

					anthropicMessages = append(anthropicMessages, AnthropicMessage{
						Role:         AnthropicMessageRoleSystem,
						Content:      *content,
						OutputConfig: perMessageOutputConfig,
					})
				case seenConversation && inlineMidConvSystem:

					appendEffortOnly()
					if inlined := inlineMidConversationSystem(content); inlined != nil {
						anthropicMessages = append(anthropicMessages, *inlined)
					}
				default:

					systemContent = appendToSystemContent(systemContent, *content)
					appendEffortOnly()
				}
				continue
			}

			if len(pendingReasoningContentBlocks) > 0 && (msg.Role == nil || *msg.Role == schemas.ResponsesInputMessageRoleUser) {

				copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
				copy(copied, pendingReasoningContentBlocks)
				assistantReasoningMsg := AnthropicMessage{
					Role: AnthropicMessageRoleAssistant,
					Content: AnthropicContent{
						ContentBlocks: copied,
					},
				}
				anthropicMessages = append(anthropicMessages, assistantReasoningMsg)
				pendingReasoningContentBlocks = nil
			}

			anthropicMsg := convertBifrostMessageToAnthropicMessage(caps, &msg, &pendingReasoningContentBlocks)
			if anthropicMsg != nil {
				anthropicMessages = append(anthropicMessages, *anthropicMsg)

				for _, b := range anthropicMsg.Content.ContentBlocks {
					if (b.Type == AnthropicContentBlockTypeToolUse || b.Type == AnthropicContentBlockTypeServerToolUse) && b.ID != nil {
						knownToolUseIDs[*b.ID] = true
						if b.ToolsetName != nil {
							toolsetNameByToolUseID[*b.ID] = *b.ToolsetName
						}
					}
				}
				seenConversation = true
			}

		case schemas.ResponsesMessageTypeReasoning:

			flushPendingToolResults()

			reasoningBlocks := convertBifrostReasoningToAnthropicThinking(ctx, &msg, caps.Provider(), caps.Model(), bridgedFromChat)
			pendingReasoningContentBlocks = append(pendingReasoningContentBlocks, reasoningBlocks...)

		case schemas.ResponsesMessageTypeFunctionCall:

			flushPendingToolResults()

			if currentAssistantMessage == nil {
				currentAssistantMessage = &AnthropicMessage{
					Role: AnthropicMessageRoleAssistant,
				}
			}

			if len(pendingReasoningContentBlocks) > 0 {
				pendingToolCalls = append(pendingToolCalls, pendingReasoningContentBlocks...)
				pendingReasoningContentBlocks = nil
			}

			toolUseBlock := convertBifrostFunctionCallToAnthropicToolUse(ctx, &msg)
			if toolUseBlock != nil {

				if len(pendingToolCalls) == 0 && len(anthropicMessages) > 0 {
					lastMsgIdx := len(anthropicMessages) - 1
					lastMsg := &anthropicMessages[lastMsgIdx]

					if lastMsg.Role == AnthropicMessageRoleAssistant {
						hasToolUse := false
						for _, block := range lastMsg.Content.ContentBlocks {
							if block.Type == AnthropicContentBlockTypeToolUse {
								hasToolUse = true
								break
							}
						}

						if !hasToolUse {

							existingBlocks := lastMsg.Content.ContentBlocks
							existingBlocks = append(existingBlocks, *toolUseBlock)
							lastMsg.Content = AnthropicContent{
								ContentBlocks: existingBlocks,
							}

							if currentToolCallIDs == nil {
								currentToolCallIDs = make(map[string]bool)
							}
							if toolUseBlock.ID != nil {
								currentToolCallIDs[*toolUseBlock.ID] = true
								if toolUseBlock.ToolsetName != nil {
									toolsetNameByToolUseID[*toolUseBlock.ID] = *toolUseBlock.ToolsetName
								}
							}

							pendingToolCalls = lastMsg.Content.ContentBlocks
							anthropicMessages = anthropicMessages[:lastMsgIdx]
							currentAssistantMessage = lastMsg
							continue
						}
					}
				}

				pendingToolCalls = append(pendingToolCalls, *toolUseBlock)

				if currentToolCallIDs == nil {
					currentToolCallIDs = make(map[string]bool)
				}
				if toolUseBlock.ID != nil {
					currentToolCallIDs[*toolUseBlock.ID] = true
					if toolUseBlock.ToolsetName != nil {
						toolsetNameByToolUseID[*toolUseBlock.ID] = *toolUseBlock.ToolsetName
					}
				}
			}

		case schemas.ResponsesMessageTypeFunctionCallOutput:

			flushPendingToolCallsWithTracking()

			toolResultBlock := convertBifrostFunctionCallOutputToAnthropicToolResultBlock(&msg)
			if toolResultBlock != nil {
				pendingToolResultBlocks = append(pendingToolResultBlocks, *toolResultBlock)
			}

		case schemas.ResponsesMessageTypeItemReference:

			flushPendingToolResults()

			referenceMsg := convertBifrostItemReferenceToAnthropicMessage(&msg)
			if referenceMsg != nil {
				anthropicMessages = append(anthropicMessages, *referenceMsg)
			}

		case schemas.ResponsesMessageTypeComputerCall:

			flushPendingToolResults()

			if currentAssistantMessage == nil {
				currentAssistantMessage = &AnthropicMessage{
					Role: AnthropicMessageRoleAssistant,
				}
			}

			if len(pendingReasoningContentBlocks) > 0 {
				copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
				copy(copied, pendingReasoningContentBlocks)
				pendingToolCalls = append(copied, pendingToolCalls...)
				pendingReasoningContentBlocks = nil
			}

			computerToolUseBlocks := convertBifrostComputerCallToAnthropicToolUse(&msg)
			for i, computerToolUseBlock := range computerToolUseBlocks {
				pendingToolCalls = append(pendingToolCalls, computerToolUseBlock)

				if currentToolCallIDs == nil {
					currentToolCallIDs = make(map[string]bool)
				}
				if computerToolUseBlock.ID != nil {
					currentToolCallIDs[*computerToolUseBlock.ID] = true
					if last := computerToolUseBlocks[len(computerToolUseBlocks)-1]; i < len(computerToolUseBlocks)-1 && last.ID != nil {
						computerCallFillerIDs[*last.ID] = append(computerCallFillerIDs[*last.ID], *computerToolUseBlock.ID)
					}
				}
			}

		case schemas.ResponsesMessageTypeMCPCall:

			if msg.ResponsesToolMessage != nil {
				if msg.ResponsesToolMessage.Name != nil {

					flushPendingToolResults()

					if currentAssistantMessage == nil {
						currentAssistantMessage = &AnthropicMessage{
							Role: AnthropicMessageRoleAssistant,
						}
					}

					if len(pendingReasoningContentBlocks) > 0 {
						copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
						copy(copied, pendingReasoningContentBlocks)
						pendingToolCalls = append(copied, pendingToolCalls...)
						pendingReasoningContentBlocks = nil
					}

					mcpToolUseBlock := convertBifrostMCPCallToAnthropicToolUse(&msg)
					if mcpToolUseBlock != nil {
						pendingToolCalls = append(pendingToolCalls, *mcpToolUseBlock)

						if currentToolCallIDs == nil {
							currentToolCallIDs = make(map[string]bool)
						}
						if mcpToolUseBlock.ID != nil {
							currentToolCallIDs[*mcpToolUseBlock.ID] = true
						}
					}
				} else if msg.ResponsesToolMessage.CallID != nil {

					mcpToolResultBlock := convertBifrostMCPCallOutputToAnthropicToolResultBlock(&msg)
					if mcpToolResultBlock != nil {
						pendingToolResultBlocks = append(pendingToolResultBlocks, *mcpToolResultBlock)
					}
				}
			}

		case schemas.ResponsesMessageTypeMCPApprovalRequest:

			flushPendingToolResults()

			if currentAssistantMessage == nil {
				currentAssistantMessage = &AnthropicMessage{
					Role: AnthropicMessageRoleAssistant,
				}
			}

			if len(pendingReasoningContentBlocks) > 0 {
				copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
				copy(copied, pendingReasoningContentBlocks)
				pendingToolCalls = append(copied, pendingToolCalls...)
				pendingReasoningContentBlocks = nil
			}

			mcpApprovalBlock := convertBifrostMCPApprovalToAnthropicToolUse(&msg)
			if mcpApprovalBlock != nil {
				pendingToolCalls = append(pendingToolCalls, *mcpApprovalBlock)

				if currentToolCallIDs == nil {
					currentToolCallIDs = make(map[string]bool)
				}
				if mcpApprovalBlock.ID != nil {
					currentToolCallIDs[*mcpApprovalBlock.ID] = true
				}
			}

		case schemas.ResponsesMessageTypeWebSearchCall:

			flushPendingToolResults()

			webSearchBlocks := convertBifrostWebSearchCallToAnthropicBlocks(&msg)
			if len(webSearchBlocks) > 0 {

				if currentAssistantMessage == nil {
					currentAssistantMessage = &AnthropicMessage{
						Role: AnthropicMessageRoleAssistant,
					}
				}

				if len(pendingReasoningContentBlocks) > 0 {
					copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
					copy(copied, pendingReasoningContentBlocks)
					pendingToolCalls = append(copied, pendingToolCalls...)
					pendingReasoningContentBlocks = nil
				}

				pendingToolCalls = append(pendingToolCalls, webSearchBlocks...)

				if len(webSearchBlocks) > 0 && webSearchBlocks[0].ID != nil {
					if currentToolCallIDs == nil {
						currentToolCallIDs = make(map[string]bool)
					}
					currentToolCallIDs[*webSearchBlocks[0].ID] = true
				}
			}

		case schemas.ResponsesMessageTypeAdvisorCall:

			flushPendingToolResults()
			advisorBlocks := convertBifrostAdvisorCallToAnthropicBlocks(&msg)
			if len(advisorBlocks) > 0 {
				if currentAssistantMessage == nil {
					currentAssistantMessage = &AnthropicMessage{
						Role: AnthropicMessageRoleAssistant,
					}
				}
				if len(pendingReasoningContentBlocks) > 0 {
					copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
					copy(copied, pendingReasoningContentBlocks)
					pendingToolCalls = append(copied, pendingToolCalls...)
					pendingReasoningContentBlocks = nil
				}
				pendingToolCalls = append(pendingToolCalls, advisorBlocks...)
				if advisorBlocks[0].ID != nil {
					if currentToolCallIDs == nil {
						currentToolCallIDs = make(map[string]bool)
					}
					currentToolCallIDs[*advisorBlocks[0].ID] = true
				}
			}

		case schemas.ResponsesMessageTypeToolSearchCall:

			flushPendingToolResults()
			toolSearchBlocks := convertBifrostToolSearchCallToAnthropicBlocks(&msg)
			if len(toolSearchBlocks) > 0 {
				if currentAssistantMessage == nil {
					currentAssistantMessage = &AnthropicMessage{
						Role: AnthropicMessageRoleAssistant,
					}
				}
				if len(pendingReasoningContentBlocks) > 0 {
					copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
					copy(copied, pendingReasoningContentBlocks)
					pendingToolCalls = append(copied, pendingToolCalls...)
					pendingReasoningContentBlocks = nil
				}
				pendingToolCalls = append(pendingToolCalls, toolSearchBlocks...)
				if toolSearchBlocks[0].ID != nil {
					if currentToolCallIDs == nil {
						currentToolCallIDs = make(map[string]bool)
					}
					currentToolCallIDs[*toolSearchBlocks[0].ID] = true
				}
			}

		case schemas.ResponsesMessageTypeCodeInterpreterCall:

			flushPendingToolResults()
			codeExecBlocks := convertBifrostCodeExecCallToAnthropicBlocks(&msg)
			if len(codeExecBlocks) > 0 {
				if currentAssistantMessage == nil {
					currentAssistantMessage = &AnthropicMessage{
						Role: AnthropicMessageRoleAssistant,
					}
				}
				if len(pendingReasoningContentBlocks) > 0 {
					copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
					copy(copied, pendingReasoningContentBlocks)
					pendingToolCalls = append(copied, pendingToolCalls...)
					pendingReasoningContentBlocks = nil
				}
				pendingToolCalls = append(pendingToolCalls, codeExecBlocks...)
				if codeExecBlocks[0].ID != nil {
					if currentToolCallIDs == nil {
						currentToolCallIDs = make(map[string]bool)
					}
					currentToolCallIDs[*codeExecBlocks[0].ID] = true
				}
			}

		case schemas.ResponsesMessageTypeWebFetchCall:
			flushPendingToolResults()
			webFetchBlocks := convertBifrostWebFetchCallToAnthropicBlocks(&msg)
			if len(webFetchBlocks) > 0 {
				if currentAssistantMessage == nil {
					currentAssistantMessage = &AnthropicMessage{
						Role: AnthropicMessageRoleAssistant,
					}
				}
				if len(pendingReasoningContentBlocks) > 0 {
					copied := make([]AnthropicContentBlock, len(pendingReasoningContentBlocks))
					copy(copied, pendingReasoningContentBlocks)
					pendingToolCalls = append(copied, pendingToolCalls...)
					pendingReasoningContentBlocks = nil
				}
				pendingToolCalls = append(pendingToolCalls, webFetchBlocks...)
				if webFetchBlocks[0].ID != nil {
					if currentToolCallIDs == nil {
						currentToolCallIDs = make(map[string]bool)
					}
					currentToolCallIDs[*webFetchBlocks[0].ID] = true
				}
			}

		case schemas.ResponsesMessageTypeFileSearchCall,
			schemas.ResponsesMessageTypeLocalShellCall,
			schemas.ResponsesMessageTypeShellCall,
			schemas.ResponsesMessageTypeApplyPatchCall,
			schemas.ResponsesMessageTypeCustomToolCall,
			schemas.ResponsesMessageTypeImageGenerationCall:

			flushPendingToolResults()
			flushPendingToolCallsWithTracking()

			unsupportedToolMsg := convertBifrostUnsupportedToolCallToAnthropicMessage(&msg, msgType)
			if unsupportedToolMsg != nil {

				if len(pendingReasoningContentBlocks) > 0 {
					blocks := make([]AnthropicContentBlock, 0, len(pendingReasoningContentBlocks)+len(unsupportedToolMsg.Content.ContentBlocks))
					blocks = append(append(blocks, pendingReasoningContentBlocks...), unsupportedToolMsg.Content.ContentBlocks...)
					unsupportedToolMsg.Content.ContentBlocks = blocks
					pendingReasoningContentBlocks = nil
				}
				anthropicMessages = append(anthropicMessages, *unsupportedToolMsg)
			}

		case schemas.ResponsesMessageTypeComputerCallOutput:

			flushPendingToolCallsWithTracking()

			computerResultBlock := convertBifrostComputerCallOutputToAnthropicToolResultBlock(&msg)
			if computerResultBlock != nil {
				for _, fillerID := range computerCallFillerIDs[*computerResultBlock.ToolUseID] {
					pendingToolResultBlocks = append(pendingToolResultBlocks, AnthropicContentBlock{
						Type:      AnthropicContentBlockTypeToolResult,
						ToolUseID: schemas.Ptr(fillerID),
						Content:   &AnthropicContent{ContentStr: schemas.Ptr("ok")},
					})
				}
				pendingToolResultBlocks = append(pendingToolResultBlocks, *computerResultBlock)
			}

		case schemas.ResponsesMessageTypeLocalShellCallOutput,
			schemas.ResponsesMessageTypeShellCallOutput,
			schemas.ResponsesMessageTypeApplyPatchCallOutput,
			schemas.ResponsesMessageTypeCustomToolCallOutput:
			flushPendingToolCallsWithTracking()

			toolOutputMsg := convertBifrostToolOutputToAnthropicMessage(&msg)
			if toolOutputMsg != nil {
				pendingToolOutputTexts = append(pendingToolOutputTexts, *toolOutputMsg)
			}

		default:

			continue
		}
	}

	flushPendingToolResults()

	flushPendingToolCallsWithTracking()

	lastMsgIndex := len(anthropicMessages) - 1
	if isRequestMessage && lastMsgIndex >= 0 && anthropicMessages[lastMsgIndex].Role == AnthropicMessageRoleAssistant {
		blocks := anthropicMessages[lastMsgIndex].Content.ContentBlocks
		for j := len(blocks) - 1; j >= 0; j-- {
			if blocks[j].Type == AnthropicContentBlockTypeText && blocks[j].Text != nil {
				anthropicMessages[lastMsgIndex].Content.ContentBlocks[j].Text = schemas.Ptr(strings.TrimRight(*blocks[j].Text, " \n\r\t"))
				break
			}
		}
	}

	return anthropicMessages, systemContent
}

func convertAnthropicSystemToBifrostMessages(systemContent *AnthropicContent) []schemas.ResponsesMessage {
	var bifrostMessages []schemas.ResponsesMessage

	if systemContent.ContentStr != nil && *systemContent.ContentStr != "" {
		bifrostMessages = append(bifrostMessages, schemas.ResponsesMessage{
			Role: schemas.Ptr(schemas.ResponsesInputMessageRoleSystem),
			Content: &schemas.ResponsesMessageContent{
				ContentStr: systemContent.ContentStr,
			},
		})
	} else if systemContent.ContentBlocks != nil {
		contentBlocks := []schemas.ResponsesMessageContentBlock{}
		for _, block := range systemContent.ContentBlocks {
			if block.Text != nil {
				contentBlocks = append(contentBlocks, schemas.ResponsesMessageContentBlock{
					Type:         schemas.ResponsesInputMessageContentBlockTypeText,
					Text:         block.Text,
					CacheControl: block.CacheControl,
				})
			}
		}
		if len(contentBlocks) > 0 {
			bifrostMessages = append(bifrostMessages, schemas.ResponsesMessage{
				Role: schemas.Ptr(schemas.ResponsesInputMessageRoleSystem),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: contentBlocks,
				},
			})
		}
	}

	return bifrostMessages
}

func convertSingleAnthropicMessageToBifrostMessages(ctx *schemas.BifrostContext, msg *AnthropicMessage, isOutputMessage bool, structuredOutputToolName string, preserveThinkingOrder bool) []schemas.ResponsesMessage {

	isOutput := isOutputMessage || msg.Role == AnthropicMessageRoleAssistant

	if msg.Content.ContentStr != nil {
		roleVal := schemas.ResponsesMessageRoleType(msg.Role)
		bifrostMsg := schemas.ResponsesMessage{
			Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role: &roleVal,
			Content: &schemas.ResponsesMessageContent{
				ContentStr: msg.Content.ContentStr,
			},
		}
		if isOutput {

			bifrostMsg.Status = schemas.Ptr("completed")
		}
		return attachPerMessageEffort(msg, []schemas.ResponsesMessage{bifrostMsg})
	}

	if msg.Content.ContentBlocks != nil {
		roleVal := schemas.ResponsesMessageRoleType(msg.Role)
		return attachPerMessageEffort(msg, convertAnthropicContentBlocksToResponsesMessagesOrdered(ctx, msg.Content.ContentBlocks, &roleVal, isOutput, structuredOutputToolName, preserveThinkingOrder))
	}

	return attachPerMessageEffort(msg, nil)
}

func attachPerMessageEffort(msg *AnthropicMessage, converted []schemas.ResponsesMessage) []schemas.ResponsesMessage {
	if msg.Role != AnthropicMessageRoleSystem || msg.OutputConfig == nil || msg.OutputConfig.Effort == nil {
		return converted
	}
	if len(converted) == 0 {
		converted = []schemas.ResponsesMessage{{
			Type:    schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role:    schemas.Ptr(schemas.ResponsesInputMessageRoleSystem),
			Content: &schemas.ResponsesMessageContent{ContentBlocks: []schemas.ResponsesMessageContentBlock{}},
		}}
	}
	converted[0].OutputConfig = &schemas.ResponsesMessageOutputConfig{Effort: schemas.Ptr(*msg.OutputConfig.Effort)}
	return converted
}

func convertSingleAnthropicMessageToBifrostMessagesGrouped(msg *AnthropicMessage, isOutputMessage bool, structuredOutputToolName string) []schemas.ResponsesMessage {

	isOutput := isOutputMessage || msg.Role == AnthropicMessageRoleAssistant

	if msg.Content.ContentStr != nil {
		roleVal := schemas.ResponsesMessageRoleType(msg.Role)
		bifrostMsg := schemas.ResponsesMessage{
			Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
			Role: &roleVal,
			Content: &schemas.ResponsesMessageContent{
				ContentStr: msg.Content.ContentStr,
			},
		}
		if isOutput {

			bifrostMsg.Status = schemas.Ptr("completed")
		}
		return attachPerMessageEffort(msg, []schemas.ResponsesMessage{bifrostMsg})
	}

	if msg.Content.ContentBlocks != nil {
		roleVal := schemas.ResponsesMessageRoleType(msg.Role)
		return attachPerMessageEffort(msg, convertAnthropicContentBlocksToResponsesMessagesGrouped(msg.Content.ContentBlocks, &roleVal, isOutput))
	}

	return attachPerMessageEffort(msg, nil)
}

func anthropicToolUseBlockToResponsesMessage(toolBlock *AnthropicContentBlock, isOutputMessage bool) schemas.ResponsesMessage {
	bifrostMsg := schemas.ResponsesMessage{
		Type:         schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
		Status:       schemas.Ptr("completed"),
		CacheControl: toolBlock.CacheControl,
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			CallID: toolBlock.ID,
			Name:   toolBlock.Name,
		},
	}
	if isOutputMessage {
		bifrostMsg.ID = schemas.Ptr("fc_" + schemas.GetRandomString(50))
	}

	if toolBlock.Name != nil && *toolBlock.Name == string(AnthropicToolNameComputer) {
		bifrostMsg.Type = schemas.Ptr(schemas.ResponsesMessageTypeComputerCall)
		bifrostMsg.ResponsesToolMessage.Name = nil
		if isOutputMessage {

			bifrostMsg.ID = schemas.Ptr("cu_" + schemas.GetRandomString(50))
		}
		var inputMap map[string]interface{}
		if err := sonic.Unmarshal(toolBlock.Input, &inputMap); err == nil {
			setResponsesComputerCallAction(bifrostMsg.ResponsesToolMessage, convertAnthropicToResponsesComputerAction(inputMap))
		}
	} else if toolBlock.Type == AnthropicContentBlockTypeServerToolUse && toolBlock.Name != nil && *toolBlock.Name == string(AnthropicToolNameWebSearch) {

		bifrostMsg.Type = schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall)
		bifrostMsg.ResponsesToolMessage.Name = nil
		if q := providerUtils.GetJSONField(toolBlock.Input, "query"); q.Exists() && q.Type == gjson.String {
			query := q.Str
			bifrostMsg.ResponsesToolMessage.Action = &schemas.ResponsesToolMessageActionStruct{
				ResponsesWebSearchToolCallAction: &schemas.ResponsesWebSearchToolCallAction{
					Type:    "search",
					Query:   schemas.Ptr(query),
					Queries: []string{query},
				},
			}
		}
	} else if toolBlock.Type == AnthropicContentBlockTypeServerToolUse && toolBlock.Name != nil && *toolBlock.Name == string(AnthropicToolNameWebFetch) {
		bifrostMsg.Type = schemas.Ptr(schemas.ResponsesMessageTypeWebFetchCall)
		bifrostMsg.ResponsesToolMessage.Name = nil
		if u := providerUtils.GetJSONField(toolBlock.Input, "url"); u.Exists() && u.Type == gjson.String {
			bifrostMsg.ResponsesToolMessage.Action = &schemas.ResponsesToolMessageActionStruct{
				ResponsesWebFetchToolCallAction: &schemas.ResponsesWebFetchToolCallAction{
					URL: u.Str,
				},
			}
		}
	} else {
		if len(toolBlock.Input) > 0 {
			bifrostMsg.ResponsesToolMessage.Arguments = schemas.Ptr(string(toolBlock.Input))
		}
	}

	return bifrostMsg
}

func convertAnthropicContentBlocksToResponsesMessagesGrouped(contentBlocks []AnthropicContentBlock, role *schemas.ResponsesMessageRoleType, isOutputMessage bool) []schemas.ResponsesMessage {
	if item, ok := systemToolChangeItem(contentBlocks, role); ok {
		return item
	}
	var bifrostMessages []schemas.ResponsesMessage
	var accumulatedTextContent []schemas.ResponsesMessageContentBlock
	var pendingToolUseBlocks []*AnthropicContentBlock

	reasoningIndexByID := map[string]int{}

	flushAccumulatedText := func() {
		if len(accumulatedTextContent) == 0 {
			return
		}
		if isOutputMessage {
			bifrostMessages = append(bifrostMessages, schemas.ResponsesMessage{
				ID:     schemas.Ptr("msg_" + schemas.GetRandomString(50)),
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				Role:   role,
				Status: schemas.Ptr("completed"),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: accumulatedTextContent,
				},
			})
		}
		accumulatedTextContent = nil
	}
	flushPendingToolUses := func() {
		for _, toolBlock := range pendingToolUseBlocks {
			bifrostMessages = append(bifrostMessages, anthropicToolUseBlockToResponsesMessage(toolBlock, isOutputMessage))
		}
		pendingToolUseBlocks = nil
	}

	for _, block := range contentBlocks {

		if block.Type != AnthropicContentBlockTypeText {
			flushAccumulatedText()
		}
		switch block.Type {
		case AnthropicContentBlockTypeToolUse,
			AnthropicContentBlockTypeServerToolUse,
			AnthropicContentBlockTypeMCPToolUse:

		default:
			flushPendingToolUses()
		}

		switch block.Type {
		case AnthropicContentBlockTypeText:
			if block.Text != nil {
				if isOutputMessage {

					accumulatedTextContent = append(accumulatedTextContent, schemas.ResponsesMessageContentBlock{
						Type: schemas.ResponsesOutputMessageContentTypeText,
						Text: block.Text,
						ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
							LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
							Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
						},
					})
				} else {

					bifrostMsg := schemas.ResponsesMessage{
						Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
						Role: role,
						Content: &schemas.ResponsesMessageContent{
							ContentBlocks: []schemas.ResponsesMessageContentBlock{
								{
									Type:         schemas.ResponsesInputMessageContentBlockTypeText,
									Text:         block.Text,
									CacheControl: block.CacheControl,
								},
							},
						},
					}
					bifrostMessages = append(bifrostMessages, bifrostMsg)
				}
			}

		case AnthropicContentBlockTypeImage:

			if block.Source != nil && block.Source.SourceObj != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role: role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{block.toBifrostResponsesImageBlock()},
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
					bifrostMsg.Status = schemas.Ptr("completed")
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}

		case AnthropicContentBlockTypeDocument:

			if block.Source != nil && block.Source.SourceObj != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role: role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{block.toBifrostResponsesDocumentBlock()},
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
					bifrostMsg.Status = schemas.Ptr("completed")
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}

		case AnthropicContentBlockTypeContainerUpload:
			if block.FileID != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role: role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{block.toBifrostResponsesContainerUploadBlock()},
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
					bifrostMsg.Status = schemas.Ptr("completed")
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}

		case AnthropicContentBlockTypeThinking:
			if block.Thinking != nil {
				id := new("rs_" + schemas.GetRandomString(50))
				var recoveredID *string
				signature := block.Signature
				if signature != nil {
					if extractedID, rest, ok := providerUtils.ExtractReasoningItemID(*signature); ok {
						id = extractedID
						recoveredID = extractedID
						signature = &rest
					}
				}
				bifrostMsg := schemas.ResponsesMessage{
					ID:   id,
					Type: new(schemas.ResponsesMessageTypeReasoning),
					Role: role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{
							{
								Type:      schemas.ResponsesOutputMessageContentTypeReasoning,
								Text:      block.Thinking,
								Signature: signature,
							},
						},
					},
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
				if recoveredID != nil {
					reasoningIndexByID[*recoveredID] = len(bifrostMessages) - 1
				}
			}

		case AnthropicContentBlockTypeRedactedThinking:

			if block.Data != nil {
				encryptedContent := *block.Data
				id := new("rs_" + schemas.GetRandomString(50))
				var recoveredID *string
				if extractedID, rest, ok := providerUtils.ExtractReasoningItemID(*block.Data); ok {
					id = extractedID
					recoveredID = extractedID
					encryptedContent = rest
				}
				if recoveredID != nil {
					if idx, found := reasoningIndexByID[*recoveredID]; found {

						bifrostMessages[idx].ResponsesReasoning = &schemas.ResponsesReasoning{
							Summary:          []schemas.ResponsesReasoningSummary{},
							EncryptedContent: &encryptedContent,
						}
						continue
					}
				}
				bifrostMsg := schemas.ResponsesMessage{
					ID:   id,
					Type: new(schemas.ResponsesMessageTypeReasoning),
					ResponsesReasoning: &schemas.ResponsesReasoning{
						Summary:          []schemas.ResponsesReasoningSummary{},
						EncryptedContent: &encryptedContent,
					},
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
				if recoveredID != nil {
					reasoningIndexByID[*recoveredID] = len(bifrostMessages) - 1
				}
			}

		case AnthropicContentBlockTypeToolUse:

			if block.ID != nil && block.Name != nil {
				blockCopy := block
				pendingToolUseBlocks = append(pendingToolUseBlocks, &blockCopy)
			}

		case AnthropicContentBlockTypeToolResult:

			if block.ToolUseID != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type:         schemas.Ptr(schemas.ResponsesMessageTypeFunctionCallOutput),
					Status:       schemas.Ptr("completed"),
					CacheControl: block.CacheControl,
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:      block.ToolUseID,
						ToolsetName: block.ToolsetName,
					},
				}

				bifrostMsg.ResponsesToolMessage.Output = &schemas.ResponsesToolMessageOutputStruct{}

				if block.Content == nil {

				} else if block.Content.ContentStr != nil {
					bifrostMsg.ResponsesToolMessage.Output.ResponsesToolCallOutputStr = block.Content.ContentStr
				} else if block.Content.ContentBlocks != nil {
					var toolMsgContentBlocks []schemas.ResponsesMessageContentBlock
					for _, contentBlock := range block.Content.ContentBlocks {
						switch contentBlock.Type {
						case AnthropicContentBlockTypeText:
							if contentBlock.Text != nil {
								var blockType schemas.ResponsesMessageContentBlockType
								if isOutputMessage {
									blockType = schemas.ResponsesOutputMessageContentTypeText
								} else {
									blockType = schemas.ResponsesInputMessageContentBlockTypeText
								}
								toolMsgContentBlocks = append(toolMsgContentBlocks, schemas.ResponsesMessageContentBlock{
									Type:         blockType,
									Text:         contentBlock.Text,
									CacheControl: contentBlock.CacheControl,
								})
							}
						case AnthropicContentBlockTypeImage:
							if contentBlock.Source != nil && contentBlock.Source.SourceObj != nil {
								toolMsgContentBlocks = append(toolMsgContentBlocks, contentBlock.toBifrostResponsesImageBlock())
							}
						case AnthropicContentBlockTypeDocument:

							if contentBlock.Source != nil && contentBlock.Source.SourceObj != nil {
								toolMsgContentBlocks = append(toolMsgContentBlocks, contentBlock.toBifrostResponsesDocumentBlock())
							}
						case AnthropicContentBlockTypeToolReference:
							if contentBlock.ToolName != nil {
								toolMsgContentBlocks = append(toolMsgContentBlocks, schemas.ResponsesMessageContentBlock{
									Type:     schemas.ResponsesInputMessageContentBlockTypeToolReference,
									ToolName: contentBlock.ToolName,
								})
							}
						}
					}
					bifrostMsg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks = toolMsgContentBlocks
				}

				if block.IsError != nil && *block.IsError {
					bifrostMsg.Status = schemas.Ptr("incomplete")
				}

				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}

		case AnthropicContentBlockTypeServerToolUse:

			if block.ID != nil && block.Name != nil && isAnthropicToolSearchToolName(*block.Name) {

				flushPendingToolUses()
				bifrostMessages = append(bifrostMessages, schemas.ResponsesMessage{
					ID:     block.ID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeToolSearchCall),
					Status: schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:    block.ID,
						Name:      block.Name,
						Arguments: anthropicToolSearchArguments(block.Input),
					},
				})
				break
			}

			if block.ID != nil && block.Name != nil {
				blockCopy := block
				pendingToolUseBlocks = append(pendingToolUseBlocks, &blockCopy)
			}

		case AnthropicContentBlockTypeToolSearchToolResult:

			if block.ToolUseID != nil {
				attachToolSearchReferencesToCall(bifrostMessages, *block.ToolUseID, block)
			}

		case AnthropicContentBlockTypeMCPToolUse:

			if block.ID != nil && block.Name != nil {
				blockCopy := block
				pendingToolUseBlocks = append(pendingToolUseBlocks, &blockCopy)
			}

		case AnthropicContentBlockTypeMCPToolResult:

		case AnthropicContentBlockTypeWebSearchResult:

			if block.ToolUseID != nil {
				attachWebSearchSourcesToCall(bifrostMessages, *block.ToolUseID, block, true)
			}
		}
	}

	flushAccumulatedText()
	flushPendingToolUses()
	return bifrostMessages
}

func isToolChangeBlock(t AnthropicContentBlockType) bool {
	return t == AnthropicContentBlockTypeToolAddition || t == AnthropicContentBlockTypeToolRemoval
}

func endsInServerToolResult(content AnthropicContent) bool {
	blocks := content.ContentBlocks
	if len(blocks) == 0 {
		return false
	}
	switch blocks[len(blocks)-1].Type {
	case AnthropicContentBlockTypeWebSearchToolResult,
		AnthropicContentBlockTypeWebFetchToolResult,
		AnthropicContentBlockTypeCodeExecutionToolResult,
		AnthropicContentBlockTypeBashCodeExecutionToolResult,
		AnthropicContentBlockTypeTextEditorCodeExecutionToolResult,
		AnthropicContentBlockTypeToolSearchToolResult,
		AnthropicContentBlockTypeAdvisorToolResult,
		AnthropicContentBlockTypeMCPToolResult:
		return true
	}
	return false
}

func systemToolChangeItem(contentBlocks []AnthropicContentBlock, role *schemas.ResponsesMessageRoleType) ([]schemas.ResponsesMessage, bool) {
	if role == nil || *role != schemas.ResponsesInputMessageRoleSystem {
		return nil, false
	}
	hasToolChange := false
	for _, block := range contentBlocks {
		if isToolChangeBlock(block.Type) {
			hasToolChange = true
			break
		}
	}
	if !hasToolChange {
		return nil, false
	}
	blocks := make([]schemas.ResponsesMessageContentBlock, 0, len(contentBlocks))
	for _, block := range contentBlocks {
		switch block.Type {
		case AnthropicContentBlockTypeToolAddition, AnthropicContentBlockTypeToolRemoval:
			if converted := convertAnthropicToolChangeBlockToBifrost(block); converted != nil {
				blocks = append(blocks, *converted)
			}
		case AnthropicContentBlockTypeText:
			if block.Text != nil {
				blocks = append(blocks, schemas.ResponsesMessageContentBlock{
					Type:         schemas.ResponsesInputMessageContentBlockTypeText,
					Text:         block.Text,
					CacheControl: block.CacheControl,
				})
			}
		}
	}
	if len(blocks) == 0 {
		return nil, true
	}
	return []schemas.ResponsesMessage{{
		Type:    schemas.Ptr(schemas.ResponsesMessageTypeMessage),
		Role:    role,
		Content: &schemas.ResponsesMessageContent{ContentBlocks: blocks},
	}}, true
}

func convertAnthropicToolChangeBlockToBifrost(block AnthropicContentBlock) *schemas.ResponsesMessageContentBlock {
	if block.Tool == nil {
		return nil
	}
	var blockType schemas.ResponsesMessageContentBlockType
	switch block.Type {
	case AnthropicContentBlockTypeToolAddition:
		blockType = schemas.ResponsesInputMessageContentBlockTypeToolAddition
	case AnthropicContentBlockTypeToolRemoval:
		blockType = schemas.ResponsesInputMessageContentBlockTypeToolRemoval
	default:
		return nil
	}
	target := &schemas.ResponsesToolChangeTarget{
		Type:       block.Tool.Type,
		Name:       block.Tool.Name,
		ServerName: block.Tool.ServerName,
	}
	if block.Tool.Definition != nil {
		target.Definition = convertInlineToolDefinitionToBifrost(block.Tool.Definition)
	}
	return &schemas.ResponsesMessageContentBlock{
		Type:         blockType,
		CacheControl: block.CacheControl,
		ToolChange:   target,
	}
}

func convertInlineToolDefinitionToBifrost(tool *AnthropicTool) *schemas.ResponsesTool {
	if tool == nil {
		return nil
	}
	if tool.MCPToolset != nil {
		bifrostTool := &schemas.ResponsesTool{
			Type:             schemas.ResponsesToolTypeMCP,
			ResponsesToolMCP: &schemas.ResponsesToolMCP{ServerLabel: tool.MCPToolset.MCPServerName},
		}
		applyMCPToolsetConfigToBifrostTool(bifrostTool, tool.MCPToolset)
		return bifrostTool
	}
	bifrostTool := convertAnthropicToolToBifrost(tool)
	if bifrostTool != nil {
		applyAnthropicToolFlagsToResponsesTool(tool, bifrostTool)
	}
	return bifrostTool
}

func convertAnthropicToolChangesToBifrost(changes []AnthropicContentBlock) []schemas.ResponsesMessageContentBlock {
	if len(changes) == 0 {
		return nil
	}
	out := make([]schemas.ResponsesMessageContentBlock, 0, len(changes))
	for _, change := range changes {
		if converted := convertAnthropicToolChangeBlockToBifrost(change); converted != nil {
			out = append(out, *converted)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func convertToolChangeTargetToAnthropic(caps schemas.ModelCaps, target *schemas.ResponsesToolChangeTarget) *AnthropicToolChangeTarget {
	if target == nil {
		return nil
	}
	out := &AnthropicToolChangeTarget{
		Type:       target.Type,
		Name:       target.Name,
		ServerName: target.ServerName,
	}
	if target.Definition != nil {
		out.Definition = convertInlineToolDefinitionToAnthropic(caps, target.Definition)
	}
	return out
}

func convertInlineToolDefinitionToAnthropic(caps schemas.ModelCaps, tool *schemas.ResponsesTool) *AnthropicTool {
	if tool == nil {
		return nil
	}
	var out *AnthropicTool
	if tool.Type == schemas.ResponsesToolTypeMCP && tool.ResponsesToolMCP != nil {
		if _, toolset := convertBifrostMCPToolToAnthropicNew(tool); toolset != nil {
			out = &AnthropicTool{MCPToolset: toolset}
		}
	} else {

		if normalized, err := normalizeInlineFunctionTool(tool); err == nil {
			tool = normalized
		}
		out = convertBifrostToolToAnthropic(caps, tool, caps.Provider(), false)
	}
	if out != nil {
		applyResponsesToolAnthropicFlags(out, tool, "")
	}
	return out
}

func normalizeInlineFunctionTool(tool *schemas.ResponsesTool) (*schemas.ResponsesTool, error) {
	if tool == nil || tool.ResponsesToolFunction == nil || tool.ResponsesToolFunction.Parameters == nil {
		return tool, nil
	}
	normalized, err := NormalizeAnthropicToolInputSchema(tool.ResponsesToolFunction.Parameters)
	if err != nil {
		return nil, err
	}
	if normalized == tool.ResponsesToolFunction.Parameters {
		return tool, nil
	}
	toolCopy := *tool
	functionCopy := *tool.ResponsesToolFunction
	functionCopy.Parameters = normalized
	toolCopy.ResponsesToolFunction = &functionCopy
	return &toolCopy, nil
}

func prepareInlineToolDefinitions(input []schemas.ResponsesMessage, tools []schemas.ResponsesTool) ([]schemas.ResponsesMessage, error) {
	needsCaller := false
	var validate func(blocks []schemas.ResponsesMessageContentBlock) error
	validate = func(blocks []schemas.ResponsesMessageContentBlock) error {
		for i := range blocks {
			block := &blocks[i]
			if block.ToolChange != nil && block.ToolChange.Definition != nil {
				if _, err := normalizeInlineFunctionTool(block.ToolChange.Definition); err != nil {
					return err
				}
				if hasProgrammaticCaller(block.ToolChange.Definition.AllowedCallers) {
					needsCaller = true
				}
			}
			if cmp := block.ResponsesOutputMessageContentCompaction; cmp != nil && len(cmp.ToolChanges) > 0 {
				if err := validate(cmp.ToolChanges); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for i := range input {
		if input[i].Content == nil {
			continue
		}
		if err := validate(input[i].Content.ContentBlocks); err != nil {
			return nil, err
		}
	}
	if !needsCaller {
		return input, nil
	}

	sentTools := tools
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeWebSearch || tool.Type == schemas.ResponsesToolTypeWebFetch {
			sentTools = slices.DeleteFunc(slices.Clone(tools), func(t schemas.ResponsesTool) bool {
				return t.Type == schemas.ResponsesToolTypeCodeInterpreter
			})
			break
		}
	}
	declaredVersion, hasCodeExecution := declaredCodeExecutionVersion(sentTools)
	caller, emitVersion := resolveAnthropicProgrammaticCaller(declaredVersion, hasCodeExecution)
	if caller == "" {
		return nil, fmt.Errorf("code execution version %q has no allowed_callers value, so a tool added mid-conversation and restricted to programmatic callers cannot be expressed; declare code_execution_20250825 or newer", declaredVersion)
	}
	if emitVersion != "" {

		return nil, fmt.Errorf("a tool added mid-conversation is restricted to programmatic callers, which needs code execution version %s; declare it on the code_interpreter tool", emitVersion)
	}

	rewritten := slices.Clone(input)
	for i := range rewritten {
		if rewritten[i].Content == nil {
			continue
		}
		changed := false
		for _, block := range rewritten[i].Content.ContentBlocks {
			if block.ToolChange != nil && block.ToolChange.Definition != nil && hasProgrammaticCaller(block.ToolChange.Definition.AllowedCallers) {
				changed = true
				break
			}
		}
		if !changed {
			continue
		}
		msg := schemas.DeepCopyResponsesMessage(rewritten[i])
		for j := range msg.Content.ContentBlocks {
			block := &msg.Content.ContentBlocks[j]
			if block.ToolChange == nil || block.ToolChange.Definition == nil || !hasProgrammaticCaller(block.ToolChange.Definition.AllowedCallers) {
				continue
			}
			block.ToolChange.Definition.AllowedCallers = anthropicAllowedCallers(block.ToolChange.Definition.AllowedCallers, caller)
		}
		rewritten[i] = msg
	}
	return rewritten, nil
}

func convertAnthropicContentBlocksToResponsesMessagesOrdered(ctx *schemas.BifrostContext, contentBlocks []AnthropicContentBlock, role *schemas.ResponsesMessageRoleType, isOutputMessage bool, structuredOutputToolName string, preserveThinkingOrder bool) []schemas.ResponsesMessage {
	if item, ok := systemToolChangeItem(contentBlocks, role); ok {
		return item
	}
	var bifrostMessages []schemas.ResponsesMessage
	var reasoningContentBlocks []schemas.ResponsesMessageContentBlock

	var reasoningItemID *string

	var reasoningEncryptedContent *string

	flushReasoning := func() {
		if len(reasoningContentBlocks) == 0 {
			return
		}
		id := reasoningItemID
		if id == nil {
			id = new("rs_" + schemas.GetRandomString(50))
		}
		bifrostMessages = append(bifrostMessages, schemas.ResponsesMessage{
			ID:   id,
			Type: new(schemas.ResponsesMessageTypeReasoning),
			ResponsesReasoning: &schemas.ResponsesReasoning{
				Summary:          []schemas.ResponsesReasoningSummary{},
				EncryptedContent: reasoningEncryptedContent,
			},
			Content: &schemas.ResponsesMessageContent{
				ContentBlocks: reasoningContentBlocks,
			},
		})
		reasoningContentBlocks = nil
		reasoningItemID = nil
		reasoningEncryptedContent = nil
	}

	for _, block := range contentBlocks {
		if preserveThinkingOrder && block.Type != AnthropicContentBlockTypeThinking && block.Type != AnthropicContentBlockTypeRedactedThinking {
			flushReasoning()
		}
		switch block.Type {
		case AnthropicContentBlockTypeCompaction:
			if block.Content != nil {
				var summaryText string
				if block.Content.ContentStr != nil {
					summaryText = *block.Content.ContentStr
				}

				bifrostMsg := schemas.ResponsesMessage{
					ID:     schemas.Ptr("cmp_" + schemas.GetRandomString(50)),
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role:   role,
					Status: schemas.Ptr("completed"),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{
							{
								Type:         schemas.ResponsesOutputMessageContentTypeCompaction,
								CacheControl: block.CacheControl,
								ResponsesOutputMessageContentCompaction: &schemas.ResponsesOutputMessageContentCompaction{
									Summary:     summaryText,
									ToolChanges: convertAnthropicToolChangesToBifrost(block.ToolChanges),
								},
							},
						},
					},
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		case AnthropicContentBlockTypeFallback:
			fallback := &schemas.ResponsesOutputMessageContentFallback{}
			if block.From != nil {
				fallback.FromModel = block.From.Model
			}
			if block.To != nil {
				fallback.ToModel = block.To.Model
			}
			if block.Trigger != nil {
				fallback.TriggerType = block.Trigger.Type
				fallback.TriggerCategory = block.Trigger.Category
			}
			bifrostMessages = append(bifrostMessages, schemas.ResponsesMessage{
				ID:     schemas.Ptr("fb_" + schemas.GetRandomString(50)),
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
				Role:   role,
				Status: schemas.Ptr("completed"),
				Content: &schemas.ResponsesMessageContent{
					ContentBlocks: []schemas.ResponsesMessageContentBlock{
						{
							Type:                                  schemas.ResponsesOutputMessageContentTypeFallback,
							ResponsesOutputMessageContentFallback: fallback,
						},
					},
				},
			})
		case AnthropicContentBlockTypeText:
			if block.Text != nil {
				var bifrostMsg schemas.ResponsesMessage
				if isOutputMessage {

					contentBlock := schemas.ResponsesMessageContentBlock{
						Type:         schemas.ResponsesOutputMessageContentTypeText,
						Text:         block.Text,
						CacheControl: block.CacheControl,
						ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
							LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
							Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
						},
					}

					if block.Citations != nil && len(block.Citations.TextCitations) > 0 {
						annotations := make([]schemas.ResponsesOutputMessageContentTextAnnotation, len(block.Citations.TextCitations))
						fullText := ""
						if block.Text != nil {
							fullText = *block.Text
						}
						for i, citation := range block.Citations.TextCitations {
							annotations[i] = convertAnthropicCitationToAnnotation(citation, fullText)
						}

						contentBlock.ResponsesOutputMessageContentText = &schemas.ResponsesOutputMessageContentText{
							Annotations: annotations,
						}
					}

					bifrostMsg = schemas.ResponsesMessage{
						ID:     schemas.Ptr("msg_" + schemas.GetRandomString(50)),
						Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
						Role:   role,
						Status: schemas.Ptr("completed"),
						Content: &schemas.ResponsesMessageContent{
							ContentBlocks: []schemas.ResponsesMessageContentBlock{contentBlock},
						},
					}
				} else {

					bifrostMsg = schemas.ResponsesMessage{
						Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
						Role: role,
						Content: &schemas.ResponsesMessageContent{
							ContentBlocks: []schemas.ResponsesMessageContentBlock{
								{
									Type:         schemas.ResponsesInputMessageContentBlockTypeText,
									Text:         block.Text,
									CacheControl: block.CacheControl,
								},
							},
						},
					}
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		case AnthropicContentBlockTypeImage:
			if block.Source != nil && block.Source.SourceObj != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role: role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{block.toBifrostResponsesImageBlock()},
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
					bifrostMsg.Status = schemas.Ptr("completed")
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		case AnthropicContentBlockTypeDocument:
			if block.Source != nil && block.Source.SourceObj != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role: role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{block.toBifrostResponsesDocumentBlock()},
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
					bifrostMsg.Status = schemas.Ptr("completed")
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		case AnthropicContentBlockTypeContainerUpload:
			if block.FileID != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role: role,
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{block.toBifrostResponsesContainerUploadBlock()},
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
					bifrostMsg.Status = schemas.Ptr("completed")
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		case AnthropicContentBlockTypeThinking:
			if block.Thinking != nil {

				signature := block.Signature
				if signature != nil {
					if id, rest, ok := providerUtils.ExtractReasoningItemID(*signature); ok {
						if reasoningItemID == nil {
							reasoningItemID = id
						}
						signature = &rest
					}
				}
				reasoningContentBlocks = append(reasoningContentBlocks, schemas.ResponsesMessageContentBlock{
					Type:      schemas.ResponsesOutputMessageContentTypeReasoning,
					Text:      block.Thinking,
					Signature: signature,
				})
			}
		case AnthropicContentBlockTypeRedactedThinking:
			if block.Data != nil {
				encryptedContent := *block.Data
				var extractedID *string
				if id, rest, ok := providerUtils.ExtractReasoningItemID(*block.Data); ok {
					extractedID = id
					encryptedContent = rest
				}
				if extractedID != nil && reasoningItemID != nil && *extractedID == *reasoningItemID {

					reasoningEncryptedContent = &encryptedContent
					continue
				}
				if preserveThinkingOrder {
					flushReasoning()
				}
				id := extractedID
				if id == nil {
					id = new("rs_" + schemas.GetRandomString(50))
				}
				bifrostMsg := schemas.ResponsesMessage{
					ID:   id,
					Type: new(schemas.ResponsesMessageTypeReasoning),
					ResponsesReasoning: &schemas.ResponsesReasoning{
						Summary:          []schemas.ResponsesReasoningSummary{},
						EncryptedContent: &encryptedContent,
					},
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		case AnthropicContentBlockTypeToolUse:

			if structuredOutputToolName != "" && block.Name != nil && *block.Name == structuredOutputToolName {

				var jsonStr string
				if block.Input != nil {
					jsonStr = string(block.Input)
				} else {
					jsonStr = "{}"
				}

				contentBlock := schemas.ResponsesMessageContentBlock{
					Type: schemas.ResponsesOutputMessageContentTypeText,
					Text: &jsonStr,
					ResponsesOutputMessageContentText: &schemas.ResponsesOutputMessageContentText{
						LogProbs:    []schemas.ResponsesOutputMessageContentTextLogProb{},
						Annotations: []schemas.ResponsesOutputMessageContentTextAnnotation{},
					},
				}

				bifrostMsg := schemas.ResponsesMessage{
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeMessage),
					Role:   role,
					Status: schemas.Ptr("completed"),
					Content: &schemas.ResponsesMessageContent{
						ContentBlocks: []schemas.ResponsesMessageContentBlock{contentBlock},
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			} else {

				if block.ID != nil && block.Name != nil {
					bifrostMsg := schemas.ResponsesMessage{
						Type:         schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
						Status:       schemas.Ptr("completed"),
						CacheControl: block.CacheControl,
						ResponsesToolMessage: &schemas.ResponsesToolMessage{
							CallID:      block.ID,
							Name:        block.Name,
							ToolsetName: block.ToolsetName,
						},
					}
					if isOutputMessage {
						bifrostMsg.ID = schemas.Ptr("fc_" + schemas.GetRandomString(50))
					}

					if block.Name != nil && *block.Name == string(AnthropicToolNameComputer) {
						bifrostMsg.Type = schemas.Ptr(schemas.ResponsesMessageTypeComputerCall)
						bifrostMsg.ResponsesToolMessage.Name = nil
						if isOutputMessage {

							bifrostMsg.ID = schemas.Ptr("cu_" + schemas.GetRandomString(50))
						}
						var inputMap map[string]interface{}
						if err := sonic.Unmarshal(block.Input, &inputMap); err == nil {
							setResponsesComputerCallAction(bifrostMsg.ResponsesToolMessage, convertAnthropicToResponsesComputerAction(inputMap))
						}
					} else if len(block.Input) > 0 {
						bifrostMsg.ResponsesToolMessage.Arguments = schemas.Ptr(string(block.Input))
					}
					bifrostMessages = append(bifrostMessages, bifrostMsg)
				}
			}
		case AnthropicContentBlockTypeToolResult:

			if block.ToolUseID != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type:         schemas.Ptr(schemas.ResponsesMessageTypeFunctionCallOutput),
					Status:       schemas.Ptr("completed"),
					CacheControl: block.CacheControl,
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:      block.ToolUseID,
						ToolsetName: block.ToolsetName,
					},
				}

				bifrostMsg.ResponsesToolMessage.Output = &schemas.ResponsesToolMessageOutputStruct{}

				if block.Content == nil {

				} else if block.Content.ContentStr != nil {
					bifrostMsg.ResponsesToolMessage.Output.ResponsesToolCallOutputStr = block.Content.ContentStr
				} else if block.Content.ContentBlocks != nil {
					var toolMsgContentBlocks []schemas.ResponsesMessageContentBlock
					for _, contentBlock := range block.Content.ContentBlocks {
						switch contentBlock.Type {
						case AnthropicContentBlockTypeText:
							if contentBlock.Text != nil {
								var blockType schemas.ResponsesMessageContentBlockType
								if isOutputMessage {
									blockType = schemas.ResponsesOutputMessageContentTypeText
								} else {
									blockType = schemas.ResponsesInputMessageContentBlockTypeText
								}
								toolMsgContentBlocks = append(toolMsgContentBlocks, schemas.ResponsesMessageContentBlock{
									Type:         blockType,
									Text:         contentBlock.Text,
									CacheControl: contentBlock.CacheControl,
								})
							}
						case AnthropicContentBlockTypeImage:
							if contentBlock.Source != nil && contentBlock.Source.SourceObj != nil {
								toolMsgContentBlocks = append(toolMsgContentBlocks, contentBlock.toBifrostResponsesImageBlock())
							}
						case AnthropicContentBlockTypeDocument:

							if contentBlock.Source != nil && contentBlock.Source.SourceObj != nil {
								toolMsgContentBlocks = append(toolMsgContentBlocks, contentBlock.toBifrostResponsesDocumentBlock())
							}
						case AnthropicContentBlockTypeToolReference:
							if contentBlock.ToolName != nil {
								toolMsgContentBlocks = append(toolMsgContentBlocks, schemas.ResponsesMessageContentBlock{
									Type:     schemas.ResponsesInputMessageContentBlockTypeToolReference,
									ToolName: contentBlock.ToolName,
								})
							}
						}
					}
					bifrostMsg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks = toolMsgContentBlocks
				}

				if block.IsError != nil && *block.IsError {
					bifrostMsg.Status = schemas.Ptr("incomplete")
				}

				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}

		case AnthropicContentBlockTypeServerToolUse:

			if block.Name != nil && *block.Name == string(AnthropicToolNameWebSearch) {
				bifrostMsg := schemas.ResponsesMessage{
					Type:                 schemas.Ptr(schemas.ResponsesMessageTypeWebSearchCall),
					Status:               schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{},
				}

				if block.Caller != nil {
					bifrostMsg.ResponsesToolMessage.Caller = &schemas.ResponsesToolCaller{
						Type:   string(block.Caller.Type),
						ToolID: block.Caller.ToolID,
					}
				}

				if block.Input != nil {
					if q := providerUtils.GetJSONField(block.Input, "query"); q.Exists() && q.Type == gjson.String {
						query := q.Str
						bifrostMsg.ResponsesToolMessage.Action = &schemas.ResponsesToolMessageActionStruct{
							ResponsesWebSearchToolCallAction: &schemas.ResponsesWebSearchToolCallAction{
								Type:    "search",
								Query:   schemas.Ptr(query),
								Queries: []string{query},
							},
						}
					}
				}

				if isOutputMessage {
					bifrostMsg.ID = block.ID
					bifrostMessages = append(bifrostMessages, bifrostMsg)
				}
			} else if block.Name != nil && *block.Name == string(AnthropicToolNameWebFetch) {
				bifrostMsg := schemas.ResponsesMessage{
					Type:                 schemas.Ptr(schemas.ResponsesMessageTypeWebFetchCall),
					Status:               schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{CallID: block.ID},
				}

				if block.Caller != nil {
					bifrostMsg.ResponsesToolMessage.Caller = &schemas.ResponsesToolCaller{
						Type:   string(block.Caller.Type),
						ToolID: block.Caller.ToolID,
					}
				}

				if block.Input != nil {
					if u := providerUtils.GetJSONField(block.Input, "url"); u.Exists() && u.Type == gjson.String {
						bifrostMsg.ResponsesToolMessage.Action = &schemas.ResponsesToolMessageActionStruct{
							ResponsesWebFetchToolCallAction: &schemas.ResponsesWebFetchToolCallAction{
								URL: u.Str,
							},
						}
					}
				}

				if isOutputMessage {
					bifrostMsg.ID = block.ID
					bifrostMessages = append(bifrostMessages, bifrostMsg)
				}
			} else if block.Name != nil && *block.Name == string(AnthropicToolNameAdvisor) {

				bifrostMsg := schemas.ResponsesMessage{
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeAdvisorCall),
					ID:     block.ID,
					Status: schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						Name:   schemas.Ptr(string(AnthropicToolNameAdvisor)),
						CallID: block.ID,
					},
				}
				if isOutputMessage {
					bifrostMessages = append(bifrostMessages, bifrostMsg)
				}
			} else if block.Name != nil && isAnthropicCodeExecutionToolName(*block.Name) {

				if isOutputMessage {
					bifrostMessages = append(bifrostMessages, buildBifrostCodeExecutionCall(block))
				}
			} else if block.Name != nil && isAnthropicToolSearchToolName(*block.Name) {

				bifrostMessages = append(bifrostMessages, schemas.ResponsesMessage{
					ID:     block.ID,
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeToolSearchCall),
					Status: schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID:    block.ID,
						Name:      block.Name,
						Arguments: anthropicToolSearchArguments(block.Input),
					},
				})
			}

		case AnthropicContentBlockTypeCodeExecutionToolResult,
			AnthropicContentBlockTypeBashCodeExecutionToolResult,
			AnthropicContentBlockTypeTextEditorCodeExecutionToolResult:

			if block.ToolUseID != nil {
				attachAnthropicCodeExecutionResult(bifrostMessages, *block.ToolUseID, block)
			}

		case AnthropicContentBlockTypeAdvisorToolResult:

			if block.ToolUseID != nil {
				for i := len(bifrostMessages) - 1; i >= 0; i-- {
					msg := &bifrostMessages[i]
					if msg.Type == nil || *msg.Type != schemas.ResponsesMessageTypeAdvisorCall {
						continue
					}
					if msg.ResponsesToolMessage == nil || msg.ResponsesToolMessage.CallID == nil ||
						*msg.ResponsesToolMessage.CallID != *block.ToolUseID {
						continue
					}
					advisor := &schemas.ResponsesAdvisorCall{}

					if c := block.Content; c != nil {
						inner := c.ContentObj
						if inner == nil && len(c.ContentBlocks) > 0 {
							inner = &c.ContentBlocks[0]
						}
						if inner != nil {
							advisor.ResultType = string(inner.Type)
							advisor.Text = inner.Text
							advisor.EncryptedContent = inner.EncryptedContent
							advisor.ErrorCode = inner.ErrorCode
							advisor.StopReason = inner.StopReason
						}
					}
					msg.ResponsesToolMessage.ResponsesAdvisorCall = advisor
					break
				}
			}

		case AnthropicContentBlockTypeWebSearchToolResult:

			if block.ToolUseID != nil {
				attachWebSearchSourcesToCall(bifrostMessages, *block.ToolUseID, block, true)
			}

		case AnthropicContentBlockTypeToolSearchToolResult:

			if block.ToolUseID != nil {
				attachToolSearchReferencesToCall(bifrostMessages, *block.ToolUseID, block)
			}

		case AnthropicContentBlockTypeWebFetchToolResult:
			if block.ToolUseID != nil {
				attachAnthropicWebFetchResult(bifrostMessages, *block.ToolUseID, block)
			}

		case AnthropicContentBlockTypeWebSearchToolResultError:

			if block.ToolUseID != nil {
				for i := len(bifrostMessages) - 1; i >= 0; i-- {
					msg := &bifrostMessages[i]
					if msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeWebSearchCall &&
						msg.ID != nil && *msg.ID == *block.ToolUseID {
						msg.Status = schemas.Ptr("failed")
						break
					}
				}
			}

		case AnthropicContentBlockTypeMCPToolUse:

			if block.ID != nil && block.Name != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type: schemas.Ptr(schemas.ResponsesMessageTypeMCPCall),
					ID:   block.ID,
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						Name: block.Name,
					},
				}
				if len(block.Input) > 0 {
					bifrostMsg.ResponsesToolMessage.Arguments = schemas.Ptr(string(block.Input))
				}
				if block.ServerName != nil {
					bifrostMsg.ResponsesToolMessage.ServerLabel = schemas.Ptr(*block.ServerName)
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		case AnthropicContentBlockTypeMCPToolResult:

			if block.ToolUseID != nil {
				bifrostMsg := schemas.ResponsesMessage{
					Type:   schemas.Ptr(schemas.ResponsesMessageTypeMCPCall),
					Status: schemas.Ptr("completed"),
					ResponsesToolMessage: &schemas.ResponsesToolMessage{
						CallID: block.ToolUseID,
					},
				}
				if isOutputMessage {
					bifrostMsg.ID = schemas.Ptr("msg_" + schemas.GetRandomString(50))
				}

				bifrostMsg.ResponsesToolMessage.Output = &schemas.ResponsesToolMessageOutputStruct{}

				if block.Content != nil {
					if block.Content.ContentStr != nil {
						bifrostMsg.ResponsesToolMessage.Output.ResponsesToolCallOutputStr = block.Content.ContentStr
					} else if block.Content.ContentBlocks != nil {
						var toolMsgContentBlocks []schemas.ResponsesMessageContentBlock
						for _, contentBlock := range block.Content.ContentBlocks {
							if contentBlock.Type == AnthropicContentBlockTypeText {
								if contentBlock.Text != nil {
									var blockType schemas.ResponsesMessageContentBlockType
									if isOutputMessage {
										blockType = schemas.ResponsesOutputMessageContentTypeText
									} else {
										blockType = schemas.ResponsesInputMessageContentBlockTypeText
									}
									toolMsgContentBlocks = append(toolMsgContentBlocks, schemas.ResponsesMessageContentBlock{
										Type:         blockType,
										Text:         contentBlock.Text,
										CacheControl: contentBlock.CacheControl,
									})
								}
							}
						}
						bifrostMsg.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks = toolMsgContentBlocks
					}
				}
				bifrostMessages = append(bifrostMessages, bifrostMsg)
			}
		default:

		}
	}

	if preserveThinkingOrder {
		flushReasoning()
	} else if len(reasoningContentBlocks) > 0 {
		id := reasoningItemID
		if id == nil {
			id = new("rs_" + schemas.GetRandomString(50))
		}
		reasoningMessage := schemas.ResponsesMessage{
			ID:   id,
			Type: new(schemas.ResponsesMessageTypeReasoning),
			ResponsesReasoning: &schemas.ResponsesReasoning{
				Summary:          []schemas.ResponsesReasoningSummary{},
				EncryptedContent: reasoningEncryptedContent,
			},
			Content: &schemas.ResponsesMessageContent{
				ContentBlocks: reasoningContentBlocks,
			},
		}

		bifrostMessages = append([]schemas.ResponsesMessage{reasoningMessage}, bifrostMessages...)
	}

	return bifrostMessages
}

func convertBifrostMessageToAnthropicSystemContent(caps schemas.ModelCaps, msg *schemas.ResponsesMessage) *AnthropicContent {
	if msg.Content != nil {
		if msg.Content.ContentStr != nil {
			return &AnthropicContent{
				ContentStr: msg.Content.ContentStr,
			}
		} else if msg.Content.ContentBlocks != nil {
			contentBlocks := convertBifrostContentBlocksToAnthropic(caps, msg.Content.ContentBlocks)
			if len(contentBlocks) > 0 {
				return &AnthropicContent{
					ContentBlocks: contentBlocks,
				}
			}
		}
	}
	return nil
}

func perMessageOutputConfigFor(msg *schemas.ResponsesMessage, supported bool) *AnthropicMessageOutputConfig {
	if !supported || msg.OutputConfig == nil || msg.OutputConfig.Effort == nil {
		return nil
	}
	effort := MapBifrostEffortToAnthropic(*msg.OutputConfig.Effort)
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return &AnthropicMessageOutputConfig{Effort: schemas.Ptr(effort)}
	}
	return nil
}

func convertBifrostMessageToAnthropicMessage(caps schemas.ModelCaps, msg *schemas.ResponsesMessage, pendingReasoningContentBlocks *[]AnthropicContentBlock) *AnthropicMessage {
	anthropicMsg := AnthropicMessage{}

	if msg.Role != nil {
		switch *msg.Role {
		case schemas.ResponsesInputMessageRoleUser:
			anthropicMsg.Role = AnthropicMessageRoleUser
		case schemas.ResponsesInputMessageRoleAssistant:
			anthropicMsg.Role = AnthropicMessageRoleAssistant
		default:
			anthropicMsg.Role = AnthropicMessageRoleUser
		}
	} else {
		anthropicMsg.Role = AnthropicMessageRoleUser
	}

	if len(*pendingReasoningContentBlocks) > 0 && anthropicMsg.Role == AnthropicMessageRoleAssistant {

		copied := make([]AnthropicContentBlock, len(*pendingReasoningContentBlocks))
		copy(copied, *pendingReasoningContentBlocks)
		contentBlocks := copied
		*pendingReasoningContentBlocks = nil

		if msg.Content != nil {
			if msg.Content.ContentStr != nil {
				contentBlocks = append(contentBlocks, AnthropicContentBlock{
					Type: AnthropicContentBlockTypeText,
					Text: msg.Content.ContentStr,
				})
			} else if msg.Content.ContentBlocks != nil {
				contentBlocks = append(contentBlocks, convertBifrostContentBlocksToAnthropic(caps, msg.Content.ContentBlocks)...)
			}
		}
		anthropicMsg.Content = AnthropicContent{
			ContentBlocks: contentBlocks,
		}
	} else {

		if msg.Content != nil {
			if msg.Content.ContentStr != nil {
				anthropicMsg.Content = AnthropicContent{
					ContentBlocks: []AnthropicContentBlock{{
						Type: AnthropicContentBlockTypeText,
						Text: msg.Content.ContentStr,
					}},
				}
			} else if msg.Content.ContentBlocks != nil {
				contentBlocks := convertBifrostContentBlocksToAnthropic(caps, msg.Content.ContentBlocks)
				if len(contentBlocks) > 0 {
					anthropicMsg.Content = AnthropicContent{
						ContentBlocks: contentBlocks,
					}
				}
			}
		}
	}

	return &anthropicMsg
}

func convertBifrostReasoningToAnthropicThinking(ctx *schemas.BifrostContext, msg *schemas.ResponsesMessage, sourceProvider schemas.ModelProvider, model string, bridgedFromChat bool) []AnthropicContentBlock {
	var thinkingBlocks []AnthropicContentBlock
	embedID := providerUtils.ShouldEmbedReasoningItemID(ctx, sourceProvider, model) &&
		(!bridgedFromChat || (msg.ResponsesReasoning != nil && msg.ResponsesReasoning.EncryptedContent != nil && *msg.ResponsesReasoning.EncryptedContent != ""))

	emittedFromContentBlocks := false
	if msg.Content != nil {
		for _, block := range msg.Content.ContentBlocks {
			if block.Type == schemas.ResponsesOutputMessageContentTypeReasoning && block.Text != nil {

				signature := block.Signature
				if signature == nil {
					signature = schemas.Ptr("")
				}
				if embedID {
					embedded := providerUtils.EmbedReasoningItemID(msg.ID, *signature)
					signature = &embedded
				}
				thinkingBlock := AnthropicContentBlock{
					Type:      AnthropicContentBlockTypeThinking,
					Thinking:  block.Text,
					Signature: signature,
				}
				thinkingBlocks = append(thinkingBlocks, thinkingBlock)
				emittedFromContentBlocks = true
			}
		}
	}
	if !emittedFromContentBlocks && msg.ResponsesReasoning != nil {

		if len(msg.ResponsesReasoning.Summary) > 0 {
			for _, reasoningContent := range msg.ResponsesReasoning.Summary {
				signature := ""
				if embedID {
					signature = providerUtils.EmbedReasoningItemID(msg.ID, "")
				}
				thinkingBlock := AnthropicContentBlock{
					Type:      AnthropicContentBlockTypeThinking,
					Thinking:  &reasoningContent.Text,
					Signature: &signature,
				}
				thinkingBlocks = append(thinkingBlocks, thinkingBlock)
			}
		}
		if msg.ResponsesReasoning.EncryptedContent != nil && *msg.ResponsesReasoning.EncryptedContent != "" {
			data := *msg.ResponsesReasoning.EncryptedContent
			if embedID {
				data = providerUtils.EmbedReasoningItemID(msg.ID, data)
			}
			thinkingBlock := AnthropicContentBlock{
				Type: AnthropicContentBlockTypeRedactedThinking,
				Data: &data,
			}
			thinkingBlocks = append(thinkingBlocks, thinkingBlock)
		}
	}

	return thinkingBlocks
}

func convertBifrostFunctionCallToAnthropicToolUse(ctx *schemas.BifrostContext, msg *schemas.ResponsesMessage) *AnthropicContentBlock {
	if msg.ResponsesToolMessage != nil {
		toolUseBlock := AnthropicContentBlock{
			Type:         AnthropicContentBlockTypeToolUse,
			CacheControl: msg.CacheControl,
		}

		if msg.ResponsesToolMessage.CallID != nil {
			toolUseBlock.ID = providerUtils.SanitizeAnthropicToolUseIDPtr(msg.ResponsesToolMessage.CallID)
		}
		if msg.ResponsesToolMessage.Name != nil {
			toolUseBlock.Name = msg.ResponsesToolMessage.Name
		}
		toolUseBlock.ToolsetName = msg.ResponsesToolMessage.ToolsetName

		if msg.ResponsesToolMessage.Arguments != nil && *msg.ResponsesToolMessage.Arguments != "" {
			argumentsJSON := *msg.ResponsesToolMessage.Arguments

			if ctx != nil {
				if IsClaudeCodeRequest(ctx) {
					if msg.ResponsesToolMessage.Name != nil && *msg.ResponsesToolMessage.Name == "WebSearch" {
						argumentsJSON = sanitizeWebSearchArguments(argumentsJSON)
					}
				}
			}
			toolUseBlock.Input = parseJSONInput(argumentsJSON)
		} else {

			toolUseBlock.Input = json.RawMessage("{}")
		}

		return &toolUseBlock
	}
	return nil
}

func convertBifrostFunctionCallOutputToAnthropicToolResultBlock(msg *schemas.ResponsesMessage) *AnthropicContentBlock {
	if msg.ResponsesToolMessage != nil {
		toolResultBlock := AnthropicContentBlock{
			Type:         AnthropicContentBlockTypeToolResult,
			ToolUseID:    providerUtils.SanitizeAnthropicToolUseIDPtr(msg.ResponsesToolMessage.CallID),
			CacheControl: msg.CacheControl,
			ToolsetName:  msg.ResponsesToolMessage.ToolsetName,
		}

		if msg.ResponsesToolMessage.Output != nil {
			toolResultBlock.Content = convertToolOutputToAnthropicContent(msg.ResponsesToolMessage.Output)
		}

		if toolError := msg.ResponsesToolMessage.Error; toolError.IsError() {
			toolResultBlock.IsError = schemas.Ptr(true)
			if toolResultBlock.Content == nil {
				errText := toolError.Text()
				if errText == "" {
					errText = "tool call returned an error"
				}
				toolResultBlock.Content = &AnthropicContent{
					ContentStr: &errText,
				}
			}
		} else if msg.Status != nil && *msg.Status == "incomplete" {
			toolResultBlock.IsError = schemas.Ptr(true)
		}

		return &toolResultBlock
	}
	return nil
}

func toolResultBlockToText(block *AnthropicContentBlock) string {
	if block == nil || block.Content == nil {
		return ""
	}
	if block.Content.ContentStr != nil && *block.Content.ContentStr != "" {
		return "Tool result: " + *block.Content.ContentStr
	}
	var sb strings.Builder
	for _, cb := range block.Content.ContentBlocks {
		if cb.Text != nil {
			sb.WriteString(*cb.Text)
		}
	}
	if sb.Len() == 0 {
		return ""
	}
	return "Tool result: " + sb.String()
}

func convertBifrostComputerCallOutputToAnthropicToolResultBlock(msg *schemas.ResponsesMessage) *AnthropicContentBlock {
	if msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.CallID != nil {
		toolResultBlock := AnthropicContentBlock{
			Type:      AnthropicContentBlockTypeToolResult,
			ToolUseID: providerUtils.SanitizeAnthropicToolUseIDPtr(msg.ResponsesToolMessage.CallID),
		}

		if msg.ResponsesToolMessage.Output != nil {
			toolResultBlock.Content = convertToolOutputToAnthropicContent(msg.ResponsesToolMessage.Output)
		}

		if toolError := msg.ResponsesToolMessage.Error; toolError.IsError() {
			toolResultBlock.IsError = schemas.Ptr(true)
			if toolResultBlock.Content == nil {
				errText := toolError.Text()
				if errText == "" {
					errText = "tool call returned an error"
				}
				toolResultBlock.Content = &AnthropicContent{
					ContentStr: &errText,
				}
			}
		} else if msg.Status != nil && *msg.Status == "incomplete" {
			toolResultBlock.IsError = schemas.Ptr(true)
		}

		return &toolResultBlock
	}
	return nil
}

func convertBifrostMCPCallOutputToAnthropicToolResultBlock(msg *schemas.ResponsesMessage) *AnthropicContentBlock {
	if msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.CallID != nil {
		toolResultBlock := AnthropicContentBlock{
			Type:      AnthropicContentBlockTypeMCPToolResult,
			ToolUseID: providerUtils.SanitizeAnthropicToolUseIDPtr(msg.ResponsesToolMessage.CallID),
		}

		if msg.ResponsesToolMessage.Output != nil {
			toolResultBlock.Content = convertToolOutputToAnthropicContent(msg.ResponsesToolMessage.Output)
		}

		if toolError := msg.ResponsesToolMessage.Error; toolError.IsError() {
			toolResultBlock.IsError = schemas.Ptr(true)
			if toolResultBlock.Content == nil {
				errText := toolError.Text()
				if errText == "" {
					errText = "tool call returned an error"
				}
				toolResultBlock.Content = &AnthropicContent{
					ContentStr: &errText,
				}
			}
		} else if msg.Status != nil && *msg.Status == "incomplete" {
			toolResultBlock.IsError = schemas.Ptr(true)
		}

		return &toolResultBlock
	}
	return nil
}

func convertBifrostItemReferenceToAnthropicMessage(msg *schemas.ResponsesMessage) *AnthropicMessage {
	if msg.Content != nil && msg.Content.ContentStr != nil {
		referenceMsg := AnthropicMessage{
			Role: AnthropicMessageRoleUser,
		}
		if msg.Role != nil && *msg.Role == schemas.ResponsesInputMessageRoleAssistant {
			referenceMsg.Role = AnthropicMessageRoleAssistant
		}

		referenceMsg.Content = AnthropicContent{
			ContentBlocks: []AnthropicContentBlock{{
				Type: AnthropicContentBlockTypeText,
				Text: msg.Content.ContentStr,
			}},
		}

		return &referenceMsg
	}
	return nil
}

func convertBifrostComputerCallToAnthropicToolUse(msg *schemas.ResponsesMessage) []AnthropicContentBlock {
	if msg.ResponsesToolMessage == nil {
		return nil
	}
	toolMsg := msg.ResponsesToolMessage

	name := string(AnthropicToolNameComputer)
	if toolMsg.Name != nil {
		name = *toolMsg.Name
	}

	var actions []schemas.ResponsesComputerToolCallAction
	if call := toolMsg.ResponsesComputerToolCall; call != nil && len(call.Actions) > 0 {
		actions = call.Actions
	} else if toolMsg.Action != nil && toolMsg.Action.ResponsesComputerToolCallAction != nil {
		actions = []schemas.ResponsesComputerToolCallAction{*toolMsg.Action.ResponsesComputerToolCallAction}
	}
	if len(actions) == 0 {

		return []AnthropicContentBlock{newComputerToolUseBlock(name, providerUtils.SanitizeAnthropicToolUseIDPtr(toolMsg.CallID), nil)}
	}

	last := len(actions) - 1
	blocks := make([]AnthropicContentBlock, len(actions))
	for i := range actions {
		var id *string
		if toolMsg.CallID != nil {
			rawID := *toolMsg.CallID
			if i < last {
				rawID = fmt.Sprintf("%s_%d", rawID, i)
			}
			id = providerUtils.SanitizeAnthropicToolUseIDPtr(&rawID)
		}
		blocks[i] = newComputerToolUseBlock(name, id, &actions[i])
	}
	return blocks
}

func newComputerToolUseBlock(name string, id *string, action *schemas.ResponsesComputerToolCallAction) AnthropicContentBlock {
	block := AnthropicContentBlock{
		Type: AnthropicContentBlockTypeToolUse,
		Name: schemas.Ptr(name),
		ID:   id,
	}
	if action != nil {
		if input, err := providerUtils.MarshalSorted(convertResponsesToAnthropicComputerAction(action)); err == nil {
			block.Input = json.RawMessage(input)
		}
	}
	return block
}

func setResponsesComputerCallAction(toolMsg *schemas.ResponsesToolMessage, action *schemas.ResponsesComputerToolCallAction) {
	if toolMsg == nil || action == nil {
		return
	}
	toolMsg.Action = &schemas.ResponsesToolMessageActionStruct{ResponsesComputerToolCallAction: action}
	if toolMsg.ResponsesComputerToolCall == nil {
		toolMsg.ResponsesComputerToolCall = &schemas.ResponsesComputerToolCall{}
	}
	toolMsg.ResponsesComputerToolCall.Actions = []schemas.ResponsesComputerToolCallAction{*action}
}

func convertBifrostMCPCallToAnthropicToolUse(msg *schemas.ResponsesMessage) *AnthropicContentBlock {
	if msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.Name != nil {
		toolUseBlock := AnthropicContentBlock{
			Type: AnthropicContentBlockTypeMCPToolUse,
		}

		if msg.ID != nil {
			toolUseBlock.ID = providerUtils.SanitizeAnthropicToolUseIDPtr(msg.ID)
		}
		toolUseBlock.Name = msg.ResponsesToolMessage.Name

		if msg.ResponsesToolMessage.ServerLabel != nil && *msg.ResponsesToolMessage.ServerLabel != "" {
			toolUseBlock.ServerName = msg.ResponsesToolMessage.ServerLabel
		}

		if msg.ResponsesToolMessage.Arguments != nil && *msg.ResponsesToolMessage.Arguments != "" {
			toolUseBlock.Input = parseJSONInput(*msg.ResponsesToolMessage.Arguments)
		} else {

			toolUseBlock.Input = json.RawMessage("{}")
		}

		return &toolUseBlock
	}
	return nil
}

func convertBifrostMCPApprovalToAnthropicToolUse(msg *schemas.ResponsesMessage) *AnthropicContentBlock {
	if msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.Name != nil {
		toolUseBlock := AnthropicContentBlock{
			Type: AnthropicContentBlockTypeMCPToolUse,
		}

		if msg.ID != nil {
			toolUseBlock.ID = providerUtils.SanitizeAnthropicToolUseIDPtr(msg.ID)
		}
		toolUseBlock.Name = msg.ResponsesToolMessage.Name

		if msg.ResponsesToolMessage.ServerLabel != nil && *msg.ResponsesToolMessage.ServerLabel != "" {
			toolUseBlock.ServerName = msg.ResponsesToolMessage.ServerLabel
		}

		if msg.ResponsesToolMessage.Arguments != nil && *msg.ResponsesToolMessage.Arguments != "" {
			toolUseBlock.Input = parseJSONInput(*msg.ResponsesToolMessage.Arguments)
		} else {

			toolUseBlock.Input = json.RawMessage("{}")
		}

		return &toolUseBlock
	}
	return nil
}

func convertBifrostWebSearchCallToAnthropicBlocks(msg *schemas.ResponsesMessage) []AnthropicContentBlock {
	if msg.ResponsesToolMessage == nil || msg.ResponsesToolMessage.Action == nil || msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction == nil {
		return nil
	}

	var blocks []AnthropicContentBlock
	action := msg.ResponsesToolMessage.Action.ResponsesWebSearchToolCallAction

	var caller *AnthropicToolCaller
	if c := msg.ResponsesToolMessage.Caller; c != nil {
		caller = &AnthropicToolCaller{Type: AnthropicToolCallerType(c.Type), ToolID: providerUtils.SanitizeAnthropicToolUseIDPtr(c.ToolID)}
	}

	serverToolUseBlock := AnthropicContentBlock{
		Type:   AnthropicContentBlockTypeServerToolUse,
		Name:   schemas.Ptr("web_search"),
		Caller: caller,
	}

	if msg.ID != nil {
		serverToolUseBlock.ID = providerUtils.SanitizeAnthropicToolUseIDPtr(msg.ID)
	}

	if action.Query != nil {
		inputBytes, err := providerUtils.MarshalSorted(map[string]interface{}{
			"query": *action.Query,
		})
		if err == nil {
			serverToolUseBlock.Input = json.RawMessage(inputBytes)
		}
	}

	blocks = append(blocks, serverToolUseBlock)

	var resultBlocks []AnthropicContentBlock
	for _, source := range action.Sources {
		if source.URL != "" {
			resultBlock := AnthropicContentBlock{
				Type:             AnthropicContentBlockTypeWebSearchResult,
				URL:              schemas.Ptr(source.URL),
				EncryptedContent: source.EncryptedContent,
				PageAge:          source.PageAge,
			}
			if source.Title != nil {
				resultBlock.Title = source.Title
			} else if source.URL != "" {
				resultBlock.Title = schemas.Ptr(source.URL)
			}
			resultBlocks = append(resultBlocks, resultBlock)
		}
	}

	var toolUseID *string
	if msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.CallID != nil {
		toolUseID = msg.ResponsesToolMessage.CallID
	} else {
		toolUseID = msg.ID
	}
	toolUseID = providerUtils.SanitizeAnthropicToolUseIDPtr(toolUseID)
	webSearchResultBlock := AnthropicContentBlock{
		Type:      AnthropicContentBlockTypeWebSearchToolResult,
		ToolUseID: toolUseID,
		Caller:    caller,
		Content: &AnthropicContent{
			ContentBlocks: resultBlocks,
		},
	}
	blocks = append(blocks, webSearchResultBlock)

	return blocks
}

func getAnthropicContentObject(content *AnthropicContent) *AnthropicContentBlock {
	if content == nil {
		return nil
	}
	if content.ContentObj != nil {
		return content.ContentObj
	}
	if len(content.ContentBlocks) > 0 {
		return &content.ContentBlocks[0]
	}
	return nil
}

func convertAnthropicWebFetchResultToBifrost(block *AnthropicContentBlock) *schemas.ResponsesWebFetchCall {
	if block == nil {
		return nil
	}
	result := &schemas.ResponsesWebFetchCall{}
	inner := getAnthropicContentObject(block.Content)
	if inner == nil {
		return result
	}

	result.ResultType = string(inner.Type)
	result.URL = inner.URL
	result.RetrievedAt = inner.RetrievedAt
	result.ErrorCode = inner.ErrorCode

	doc := getAnthropicContentObject(inner.Content)
	if doc != nil {
		result.Document = &schemas.ResponsesWebFetchDocument{
			Type:    string(doc.Type),
			Text:    doc.Text,
			Title:   doc.Title,
			Context: doc.Context,
		}
		if doc.Citations != nil && doc.Citations.Config != nil {
			result.Document.Citations = doc.Citations.Config
		}
		if doc.Source != nil && doc.Source.SourceObj != nil {
			src := doc.Source.SourceObj
			result.Document.Source = &schemas.ResponsesWebFetchSource{
				Type:      src.Type,
				MediaType: src.MediaType,
				Data:      src.Data,
				URL:       src.URL,
				FileID:    src.FileID,
			}
		}
	}

	return result
}

func attachAnthropicWebFetchResult(messages []schemas.ResponsesMessage, toolUseID string, block AnthropicContentBlock) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := &messages[i]
		if msg.Type == nil || *msg.Type != schemas.ResponsesMessageTypeWebFetchCall {
			continue
		}
		if msg.ID == nil || *msg.ID != toolUseID {
			continue
		}
		if msg.ResponsesToolMessage == nil {
			msg.ResponsesToolMessage = &schemas.ResponsesToolMessage{}
		}
		msg.ResponsesToolMessage.CallID = &toolUseID
		msg.ResponsesToolMessage.ResponsesWebFetchCall = convertAnthropicWebFetchResultToBifrost(&block)
		break
	}
}

func convertBifrostWebFetchCallToAnthropicBlocks(msg *schemas.ResponsesMessage) []AnthropicContentBlock {
	if msg == nil || msg.ResponsesToolMessage == nil {
		return nil
	}
	tm := msg.ResponsesToolMessage

	var toolUseID *string
	if tm.CallID != nil {
		toolUseID = tm.CallID
	} else {
		toolUseID = msg.ID
	}
	toolUseID = providerUtils.SanitizeAnthropicToolUseIDPtr(toolUseID)

	var caller *AnthropicToolCaller
	if tm.Caller != nil {
		caller = &AnthropicToolCaller{Type: AnthropicToolCallerType(tm.Caller.Type), ToolID: providerUtils.SanitizeAnthropicToolUseIDPtr(tm.Caller.ToolID)}
	}

	serverToolUseBlock := AnthropicContentBlock{
		Type:   AnthropicContentBlockTypeServerToolUse,
		ID:     toolUseID,
		Name:   schemas.Ptr(string(AnthropicToolNameWebFetch)),
		Caller: caller,
		Input:  json.RawMessage("{}"),
	}
	if tm.Action != nil && tm.Action.ResponsesWebFetchToolCallAction != nil &&
		tm.Action.ResponsesWebFetchToolCallAction.URL != "" {
		if inputBytes, err := providerUtils.MarshalSorted(map[string]interface{}{"url": tm.Action.ResponsesWebFetchToolCallAction.URL}); err == nil {
			serverToolUseBlock.Input = json.RawMessage(inputBytes)
		}
	}

	blocks := []AnthropicContentBlock{serverToolUseBlock}
	if tm.ResponsesWebFetchCall == nil {
		return blocks
	}

	wf := tm.ResponsesWebFetchCall
	resultType := wf.ResultType
	if resultType == "" {
		resultType = "web_fetch_result"
	}
	inner := AnthropicContentBlock{
		Type:        AnthropicContentBlockType(resultType),
		URL:         wf.URL,
		RetrievedAt: wf.RetrievedAt,
		ErrorCode:   wf.ErrorCode,
	}
	if wf.Document != nil {
		doc := &AnthropicContentBlock{
			Type:    AnthropicContentBlockType(wf.Document.Type),
			Text:    wf.Document.Text,
			Title:   wf.Document.Title,
			Context: wf.Document.Context,
		}
		if doc.Type == "" {
			doc.Type = AnthropicContentBlockTypeDocument
		}
		if wf.Document.Citations != nil {
			doc.Citations = &AnthropicCitations{Config: wf.Document.Citations}
		}
		if wf.Document.Source != nil {
			src := wf.Document.Source
			doc.Source = &AnthropicBlockSource{SourceObj: &AnthropicSource{
				Type:      src.Type,
				MediaType: src.MediaType,
				Data:      src.Data,
				URL:       src.URL,
				FileID:    src.FileID,
			}}
		}
		inner.Content = &AnthropicContent{ContentObj: doc}
	}

	blocks = append(blocks, AnthropicContentBlock{
		Type:      AnthropicContentBlockTypeWebFetchToolResult,
		ToolUseID: toolUseID,
		Caller:    caller,
		Content:   &AnthropicContent{ContentObj: &inner},
	})

	return blocks
}

func convertBifrostAdvisorCallToAnthropicBlocks(msg *schemas.ResponsesMessage) []AnthropicContentBlock {

	var toolUseID *string
	if msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.CallID != nil {
		toolUseID = msg.ResponsesToolMessage.CallID
	} else {
		toolUseID = msg.ID
	}
	toolUseID = providerUtils.SanitizeAnthropicToolUseIDPtr(toolUseID)

	serverToolUseBlock := AnthropicContentBlock{
		Type:  AnthropicContentBlockTypeServerToolUse,
		ID:    toolUseID,
		Name:  schemas.Ptr(string(AnthropicToolNameAdvisor)),
		Input: json.RawMessage("{}"),
	}

	resultBlock := AnthropicContentBlock{
		Type:      AnthropicContentBlockTypeAdvisorToolResult,
		ToolUseID: toolUseID,
	}
	if msg.ResponsesToolMessage != nil && msg.ResponsesToolMessage.ResponsesAdvisorCall != nil {
		adv := msg.ResponsesToolMessage.ResponsesAdvisorCall
		resultType := adv.ResultType
		if resultType == "" {
			resultType = "advisor_result"
		}
		resultBlock.Content = &AnthropicContent{
			ContentObj: &AnthropicContentBlock{
				Type:             AnthropicContentBlockType(resultType),
				Text:             adv.Text,
				EncryptedContent: adv.EncryptedContent,
				ErrorCode:        adv.ErrorCode,
				StopReason:       adv.StopReason,
			},
		}
	}

	return []AnthropicContentBlock{serverToolUseBlock, resultBlock}
}

func convertBifrostToolSearchCallToAnthropicBlocks(msg *schemas.ResponsesMessage) []AnthropicContentBlock {
	if msg.ResponsesToolMessage == nil {
		return nil
	}

	var toolUseID *string
	if msg.ResponsesToolMessage.CallID != nil {
		toolUseID = msg.ResponsesToolMessage.CallID
	} else {
		toolUseID = msg.ID
	}

	if toolUseID == nil {
		return nil
	}

	name := string(AnthropicToolNameToolSearchRegex)
	if msg.ResponsesToolMessage.Name != nil && *msg.ResponsesToolMessage.Name != "" {
		name = *msg.ResponsesToolMessage.Name
	}
	input := json.RawMessage("{}")
	if args := msg.ResponsesToolMessage.Arguments; args != nil && *args != "" && json.Valid([]byte(*args)) {
		input = json.RawMessage(*args)
	}
	serverToolUseBlock := AnthropicContentBlock{
		Type:  AnthropicContentBlockTypeServerToolUse,
		ID:    toolUseID,
		Name:  schemas.Ptr(name),
		Input: input,
	}

	var toolReferences []AnthropicContentBlock
	if msg.ResponsesToolMessage.ResponsesToolSearchCall != nil {
		for _, toolName := range msg.ResponsesToolMessage.ResponsesToolSearchCall.ToolReferences {
			toolReferences = append(toolReferences, AnthropicContentBlock{
				Type:     AnthropicContentBlockTypeToolReference,
				ToolName: schemas.Ptr(toolName),
			})
		}
	}
	return []AnthropicContentBlock{serverToolUseBlock, anthropicToolSearchResultBlock(toolUseID, toolReferences)}
}

func normalizeOpenAIToolSearchItems(ctx *schemas.BifrostContext, msgs []schemas.ResponsesMessage, isRequestMessage bool) []schemas.ResponsesMessage {
	isVerbatimToolSearch := func(m *schemas.ResponsesMessage) bool {
		return m.Type != nil && (*m.Type == schemas.ResponsesMessageTypeToolSearchCall ||
			*m.Type == schemas.ResponsesMessageTypeToolSearchOutput) && m.IsVerbatimItem() &&
			!isAnthropicToolSearchReplay(m)
	}
	if !slices.ContainsFunc(msgs, func(m schemas.ResponsesMessage) bool { return isVerbatimToolSearch(&m) }) {
		return msgs
	}
	searchName := anthropicClientToolSearchFrom(ctx)
	if searchName == "" {
		searchName = "tool_search"
	}
	out := make([]schemas.ResponsesMessage, 0, len(msgs))
	var pendingHosted []int
	for i := range msgs {
		msg := msgs[i]
		if !isVerbatimToolSearch(&msg) {
			out = append(out, msg)
			continue
		}
		if isRequestMessage {
			continue
		}
		if *msg.Type == schemas.ResponsesMessageTypeToolSearchOutput {
			if msg.IsClientToolSearchItem() || len(pendingHosted) == 0 {
				continue
			}

			pick := 0
			if msg.ResponsesToolMessage != nil && msg.CallID != nil {
				for i, idx := range pendingHosted {
					if c := out[idx].ResponsesToolMessage.CallID; c != nil && *c == *msg.CallID {
						pick = i
						break
					}
				}
			}
			idx := pendingHosted[pick]
			pendingHosted = slices.Delete(pendingHosted, pick, pick+1)
			toolMsg := *out[idx].ResponsesToolMessage
			toolMsg.ResponsesToolSearchCall = &schemas.ResponsesToolSearchCall{ToolReferences: msg.ToolSearchOutputToolNames()}
			out[idx].ResponsesToolMessage = &toolMsg
			continue
		}
		var toolMsg schemas.ResponsesToolMessage
		if msg.ResponsesToolMessage != nil {
			toolMsg.CallID = msg.CallID
			toolMsg.Arguments = msg.Arguments
		}
		if msg.IsClientToolSearchItem() {
			if toolMsg.CallID == nil {
				continue
			}
			toolMsg.Name = schemas.Ptr(searchName)
			out = append(out, schemas.ResponsesMessage{
				ID:                   msg.ID,
				Type:                 schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall),
				Status:               msg.Status,
				ResponsesToolMessage: &toolMsg,
			})
			continue
		}

		toolMsg.Name = schemas.Ptr(string(AnthropicToolNameToolSearchBM25))
		out = append(out, schemas.ResponsesMessage{
			ID:                   msg.ID,
			Type:                 msg.Type,
			Status:               msg.Status,
			ResponsesToolMessage: &toolMsg,
		})
		pendingHosted = append(pendingHosted, len(out)-1)
	}
	return out
}

func anthropicToolSearchResultBlock(toolUseID *string, refs []AnthropicContentBlock) AnthropicContentBlock {
	return AnthropicContentBlock{
		Type:      AnthropicContentBlockTypeToolSearchToolResult,
		ToolUseID: toolUseID,
		Content: &AnthropicContent{ContentObj: &AnthropicContentBlock{
			Type:           AnthropicContentBlockTypeToolSearchToolSearchResult,
			ToolReferences: refs,
		}},
	}
}

func anthropicToolSearchArguments(input json.RawMessage) *string {
	if len(input) == 0 {
		return nil
	}
	return schemas.Ptr(string(input))
}

func isAnthropicToolSearchToolName(name string) bool {
	switch AnthropicToolName(name) {
	case AnthropicToolNameToolSearchRegex, AnthropicToolNameToolSearchBM25:
		return true
	default:
		return false
	}
}

func isAnthropicToolSearchReplay(m *schemas.ResponsesMessage) bool {
	return m.ResponsesToolMessage != nil && m.Name != nil && isAnthropicToolSearchToolName(*m.Name)
}

func isAnthropicCodeExecutionToolName(name string) bool {
	switch AnthropicToolName(name) {
	case AnthropicToolNameCodeExecution, AnthropicToolNameBashCodeExecution, AnthropicToolNameTextEditorCodeExecution:
		return true
	default:
		return false
	}
}

func anthropicCodeExecResultBlockType(toolName string) AnthropicContentBlockType {
	switch AnthropicToolName(toolName) {
	case AnthropicToolNameBashCodeExecution:
		return AnthropicContentBlockTypeBashCodeExecutionToolResult
	case AnthropicToolNameTextEditorCodeExecution:
		return AnthropicContentBlockTypeTextEditorCodeExecutionToolResult
	default:
		return AnthropicContentBlockTypeCodeExecutionToolResult
	}
}

func anthropicCodeExecInnerResultType(toolName string) AnthropicContentBlockType {
	switch AnthropicToolName(toolName) {
	case AnthropicToolNameBashCodeExecution:
		return AnthropicContentBlockTypeBashCodeExecutionResult
	case AnthropicToolNameTextEditorCodeExecution:
		return AnthropicContentBlockTypeTextEditorCodeExecutionResult
	default:
		return AnthropicContentBlockTypeCodeExecutionResult
	}
}

func anthropicCodeExecOutputBlockType(toolName string) AnthropicContentBlockType {
	if AnthropicToolName(toolName) == AnthropicToolNameBashCodeExecution {
		return AnthropicContentBlockTypeBashCodeExecutionOutput
	}
	return AnthropicContentBlockTypeCodeExecutionOutput
}

func buildBifrostCodeExecutionCall(block AnthropicContentBlock) schemas.ResponsesMessage {
	carry := &schemas.ResponsesCodeExecutionCall{}
	if block.Name != nil {
		carry.ToolName = *block.Name
	}
	ci := &schemas.ResponsesCodeInterpreterToolCall{}

	if len(block.Input) > 0 {
		raw := string(block.Input)
		carry.Input = &raw

		switch AnthropicToolName(carry.ToolName) {
		case AnthropicToolNameCodeExecution:
			if c := providerUtils.GetJSONField(block.Input, "code"); c.Exists() && c.Type == gjson.String {
				ci.Code = schemas.Ptr(c.Str)
			}
		case AnthropicToolNameBashCodeExecution:
			if c := providerUtils.GetJSONField(block.Input, "command"); c.Exists() && c.Type == gjson.String {
				ci.Code = schemas.Ptr(c.Str)
			}
		}
	}

	if block.Caller != nil {
		carry.Caller = &schemas.ResponsesToolCaller{
			Type:   string(block.Caller.Type),
			ToolID: block.Caller.ToolID,
		}
	}

	return schemas.ResponsesMessage{
		Type:   schemas.Ptr(schemas.ResponsesMessageTypeCodeInterpreterCall),
		ID:     block.ID,
		Status: schemas.Ptr("completed"),
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			CallID:                           block.ID,
			ResponsesCodeInterpreterToolCall: ci,
			ResponsesCodeExecutionCall:       carry,
		},
	}
}

func attachAnthropicCodeExecutionResult(msgs []schemas.ResponsesMessage, toolUseID string, block AnthropicContentBlock) {
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := &msgs[i]
		if msg.Type == nil || *msg.Type != schemas.ResponsesMessageTypeCodeInterpreterCall {
			continue
		}
		if msg.ResponsesToolMessage == nil || msg.ResponsesToolMessage.CallID == nil ||
			*msg.ResponsesToolMessage.CallID != toolUseID {
			continue
		}

		tm := msg.ResponsesToolMessage
		if tm.ResponsesCodeExecutionCall == nil {
			tm.ResponsesCodeExecutionCall = &schemas.ResponsesCodeExecutionCall{}
		}
		if tm.ResponsesCodeInterpreterToolCall == nil {
			tm.ResponsesCodeInterpreterToolCall = &schemas.ResponsesCodeInterpreterToolCall{}
		}
		carry := tm.ResponsesCodeExecutionCall
		ci := tm.ResponsesCodeInterpreterToolCall

		var inner *AnthropicContentBlock
		if c := block.Content; c != nil {
			inner = c.ContentObj
			if inner == nil && len(c.ContentBlocks) > 0 {
				inner = &c.ContentBlocks[0]
			}
		}
		if inner != nil {
			carry.ResultType = string(inner.Type)
			carry.Stdout = inner.Stdout
			carry.Stderr = inner.Stderr
			carry.ReturnCode = inner.ReturnCode
			carry.EncryptedStdout = inner.EncryptedStdout
			carry.FileType = inner.FileType
			carry.StartLine = inner.StartLine
			carry.NumLines = inner.NumLines
			carry.TotalLines = inner.TotalLines
			carry.IsFileUpdate = inner.IsFileUpdate
			carry.OldStart = inner.OldStart
			carry.OldLines = inner.OldLines
			carry.NewStart = inner.NewStart
			carry.NewLines = inner.NewLines
			carry.Lines = inner.Lines
			carry.ErrorCode = inner.ErrorCode

			if ic := inner.Content; ic != nil {
				if ic.ContentStr != nil {
					carry.FileContent = ic.ContentStr
				}
				for _, fb := range ic.ContentBlocks {
					if fb.FileID != nil {
						carry.Files = append(carry.Files, schemas.ResponsesCodeExecutionFileOutput{FileID: *fb.FileID})
					}
				}
			}

			if inner.Stdout != nil && *inner.Stdout != "" {
				ci.Outputs = append(ci.Outputs, schemas.ResponsesCodeInterpreterOutput{
					ResponsesCodeInterpreterOutputLogs: &schemas.ResponsesCodeInterpreterOutputLogs{
						Type: "logs",
						Logs: *inner.Stdout,
					},
				})
			}
		}

		if block.Caller != nil && carry.Caller == nil {
			carry.Caller = &schemas.ResponsesToolCaller{
				Type:   string(block.Caller.Type),
				ToolID: block.Caller.ToolID,
			}
		}

		if carry.ErrorCode != nil {
			msg.Status = schemas.Ptr("failed")
		}
		return
	}
}

func convertBifrostCodeExecCallToAnthropicBlocks(msg *schemas.ResponsesMessage) []AnthropicContentBlock {
	tm := msg.ResponsesToolMessage
	if tm == nil {
		return nil
	}
	cec := tm.ResponsesCodeExecutionCall
	ci := tm.ResponsesCodeInterpreterToolCall
	if cec == nil && ci == nil {
		return nil
	}
	if cec == nil {

		cec = &schemas.ResponsesCodeExecutionCall{}
	}

	var toolUseID *string
	if tm.CallID != nil {
		toolUseID = tm.CallID
	} else {
		toolUseID = msg.ID
	}
	toolUseID = providerUtils.SanitizeAnthropicToolUseIDPtr(toolUseID)

	toolName := cec.ToolName
	if toolName == "" {
		toolName = string(AnthropicToolNameCodeExecution)
	}

	serverToolUse := AnthropicContentBlock{
		Type: AnthropicContentBlockTypeServerToolUse,
		ID:   toolUseID,
		Name: schemas.Ptr(toolName),
	}
	if cec.Input != nil {
		serverToolUse.Input = json.RawMessage(*cec.Input)
	} else if ci != nil && ci.Code != nil {

		key := "code"
		if AnthropicToolName(toolName) == AnthropicToolNameBashCodeExecution {
			key = "command"
		}
		if inputBytes, err := providerUtils.MarshalSorted(map[string]interface{}{key: *ci.Code}); err == nil {
			serverToolUse.Input = json.RawMessage(inputBytes)
		}
	}
	if cec.Caller != nil {
		serverToolUse.Caller = &AnthropicToolCaller{
			Type:   AnthropicToolCallerType(cec.Caller.Type),
			ToolID: providerUtils.SanitizeAnthropicToolUseIDPtr(cec.Caller.ToolID),
		}
	}

	stdout, stderr, returnCode := cec.Stdout, cec.Stderr, cec.ReturnCode
	if stdout == nil && stderr == nil && ci != nil {

		failed := msg.Status != nil && *msg.Status == "failed"
		for _, o := range ci.Outputs {
			if o.ResponsesCodeInterpreterOutputLogs != nil {
				logs := o.ResponsesCodeInterpreterOutputLogs.Logs
				if failed {
					stderr = &logs
					if returnCode == nil {
						returnCode = schemas.Ptr(1)
					}
				} else {
					stdout = &logs
				}
				break
			}
		}
	}
	inner := AnthropicContentBlock{
		Type:            AnthropicContentBlockType(cec.ResultType),
		Stdout:          stdout,
		Stderr:          stderr,
		ReturnCode:      returnCode,
		EncryptedStdout: cec.EncryptedStdout,
		FileType:        cec.FileType,
		StartLine:       cec.StartLine,
		NumLines:        cec.NumLines,
		TotalLines:      cec.TotalLines,
		IsFileUpdate:    cec.IsFileUpdate,
		OldStart:        cec.OldStart,
		OldLines:        cec.OldLines,
		NewStart:        cec.NewStart,
		NewLines:        cec.NewLines,
		Lines:           cec.Lines,
		ErrorCode:       cec.ErrorCode,
	}
	if inner.Type == "" {
		inner.Type = anthropicCodeExecInnerResultType(toolName)
	}

	var innerContent *AnthropicContent
	if cec.FileContent != nil {
		innerContent = &AnthropicContent{ContentStr: cec.FileContent}
	}
	if len(cec.Files) > 0 {
		outputType := anthropicCodeExecOutputBlockType(toolName)
		fileBlocks := make([]AnthropicContentBlock, 0, len(cec.Files))
		for _, f := range cec.Files {
			fileID := f.FileID
			fileBlocks = append(fileBlocks, AnthropicContentBlock{Type: outputType, FileID: &fileID})
		}
		if innerContent == nil {
			innerContent = &AnthropicContent{}
		}
		innerContent.ContentBlocks = fileBlocks
	}

	if innerContent == nil {
		switch inner.Type {
		case AnthropicContentBlockTypeCodeExecutionResult,
			AnthropicContentBlockTypeBashCodeExecutionResult,
			AnthropicContentBlockTypeEncryptedCodeExecutionResult:
			innerContent = &AnthropicContent{ContentBlocks: []AnthropicContentBlock{}}
		}
	}
	inner.Content = innerContent

	resultBlock := AnthropicContentBlock{
		Type:      anthropicCodeExecResultBlockType(toolName),
		ToolUseID: toolUseID,
		Content:   &AnthropicContent{ContentObj: &inner},
	}
	if cec.Caller != nil {
		resultBlock.Caller = &AnthropicToolCaller{
			Type:   AnthropicToolCallerType(cec.Caller.Type),
			ToolID: providerUtils.SanitizeAnthropicToolUseIDPtr(cec.Caller.ToolID),
		}
	}

	return []AnthropicContentBlock{serverToolUse, resultBlock}
}

func convertBifrostUnsupportedToolCallToAnthropicMessage(msg *schemas.ResponsesMessage, msgType schemas.ResponsesMessageType) *AnthropicMessage {
	if msg.ResponsesToolMessage != nil {
		var description string
		if msg.ResponsesToolMessage.Name != nil {
			description = fmt.Sprintf("Tool call: %s", *msg.ResponsesToolMessage.Name)
			if msg.ResponsesToolMessage.Arguments != nil {
				description += fmt.Sprintf(" with arguments: %s", *msg.ResponsesToolMessage.Arguments)
			}
		} else {
			description = fmt.Sprintf("Tool call of type: %s", msgType)

			if msg.ResponsesToolMessage.Action != nil {
				if action, err := schemas.Marshal(msg.ResponsesToolMessage.Action); err == nil {
					description += fmt.Sprintf(" with action: %s", action)
				}
			}
			if msg.ResponsesToolMessage.ResponsesApplyPatchCall != nil && msg.ResponsesToolMessage.Operation != nil {
				if operation, err := schemas.Marshal(msg.ResponsesToolMessage.Operation); err == nil {
					description += fmt.Sprintf(" with operation: %s", operation)
				}
			}
		}

		return &AnthropicMessage{
			Role: AnthropicMessageRoleAssistant,
			Content: AnthropicContent{
				ContentBlocks: []AnthropicContentBlock{{
					Type: AnthropicContentBlockTypeText,
					Text: &description,
				}},
			},
		}
	}
	return nil
}

func shellCallOutputReplayText(output []schemas.ResponsesShellCallOutputContent) string {
	var sb strings.Builder
	write := func(text string) {
		if text == "" {
			return
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(text)
	}
	for _, content := range output {
		write(content.Stdout)
		write(content.Stderr)
		switch content.Outcome.Type {
		case "timeout":
			write("[command timed out]")
		case "exit":
			if content.Outcome.ExitCode != nil {
				write(fmt.Sprintf("[exit code %d]", *content.Outcome.ExitCode))
			}
		}
	}
	return sb.String()
}

func toolOutputFailureReplayText(msg *schemas.ResponsesMessage) string {
	detail := msg.ResponsesToolMessage.Error.Text()
	failed := msg.Status != nil && (*msg.Status == "failed" || *msg.Status == "incomplete")

	switch {
	case detail != "" && failed:
		return "[tool call failed: " + detail + "]"
	case detail != "":
		return "[tool call error: " + detail + "]"
	case failed:
		return "[tool call failed]"
	}
	return ""
}

func convertBifrostToolOutputToAnthropicMessage(msg *schemas.ResponsesMessage) *AnthropicMessage {
	if msg.ResponsesToolMessage != nil {
		var outputText string

		if output := msg.ResponsesToolMessage.Output; output != nil {
			if output.ResponsesToolCallOutputStr != nil {
				outputText = *output.ResponsesToolCallOutputStr
			} else if len(output.ResponsesShellCallOutput) > 0 {

				outputText = shellCallOutputReplayText(output.ResponsesShellCallOutput)
				if outputText == "" {
					outputText = "[no output]"
				}
			}
		}

		if outputText == "" {
			outputText = toolOutputFailureReplayText(msg)
		}

		if outputText != "" {
			return &AnthropicMessage{
				Role: AnthropicMessageRoleUser,
				Content: AnthropicContent{
					ContentBlocks: []AnthropicContentBlock{{
						Type: AnthropicContentBlockTypeText,
						Text: &outputText,
					}},
				},
			}
		}
	}
	return nil
}

func convertAnthropicToolToBifrost(tool *AnthropicTool) *schemas.ResponsesTool {
	if tool == nil {
		return nil
	}

	if tool.MCPToolset != nil {
		return nil
	}

	if tool.Type != nil {

		switch typeStr := string(*tool.Type); {
		case strings.HasPrefix(typeStr, "web_search_"):
			bifrostTool := &schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeWebSearch,
			}
			if tool.AnthropicToolWebSearch != nil {
				bifrostTool.ResponsesToolWebSearch = &schemas.ResponsesToolWebSearch{
					Filters: &schemas.ResponsesToolWebSearchFilters{
						AllowedDomains: tool.AnthropicToolWebSearch.AllowedDomains,
						BlockedDomains: tool.AnthropicToolWebSearch.BlockedDomains,
					},
				}
				if tool.AnthropicToolWebSearch.MaxUses != nil {
					bifrostTool.ResponsesToolWebSearch.MaxUses = tool.AnthropicToolWebSearch.MaxUses
				}
				if tool.AnthropicToolWebSearch.UserLocation != nil {
					bifrostTool.ResponsesToolWebSearch.UserLocation = &schemas.ResponsesToolWebSearchUserLocation{
						Type:     tool.AnthropicToolWebSearch.UserLocation.Type,
						City:     tool.AnthropicToolWebSearch.UserLocation.City,
						Country:  tool.AnthropicToolWebSearch.UserLocation.Country,
						Timezone: tool.AnthropicToolWebSearch.UserLocation.Timezone,
					}
				}
			}

			return bifrostTool

		case strings.HasPrefix(typeStr, "web_fetch_"):
			bifrostTool := &schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeWebFetch,
			}
			if tool.AnthropicToolWebFetch != nil {
				bifrostTool.ResponsesToolWebFetch = &schemas.ResponsesToolWebFetch{
					MaxUses:           tool.AnthropicToolWebFetch.MaxUses,
					MaxContentTokens:  tool.AnthropicToolWebFetch.MaxContentTokens,
					UseCache:          tool.AnthropicToolWebFetch.UseCache,
					ResponseInclusion: tool.AnthropicToolWebFetch.ResponseInclusion,
				}
				if len(tool.AnthropicToolWebFetch.AllowedDomains) > 0 || len(tool.AnthropicToolWebFetch.BlockedDomains) > 0 {
					bifrostTool.ResponsesToolWebFetch.Filters = &schemas.ResponsesToolWebSearchFilters{
						AllowedDomains: tool.AnthropicToolWebFetch.AllowedDomains,
						BlockedDomains: tool.AnthropicToolWebFetch.BlockedDomains,
					}
				}
			}
			return bifrostTool
		}

		switch *tool.Type {
		case AnthropicToolTypeComputerToolset20260801, AnthropicToolTypeComputer20250124, AnthropicToolTypeComputer20251124:
			return convertAnthropicToResponsesComputerTool(tool)

		case AnthropicToolTypeCodeExecution20250522, AnthropicToolTypeCodeExecution,
			AnthropicToolTypeCodeExecution20260120, AnthropicToolTypeCodeExecution20260521:

			return &schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeCodeInterpreter,
				ResponsesToolCodeInterpreter: &schemas.ResponsesToolCodeInterpreter{
					Version: schemas.Ptr(string(*tool.Type)),
				},
			}

		case AnthropicToolTypeMemory20250818:
			return &schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeMemory,
				Name: &tool.Name,
			}

		case AnthropicToolTypeToolSearchBM25, AnthropicToolTypeToolSearchBM2520251119,
			AnthropicToolTypeToolSearchRegex, AnthropicToolTypeToolSearchRegex20251119:
			return &schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeToolSearch,
				Name: &tool.Name,
			}

		case AnthropicToolTypeBash20250124:
			return &schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeLocalShell,
			}

		case AnthropicToolTypeTextEditor20250124:
			return &schemas.ResponsesTool{
				Type: schemas.ResponsesToolType(AnthropicToolTypeTextEditor20250124),
				Name: &tool.Name,
			}
		case AnthropicToolTypeTextEditor20250429:
			return &schemas.ResponsesTool{
				Type: schemas.ResponsesToolType(AnthropicToolTypeTextEditor20250429),
				Name: &tool.Name,
			}
		case AnthropicToolTypeTextEditor20250728:
			return &schemas.ResponsesTool{
				Type: schemas.ResponsesToolType(AnthropicToolTypeTextEditor20250728),
				Name: &tool.Name,
			}

		case AnthropicToolTypeAdvisor20260301:
			bifrostTool := &schemas.ResponsesTool{
				Type: schemas.ResponsesToolTypeAdvisor,
			}
			if tool.AnthropicToolAdvisor != nil {
				bifrostTool.ResponsesToolAdvisor = &schemas.ResponsesToolAdvisor{
					Model:     tool.AnthropicToolAdvisor.Model,
					MaxUses:   tool.AnthropicToolAdvisor.MaxUses,
					MaxTokens: tool.AnthropicToolAdvisor.MaxTokens,
				}
				if tool.AnthropicToolAdvisor.Caching != nil {
					bifrostTool.ResponsesToolAdvisor.Caching = &schemas.ResponsesToolAdvisorCaching{
						Type: tool.AnthropicToolAdvisor.Caching.Type,
						TTL:  tool.AnthropicToolAdvisor.Caching.TTL,
					}
				}
			}
			return bifrostTool
		}
	}

	bifrostTool := &schemas.ResponsesTool{
		Type:        schemas.ResponsesToolTypeFunction,
		Name:        &tool.Name,
		Description: tool.Description,
	}

	if tool.InputSchema != nil || tool.Strict != nil {
		bifrostTool.ResponsesToolFunction = &schemas.ResponsesToolFunction{
			Parameters: tool.InputSchema,
			Strict:     tool.Strict,
		}
	}

	if tool.CacheControl != nil {
		bifrostTool.CacheControl = tool.CacheControl
	}

	return bifrostTool
}

func convertToolOutputToAnthropicContent(output *schemas.ResponsesToolMessageOutputStruct) *AnthropicContent {
	if output == nil {
		return nil
	}

	if output.ResponsesToolCallOutputStr != nil {
		return &AnthropicContent{
			ContentStr: output.ResponsesToolCallOutputStr,
		}
	}

	if len(output.ResponsesShellCallOutput) > 0 {
		text := schemas.ShellCallOutputText(output.ResponsesShellCallOutput)
		if text == "" {
			text = "[no output]"
		}
		return &AnthropicContent{ContentStr: &text}
	}

	if output.ResponsesFunctionToolCallOutputBlocks != nil {
		var resultBlocks []AnthropicContentBlock
		for _, block := range output.ResponsesFunctionToolCallOutputBlocks {
			if converted := convertContentBlockToAnthropic(block); converted != nil {
				resultBlocks = append(resultBlocks, *converted)
			}
		}
		if len(resultBlocks) > 0 {
			return &AnthropicContent{
				ContentBlocks: resultBlocks,
			}
		}
	}

	if output.ResponsesComputerToolCallOutput != nil && output.ResponsesComputerToolCallOutput.ImageURL != nil {
		imgBlock := ConvertToAnthropicImageBlock(schemas.ChatContentBlock{
			Type: schemas.ChatContentBlockTypeImage,
			ImageURLStruct: &schemas.ChatInputImage{
				URL: *output.ResponsesComputerToolCallOutput.ImageURL,
			},
		})
		return &AnthropicContent{
			ContentBlocks: []AnthropicContentBlock{imgBlock},
		}
	}

	return nil
}

func convertBifrostToolsToAnthropic(caps schemas.ModelCaps, tools []schemas.ResponsesTool, provider schemas.ModelProvider) ([]AnthropicTool, []AnthropicMCPServerV2, error) {

	hasWebSearchOrFetch := false
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeWebSearch || tool.Type == schemas.ResponsesToolTypeWebFetch {
			hasWebSearchOrFetch = true
			break
		}
	}

	sentTools := tools
	if hasWebSearchOrFetch {
		sentTools = slices.DeleteFunc(slices.Clone(tools), func(t schemas.ResponsesTool) bool {
			return t.Type == schemas.ResponsesToolTypeCodeInterpreter
		})
	}
	programmaticCaller, codeExecEmitVersion := "", ""
	for _, tool := range sentTools {
		if tool.Type == schemas.ResponsesToolTypeMCP && tool.ResponsesToolMCP != nil {
			continue
		}
		if hasProgrammaticCaller(tool.AllowedCallers) {
			declaredVersion, hasCodeExecution := declaredCodeExecutionVersion(sentTools)
			programmaticCaller, codeExecEmitVersion = resolveAnthropicProgrammaticCaller(declaredVersion, hasCodeExecution)
			if programmaticCaller == "" {

				return nil, nil, fmt.Errorf("code execution version %q has no allowed_callers value, so a tool restricted to programmatic callers cannot be expressed; declare code_execution_20250825 or newer", declaredVersion)
			}
			break
		}
	}

	anthropicTools := []AnthropicTool{}
	mcpServers := []AnthropicMCPServerV2{}
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeMCP && tool.ResponsesToolMCP != nil {
			server, toolset := convertBifrostMCPToolToAnthropicNew(&tool)
			if server != nil {
				mcpServers = append(mcpServers, *server)
			}
			if toolset != nil {
				mcpTool := AnthropicTool{MCPToolset: toolset}
				applyResponsesToolAnthropicFlags(&mcpTool, &tool, programmaticCaller)
				anthropicTools = append(anthropicTools, mcpTool)
			}
			continue
		}
		toolForConversion := &tool
		if codeExecEmitVersion != "" && tool.Type == schemas.ResponsesToolTypeCodeInterpreter {
			toolCopy := tool
			codeInterpreterCopy := schemas.ResponsesToolCodeInterpreter{}
			if tool.ResponsesToolCodeInterpreter != nil {
				codeInterpreterCopy = *tool.ResponsesToolCodeInterpreter
			}
			codeInterpreterCopy.Version = schemas.Ptr(codeExecEmitVersion)
			toolCopy.ResponsesToolCodeInterpreter = &codeInterpreterCopy
			toolForConversion = &toolCopy
		}
		if tool.ResponsesToolFunction != nil && tool.ResponsesToolFunction.Parameters != nil {
			normalized, err := NormalizeAnthropicToolInputSchema(tool.ResponsesToolFunction.Parameters)
			if err != nil {
				return nil, nil, err
			}
			toolCopy := *toolForConversion
			functionCopy := *tool.ResponsesToolFunction
			functionCopy.Parameters = normalized
			toolCopy.ResponsesToolFunction = &functionCopy
			toolForConversion = &toolCopy
		}
		anthropicTool := convertBifrostToolToAnthropic(caps, toolForConversion, provider, hasWebSearchOrFetch)
		if anthropicTool != nil {
			applyResponsesToolAnthropicFlags(anthropicTool, &tool, programmaticCaller)
			anthropicTools = append(anthropicTools, *anthropicTool)
		}
	}
	return anthropicTools, mcpServers, nil
}

func applyAnthropicToolFlagsToResponsesTool(at *AnthropicTool, rt *schemas.ResponsesTool) {
	if at == nil || rt == nil {
		return
	}
	if at.DeferLoading != nil {
		rt.DeferLoading = at.DeferLoading
	}
	if len(at.AllowedCallers) > 0 {
		rt.AllowedCallers = at.AllowedCallers
	}
	if len(at.InputExamples) > 0 {
		rt.InputExamples = make([]schemas.ChatToolInputExample, len(at.InputExamples))
		for i, ex := range at.InputExamples {
			rt.InputExamples[i] = schemas.ChatToolInputExample{
				Input:       ex.Input,
				Description: ex.Description,
			}
		}
	}
	if at.EagerInputStreaming != nil {
		rt.EagerInputStreaming = at.EagerInputStreaming
	}
	if at.AnthropicToolTextEditor != nil && at.MaxCharacters != nil {
		rt.MaxCharacters = at.MaxCharacters
	}
	if at.CacheControl != nil {
		rt.CacheControl = schemas.Ptr(*at.CacheControl)
	}
}

func applyResponsesToolAnthropicFlags(at *AnthropicTool, rt *schemas.ResponsesTool, programmaticCaller string) {
	if at == nil || rt == nil {
		return
	}
	if rt.DeferLoading != nil {
		at.DeferLoading = rt.DeferLoading
	}
	if len(rt.AllowedCallers) > 0 {
		at.AllowedCallers = anthropicAllowedCallers(rt.AllowedCallers, programmaticCaller)
	}
	if len(rt.InputExamples) > 0 && (at.Type == nil || *at.Type != AnthropicToolTypeComputerToolset20260801) {
		at.InputExamples = make([]AnthropicToolInputExample, len(rt.InputExamples))
		for i, ex := range rt.InputExamples {
			at.InputExamples[i] = AnthropicToolInputExample{
				Input:       ex.Input,
				Description: ex.Description,
			}
		}
	}
	if rt.EagerInputStreaming != nil {
		at.EagerInputStreaming = rt.EagerInputStreaming
	}
	if rt.MaxCharacters != nil && at.Type != nil && strings.HasPrefix(string(*at.Type), "text_editor_") {
		if at.AnthropicToolTextEditor == nil {
			at.AnthropicToolTextEditor = &AnthropicToolTextEditor{}
		}
		at.MaxCharacters = rt.MaxCharacters
	}
	if rt.CacheControl != nil {
		at.CacheControl = schemas.Ptr(*rt.CacheControl)
	}
}

func declaredCodeExecutionVersion(tools []schemas.ResponsesTool) (string, bool) {
	for _, tool := range tools {
		if tool.Type != schemas.ResponsesToolTypeCodeInterpreter {
			continue
		}
		if tool.ResponsesToolCodeInterpreter != nil && tool.ResponsesToolCodeInterpreter.Version != nil {
			return *tool.ResponsesToolCodeInterpreter.Version, true
		}
		return "", true
	}
	return "", false
}

func convertBifrostToolToAnthropic(caps schemas.ModelCaps, tool *schemas.ResponsesTool, provider schemas.ModelProvider, hasWebSearchOrFetch bool) *AnthropicTool {
	if tool == nil {
		return nil
	}

	if baseTool := computerUseBaseTool(string(tool.Type)); baseTool == "text_editor" {
		if wantType, wantName := NormalizedToolSpec(TextEditorGeneration(caps), baseTool); wantType != "" {
			anthropicType := AnthropicToolType(wantType)
			return &AnthropicTool{
				Type: &anthropicType,
				Name: wantName,
			}
		}
	}

	switch tool.Type {
	case schemas.ResponsesToolTypeCodeInterpreter:
		if hasWebSearchOrFetch {

			return nil
		}

		codeExecVersion := AnthropicToolTypeCodeExecution
		if tool.ResponsesToolCodeInterpreter != nil &&
			tool.ResponsesToolCodeInterpreter.Version != nil &&
			*tool.ResponsesToolCodeInterpreter.Version != "" {
			codeExecVersion = AnthropicToolType(*tool.ResponsesToolCodeInterpreter.Version)
		}
		return &AnthropicTool{
			Type: schemas.Ptr(codeExecVersion),
			Name: string(AnthropicToolNameCodeExecution),
		}
	case schemas.ResponsesToolTypeComputerUsePreview, schemas.ResponsesToolTypeComputer:
		return convertResponsesToAnthropicComputerTool(caps, tool)
	case schemas.ResponsesToolTypeWebSearch:
		webSearchType := AnthropicToolTypeWebSearch20250305

		features, ok := ProviderFeatures[provider]
		model := caps.Model()
		if version, pinned := caps.ServerTool(ServerToolWebSearch); pinned {
			webSearchType = AnthropicToolType(version)
		} else if ok && features.WebSearchDynamic &&
			(strings.Contains(model, "4.6") || strings.Contains(model, "4-6") || IsOpus47Plus(model) || IsSonnet5Plus(model) || IsFableFamily(model)) {
			webSearchType = AnthropicToolTypeWebSearch20260209
		}
		anthropicTool := &AnthropicTool{
			Type:                   schemas.Ptr(webSearchType),
			Name:                   string(AnthropicToolNameWebSearch),
			AnthropicToolWebSearch: &AnthropicToolWebSearch{},
		}
		if tool.ResponsesToolWebSearch != nil {
			if tool.ResponsesToolWebSearch.MaxUses != nil {
				anthropicTool.AnthropicToolWebSearch.MaxUses = tool.ResponsesToolWebSearch.MaxUses
			}
			if tool.ResponsesToolWebSearch.Filters != nil {
				anthropicTool.AnthropicToolWebSearch.AllowedDomains = tool.ResponsesToolWebSearch.Filters.AllowedDomains
				anthropicTool.AnthropicToolWebSearch.BlockedDomains = tool.ResponsesToolWebSearch.Filters.BlockedDomains
			}
			if tool.ResponsesToolWebSearch.UserLocation != nil {
				anthropicTool.AnthropicToolWebSearch.UserLocation = &AnthropicToolWebSearchUserLocation{
					Type:     tool.ResponsesToolWebSearch.UserLocation.Type,
					City:     tool.ResponsesToolWebSearch.UserLocation.City,
					Country:  tool.ResponsesToolWebSearch.UserLocation.Country,
					Timezone: tool.ResponsesToolWebSearch.UserLocation.Timezone,
				}
			}
		}

		return anthropicTool
	case schemas.ResponsesToolTypeWebFetch:
		webFetchType := AnthropicToolTypeWebFetch20250910

		features, ok := ProviderFeatures[provider]
		model := caps.Model()
		if version, pinned := caps.ServerTool(ServerToolWebFetch); pinned {
			webFetchType = AnthropicToolType(version)
		} else if ok && features.WebSearchDynamic &&
			(strings.Contains(model, "4.6") || strings.Contains(model, "4-6")) {
			webFetchType = AnthropicToolTypeWebFetch20260309
		}
		if tool.ResponsesToolWebFetch != nil && tool.ResponsesToolWebFetch.ResponseInclusion != nil {
			webFetchType = AnthropicToolTypeWebFetch20260318
		} else if tool.ResponsesToolWebFetch != nil && tool.ResponsesToolWebFetch.UseCache != nil &&
			webFetchType == AnthropicToolTypeWebFetch20250910 {
			webFetchType = AnthropicToolTypeWebFetch20260309
		}
		anthropicTool := &AnthropicTool{
			Type:                  schemas.Ptr(webFetchType),
			Name:                  string(AnthropicToolNameWebFetch),
			AnthropicToolWebFetch: &AnthropicToolWebFetch{},
		}
		if tool.ResponsesToolWebFetch != nil {
			anthropicTool.AnthropicToolWebFetch.MaxUses = tool.ResponsesToolWebFetch.MaxUses
			anthropicTool.AnthropicToolWebFetch.MaxContentTokens = tool.ResponsesToolWebFetch.MaxContentTokens
			anthropicTool.AnthropicToolWebFetch.UseCache = tool.ResponsesToolWebFetch.UseCache
			anthropicTool.AnthropicToolWebFetch.ResponseInclusion = tool.ResponsesToolWebFetch.ResponseInclusion
			if tool.ResponsesToolWebFetch.Filters != nil {
				anthropicTool.AnthropicToolWebFetch.AllowedDomains = tool.ResponsesToolWebFetch.Filters.AllowedDomains
				anthropicTool.AnthropicToolWebFetch.BlockedDomains = tool.ResponsesToolWebFetch.Filters.BlockedDomains
			}
		}
		return anthropicTool
	case schemas.ResponsesToolTypeMemory:
		anthropicTool := &AnthropicTool{
			Type: schemas.Ptr(AnthropicToolTypeMemory20250818),
			Name: string(AnthropicToolNameMemory),
		}
		return anthropicTool
	case schemas.ResponsesToolTypeToolSearch:
		toolSearchType := AnthropicToolTypeToolSearchBM2520251119
		toolSearchName := AnthropicToolNameToolSearchBM25
		if tool.Name != nil && strings.Contains(*tool.Name, "regex") {
			toolSearchType = AnthropicToolTypeToolSearchRegex20251119
			toolSearchName = AnthropicToolNameToolSearchRegex
		}
		return &AnthropicTool{
			Type: schemas.Ptr(toolSearchType),
			Name: string(toolSearchName),
		}
	case schemas.ResponsesToolTypeLocalShell:
		return &AnthropicTool{
			Type: schemas.Ptr(AnthropicToolTypeBash20250124),
			Name: string(AnthropicToolNameBash),
		}
	case schemas.ResponsesToolType(AnthropicToolTypeTextEditor20250124):
		return &AnthropicTool{
			Type: schemas.Ptr(AnthropicToolTypeTextEditor20250124),
			Name: string(AnthropicToolNameTextEditorLegacy),
		}
	case schemas.ResponsesToolType(AnthropicToolTypeTextEditor20250429):
		return &AnthropicTool{
			Type: schemas.Ptr(AnthropicToolTypeTextEditor20250429),
			Name: string(AnthropicToolNameTextEditorLegacy),
		}
	case schemas.ResponsesToolType(AnthropicToolTypeTextEditor20250728):
		return &AnthropicTool{
			Type: schemas.Ptr(AnthropicToolTypeTextEditor20250728),
			Name: string(AnthropicToolNameTextEditor),
		}
	case schemas.ResponsesToolTypeAdvisor:
		advisor := &AnthropicToolAdvisor{}
		if tool.ResponsesToolAdvisor != nil {
			advisor.Model = tool.ResponsesToolAdvisor.Model
			advisor.MaxUses = tool.ResponsesToolAdvisor.MaxUses
			advisor.MaxTokens = tool.ResponsesToolAdvisor.MaxTokens
			if tool.ResponsesToolAdvisor.Caching != nil {
				advisor.Caching = &AnthropicToolAdvisorCaching{
					Type: tool.ResponsesToolAdvisor.Caching.Type,
					TTL:  tool.ResponsesToolAdvisor.Caching.TTL,
				}
			}
		}
		return &AnthropicTool{
			Type:                 schemas.Ptr(AnthropicToolTypeAdvisor20260301),
			Name:                 string(AnthropicToolNameAdvisor),
			AnthropicToolAdvisor: advisor,
		}
	}

	if tool.Name == nil || *tool.Name == "" {
		return nil
	}

	anthropicTool := &AnthropicTool{}
	anthropicTool.Name = *tool.Name

	if tool.Description != nil {
		anthropicTool.Description = tool.Description
	}

	if tool.ResponsesToolFunction != nil {
		anthropicTool.Strict = tool.ResponsesToolFunction.Strict
	}
	if tool.ResponsesToolFunction != nil && tool.ResponsesToolFunction.Parameters != nil {
		anthropicTool.InputSchema = tool.ResponsesToolFunction.Parameters
	} else {

		anthropicTool.InputSchema = &schemas.ToolFunctionParameters{
			Type:       "object",
			Properties: &schemas.OrderedMap{},
		}
	}

	if anthropicTool.InputSchema != nil {
		anthropicTool.InputSchema = anthropicTool.InputSchema.Normalized()
	}

	if tool.CacheControl != nil {
		anthropicTool.CacheControl = tool.CacheControl
	}

	return anthropicTool
}

func convertResponsesToolChoiceToAnthropic(toolChoice *schemas.ResponsesToolChoice) *AnthropicToolChoice {
	if toolChoice == nil {
		return nil
	}

	if toolChoice.ResponsesToolChoiceStruct == nil && toolChoice.ResponsesToolChoiceStr != nil {
		switch schemas.ResponsesToolChoiceType(*toolChoice.ResponsesToolChoiceStr) {
		case schemas.ResponsesToolChoiceTypeAuto:
			return &AnthropicToolChoice{Type: "auto"}
		case schemas.ResponsesToolChoiceTypeAny, schemas.ResponsesToolChoiceTypeRequired:
			return &AnthropicToolChoice{Type: "any"}
		case schemas.ResponsesToolChoiceTypeNone:
			return &AnthropicToolChoice{Type: "none"}
		default:
			return nil
		}
	}

	if toolChoice.ResponsesToolChoiceStruct == nil {
		return nil
	}

	anthropicChoice := &AnthropicToolChoice{}

	var toolChoiceType *string
	if toolChoice.ResponsesToolChoiceStruct != nil {
		toolChoiceType = schemas.Ptr(string(toolChoice.ResponsesToolChoiceStruct.Type))
	} else {
		toolChoiceType = toolChoice.ResponsesToolChoiceStr
	}

	switch *toolChoiceType {
	case "auto":
		anthropicChoice.Type = "auto"
	case "required":
		anthropicChoice.Type = "any"
	case "function":

		if toolChoice.ResponsesToolChoiceStruct != nil && toolChoice.ResponsesToolChoiceStruct.Name != nil {
			anthropicChoice.Type = "tool"
			anthropicChoice.Name = *toolChoice.ResponsesToolChoiceStruct.Name
		}
		return anthropicChoice
	}

	if toolChoice.ResponsesToolChoiceStruct != nil && toolChoice.ResponsesToolChoiceStruct.Name != nil {
		anthropicChoice.Type = "tool"
		anthropicChoice.Name = *toolChoice.ResponsesToolChoiceStruct.Name
	}

	return anthropicChoice
}

func convertContentBlockToAnthropic(block schemas.ResponsesMessageContentBlock) *AnthropicContentBlock {
	return convertContentBlockToAnthropicWithCaps(schemas.ModelCaps{}, block)
}

func convertContentBlockToAnthropicWithCaps(caps schemas.ModelCaps, block schemas.ResponsesMessageContentBlock) *AnthropicContentBlock {
	switch block.Type {
	case schemas.ResponsesInputMessageContentBlockTypeToolAddition, schemas.ResponsesInputMessageContentBlockTypeToolRemoval:
		if block.ToolChange == nil {
			return nil
		}
		blockType := AnthropicContentBlockTypeToolAddition
		if block.Type == schemas.ResponsesInputMessageContentBlockTypeToolRemoval {
			blockType = AnthropicContentBlockTypeToolRemoval
		}
		return &AnthropicContentBlock{
			Type:         blockType,
			Tool:         convertToolChangeTargetToAnthropic(caps, block.ToolChange),
			CacheControl: block.CacheControl,
		}
	case schemas.ResponsesInputMessageContentBlockTypeText, schemas.ResponsesOutputMessageContentTypeText:
		anthropicBlock := AnthropicContentBlock{}
		if block.Text != nil {
			anthropicBlock = AnthropicContentBlock{
				Type:         AnthropicContentBlockTypeText,
				Text:         block.Text,
				CacheControl: block.CacheControl,
			}
			if block.ResponsesOutputMessageContentText != nil && len(block.ResponsesOutputMessageContentText.Annotations) > 0 {
				anthropicBlock.Citations = &AnthropicCitations{
					TextCitations: make([]AnthropicTextCitation, len(block.ResponsesOutputMessageContentText.Annotations)),
				}
				for i, annotation := range block.ResponsesOutputMessageContentText.Annotations {
					anthropicBlock.Citations.TextCitations[i] = convertAnnotationToAnthropicCitation(annotation)
				}
			}
			return &anthropicBlock
		}
	case schemas.ResponsesInputMessageContentBlockTypeImage:
		if block.ResponsesInputMessageContentBlockImage != nil && block.ResponsesInputMessageContentBlockImage.ImageURL != nil {

			chatBlock := schemas.ChatContentBlock{
				Type: schemas.ChatContentBlockTypeImage,
				ImageURLStruct: &schemas.ChatInputImage{
					URL: *block.ResponsesInputMessageContentBlockImage.ImageURL,
				},
				CacheControl: block.CacheControl,
			}
			anthropicBlock := ConvertToAnthropicImageBlock(chatBlock)
			return &anthropicBlock
		}
	case schemas.ResponsesOutputMessageContentTypeCompaction:
		if block.ResponsesOutputMessageContentCompaction != nil {
			return &AnthropicContentBlock{
				Type: AnthropicContentBlockTypeCompaction,
				Content: &AnthropicContent{
					ContentStr: &block.ResponsesOutputMessageContentCompaction.Summary,
				},
				CacheControl: block.CacheControl,
				ToolChanges:  convertBifrostContentBlocksToAnthropic(caps, block.ResponsesOutputMessageContentCompaction.ToolChanges),
			}
		}
	case schemas.ResponsesOutputMessageContentTypeFallback:
		if fb := block.ResponsesOutputMessageContentFallback; fb != nil {
			fallbackBlock := &AnthropicContentBlock{
				Type: AnthropicContentBlockTypeFallback,
				From: &AnthropicFallbackModel{Model: fb.FromModel},
				To:   &AnthropicFallbackModel{Model: fb.ToModel},
			}
			if fb.TriggerType != "" {
				fallbackBlock.Trigger = &AnthropicFallbackTrigger{Type: fb.TriggerType, Category: fb.TriggerCategory}
			}
			return fallbackBlock
		}
	case schemas.ResponsesInputMessageContentBlockTypeFile:
		if block.ResponsesInputMessageContentBlockFile != nil || block.FileID != nil {

			anthropicBlock := ConvertResponsesFileBlockToAnthropic(
				block.ResponsesInputMessageContentBlockFile,
				block.FileID,
				block.CacheControl,
				block.Citations,
			)
			return &anthropicBlock
		}
	case schemas.ResponsesInputMessageContentBlockTypeContainer:
		if block.FileID != nil {
			return &AnthropicContentBlock{
				Type:         AnthropicContentBlockTypeContainerUpload,
				FileID:       block.FileID,
				CacheControl: block.CacheControl,
			}
		}
	case schemas.ResponsesInputMessageContentBlockTypeToolReference:
		if block.ToolName != nil {
			return &AnthropicContentBlock{
				Type:     AnthropicContentBlockTypeToolReference,
				ToolName: block.ToolName,
			}
		}
	case schemas.ResponsesOutputMessageContentTypeReasoning:
		if block.Text != nil {
			return &AnthropicContentBlock{
				Type:      AnthropicContentBlockTypeThinking,
				Thinking:  block.Text,
				Signature: block.Signature,
			}
		}
	}
	return nil
}

func convertBifrostContentBlocksToAnthropic(caps schemas.ModelCaps, blocks []schemas.ResponsesMessageContentBlock) []AnthropicContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	var result []AnthropicContentBlock
	for _, block := range blocks {
		if converted := convertContentBlockToAnthropicWithCaps(caps, block); converted != nil {
			result = append(result, *converted)
		}
	}
	if len(result) > 0 {
		return result
	}
	return nil
}

func (block AnthropicContentBlock) toBifrostResponsesImageBlock() schemas.ResponsesMessageContentBlock {
	return schemas.ResponsesMessageContentBlock{
		Type: schemas.ResponsesInputMessageContentBlockTypeImage,
		ResponsesInputMessageContentBlockImage: &schemas.ResponsesInputMessageContentBlockImage{
			ImageURL: schemas.Ptr(getImageURLFromBlock(block)),
		},
		CacheControl: block.CacheControl,
	}
}

func (block AnthropicContentBlock) toBifrostResponsesContainerUploadBlock() schemas.ResponsesMessageContentBlock {
	return schemas.ResponsesMessageContentBlock{
		Type:         schemas.ResponsesInputMessageContentBlockTypeContainer,
		FileID:       block.FileID,
		CacheControl: block.CacheControl,
	}
}

func (block AnthropicContentBlock) toBifrostResponsesDocumentBlock() schemas.ResponsesMessageContentBlock {
	resultBlock := schemas.ResponsesMessageContentBlock{
		Type:                                  schemas.ResponsesInputMessageContentBlockTypeFile,
		CacheControl:                          block.CacheControl,
		ResponsesInputMessageContentBlockFile: &schemas.ResponsesInputMessageContentBlockFile{},
	}

	if block.Citations != nil && block.Citations.Config != nil {
		resultBlock.Citations = block.Citations.Config
	}

	if block.Title != nil {
		resultBlock.ResponsesInputMessageContentBlockFile.Filename = block.Title
	}

	if block.Source == nil || block.Source.SourceObj == nil {

		return resultBlock
	}
	src := block.Source.SourceObj

	switch src.Type {
	case "url":

		if src.URL != nil {
			resultBlock.ResponsesInputMessageContentBlockFile.FileURL = src.URL
		}
	case "base64":

		if src.Data != nil {

			mediaType := "application/pdf"
			if src.MediaType != nil {
				mediaType = *src.MediaType
			}
			dataURL := *src.Data
			if !strings.HasPrefix(dataURL, "data:") {
				dataURL = "data:" + mediaType + ";base64," + *src.Data
			}
			resultBlock.ResponsesInputMessageContentBlockFile.FileData = &dataURL
		}
	case "text":

		if src.Data != nil {
			resultBlock.ResponsesInputMessageContentBlockFile.FileType = schemas.Ptr("text/plain")
			resultBlock.ResponsesInputMessageContentBlockFile.FileData = src.Data
		}
	case "file":

		if src.FileID != nil {
			resultBlock.FileID = src.FileID
		}
	}

	file := resultBlock.ResponsesInputMessageContentBlockFile
	if file.FileData != nil && (file.Filename == nil || *file.Filename == "") {
		file.Filename = schemas.Ptr(defaultDocumentFilename(src))
	}

	return resultBlock
}

func defaultDocumentFilename(src *AnthropicSource) string {
	mediaType := ""
	if src != nil && src.MediaType != nil {
		mediaType = *src.MediaType
	}
	if src != nil && src.Type == "text" {
		mediaType = "text/plain"
	}

	ext := ".pdf"
	switch mediaType {
	case "", "application/pdf":
		ext = ".pdf"
	case "text/plain":
		ext = ".txt"
	default:

		if _, subtype, found := strings.Cut(mediaType, "/"); found && subtype != "" {

			if _, suffix, hasSuffix := strings.Cut(subtype, "+"); hasSuffix && suffix != "" {
				subtype = suffix
			}
			ext = "." + subtype
		}
	}
	return "document" + ext
}

func applyMCPToolsetConfigToBifrostTool(bifrostTool *schemas.ResponsesTool, toolset *AnthropicMCPToolsetTool) {
	if bifrostTool == nil || bifrostTool.ResponsesToolMCP == nil || toolset == nil {
		return
	}

	if toolset.Configs != nil {
		defaultEnabled := true
		if toolset.DefaultConfig != nil && toolset.DefaultConfig.Enabled != nil {
			defaultEnabled = *toolset.DefaultConfig.Enabled
		}

		if !defaultEnabled {

			allowedTools := make([]string, 0, len(toolset.Configs))
			for toolName, config := range toolset.Configs {
				if config != nil && config.Enabled != nil && *config.Enabled {
					allowedTools = append(allowedTools, toolName)
				}
			}
			bifrostTool.ResponsesToolMCP.AllowedTools = &schemas.ResponsesToolMCPAllowedTools{
				ToolNames: allowedTools,
			}
		}
	}

	if toolset.CacheControl != nil {
		bifrostTool.CacheControl = toolset.CacheControl
	}

	if toolset.DefaultConfig != nil {
		bifrostTool.ResponsesToolMCP.DefaultConfig = convertAnthropicMCPToolsetConfigToBifrost(toolset.DefaultConfig)
	}
	if len(toolset.Configs) > 0 {
		bifrostTool.ResponsesToolMCP.ToolConfigs = make(map[string]*schemas.ResponsesToolMCPToolConfig, len(toolset.Configs))
		for name, config := range toolset.Configs {
			bifrostTool.ResponsesToolMCP.ToolConfigs[name] = convertAnthropicMCPToolsetConfigToBifrost(config)
		}
	}
}

func convertAnthropicMCPToolsetConfigToBifrost(config *AnthropicMCPToolsetConfig) *schemas.ResponsesToolMCPToolConfig {
	if config == nil {
		return nil
	}
	return &schemas.ResponsesToolMCPToolConfig{Enabled: config.Enabled, DeferLoading: config.DeferLoading}
}

func convertBifrostMCPToolsetConfigToAnthropic(config *schemas.ResponsesToolMCPToolConfig) *AnthropicMCPToolsetConfig {
	if config == nil {
		return nil
	}
	return &AnthropicMCPToolsetConfig{Enabled: config.Enabled, DeferLoading: config.DeferLoading}
}

func convertBifrostMCPToolToAnthropicNew(tool *schemas.ResponsesTool) (*AnthropicMCPServerV2, *AnthropicMCPToolsetTool) {
	if tool == nil || tool.Type != schemas.ResponsesToolTypeMCP || tool.ResponsesToolMCP == nil {
		return nil, nil
	}

	server := &AnthropicMCPServerV2{
		Type: "url",
		Name: tool.ResponsesToolMCP.ServerLabel,
	}
	if tool.ResponsesToolMCP.ServerURL != nil {
		server.URL = *tool.ResponsesToolMCP.ServerURL
	}
	if tool.ResponsesToolMCP.Authorization != nil {
		server.AuthorizationToken = tool.ResponsesToolMCP.Authorization
	}

	toolset := &AnthropicMCPToolsetTool{
		Type:          "mcp_toolset",
		MCPServerName: tool.ResponsesToolMCP.ServerLabel,
		CacheControl:  tool.CacheControl,
	}

	if mcp := tool.ResponsesToolMCP; mcp.DefaultConfig != nil || len(mcp.ToolConfigs) > 0 {
		toolset.DefaultConfig = convertBifrostMCPToolsetConfigToAnthropic(mcp.DefaultConfig)
		if len(mcp.ToolConfigs) > 0 {
			toolset.Configs = make(map[string]*AnthropicMCPToolsetConfig, len(mcp.ToolConfigs))
			for name, config := range mcp.ToolConfigs {
				toolset.Configs[name] = convertBifrostMCPToolsetConfigToAnthropic(config)
			}
		}
		return server, toolset
	}

	if tool.ResponsesToolMCP.AllowedTools != nil {

		toolset.DefaultConfig = &AnthropicMCPToolsetConfig{Enabled: new(false)}
		if len(tool.ResponsesToolMCP.AllowedTools.ToolNames) > 0 {
			toolset.Configs = make(map[string]*AnthropicMCPToolsetConfig, len(tool.ResponsesToolMCP.AllowedTools.ToolNames))
			for _, toolName := range tool.ResponsesToolMCP.AllowedTools.ToolNames {
				toolset.Configs[toolName] = &AnthropicMCPToolsetConfig{Enabled: schemas.Ptr(true)}
			}
		}
	}

	return server, toolset
}

func convertAnthropicCitationToAnnotation(citation AnthropicTextCitation, fullText string) schemas.ResponsesOutputMessageContentTextAnnotation {
	annotation := schemas.ResponsesOutputMessageContentTextAnnotation{
		Type:  string(citation.Type),
		Index: citation.DocumentIndex,
		Text:  schemas.Ptr(citation.CitedText),
	}

	switch citation.Type {
	case AnthropicCitationTypeCharLocation:

		annotation.StartCharIndex = citation.StartCharIndex
		annotation.EndCharIndex = citation.EndCharIndex
		annotation.Filename = citation.DocumentTitle
		annotation.FileID = citation.FileID

	case AnthropicCitationTypePageLocation:

		annotation.StartPageNumber = citation.StartPageNumber
		annotation.EndPageNumber = citation.EndPageNumber
		annotation.Filename = citation.DocumentTitle
		annotation.FileID = citation.FileID

	case AnthropicCitationTypeContentBlockLocation:

		annotation.StartBlockIndex = citation.StartBlockIndex
		annotation.EndBlockIndex = citation.EndBlockIndex
		annotation.Filename = citation.DocumentTitle
		annotation.FileID = citation.FileID

	case AnthropicCitationTypeWebSearchResultLocation:

		annotation.Type = "url_citation"
		annotation.Title = citation.Title
		annotation.URL = citation.URL
		annotation.EncryptedIndex = citation.EncryptedIndex

		if fullText != "" && citation.URL != nil && *citation.URL != "" {
			startIdx := strings.Index(fullText, *citation.URL)
			if startIdx != -1 {
				endIdx := startIdx + len(*citation.URL)
				annotation.StartIndex = schemas.Ptr(startIdx)
				annotation.EndIndex = schemas.Ptr(endIdx)
			} else {

				annotation.StartIndex = schemas.Ptr(0)
				annotation.EndIndex = schemas.Ptr(len(fullText))
			}
		}

	case AnthropicCitationTypeSearchResultLocation:

		annotation.StartBlockIndex = citation.StartBlockIndex
		annotation.EndBlockIndex = citation.EndBlockIndex
		annotation.Title = citation.Title
		annotation.Source = citation.Source
	}

	return annotation
}

func convertAnnotationToAnthropicCitation(annotation schemas.ResponsesOutputMessageContentTextAnnotation) AnthropicTextCitation {
	citation := AnthropicTextCitation{
		Type:      AnthropicCitationType(annotation.Type),
		CitedText: "",
	}

	if annotation.Text != nil {
		citation.CitedText = *annotation.Text
	}

	switch annotation.Type {
	case string(AnthropicCitationTypeCharLocation):

		citation.StartCharIndex = annotation.StartCharIndex
		citation.EndCharIndex = annotation.EndCharIndex
		citation.DocumentTitle = annotation.Filename
		citation.DocumentIndex = annotation.Index
		citation.FileID = annotation.FileID

	case string(AnthropicCitationTypePageLocation):

		citation.StartPageNumber = annotation.StartPageNumber
		citation.EndPageNumber = annotation.EndPageNumber
		citation.DocumentTitle = annotation.Filename
		citation.DocumentIndex = annotation.Index
		citation.FileID = annotation.FileID

	case string(AnthropicCitationTypeContentBlockLocation):

		citation.StartBlockIndex = annotation.StartBlockIndex
		citation.EndBlockIndex = annotation.EndBlockIndex
		citation.DocumentTitle = annotation.Filename
		citation.DocumentIndex = annotation.Index
		citation.FileID = annotation.FileID

	case string(AnthropicCitationTypeWebSearchResultLocation):

		citation.Title = annotation.Title
		citation.URL = annotation.URL
		citation.EncryptedIndex = annotation.EncryptedIndex

	case string(AnthropicCitationTypeSearchResultLocation):

		citation.StartBlockIndex = annotation.StartBlockIndex
		citation.EndBlockIndex = annotation.EndBlockIndex
		citation.Title = annotation.Title
		citation.Source = annotation.Source

	case "url_citation":
		citation.Type = AnthropicCitationTypeWebSearchResultLocation
		citation.URL = annotation.URL
		citation.Title = annotation.Title
		citation.EncryptedIndex = annotation.EncryptedIndex

	case "file_citation", "container_file_citation", "file_path", "text_annotation":

		citation.Type = "char_location"
		citation.StartCharIndex = annotation.StartIndex
		citation.EndCharIndex = annotation.EndIndex
		citation.DocumentTitle = annotation.Filename
		citation.Title = annotation.Title
		citation.FileID = annotation.FileID
	}

	return citation
}

func convertResponsesToAnthropicComputerTool(caps schemas.ModelCaps, tool *schemas.ResponsesTool) *AnthropicTool {

	toolset := &AnthropicTool{Type: schemas.Ptr(AnthropicToolTypeComputerToolset20260801)}
	generation := ComputerUseGeneration(caps)
	if generation == ComputerUseGenToolset20260801 {
		return toolset
	}

	preview := tool.ResponsesToolComputerUsePreview
	if tool.Type == schemas.ResponsesToolTypeComputer || preview == nil || preview.DisplayWidth <= 0 || preview.DisplayHeight <= 0 {
		if AcceptsComputerToolset(caps) {
			return toolset
		}
		return nil
	}
	computerToolType := AnthropicToolTypeComputer20250124
	if generation == ComputerUseGen20251124 {
		computerToolType = AnthropicToolTypeComputer20251124
	}
	return &AnthropicTool{
		Type: schemas.Ptr(computerToolType),
		Name: string(AnthropicToolNameComputer),
		AnthropicToolComputerUse: &AnthropicToolComputerUse{
			DisplayWidthPx:  schemas.Ptr(preview.DisplayWidth),
			DisplayHeightPx: schemas.Ptr(preview.DisplayHeight),
			DisplayNumber:   schemas.Ptr(1),
			EnableZoom:      preview.EnableZoom,
		},
	}
}

func convertAnthropicToResponsesComputerTool(tool *AnthropicTool) *schemas.ResponsesTool {
	if tool.Type != nil && *tool.Type == AnthropicToolTypeComputerToolset20260801 {
		return &schemas.ResponsesTool{Type: schemas.ResponsesToolTypeComputer}
	}
	bifrostTool := &schemas.ResponsesTool{
		Type: schemas.ResponsesToolTypeComputerUsePreview,
	}
	if tool.AnthropicToolComputerUse != nil {
		bifrostTool.ResponsesToolComputerUsePreview = &schemas.ResponsesToolComputerUsePreview{
			Environment: "browser",
		}
		if tool.AnthropicToolComputerUse.DisplayWidthPx != nil {
			bifrostTool.ResponsesToolComputerUsePreview.DisplayWidth = *tool.AnthropicToolComputerUse.DisplayWidthPx
		}
		if tool.AnthropicToolComputerUse.DisplayHeightPx != nil {
			bifrostTool.ResponsesToolComputerUsePreview.DisplayHeight = *tool.AnthropicToolComputerUse.DisplayHeightPx
		}
		if tool.AnthropicToolComputerUse.EnableZoom != nil {
			bifrostTool.ResponsesToolComputerUsePreview.EnableZoom = tool.AnthropicToolComputerUse.EnableZoom
		}
	}
	return bifrostTool
}

func convertResponsesToAnthropicComputerAction(action *schemas.ResponsesComputerToolCallAction) map[string]any {
	input := map[string]any{}
	var actionStr string

	switch action.Type {
	case "screenshot":
		actionStr = "screenshot"

	case "click":

		if action.Button != nil {
			switch *action.Button {
			case "right":
				actionStr = "right_click"
			case "wheel":
				actionStr = "middle_click"
			default:
				actionStr = "left_click"
			}
		} else {
			actionStr = "left_click"
		}

		if action.X != nil && action.Y != nil {
			input["coordinate"] = []int{*action.X, *action.Y}
		}

	case "double_click":
		actionStr = "double_click"
		if action.X != nil && action.Y != nil {
			input["coordinate"] = []int{*action.X, *action.Y}
		}

	case "move":
		actionStr = "mouse_move"
		if action.X != nil && action.Y != nil {
			input["coordinate"] = []int{*action.X, *action.Y}
		}

	case "type":
		actionStr = "type"
		if action.Text != nil {
			input["text"] = *action.Text
		}

	case "keypress":
		actionStr = "key"
		if len(action.Keys) > 0 {

			text := ""
			for i, key := range action.Keys {
				if i > 0 {
					text += "+"
				}
				text += key
			}
			input["text"] = text
		}

	case "scroll":
		actionStr = "scroll"
		if action.X != nil && action.Y != nil {
			input["coordinate"] = []int{*action.X, *action.Y}
		}

		scrollX := 0
		scrollY := 0
		if action.ScrollX != nil {
			scrollX = *action.ScrollX
		}
		if action.ScrollY != nil {
			scrollY = *action.ScrollY
		}

		if math.Abs(float64(scrollY)) >= math.Abs(float64(scrollX)) && scrollY != 0 {

			if scrollY > 0 {
				input["scroll_direction"] = "down"
				input["scroll_amount"] = scrollY / 100
			} else {
				input["scroll_direction"] = "up"
				input["scroll_amount"] = (-scrollY) / 100
			}
		} else if scrollX != 0 {

			if scrollX > 0 {
				input["scroll_direction"] = "right"
				input["scroll_amount"] = scrollX / 100
			} else {
				input["scroll_direction"] = "left"
				input["scroll_amount"] = (-scrollX) / 100
			}
		}

	case "drag":
		actionStr = "left_click_drag"
		if len(action.Path) >= 2 {

			input["start_coordinate"] = []int{action.Path[0].X, action.Path[0].Y}
			input["end_coordinate"] = []int{action.Path[len(action.Path)-1].X, action.Path[len(action.Path)-1].Y}
		}

	case "wait":
		actionStr = "wait"
		input["duration"] = 2

	case "zoom":
		actionStr = "zoom"

		if len(action.Region) == 4 {
			input["region"] = action.Region
		}

	default:

		actionStr = action.Type
	}

	input["action"] = actionStr

	return input
}

func convertAnthropicToResponsesComputerAction(inputMap map[string]interface{}) *schemas.ResponsesComputerToolCallAction {
	action := &schemas.ResponsesComputerToolCallAction{}

	actionStr, ok := inputMap["action"].(string)
	if !ok {
		return action
	}

	switch actionStr {
	case "screenshot":
		action.Type = "screenshot"

	case "left_click":
		action.Type = "click"
		action.Button = schemas.Ptr("left")

	case "right_click":
		action.Type = "click"
		action.Button = schemas.Ptr("right")

	case "middle_click":
		action.Type = "click"
		action.Button = schemas.Ptr("wheel")

	case "double_click":
		action.Type = "double_click"

	case "mouse_move":
		action.Type = "move"

	case "type":
		action.Type = "type"
		if text, ok := inputMap["text"].(string); ok {
			action.Text = schemas.Ptr(text)
		}

	case "key":
		action.Type = "keypress"
		if text, ok := inputMap["text"].(string); ok {

			keys := strings.Split(text, "+")
			action.Keys = keys
		}

	case "scroll":
		action.Type = "scroll"

		if direction, ok := inputMap["scroll_direction"].(string); ok {
			amount := 100
			if scrollAmount, ok := inputMap["scroll_amount"].(float64); ok {
				amount = int(scrollAmount) * 100
			}
			switch direction {
			case "down":
				action.ScrollY = schemas.Ptr(amount)
				action.ScrollX = schemas.Ptr(0)
			case "up":
				action.ScrollY = schemas.Ptr(-amount)
				action.ScrollX = schemas.Ptr(0)
			case "right":
				action.ScrollX = schemas.Ptr(amount)
				action.ScrollY = schemas.Ptr(0)
			case "left":
				action.ScrollX = schemas.Ptr(-amount)
				action.ScrollY = schemas.Ptr(0)
			}
		}

	case "left_click_drag":
		action.Type = "drag"

		if startCoord, ok := inputMap["start_coordinate"].([]interface{}); ok && len(startCoord) == 2 {
			if endCoord, ok := inputMap["end_coordinate"].([]interface{}); ok && len(endCoord) == 2 {

				startX, startXOk := startCoord[0].(float64)
				startY, startYOk := startCoord[1].(float64)
				endX, endXOk := endCoord[0].(float64)
				endY, endYOk := endCoord[1].(float64)
				if startXOk && startYOk && endXOk && endYOk {
					action.Path = []schemas.ResponsesComputerToolCallActionPath{
						{X: int(startX), Y: int(startY)},
						{X: int(endX), Y: int(endY)},
					}
				}
			}
		}

	case "wait":
		action.Type = "wait"

	case "zoom":
		action.Type = "zoom"

		if region, ok := inputMap["region"].([]interface{}); ok && len(region) == 4 {

			x1, x1Ok := region[0].(float64)
			y1, y1Ok := region[1].(float64)
			x2, x2Ok := region[2].(float64)
			y2, y2Ok := region[3].(float64)
			if x1Ok && y1Ok && x2Ok && y2Ok {
				action.Region = []int{int(x1), int(y1), int(x2), int(y2)}
			}
		}

	default:

		action.Type = actionStr
	}

	if coordinate, ok := inputMap["coordinate"].([]interface{}); ok && len(coordinate) == 2 {

		if x, xOk := coordinate[0].(float64); xOk {
			if y, yOk := coordinate[1].(float64); yOk {
				action.X = schemas.Ptr(int(x))
				action.Y = schemas.Ptr(int(y))
			}
		}
	}

	return action
}
