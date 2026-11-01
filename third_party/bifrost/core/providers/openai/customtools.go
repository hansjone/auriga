package openai

import (
	"slices"
	"strings"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type customToolStreamRestorer struct {
	names   map[string]struct{}
	itemIDs map[string]struct{}
	shift   int
}

const customToolInputParam = "input"

func customToolsUnsupported(ctx *schemas.BifrostContext, provider schemas.ModelProvider, model string) bool {
	capModel := schemas.ResolveCanonicalModel(ctx, model)
	caps := schemas.ResolveModelCaps(schemas.ResolveBaseProvider(ctx, provider), capModel)
	return !caps.SupportsCustomTools(true)
}

func customToolParameters(syntax, definition string) *schemas.ToolFunctionParameters {
	description := "Raw input for the tool, passed as a plain string."
	if definition != "" {
		if syntax == "" {
			syntax = "grammar"
		}
		description += " It must match this " + syntax + " definition:\n" + definition
	}
	return &schemas.ToolFunctionParameters{
		Type: "object",
		Properties: schemas.NewOrderedMapFromPairs(
			schemas.KV(customToolInputParam, schemas.NewOrderedMapFromPairs(
				schemas.KV("type", "string"),
				schemas.KV("description", description),
			)),
		),
		Required:             []string{customToolInputParam},
		AdditionalProperties: &schemas.AdditionalPropertiesStruct{AdditionalPropertiesBool: schemas.Ptr(false)},
	}
}

func wrapCustomToolInput(input string) string {
	encoded, err := sonic.MarshalString(input)
	if err != nil {
		return `{"` + customToolInputParam + `":""}`
	}
	return `{"` + customToolInputParam + `":` + encoded + `}`
}

func unwrapCustomToolInput(arguments string) string {
	field := providerUtils.GetJSONField([]byte(arguments), customToolInputParam)
	if !field.Exists() {
		return arguments
	}
	return field.String()
}

func responsesCustomToolsAsFunctions(tools []schemas.ResponsesTool) []schemas.ResponsesTool {
	if !containsResponsesCustomTool(tools) {
		return tools
	}
	rewritten := make([]schemas.ResponsesTool, len(tools))
	for i, tool := range tools {
		switch {
		case tool.Type == schemas.ResponsesToolTypeCustom:
			var syntax, definition string
			if tool.ResponsesToolCustom != nil && tool.ResponsesToolCustom.Format != nil {
				if f := tool.ResponsesToolCustom.Format; f.Definition != nil {
					definition = *f.Definition
					if f.Syntax != nil {
						syntax = *f.Syntax
					}
				}
			}
			tool.Type = schemas.ResponsesToolTypeFunction
			tool.ResponsesToolCustom = nil
			tool.ResponsesToolFunction = &schemas.ResponsesToolFunction{
				Parameters: customToolParameters(syntax, definition),
				Strict:     schemas.Ptr(false),
			}
		case tool.ResponsesToolNamespace != nil && containsResponsesCustomTool(tool.ResponsesToolNamespace.Tools):
			namespaceCopy := *tool.ResponsesToolNamespace
			namespaceCopy.Tools = responsesCustomToolsAsFunctions(tool.ResponsesToolNamespace.Tools)
			tool.ResponsesToolNamespace = &namespaceCopy
		}
		rewritten[i] = tool
	}
	return rewritten
}

func containsResponsesCustomTool(tools []schemas.ResponsesTool) bool {
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeCustom {
			return true
		}
		if tool.ResponsesToolNamespace != nil && containsResponsesCustomTool(tool.ResponsesToolNamespace.Tools) {
			return true
		}
	}
	return false
}

func customToolKey(namespace, name string) string {
	if namespace == "" || namespace == "functions" {
		return name
	}
	return namespace + "\x00" + name
}

func collectCustomToolNames(tools []schemas.ResponsesTool, namespace string, names map[string]struct{}) {
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeCustom && tool.Name != nil {
			names[customToolKey(namespace, *tool.Name)] = struct{}{}
		}
		if tool.ResponsesToolNamespace != nil && tool.Name != nil {
			collectCustomToolNames(tool.ResponsesToolNamespace.Tools, *tool.Name, names)
		}
	}
}

