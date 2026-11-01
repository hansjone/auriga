package openai

import (
	"encoding/json"
	"slices"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

const toolSearchExecutionServer = "server"

func (req *OpenAIResponsesRequest) provisionToolSearch(supported bool) {
	req.keepDeferLoading = false
	if !supported || len(req.Tools) == 0 {
		req.dropTypedToolSearchCalls()
		return
	}
	hasDeferred := false
	searchIdx := -1
	for i := range req.Tools {
		tool := &req.Tools[i]
		if tool.DeferLoading != nil && *tool.DeferLoading {
			hasDeferred = true
		}
		if tool.Type == schemas.ResponsesToolTypeToolSearch && searchIdx < 0 {
			searchIdx = i
		}
	}
	if !hasDeferred && searchIdx < 0 {
		req.dropTypedToolSearchCalls()
		return
	}
	req.keepDeferLoading = true
	req.Tools = slices.Clone(req.Tools)

	if searchIdx >= 0 {

		req.Tools[searchIdx].Name = nil
	} else if name := schemas.ClientToolSearchFunctionName(req.Tools, req.Input.OpenAIResponsesRequestInputArray); name != "" &&
		!toolChoiceNamesFunction(req.ToolChoice, name) {
		req.mapClientToolSearchFunction(name)
	} else {
		req.Tools = append(req.Tools, schemas.ResponsesTool{Type: schemas.ResponsesToolTypeToolSearch})
	}
	req.replayTypedToolSearchCalls()
}

func (req *OpenAIResponsesRequest) mapClientToolSearchFunction(name string) {
	idx := slices.IndexFunc(req.Tools, func(t schemas.ResponsesTool) bool {
		return t.Type == schemas.ResponsesToolTypeFunction && t.Name != nil && *t.Name == name
	})
	if idx < 0 {
		return
	}
	search := schemas.ResponsesTool{
		Type:                    schemas.ResponsesToolTypeToolSearch,
		Description:             req.Tools[idx].Description,
		ResponsesToolToolSearch: &schemas.ResponsesToolToolSearch{Execution: schemas.Ptr(schemas.ToolSearchExecutionClient)},
	}
	if fn := req.Tools[idx].ResponsesToolFunction; fn != nil {
		search.ResponsesToolToolSearch.Parameters = fn.Parameters
	}
	req.Tools[idx] = search

	input := req.Input.OpenAIResponsesRequestInputArray
	if len(input) == 0 {
		return
	}
	searchCalls := make(map[string]struct{})
	rewritten := make([]schemas.ResponsesMessage, len(input))
	for i := range input {
		msg := input[i]
		switch {
		case msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeFunctionCall &&
			msg.ResponsesToolMessage != nil && msg.Name != nil && *msg.Name == name && msg.CallID != nil:
			searchCalls[*msg.CallID] = struct{}{}
			args := "{}"
			if msg.Arguments != nil && json.Valid([]byte(*msg.Arguments)) {
				args = *msg.Arguments
			}
			msg = schemas.ResponsesMessage{
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeToolSearchCall),
				Status: schemas.Ptr("completed"),
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					CallID:    msg.CallID,
					Execution: schemas.Ptr(schemas.ToolSearchExecutionClient),
					Arguments: &args,
				},
			}
		case msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeFunctionCallOutput &&
			msg.ResponsesToolMessage != nil && msg.CallID != nil:
			if _, ok := searchCalls[*msg.CallID]; !ok {
				break
			}
			var refs []string
			if msg.Output != nil {
				for _, block := range msg.Output.ResponsesFunctionToolCallOutputBlocks {
					if block.Type == schemas.ResponsesInputMessageContentBlockTypeToolReference && block.ToolName != nil {
						refs = append(refs, *block.ToolName)
					}
				}
			}
			msg = req.toolSearchOutputItem(msg.CallID, schemas.ToolSearchExecutionClient, refs)
		}
		rewritten[i] = msg
	}
	req.Input.OpenAIResponsesRequestInputArray = rewritten
}