func responsesCustomToolChoiceAsFunction(choice *schemas.ResponsesToolChoice) *schemas.ResponsesToolChoice {
	if choice == nil || choice.ResponsesToolChoiceStruct == nil {
		return choice
	}
	s := choice.ResponsesToolChoiceStruct
	if s.Type == schemas.ResponsesToolChoiceTypeCustom {
		structCopy := *s
		structCopy.Type = schemas.ResponsesToolChoiceTypeFunction
		return &schemas.ResponsesToolChoice{ResponsesToolChoiceStruct: &structCopy}
	}
	if s.Type == schemas.ResponsesToolChoiceTypeAllowedTools {
		var tools []schemas.ResponsesToolChoiceAllowedToolDef
		for i, tool := range s.Tools {
			if tool.Type != string(schemas.ResponsesToolTypeCustom) {
				continue
			}
			if tools == nil {
				tools = slices.Clone(s.Tools)
			}
			tools[i].Type = string(schemas.ResponsesToolTypeFunction)
		}
		if tools != nil {
			structCopy := *s
			structCopy.Tools = tools
			return &schemas.ResponsesToolChoice{ResponsesToolChoiceStruct: &structCopy}
		}
	}
	return choice
}

func responsesCustomCallItemsAsFunctions(messages []schemas.ResponsesMessage) []schemas.ResponsesMessage {
	for i := range messages {
		message := &messages[i]
		if message.Type == nil || message.ResponsesToolMessage == nil {
			continue
		}
		switch *message.Type {
		case schemas.ResponsesMessageTypeCustomToolCall:
			toolMessage := *message.ResponsesToolMessage
			input := ""
			if toolMessage.ResponsesCustomToolCall != nil {
				input = toolMessage.ResponsesCustomToolCall.Input
			}
			toolMessage.ResponsesCustomToolCall = nil
			toolMessage.Arguments = schemas.Ptr(wrapCustomToolInput(input))
			message.ResponsesToolMessage = &toolMessage
			message.Type = schemas.Ptr(schemas.ResponsesMessageTypeFunctionCall)
		case schemas.ResponsesMessageTypeCustomToolCallOutput:
			message.Type = schemas.Ptr(schemas.ResponsesMessageTypeFunctionCallOutput)
		default:
			continue
		}

		if message.ID != nil && !strings.HasPrefix(*message.ID, "fc") {
			message.ID = nil
		}
	}
	return messages
}

func customToolNamesToRestore(ctx *schemas.BifrostContext, request *schemas.BifrostResponsesRequest) map[string]struct{} {
	if request == nil || !customToolsUnsupported(ctx, request.Provider, request.Model) {
		return nil
	}
	if ctx != nil {
		if _, raw := providerUtils.CheckAndGetRawRequestBody(ctx, request); raw {
			return nil
		}
	}
	names := make(map[string]struct{})
	if request.Params != nil {
		collectCustomToolNames(request.Params.Tools, "", names)
	}
	for _, message := range request.Input {
		if message.Type != nil && *message.Type == schemas.ResponsesMessageTypeAdditionalTools {
			collectCustomToolNames(hoistAdditionalTools(message), "", names)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func restoreCustomToolCall(item *schemas.ResponsesMessage, names map[string]struct{}) bool {
	if item == nil || item.Type == nil || *item.Type != schemas.ResponsesMessageTypeFunctionCall ||
		item.ResponsesToolMessage == nil || item.ResponsesToolMessage.Name == nil {
		return false
	}
	namespace := ""
	if item.ResponsesToolMessage.Namespace != nil {
		namespace = *item.ResponsesToolMessage.Namespace
	}
	if _, ok := names[customToolKey(namespace, *item.ResponsesToolMessage.Name)]; !ok {
		return false
	}
	toolMessage := *item.ResponsesToolMessage
	input := ""
	if toolMessage.Arguments != nil {
		input = unwrapCustomToolInput(*toolMessage.Arguments)
	}
	toolMessage.Arguments = nil
	toolMessage.ResponsesCustomToolCall = &schemas.ResponsesCustomToolCall{Input: input}
	item.ResponsesToolMessage = &toolMessage
	item.Type = schemas.Ptr(schemas.ResponsesMessageTypeCustomToolCall)
	return true
}

func restoreCustomToolCalls(output []schemas.ResponsesMessage, names map[string]struct{}) {
	for i := range output {
		restoreCustomToolCall(&output[i], names)
	}
}

func newCustomToolStreamRestorer(names map[string]struct{}) *customToolStreamRestorer {
	if len(names) == 0 {
		return nil
	}
	return &customToolStreamRestorer{names: names, itemIDs: map[string]struct{}{}}
}

func (r *customToolStreamRestorer) restore(event *schemas.BifrostResponsesStreamResponse) (prefix *schemas.BifrostResponsesStreamResponse, drop bool) {
	if r == nil || event == nil {
		return nil, false
	}
	event.SequenceNumber += r.shift
	switch event.Type {
	case schemas.ResponsesStreamResponseTypeOutputItemAdded, schemas.ResponsesStreamResponseTypeOutputItemDone:
		if restoreCustomToolCall(event.Item, r.names) && event.Item.ID != nil {
			r.itemIDs[*event.Item.ID] = struct{}{}
		}
	case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta:
		if r.tracked(event.ItemID) {
			return nil, true
		}
	case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDone:
		if !r.tracked(event.ItemID) {
			return nil, false
		}
		input := ""
		if event.Arguments != nil {
			input = unwrapCustomToolInput(*event.Arguments)
		}
		prefix = &schemas.BifrostResponsesStreamResponse{
			Type:           schemas.ResponsesStreamResponseTypeCustomToolCallInputDelta,
			SequenceNumber: event.SequenceNumber,
			OutputIndex:    event.OutputIndex,
			ItemID:         event.ItemID,
			Delta:          schemas.Ptr(input),
		}
		r.shift++
		event.SequenceNumber++
		event.Type = schemas.ResponsesStreamResponseTypeCustomToolCallInputDone
		event.Arguments = nil
		event.Input = schemas.Ptr(input)
		return prefix, false
	case schemas.ResponsesStreamResponseTypeCompleted, schemas.ResponsesStreamResponseTypeIncomplete:
		if event.Response != nil {
			restoreCustomToolCalls(event.Response.Output, r.names)
		}
	}
	return nil, false
}

func (r *customToolStreamRestorer) tracked(itemID *string) bool {
	if itemID == nil {
		return false
	}
	_, ok := r.itemIDs[*itemID]
	return ok
}

func chatCustomToolAsFunction(tool schemas.ChatTool) schemas.ChatTool {
	var syntax, definition string
	if tool.Custom != nil && tool.Custom.Format != nil && tool.Custom.Format.Grammar != nil {
		syntax = tool.Custom.Format.Grammar.Syntax
		definition = tool.Custom.Format.Grammar.Definition
	}
	tool.Type = schemas.ChatToolTypeFunction
	tool.Function = &schemas.ChatToolFunction{
		Name:       tool.Name,
		Parameters: customToolParameters(syntax, definition),
	}
	tool.Custom = nil
	tool.Name = ""

	tool.InvalidateSerialized()
	return tool
}

func chatCustomToolChoiceAsFunction(choice *schemas.ChatToolChoice) *schemas.ChatToolChoice {
	if choice == nil || choice.ChatToolChoiceStruct == nil ||
		choice.ChatToolChoiceStruct.Type != schemas.ChatToolChoiceTypeCustom {
		return choice
	}
	name := ""
	if choice.ChatToolChoiceStruct.Custom != nil {
		name = choice.ChatToolChoiceStruct.Custom.Name
	}
	return &schemas.ChatToolChoice{ChatToolChoiceStruct: &schemas.ChatToolChoiceStruct{
		Type:     schemas.ChatToolChoiceTypeFunction,
		Function: &schemas.ChatToolChoiceFunction{Name: name},
	}}
}