func (req *OpenAIResponsesRequest) replayTypedToolSearchCalls() {
	input := req.Input.OpenAIResponsesRequestInputArray
	if !slices.ContainsFunc(input, isTypedToolSearchCall) {
		return
	}
	replayed := make([]schemas.ResponsesMessage, 0, len(input)+1)
	replayedCalls := make(map[string]struct{})
	for i := range input {
		msg := input[i]
		if isTypedToolSearchOutput(msg) && msg.CallID != nil {
			if _, ok := replayedCalls[*msg.CallID]; ok {
				continue
			}
		}
		if !isTypedToolSearchCall(msg) {
			replayed = append(replayed, msg)
			continue
		}
		args := "{}"
		var refs []string
		if msg.ResponsesToolMessage != nil {
			if msg.CallID != nil {
				replayedCalls[*msg.CallID] = struct{}{}
			}
			if msg.Arguments != nil && json.Valid([]byte(*msg.Arguments)) {
				args = *msg.Arguments
			}
			if msg.ResponsesToolSearchCall != nil {
				refs = msg.ResponsesToolSearchCall.ToolReferences
			}
		}
		replayed = append(replayed,
			schemas.ResponsesMessage{
				Type:   schemas.Ptr(schemas.ResponsesMessageTypeToolSearchCall),
				Status: schemas.Ptr("completed"),
				ResponsesToolMessage: &schemas.ResponsesToolMessage{
					Execution: schemas.Ptr(toolSearchExecutionServer),
					Arguments: &args,
				},
			},
			req.toolSearchOutputItem(nil, toolSearchExecutionServer, refs),
		)
	}
	req.Input.OpenAIResponsesRequestInputArray = replayed
}

func toolChoiceNamesFunction(choice *schemas.ResponsesToolChoice, name string) bool {
	if choice == nil || choice.ResponsesToolChoiceStruct == nil {
		return false
	}
	pinned := choice.ResponsesToolChoiceStruct
	if pinned.Type == schemas.ResponsesToolChoiceTypeFunction && pinned.Name != nil && *pinned.Name == name {
		return true
	}
	return slices.ContainsFunc(pinned.Tools, func(t schemas.ResponsesToolChoiceAllowedToolDef) bool {
		return t.Type == string(schemas.ResponsesToolTypeFunction) && t.Name != nil && *t.Name == name
	})
}

func (req *OpenAIResponsesRequest) dropTypedToolSearchCalls() {
	input := req.Input.OpenAIResponsesRequestInputArray
	if !slices.ContainsFunc(input, func(m schemas.ResponsesMessage) bool { return isTypedToolSearchCall(m) || isTypedToolSearchOutput(m) }) {
		return
	}
	req.Input.OpenAIResponsesRequestInputArray = slices.DeleteFunc(slices.Clone(input), func(m schemas.ResponsesMessage) bool {
		return isTypedToolSearchCall(m) || isTypedToolSearchOutput(m)
	})
}

func isTypedToolSearchCall(msg schemas.ResponsesMessage) bool {
	return msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeToolSearchCall &&
		!msg.IsVerbatimItem() && !msg.IsClientToolSearchItem()
}

func isTypedToolSearchOutput(msg schemas.ResponsesMessage) bool {
	return msg.Type != nil && *msg.Type == schemas.ResponsesMessageTypeToolSearchOutput &&
		!msg.IsVerbatimItem() && !msg.IsClientToolSearchItem()
}

func (req *OpenAIResponsesRequest) toolSearchOutputItem(callID *string, execution string, names []string) schemas.ResponsesMessage {
	loaded := make([]schemas.ResponsesTool, 0, len(names))
	for _, name := range names {
		idx := slices.IndexFunc(req.Tools, func(t schemas.ResponsesTool) bool {
			return t.Type == schemas.ResponsesToolTypeFunction && t.Name != nil && *t.Name == name
		})
		if idx < 0 {
			continue
		}
		tool := req.Tools[idx]
		tool.DeferLoading = schemas.Ptr(true)
		loaded = append(loaded, tool)
	}
	tools := json.RawMessage("[]")
	if encoded, err := providerUtils.MarshalSorted(loaded); err == nil {
		tools = encoded
	}
	return schemas.ResponsesMessage{
		Type:                  schemas.Ptr(schemas.ResponsesMessageTypeToolSearchOutput),
		Status:                schemas.Ptr("completed"),
		ToolSearchOutputTools: tools,
		ResponsesToolMessage: &schemas.ResponsesToolMessage{
			CallID:    callID,
			Execution: schemas.Ptr(execution),
		},
	}
}
