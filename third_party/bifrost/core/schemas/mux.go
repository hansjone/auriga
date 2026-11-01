// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

type ChatToResponsesStreamState struct {
	ToolArgumentBuffers   map[string]string
	ItemIDs               map[string]string
	ToolCallNames         map[string]string
	ToolCallIndexToID     map[uint16]string
	MessageID             *string
	Model                 *string
	CreatedAt             int
	HasEmittedCreated     bool
	HasEmittedInProgress  bool
	TextItemAdded         bool
	TextItemClosed        bool
	TextItemHasContent    bool
	TextBuffer            strings.Builder
	TextOutputIndex       int
	ReasoningItemAdded    bool
	ReasoningItemClosed   bool
	ReasoningOutputIndex  int
	ReasoningBuffer       strings.Builder
	CurrentOutputIndex    int
	ToolCallOutputIndices map[string]int
	SequenceNumber        int
	ServiceTier           *BifrostServiceTier
	Speed                 *string
	InferenceGeo          *string
}

var chatToResponsesStreamStatePool = sync.Pool{
	New: func() interface{} {
		return &ChatToResponsesStreamState{
			ToolArgumentBuffers:   make(map[string]string),
			ItemIDs:               make(map[string]string),
			ToolCallNames:         make(map[string]string),
			ToolCallIndexToID:     make(map[uint16]string),
			CreatedAt:             int(time.Now().Unix()),
			CurrentOutputIndex:    0,
			ToolCallOutputIndices: make(map[string]int),
			SequenceNumber:        0,
			HasEmittedCreated:     false,
			HasEmittedInProgress:  false,
			TextItemAdded:         false,
			TextItemClosed:        false,
			TextItemHasContent:    false,
			TextBuffer:            strings.Builder{},
		}
	},
}

func (ct *ChatTool) ToResponsesTool() *ResponsesTool {
	if ct == nil {
		return &ResponsesTool{}
	}

	rt := &ResponsesTool{
		Type: ResponsesToolType(ct.Type),
	}

	if ct.Type == ChatToolTypeFunction && ct.Function != nil {
		rt.Name = &ct.Function.Name
		rt.Description = ct.Function.Description

		if ct.Function.Parameters != nil || ct.Function.Strict != nil {
			rt.ResponsesToolFunction = &ResponsesToolFunction{
				Parameters: ct.Function.Parameters,
				Strict:     ct.Function.Strict,
			}
		}
	}

	if ct.Type == ChatToolTypeCustom && ct.Custom != nil {
		if ct.Custom.Format != nil {
			rt.ResponsesToolCustom = &ResponsesToolCustom{
				Format: &ResponsesToolCustomFormat{
					Type: ct.Custom.Format.Type,
				},
			}
			if ct.Custom.Format.Grammar != nil {
				rt.ResponsesToolCustom.Format.Definition = &ct.Custom.Format.Grammar.Definition
				rt.ResponsesToolCustom.Format.Syntax = &ct.Custom.Format.Grammar.Syntax
			}
		}
	}

	return rt
}

func (rt *ResponsesTool) ToChatTool() *ChatTool {
	if rt == nil {
		return &ChatTool{}
	}

	ct := &ChatTool{
		Type: ChatToolType(rt.Type),
	}

	if rt.Type == "function" {
		ct.Function = &ChatToolFunction{}

		if rt.Name != nil {
			ct.Function.Name = *rt.Name
		}
		if rt.Description != nil {
			ct.Function.Description = rt.Description
		}
		if rt.ResponsesToolFunction != nil {
			ct.Function.Parameters = rt.ResponsesToolFunction.Parameters
			ct.Function.Strict = rt.ResponsesToolFunction.Strict
		}
	}

	if rt.Type == "custom" && rt.ResponsesToolCustom != nil {
		ct.Custom = &ChatToolCustom{}
		if rt.ResponsesToolCustom.Format != nil {
			ct.Custom.Format = &ChatToolCustomFormat{
				Type: rt.ResponsesToolCustom.Format.Type,
			}
			if rt.ResponsesToolCustom.Format.Definition != nil && rt.ResponsesToolCustom.Format.Syntax != nil {
				ct.Custom.Format.Grammar = &ChatToolCustomGrammarFormat{
					Definition: *rt.ResponsesToolCustom.Format.Definition,
					Syntax:     *rt.ResponsesToolCustom.Format.Syntax,
				}
			}
		}
	}

	return ct
}

func responsesInputBlockType(t ChatContentBlockType) ResponsesMessageContentBlockType {
	switch t {
	case ChatContentBlockTypeText:
		return ResponsesInputMessageContentBlockTypeText
	case ChatContentBlockTypeImage:
		return ResponsesInputMessageContentBlockTypeImage
	case ChatContentBlockTypeFile:
		return ResponsesInputMessageContentBlockTypeFile
	case ChatContentBlockTypeInputAudio:
		return ResponsesInputMessageContentBlockTypeAudio
	}
	return ResponsesMessageContentBlockType(t)
}

func (ctc *ChatToolChoice) ToResponsesToolChoice() *ResponsesToolChoice {
	if ctc == nil {
		return &ResponsesToolChoice{}
	}

	rtc := &ResponsesToolChoice{}

	if ctc.ChatToolChoiceStr != nil {
		rtc.ResponsesToolChoiceStr = ctc.ChatToolChoiceStr
		return rtc
	}

	if ctc.ChatToolChoiceStruct != nil {
		rtc.ResponsesToolChoiceStruct = &ResponsesToolChoiceStruct{
			Type: ResponsesToolChoiceType(ctc.ChatToolChoiceStruct.Type),
		}

		switch ctc.ChatToolChoiceStruct.Type {
		case ChatToolChoiceTypeNone, ChatToolChoiceTypeAny, ChatToolChoiceTypeRequired:

			modeStr := string(ctc.ChatToolChoiceStruct.Type)
			rtc.ResponsesToolChoiceStruct.Mode = &modeStr

		case ChatToolChoiceTypeFunction:

			if ctc.ChatToolChoiceStruct.Function != nil && ctc.ChatToolChoiceStruct.Function.Name != "" {
				rtc.ResponsesToolChoiceStruct.Name = &ctc.ChatToolChoiceStruct.Function.Name
			}

		case ChatToolChoiceTypeAllowedTools:

			if ctc.ChatToolChoiceStruct.AllowedTools != nil && len(ctc.ChatToolChoiceStruct.AllowedTools.Tools) > 0 {
				tools := make([]ResponsesToolChoiceAllowedToolDef, len(ctc.ChatToolChoiceStruct.AllowedTools.Tools))
				for i, tool := range ctc.ChatToolChoiceStruct.AllowedTools.Tools {
					tools[i] = ResponsesToolChoiceAllowedToolDef{
						Type: tool.Type,
					}
					if tool.Function.Name != "" {
						name := tool.Function.Name
						tools[i].Name = &name
					}
				}
				rtc.ResponsesToolChoiceStruct.Tools = tools
			}

			if ctc.ChatToolChoiceStruct.AllowedTools != nil && ctc.ChatToolChoiceStruct.AllowedTools.Mode != "" {
				mode := ctc.ChatToolChoiceStruct.AllowedTools.Mode
				rtc.ResponsesToolChoiceStruct.Mode = &mode
			}

		case ChatToolChoiceTypeCustom:

			if ctc.ChatToolChoiceStruct.Custom != nil && ctc.ChatToolChoiceStruct.Custom.Name != "" {
				rtc.ResponsesToolChoiceStruct.Name = &ctc.ChatToolChoiceStruct.Custom.Name
			}
		}
	}

	return rtc
}

func (tc *ResponsesToolChoice) ToChatToolChoice() *ChatToolChoice {
	if tc == nil {
		return &ChatToolChoice{}
	}

	ctc := &ChatToolChoice{}

	if tc.ResponsesToolChoiceStr != nil {
		ctc.ChatToolChoiceStr = tc.ResponsesToolChoiceStr
		return ctc
	}

	if tc.ResponsesToolChoiceStruct != nil {
		ctc.ChatToolChoiceStruct = &ChatToolChoiceStruct{
			Type: ChatToolChoiceType(tc.ResponsesToolChoiceStruct.Type),
		}

		if tc.ResponsesToolChoiceStruct.Mode != nil {
			switch *tc.ResponsesToolChoiceStruct.Mode {
			case "none":
				ctc.ChatToolChoiceStruct.Type = ChatToolChoiceTypeNone
			case "auto":
				ctc.ChatToolChoiceStruct.Type = ChatToolChoiceTypeAny
			case "required":
				ctc.ChatToolChoiceStruct.Type = ChatToolChoiceTypeRequired
			}
		}

		if tc.ResponsesToolChoiceStruct.Type == ResponsesToolChoiceTypeFunction && tc.ResponsesToolChoiceStruct.Name != nil {
			ctc.ChatToolChoiceStruct.Function = &ChatToolChoiceFunction{
				Name: *tc.ResponsesToolChoiceStruct.Name,
			}
		}

		if tc.ResponsesToolChoiceStruct.Type == ResponsesToolChoiceTypeCustom && tc.ResponsesToolChoiceStruct.Name != nil {
			ctc.ChatToolChoiceStruct.Custom = &ChatToolChoiceCustom{
				Name: *tc.ResponsesToolChoiceStruct.Name,
			}
		}

		if len(tc.ResponsesToolChoiceStruct.Tools) > 0 {
			ctc.ChatToolChoiceStruct.Type = ChatToolChoiceTypeAllowedTools
			tools := make([]ChatToolChoiceAllowedToolsTool, len(tc.ResponsesToolChoiceStruct.Tools))
			for i, tool := range tc.ResponsesToolChoiceStruct.Tools {
				tools[i] = ChatToolChoiceAllowedToolsTool{
					Type: tool.Type,
				}
				if tool.Name != nil {
					tools[i].Function = ChatToolChoiceFunction{Name: *tool.Name}
				}
			}

			mode := "auto"
			if tc.ResponsesToolChoiceStruct.Mode != nil && *tc.ResponsesToolChoiceStruct.Mode != "" {
				mode = *tc.ResponsesToolChoiceStruct.Mode
			}
			ctc.ChatToolChoiceStruct.AllowedTools = &ChatToolChoiceAllowedTools{
				Mode:  mode,
				Tools: tools,
			}
		}

		return ctc
	}

	return nil
}

func (cm *ChatMessage) ToResponsesMessages() []ResponsesMessage {
	if cm == nil {
		return []ResponsesMessage{}
	}

	var messages []ResponsesMessage

	if cm.Role == ChatMessageRoleAssistant && cm.ChatAssistantMessage != nil {
		am := cm.ChatAssistantMessage
		if len(am.ReasoningDetails) > 0 {
			var contentBlocks []ResponsesMessageContentBlock
			var encryptedContent *string

			summaries := []ResponsesReasoningSummary{}

			var emittedToken *ChatReasoningDetails
			for i := range am.ReasoningDetails {
				if d := &am.ReasoningDetails[i]; d.Type == BifrostReasoningDetailsTypeEncrypted && d.Data != nil {
					emittedToken = d
				}
			}
			reasoningID := ""
			if emittedToken != nil {
				if emittedToken.ID != nil {
					reasoningID = *emittedToken.ID
				}
			} else {
				for _, d := range am.ReasoningDetails {
					if d.ID != nil && *d.ID != "" {
						reasoningID = *d.ID
						break
					}
				}
			}
			if reasoningID == "" {
				reasoningID = "rs_" + GetRandomString(50)
			}
			for _, d := range am.ReasoningDetails {
				switch d.Type {
				case BifrostReasoningDetailsTypeText:
					if d.Text != nil && *d.Text != "" {
						contentBlocks = append(contentBlocks, ResponsesMessageContentBlock{
							Type:      ResponsesOutputMessageContentTypeReasoning,
							Text:      d.Text,
							Signature: d.Signature,
						})
					}
				case BifrostReasoningDetailsTypeSummary:
					if d.Summary != nil {
						summaries = append(summaries, ResponsesReasoningSummary{
							Type: ResponsesReasoningContentBlockTypeSummaryText,
							Text: *d.Summary,
						})
					}
				case BifrostReasoningDetailsTypeEncrypted:
					if d.Data != nil {
						encryptedContent = d.Data
					}
				}
			}
			if len(contentBlocks) > 0 || len(summaries) > 0 || encryptedContent != nil {
				reasoningType := ResponsesMessageTypeReasoning
				reasoningRole := ResponsesInputMessageRoleAssistant
				rm := ResponsesMessage{
					ID:   new(reasoningID),
					Type: &reasoningType,
					Role: &reasoningRole,
				}
				if len(contentBlocks) > 0 {
					rm.Content = &ResponsesMessageContent{ContentBlocks: contentBlocks}
				}
				if len(summaries) > 0 || encryptedContent != nil {
					rm.ResponsesReasoning = &ResponsesReasoning{
						Summary:          summaries,
						EncryptedContent: encryptedContent,
					}
				}
				messages = append(messages, rm)
			}
		} else if am.Reasoning != nil && *am.Reasoning != "" {

			reasoningType := ResponsesMessageTypeReasoning
			reasoningRole := ResponsesInputMessageRoleAssistant
			reasoningText := *am.Reasoning
			messages = append(messages, ResponsesMessage{
				ID:   new("rs_" + GetRandomString(50)),
				Type: &reasoningType,
				Role: &reasoningRole,
				Content: &ResponsesMessageContent{
					ContentBlocks: []ResponsesMessageContentBlock{
						{
							Type: ResponsesOutputMessageContentTypeReasoning,
							Text: &reasoningText,
						},
					},
				},
			})
		}
	}

	if cm.ChatAssistantMessage != nil && cm.ChatAssistantMessage.ToolCalls != nil && len(cm.ChatAssistantMessage.ToolCalls) > 0 {

		for _, tc := range cm.ChatAssistantMessage.ToolCalls {
			messageType := ResponsesMessageTypeFunctionCall

			var callID *string
			if tc.ID != nil && *tc.ID != "" {
				callID = tc.ID
			}

			var namePtr *string
			if tc.Function.Name != nil && *tc.Function.Name != "" {
				namePtr = tc.Function.Name
			}

			var argumentsPtr *string
			if tc.Function.Arguments != "" {
				argumentsPtr = Ptr(tc.Function.Arguments)
			}

			rm := ResponsesMessage{
				ID:     Ptr("fc_" + GetRandomString(50)),
				Type:   &messageType,
				Role:   Ptr(ResponsesInputMessageRoleAssistant),
				Status: Ptr("completed"),
				ResponsesToolMessage: &ResponsesToolMessage{
					CallID:    callID,
					Name:      namePtr,
					Arguments: argumentsPtr,
				},
			}

			messages = append(messages, rm)
		}
		return messages
	}

	messageType := ResponsesMessageTypeMessage
	role := ResponsesInputMessageRoleUser

	switch cm.Role {
	case ChatMessageRoleAssistant:
		role = ResponsesInputMessageRoleAssistant

		if cm.ChatAssistantMessage != nil && cm.ChatAssistantMessage.Refusal != nil {
			messageType = ResponsesMessageTypeRefusal
		}
	case ChatMessageRoleUser:
		role = ResponsesInputMessageRoleUser
	case ChatMessageRoleSystem:
		role = ResponsesInputMessageRoleSystem
	case ChatMessageRoleTool:
		messageType = ResponsesMessageTypeFunctionCallOutput
		role = ""
	case ChatMessageRoleDeveloper:
		role = ResponsesInputMessageRoleDeveloper
	}

	rm := ResponsesMessage{
		Type: &messageType,
	}

	if role != "" {
		rm.Role = &role
	}

	if role == ResponsesInputMessageRoleAssistant && messageType != ResponsesMessageTypeFunctionCallOutput {
		rm.ID = Ptr("msg_" + GetRandomString(50))
	}

	if messageType == ResponsesMessageTypeRefusal && cm.ChatAssistantMessage != nil && cm.ChatAssistantMessage.Refusal != nil {
		refusalBlock := ResponsesMessageContentBlock{
			Type: ResponsesOutputMessageContentTypeRefusal,
			ResponsesOutputMessageContentRefusal: &ResponsesOutputMessageContentRefusal{
				Refusal: *cm.ChatAssistantMessage.Refusal,
			},
		}
		rm.Content = &ResponsesMessageContent{
			ContentBlocks: []ResponsesMessageContentBlock{refusalBlock},
		}
	} else if cm.Content != nil && cm.Content.ContentStr != nil {

		if messageType == ResponsesMessageTypeFunctionCallOutput {

		} else if cm.Role == ChatMessageRoleAssistant {
			rm.Content = &ResponsesMessageContent{
				ContentBlocks: []ResponsesMessageContentBlock{
					{
						Type: ResponsesOutputMessageContentTypeText,
						Text: cm.Content.ContentStr,
						ResponsesOutputMessageContentText: &ResponsesOutputMessageContentText{
							LogProbs:    []ResponsesOutputMessageContentTextLogProb{},
							Annotations: []ResponsesOutputMessageContentTextAnnotation{},
						},
					},
				},
			}

			rm.Status = Ptr("completed")
		} else {
			rm.Content = &ResponsesMessageContent{
				ContentStr: cm.Content.ContentStr,
			}
		}
	} else if cm.Content != nil && cm.Content.ContentBlocks != nil {

		if messageType == ResponsesMessageTypeFunctionCallOutput {

		} else {
			responseBlocks := make([]ResponsesMessageContentBlock, 0, len(cm.Content.ContentBlocks))
			for _, block := range cm.Content.ContentBlocks {

				if block.Type == "" && block.CachePoint != nil {
					continue
				}
				blockType := ResponsesMessageContentBlockType(block.Type)

				switch block.Type {
				case ChatContentBlockTypeText:
					if cm.Role == ChatMessageRoleAssistant {
						blockType = ResponsesOutputMessageContentTypeText
					} else {
						blockType = ResponsesInputMessageContentBlockTypeText
					}
				case ChatContentBlockTypeImage:
					blockType = ResponsesInputMessageContentBlockTypeImage
				case ChatContentBlockTypeFile:
					blockType = ResponsesInputMessageContentBlockTypeFile
				case ChatContentBlockTypeInputAudio:
					blockType = ResponsesInputMessageContentBlockTypeAudio
				}

				responseBlocks = append(responseBlocks, ResponsesMessageContentBlock{
					Type:         blockType,
					Text:         block.Text,
					GuardContent: block.GuardContent,
				})
				rb := &responseBlocks[len(responseBlocks)-1]

				if block.ImageURLStruct != nil {
					rb.ResponsesInputMessageContentBlockImage = &ResponsesInputMessageContentBlockImage{
						ImageURL: &block.ImageURLStruct.URL,
						Detail:   block.ImageURLStruct.Detail,
					}
				}
				if block.File != nil {
					rb.ResponsesInputMessageContentBlockFile = &ResponsesInputMessageContentBlockFile{
						FileData: block.File.FileData,
						FileURL:  block.File.FileURL,
						Filename: block.File.Filename,
						FileType: block.File.FileType,
					}
					rb.FileID = block.File.FileID
				}
				if block.InputAudio != nil {
					format := ""
					if block.InputAudio.Format != nil {
						format = *block.InputAudio.Format
					}
					rb.Audio = &ResponsesInputMessageContentBlockAudio{
						Data:   block.InputAudio.Data,
						Format: format,
					}
				}
			}
			rm.Content = &ResponsesMessageContent{
				ContentBlocks: responseBlocks,
			}
		}
	}

	if cm.ChatToolMessage != nil {
		rm.ResponsesToolMessage = &ResponsesToolMessage{}
		if cm.ChatToolMessage.ToolCallID != nil {
			rm.ResponsesToolMessage.CallID = cm.ChatToolMessage.ToolCallID
		}

		if cm.ChatToolMessage.IsError != nil && *cm.ChatToolMessage.IsError {
			rm.Status = Ptr("incomplete")
		}

		if messageType == ResponsesMessageTypeFunctionCallOutput && cm.Content != nil {

			if cm.Content.ContentStr != nil {
				rm.ResponsesToolMessage.Output = &ResponsesToolMessageOutputStruct{
					ResponsesToolCallOutputStr: cm.Content.ContentStr,
				}
			} else if len(cm.Content.ContentBlocks) > 0 {

				respBlocks := make([]ResponsesMessageContentBlock, len(cm.Content.ContentBlocks))
				for i, block := range cm.Content.ContentBlocks {
					respBlocks[i] = ResponsesMessageContentBlock{
						Type:         responsesInputBlockType(block.Type),
						Text:         block.Text,
						CacheControl: block.CacheControl,
						GuardContent: block.GuardContent,
					}

					if block.ImageURLStruct != nil {
						respBlocks[i].ResponsesInputMessageContentBlockImage = &ResponsesInputMessageContentBlockImage{
							ImageURL: &block.ImageURLStruct.URL,
							Detail:   block.ImageURLStruct.Detail,
						}
					}

					if block.File != nil {
						respBlocks[i].FileID = block.File.FileID
						respBlocks[i].ResponsesInputMessageContentBlockFile = &ResponsesInputMessageContentBlockFile{
							FileData: block.File.FileData,
							Filename: block.File.Filename,
							FileType: block.File.FileType,
						}
					}

					if block.InputAudio != nil {
						format := ""
						if block.InputAudio.Format != nil {
							format = *block.InputAudio.Format
						}
						respBlocks[i].Audio = &ResponsesInputMessageContentBlockAudio{
							Data:   block.InputAudio.Data,
							Format: format,
						}
					}
				}
				rm.ResponsesToolMessage.Output = &ResponsesToolMessageOutputStruct{
					ResponsesFunctionToolCallOutputBlocks: respBlocks,
				}
			}
		}
	}

	messages = append(messages, rm)
	return messages
}

func responsesBlocksWithoutToolChanges(blocks []ResponsesMessageContentBlock) []ResponsesMessageContentBlock {
	drop := false
	for _, block := range blocks {
		if block.Type == ResponsesInputMessageContentBlockTypeToolAddition || block.Type == ResponsesInputMessageContentBlockTypeToolRemoval {
			drop = true
			break
		}
	}
	if !drop {
		return blocks
	}
	kept := make([]ResponsesMessageContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == ResponsesInputMessageContentBlockTypeToolAddition || block.Type == ResponsesInputMessageContentBlockTypeToolRemoval {
			continue
		}
		kept = append(kept, block)
	}
	return kept
}

func ToChatMessages(rms []ResponsesMessage) []ChatMessage {
	if len(rms) == 0 {
		return []ChatMessage{}
	}

	var chatMessages []ChatMessage
	var currentToolCalls []ChatAssistantMessageToolCall
	var pendingReasoning strings.Builder
	var pendingReasoningDetails []ChatReasoningDetails

	attachPendingReasoning := func(msg *ChatAssistantMessage) {
		if pendingReasoning.Len() == 0 && len(pendingReasoningDetails) == 0 {
			return
		}
		if msg.Reasoning == nil && pendingReasoning.Len() > 0 {
			text := pendingReasoning.String()
			msg.Reasoning = &text
		}
		if len(pendingReasoningDetails) > 0 {
			msg.ReasoningDetails = append(msg.ReasoningDetails, pendingReasoningDetails...)
		}
		pendingReasoning.Reset()
		pendingReasoningDetails = nil
	}

	for _, rm := range rms {

		if rm.Type != nil && *rm.Type == ResponsesMessageTypeConfigurationUpdate {
			continue
		}
		if rm.Type != nil && *rm.Type == ResponsesMessageTypeReasoning {

			if rm.Content != nil {
				for _, block := range rm.Content.ContentBlocks {
					if block.Type == ResponsesOutputMessageContentTypeReasoning && block.Text != nil {
						if pendingReasoning.Len() > 0 {
							pendingReasoning.WriteByte('\n')
						}
						pendingReasoning.WriteString(*block.Text)
						pendingReasoningDetails = append(pendingReasoningDetails, ChatReasoningDetails{
							ID:        rm.ID,
							Index:     len(pendingReasoningDetails),
							Type:      BifrostReasoningDetailsTypeText,
							Text:      block.Text,
							Signature: block.Signature,
						})
					}
				}
			}
			if rm.ResponsesReasoning != nil {
				for _, summary := range rm.ResponsesReasoning.Summary {
					summaryText := summary.Text
					pendingReasoningDetails = append(pendingReasoningDetails, ChatReasoningDetails{
						ID:      rm.ID,
						Index:   len(pendingReasoningDetails),
						Type:    BifrostReasoningDetailsTypeSummary,
						Summary: &summaryText,
					})
				}
				if rm.ResponsesReasoning.EncryptedContent != nil {
					pendingReasoningDetails = append(pendingReasoningDetails, ChatReasoningDetails{
						ID:    rm.ID,
						Index: len(pendingReasoningDetails),
						Type:  BifrostReasoningDetailsTypeEncrypted,
						Data:  rm.ResponsesReasoning.EncryptedContent,
					})
				}
			}
			continue
		}

		if rm.Type != nil && *rm.Type == ResponsesMessageTypeFunctionCall {
			if rm.ResponsesToolMessage != nil {
				tc := ChatAssistantMessageToolCall{
					Type: Ptr("function"),
				}

				if rm.ResponsesToolMessage.CallID != nil {
					tc.ID = rm.ResponsesToolMessage.CallID
				}

				tc.Function = ChatAssistantMessageToolCallFunction{}
				if rm.ResponsesToolMessage.Name != nil {
					tc.Function.Name = rm.ResponsesToolMessage.Name
				}
				if rm.ResponsesToolMessage.Arguments != nil {
					tc.Function.Arguments = *rm.ResponsesToolMessage.Arguments
				}

				currentToolCalls = append(currentToolCalls, tc)
			}
			continue
		}

		if len(currentToolCalls) > 0 {

			toolCallsCopy := append([]ChatAssistantMessageToolCall(nil), currentToolCalls...)
			assistant := &ChatAssistantMessage{
				ToolCalls: toolCallsCopy,
			}
			attachPendingReasoning(assistant)
			chatMessages = append(chatMessages, ChatMessage{
				Role:                 ChatMessageRoleAssistant,
				ChatAssistantMessage: assistant,
			})
			currentToolCalls = nil
		}

		cm := ChatMessage{}

		if rm.Role != nil {
			switch *rm.Role {
			case ResponsesInputMessageRoleAssistant:
				cm.Role = ChatMessageRoleAssistant
			case ResponsesInputMessageRoleUser:
				cm.Role = ChatMessageRoleUser
			case ResponsesInputMessageRoleSystem:
				cm.Role = ChatMessageRoleSystem
			case ResponsesInputMessageRoleDeveloper:
				cm.Role = ChatMessageRoleDeveloper
			}
		}

		if rm.Type != nil {
			switch *rm.Type {
			case ResponsesMessageTypeFunctionCallOutput:
				cm.Role = ChatMessageRoleTool
				if rm.ResponsesToolMessage != nil && rm.ResponsesToolMessage.CallID != nil {
					cm.ChatToolMessage = &ChatToolMessage{
						ToolCallID: rm.ResponsesToolMessage.CallID,
					}

					if rm.ResponsesToolMessage.Error.IsError() ||
						(rm.Status != nil && *rm.Status == "incomplete") {
						cm.ChatToolMessage.IsError = Ptr(true)
					}

					if rm.ResponsesToolMessage.Output != nil {
						if rm.Content == nil {
							rm.Content = &ResponsesMessageContent{}
						}

						if rm.Content.ContentStr == nil && rm.Content.ContentBlocks == nil {
							if rm.ResponsesToolMessage.Output.ResponsesToolCallOutputStr != nil {
								rm.Content.ContentStr = rm.ResponsesToolMessage.Output.ResponsesToolCallOutputStr
							} else if rm.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks != nil {
								rm.Content.ContentBlocks = rm.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks
							}
						}
					}
				}
			case ResponsesMessageTypeRefusal:
				cm.ChatAssistantMessage = &ChatAssistantMessage{}

				if rm.Content != nil {
					if rm.Content.ContentBlocks != nil {

						for _, block := range rm.Content.ContentBlocks {
							if block.Type == ResponsesOutputMessageContentTypeRefusal && block.ResponsesOutputMessageContentRefusal != nil {
								refusalText := block.ResponsesOutputMessageContentRefusal.Refusal
								cm.ChatAssistantMessage.Refusal = &refusalText
								break
							}
						}
					} else if rm.Content.ContentStr != nil {

						cm.ChatAssistantMessage.Refusal = rm.Content.ContentStr
					}
				}
			}
		}

		if rm.Content != nil && (rm.Type == nil || *rm.Type != ResponsesMessageTypeRefusal) {
			if rm.Content.ContentStr != nil ||
				(len(rm.Content.ContentBlocks) == 1 &&
					(rm.Content.ContentBlocks[0].Type == ResponsesInputMessageContentBlockTypeText || rm.Content.ContentBlocks[0].Type == ResponsesOutputMessageContentTypeText)) {
				if rm.Content.ContentStr != nil {
					cm.Content = &ChatMessageContent{
						ContentStr: rm.Content.ContentStr,
					}
				} else {
					cm.Content = &ChatMessageContent{
						ContentStr: rm.Content.ContentBlocks[0].Text,
					}
				}
			} else if rm.Content.ContentBlocks != nil {

				contentBlocks := responsesBlocksWithoutToolChanges(rm.Content.ContentBlocks)
				if len(contentBlocks) == 0 && len(rm.Content.ContentBlocks) > 0 {
					continue
				}
				chatBlocks := make([]ChatContentBlock, len(contentBlocks))
				for i, block := range contentBlocks {

					var chatBlockType ChatContentBlockType
					switch block.Type {
					case ResponsesInputMessageContentBlockTypeText:
						chatBlockType = ChatContentBlockTypeText
					case ResponsesOutputMessageContentTypeText:

						chatBlockType = ChatContentBlockTypeText
					case ResponsesInputMessageContentBlockTypeImage:
						chatBlockType = ChatContentBlockTypeImage
					case ResponsesInputMessageContentBlockTypeFile:
						chatBlockType = ChatContentBlockTypeFile
					case ResponsesInputMessageContentBlockTypeAudio:
						chatBlockType = ChatContentBlockTypeInputAudio
					default:

						chatBlockType = ChatContentBlockType(block.Type)
					}

					chatBlocks[i] = ChatContentBlock{
						Type:         chatBlockType,
						Text:         block.Text,
						GuardContent: block.GuardContent,
					}

					if block.ResponsesInputMessageContentBlockImage != nil {
						chatBlocks[i].ImageURLStruct = &ChatInputImage{
							Detail: block.ResponsesInputMessageContentBlockImage.Detail,
						}
						if block.ResponsesInputMessageContentBlockImage.ImageURL != nil {
							chatBlocks[i].ImageURLStruct.URL = *block.ResponsesInputMessageContentBlockImage.ImageURL
						}
					}
					if block.ResponsesInputMessageContentBlockFile != nil {
						chatBlocks[i].File = &ChatInputFile{
							FileData: block.ResponsesInputMessageContentBlockFile.FileData,
							FileURL:  block.ResponsesInputMessageContentBlockFile.FileURL,
							Filename: block.ResponsesInputMessageContentBlockFile.Filename,
							FileType: block.ResponsesInputMessageContentBlockFile.FileType,
							FileID:   block.FileID,
						}
					}
					if block.Audio != nil {
						chatBlocks[i].InputAudio = &ChatInputAudio{
							Data: block.Audio.Data,
						}
						if block.Audio.Format != "" {
							chatBlocks[i].InputAudio.Format = &block.Audio.Format
						}
					}
				}
				cm.Content = &ChatMessageContent{
					ContentBlocks: chatBlocks,
				}
			}
		}

		if cm.Role == ChatMessageRoleAssistant && (pendingReasoning.Len() > 0 || len(pendingReasoningDetails) > 0) {
			if cm.ChatAssistantMessage == nil {
				cm.ChatAssistantMessage = &ChatAssistantMessage{}
			}
			attachPendingReasoning(cm.ChatAssistantMessage)
		}

		if cm.Role == ChatMessageRoleTool &&
			(cm.Content == nil || (cm.Content.ContentStr == nil && len(cm.Content.ContentBlocks) == 0)) {
			cm.Content = &ChatMessageContent{ContentStr: Ptr("")}
		}

		chatMessages = append(chatMessages, cm)
	}

	if len(currentToolCalls) > 0 {

		toolCallsCopy := append([]ChatAssistantMessageToolCall(nil), currentToolCalls...)
		assistant := &ChatAssistantMessage{
			ToolCalls: toolCallsCopy,
		}
		attachPendingReasoning(assistant)
		chatMessages = append(chatMessages, ChatMessage{
			Role:                 ChatMessageRoleAssistant,
			ChatAssistantMessage: assistant,
		})
	}

	return chatMessages
}

func (cu *BifrostLLMUsage) ToResponsesResponseUsage() *ResponsesResponseUsage {
	if cu == nil {
		return nil
	}

	usage := &ResponsesResponseUsage{
		InputTokens:    cu.PromptTokens,
		OutputTokens:   cu.CompletionTokens,
		TotalTokens:    cu.TotalTokens,
		Cost:           cu.Cost,
		CostInUsdTicks: cu.CostInUsdTicks,
	}

	if cu.PromptTokensDetails != nil {
		usage.InputTokensDetails = &ResponsesResponseInputTokens{
			TextTokens:              cu.PromptTokensDetails.TextTokens,
			AudioTokens:             cu.PromptTokensDetails.AudioTokens,
			ImageTokens:             cu.PromptTokensDetails.ImageTokens,
			CachedReadTokens:        cu.PromptTokensDetails.CachedReadTokens,
			CachedWriteTokens:       cu.PromptTokensDetails.CachedWriteTokens,
			CachedWriteTokenDetails: cu.PromptTokensDetails.CachedWriteTokenDetails,

			StandardSpeechAudioTokens: cu.PromptTokensDetails.StandardSpeechAudioTokens,
			CustomSpeechAudioTokens:   cu.PromptTokensDetails.CustomSpeechAudioTokens,
		}
	}
	if cu.CompletionTokensDetails != nil {
		usage.OutputTokensDetails = &ResponsesResponseOutputTokens{
			TextTokens:               cu.CompletionTokensDetails.TextTokens,
			AcceptedPredictionTokens: cu.CompletionTokensDetails.AcceptedPredictionTokens,
			AudioTokens:              cu.CompletionTokensDetails.AudioTokens,
			ReasoningTokens:          cu.CompletionTokensDetails.ReasoningTokens,
			RejectedPredictionTokens: cu.CompletionTokensDetails.RejectedPredictionTokens,
			CitationTokens:           cu.CompletionTokensDetails.CitationTokens,
			NumSearchQueries:         cu.CompletionTokensDetails.NumSearchQueries,

			StandardSpeechAudioTokens: cu.CompletionTokensDetails.StandardSpeechAudioTokens,
			CustomSpeechAudioTokens:   cu.CompletionTokensDetails.CustomSpeechAudioTokens,
		}
	}
	usage.ToolUsage = cu.ToolUsage.DeepCopy()
	usage.Iterations = slices.Clone(cu.Iterations)

	return usage
}

func (ru *ResponsesResponseUsage) ToBifrostLLMUsage() *BifrostLLMUsage {
	if ru == nil {
		return nil
	}

	usage := &BifrostLLMUsage{
		PromptTokens:     ru.InputTokens,
		CompletionTokens: ru.OutputTokens,
		TotalTokens:      ru.TotalTokens,
		Cost:             ru.Cost,
		CostInUsdTicks:   ru.CostInUsdTicks,
	}

	if ru.InputTokensDetails != nil {
		usage.PromptTokensDetails = &ChatPromptTokensDetails{
			TextTokens:              ru.InputTokensDetails.TextTokens,
			AudioTokens:             ru.InputTokensDetails.AudioTokens,
			ImageTokens:             ru.InputTokensDetails.ImageTokens,
			CachedReadTokens:        ru.InputTokensDetails.CachedReadTokens,
			CachedWriteTokens:       ru.InputTokensDetails.CachedWriteTokens,
			CachedWriteTokenDetails: ru.InputTokensDetails.CachedWriteTokenDetails,

			StandardSpeechAudioTokens: ru.InputTokensDetails.StandardSpeechAudioTokens,
			CustomSpeechAudioTokens:   ru.InputTokensDetails.CustomSpeechAudioTokens,
		}
	}
	if ru.OutputTokensDetails != nil {
		usage.CompletionTokensDetails = &ChatCompletionTokensDetails{
			TextTokens:               ru.OutputTokensDetails.TextTokens,
			AcceptedPredictionTokens: ru.OutputTokensDetails.AcceptedPredictionTokens,
			AudioTokens:              ru.OutputTokensDetails.AudioTokens,
			ImageTokens:              ru.OutputTokensDetails.ImageTokens,
			ReasoningTokens:          ru.OutputTokensDetails.ReasoningTokens,
			RejectedPredictionTokens: ru.OutputTokensDetails.RejectedPredictionTokens,
			CitationTokens:           ru.OutputTokensDetails.CitationTokens,
			NumSearchQueries:         ru.OutputTokensDetails.NumSearchQueries,

			StandardSpeechAudioTokens: ru.OutputTokensDetails.StandardSpeechAudioTokens,
			CustomSpeechAudioTokens:   ru.OutputTokensDetails.CustomSpeechAudioTokens,
		}
	}
	usage.ToolUsage = ru.ToolUsage.DeepCopy()
	usage.Iterations = slices.Clone(ru.Iterations)

	return usage
}

func (cr *BifrostChatRequest) ToResponsesRequest() *BifrostResponsesRequest {
	if cr == nil {
		return &BifrostResponsesRequest{}
	}

	brr := &BifrostResponsesRequest{
		Provider:  cr.Provider,
		Model:     cr.Model,
		Fallbacks: cr.Fallbacks,
	}

	var allResponsesMessages []ResponsesMessage
	for _, chatMsg := range cr.Input {
		msg := &chatMsg
		if chatMsg.Role == ChatMessageRoleTool {

			msg = ToolResultForResponses(msg)
		}
		responsesMessages := msg.ToResponsesMessages()
		allResponsesMessages = append(allResponsesMessages, responsesMessages...)
	}
	brr.Input = allResponsesMessages

	if cr.Params != nil {
		brr.Params = &ResponsesParameters{

			ParallelToolCalls:    cr.Params.ParallelToolCalls,
			PromptCacheKey:       cr.Params.PromptCacheKey,
			PromptCacheRetention: cr.Params.PromptCacheRetention,
			PromptCacheOptions:   cr.Params.PromptCacheOptions,
			SafetyIdentifier:     cr.Params.SafetyIdentifier,
			ServiceTier:          cr.Params.ServiceTier,
			Store:                cr.Params.Store,
			Temperature:          cr.Params.Temperature,
			TopLogProbs:          cr.Params.TopLogProbs,
			TopP:                 cr.Params.TopP,
			ExtraParams:          cr.Params.ExtraParams,

			MaxOutputTokens: cr.Params.MaxCompletionTokens,
			Metadata:        cr.Params.Metadata,
		}

		if cr.Params.StreamOptions != nil {
			brr.Params.StreamOptions = &ResponsesStreamOptions{
				IncludeObfuscation: cr.Params.StreamOptions.IncludeObfuscation,
			}
		}

		if len(cr.Params.Tools) > 0 {
			responsesTools := make([]ResponsesTool, 0, len(cr.Params.Tools))
			for _, chatTool := range cr.Params.Tools {
				responsesTool := chatTool.ToResponsesTool()
				responsesTools = append(responsesTools, *responsesTool)
			}
			brr.Params.Tools = responsesTools
		}

		if cr.Params.ToolChoice != nil {
			responsesToolChoice := cr.Params.ToolChoice.ToResponsesToolChoice()
			brr.Params.ToolChoice = responsesToolChoice
		}

		if cr.Params.Reasoning != nil && (cr.Params.Reasoning.Enabled != nil || cr.Params.Reasoning.Effort != nil || cr.Params.Reasoning.MaxTokens != nil || cr.Params.Reasoning.Type != nil || cr.Params.Reasoning.Mode != nil || cr.Params.Reasoning.Summary != nil) {
			brr.Params.Reasoning = &ResponsesParametersReasoning{
				Effort:    cr.Params.Reasoning.Effort,
				MaxTokens: cr.Params.Reasoning.MaxTokens,
				Type:      cr.Params.Reasoning.Type,
				Mode:      cr.Params.Reasoning.Mode,
				Summary:   cr.Params.Reasoning.Summary,
			}
		}

		if format := ResponsesTextConfigFormatFromChatResponseFormat(cr.Params.ResponseFormat); format != nil {
			if brr.Params.Text == nil {
				brr.Params.Text = &ResponsesTextConfig{}
			}
			brr.Params.Text.Format = format
		}

		if cr.Params.Verbosity != nil {
			if brr.Params.Text == nil {
				brr.Params.Text = &ResponsesTextConfig{}
			}
			brr.Params.Text.Verbosity = cr.Params.Verbosity
		}
	}

	brr.RawRequestBody = cr.RawRequestBody

	return brr
}

func (brr *BifrostResponsesRequest) ToChatRequest() *BifrostChatRequest {
	if brr == nil {
		return &BifrostChatRequest{}
	}

	bcr := &BifrostChatRequest{
		Provider:  brr.Provider,
		Model:     brr.Model,
		Fallbacks: brr.Fallbacks,
	}

	bcr.Input = ToChatMessages(brr.Input)

	if brr.Params != nil && brr.Params.Instructions != nil && *brr.Params.Instructions != "" {
		bcr.Input = append([]ChatMessage{{
			Role:    ChatMessageRoleSystem,
			Content: &ChatMessageContent{ContentStr: brr.Params.Instructions},
		}}, bcr.Input...)
	}

	normalizeDeveloperRoleForChatFallback(bcr.Input)

	if brr.Params != nil {
		bcr.Params = &ChatParameters{

			ParallelToolCalls:    brr.Params.ParallelToolCalls,
			PromptCacheKey:       brr.Params.PromptCacheKey,
			PromptCacheRetention: brr.Params.PromptCacheRetention,
			PromptCacheOptions:   brr.Params.PromptCacheOptions,
			SafetyIdentifier:     brr.Params.SafetyIdentifier,
			ServiceTier:          brr.Params.ServiceTier,
			Store:                brr.Params.Store,
			Temperature:          brr.Params.Temperature,
			TopLogProbs:          brr.Params.TopLogProbs,
			TopP:                 brr.Params.TopP,
			ExtraParams:          brr.Params.ExtraParams,

			MaxCompletionTokens: brr.Params.MaxOutputTokens,
			Metadata:            brr.Params.Metadata,
		}

		if brr.Params.StreamOptions != nil {
			bcr.Params.StreamOptions = &ChatStreamOptions{
				IncludeObfuscation: brr.Params.StreamOptions.IncludeObfuscation,
				IncludeUsage:       Ptr(true),
			}
		}

		bcr.Params.Tools = sanitizeResponsesToolsForChatFallback(brr.Params.Tools)

		if brr.Params.ToolChoice != nil {
			chatToolChoice := brr.Params.ToolChoice.ToChatToolChoice()
			bcr.Params.ToolChoice = sanitizeChatToolChoiceForFallback(chatToolChoice, bcr.Params.Tools)
		}

		if brr.Params.Reasoning != nil {
			bcr.Params.Reasoning = &ChatReasoning{
				Effort:    brr.Params.Reasoning.Effort,
				MaxTokens: brr.Params.Reasoning.MaxTokens,
				Type:      brr.Params.Reasoning.Type,
				Mode:      brr.Params.Reasoning.Mode,
				Summary:   brr.Params.Reasoning.Summary,
			}
		}

		if rf := ChatResponseFormatFromResponsesFormat(brr.Params.Text.GetFormat()); rf != nil {
			bcr.Params.ResponseFormat = rf
		}

		if brr.Params.Text != nil && brr.Params.Text.Verbosity != nil {
			bcr.Params.Verbosity = brr.Params.Text.Verbosity
		}
	}

	bcr.RawRequestBody = brr.RawRequestBody

	return bcr
}

func sanitizeResponsesToolsForChatFallback(tools []ResponsesTool) []ChatTool {
	if len(tools) == 0 {
		return nil
	}

	chatTools := make([]ChatTool, 0, len(tools))
	for _, responsesTool := range tools {
		if responsesTool.Type != ResponsesToolTypeFunction {
			continue
		}
		if responsesTool.Name == nil || strings.TrimSpace(*responsesTool.Name) == "" {
			continue
		}

		chatTool := responsesTool.ToChatTool()
		if chatTool == nil || chatTool.Function == nil || strings.TrimSpace(chatTool.Function.Name) == "" {
			continue
		}

		chatTool.Type = ChatToolTypeFunction
		chatTool.Custom = nil
		chatTools = append(chatTools, *chatTool)
	}

	if len(chatTools) == 0 {
		return nil
	}

	return chatTools
}

func normalizeDeveloperRoleForChatFallback(messages []ChatMessage) {
	for i := range messages {
		if messages[i].Role == ChatMessageRoleDeveloper {
			messages[i].Role = ChatMessageRoleSystem
		}
	}
}

func sanitizeChatToolChoiceForFallback(toolChoice *ChatToolChoice, tools []ChatTool) *ChatToolChoice {
	if toolChoice == nil {
		return nil
	}
	if len(tools) == 0 {
		return nil
	}

	validToolNames := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool.Function != nil && strings.TrimSpace(tool.Function.Name) != "" {
			validToolNames[tool.Function.Name] = struct{}{}
		}
	}

	if toolChoice.ChatToolChoiceStruct != nil {
		switch toolChoice.ChatToolChoiceStruct.Type {
		case ChatToolChoiceTypeFunction:
			if toolChoice.ChatToolChoiceStruct.Function == nil {
				return nil
			}
			if _, ok := validToolNames[toolChoice.ChatToolChoiceStruct.Function.Name]; !ok {
				return nil
			}
		case ChatToolChoiceTypeAllowedTools, ChatToolChoiceTypeCustom:
			return nil
		}
	}

	return toolChoice
}

func responsesStatusFromChatFinishReason(finishReason string) (status string, incompleteDetails *ResponsesResponseIncompleteDetails, mapped bool) {
	switch finishReason {
	case string(BifrostFinishReasonLength):
		return "incomplete", &ResponsesResponseIncompleteDetails{Reason: ResponsesResponseIncompleteReasonMaxOutputTokens}, true
	case "content_filter", "guardrail_intervened":
		return "incomplete", &ResponsesResponseIncompleteDetails{Reason: ResponsesResponseIncompleteReasonContentFilter}, true
	case string(BifrostFinishReasonStop), string(BifrostFinishReasonToolCalls):
		return "completed", nil, true
	default:
		return "", nil, false
	}
}

func ResponsesStatusFromFinishReason(finishReason string) (status string, incompleteDetails *ResponsesResponseIncompleteDetails, mapped bool) {
	return responsesStatusFromChatFinishReason(finishReason)
}

func MarkTruncatedOutputItem(output []ResponsesMessage) {
	if len(output) == 0 {
		return
	}
	output[len(output)-1].Status = Ptr(ResponsesResponseStatusIncomplete)
}

func responsesStopReasonFromChatFinishReason(finishReason *string) *string {
	if finishReason == nil || *finishReason == "" {
		return nil
	}
	if _, _, mapped := responsesStatusFromChatFinishReason(*finishReason); !mapped {
		return nil
	}
	return Ptr(*finishReason)
}

func responsesTerminalFromChatFinishReason(finishReason *string) (eventType ResponsesStreamResponseType, status string, incompleteDetails *ResponsesResponseIncompleteDetails) {

	eventType = ResponsesStreamResponseTypeCompleted
	status = "completed"

	if finishReason == nil || *finishReason == "" {
		return eventType, status, nil
	}

	mappedStatus, mappedIncompleteDetails, mapped := responsesStatusFromChatFinishReason(*finishReason)
	if !mapped {
		return eventType, status, nil
	}

	if mappedStatus == "incomplete" {
		eventType = ResponsesStreamResponseTypeIncomplete
	}

	return eventType, mappedStatus, mappedIncompleteDetails
}

func chatFinishReasonFromResponses(status *string, incompleteDetails *ResponsesResponseIncompleteDetails, stopReason *string, output []ResponsesMessage) *string {
	if status != nil && (*status == ResponsesResponseStatusInProgress || *status == ResponsesResponseStatusQueued) {
		return nil
	}

	if stopReason != nil && *stopReason != "" {
		if _, _, mapped := responsesStatusFromChatFinishReason(*stopReason); mapped {
			return Ptr(*stopReason)
		}
	}

	if incompleteDetails != nil {
		switch incompleteDetails.Reason {
		case ResponsesResponseIncompleteReasonMaxOutputTokens:
			return Ptr(string(BifrostFinishReasonLength))
		case ResponsesResponseIncompleteReasonContentFilter:
			return Ptr("content_filter")
		}
	}

	if status != nil && *status == ResponsesResponseStatusCompleted {
		for _, item := range output {
			if item.Type != nil && *item.Type == ResponsesMessageTypeFunctionCall {
				return Ptr(string(BifrostFinishReasonToolCalls))
			}
		}
		return Ptr(string(BifrostFinishReasonStop))
	}

	return nil
}

func (cr *BifrostChatResponse) ToBifrostResponsesResponse() *BifrostResponsesResponse {
	if cr == nil {
		return nil
	}

	responsesResp := &BifrostResponsesResponse{
		ID:            Ptr(cr.ID),
		Object:        "response",
		CreatedAt:     cr.Created,
		Model:         cr.Model,
		ServiceTier:   cr.ServiceTier,
		Speed:         cr.Speed,
		InferenceGeo:  cr.InferenceGeo,
		Citations:     cr.Citations,
		SearchResults: cr.SearchResults,
		Videos:        cr.Videos,
	}

	var outputMessages []ResponsesMessage
	for _, choice := range cr.Choices {
		if choice.ChatNonStreamResponseChoice != nil && choice.ChatNonStreamResponseChoice.Message != nil {

			responsesMessages := choice.ChatNonStreamResponseChoice.Message.ToResponsesMessages()
			outputMessages = append(outputMessages, responsesMessages...)
		}
	}

	if len(outputMessages) > 0 {
		responsesResp.Output = outputMessages
	}

	if cr.Usage != nil {
		responsesResp.Usage = cr.Usage.ToResponsesResponseUsage()
	}

	hasCompletedFinishReason := false
	for _, choice := range cr.Choices {
		if choice.FinishReason == nil || *choice.FinishReason == "" {
			continue
		}
		status, incompleteDetails, mapped := responsesStatusFromChatFinishReason(*choice.FinishReason)
		if !mapped {
			continue
		}
		if status == "incomplete" {
			responsesResp.Status = Ptr(status)
			responsesResp.IncompleteDetails = incompleteDetails
			responsesResp.StopReason = responsesStopReasonFromChatFinishReason(choice.FinishReason)
			hasCompletedFinishReason = false
			break
		}
		if responsesResp.StopReason == nil {
			responsesResp.StopReason = responsesStopReasonFromChatFinishReason(choice.FinishReason)
		}
		hasCompletedFinishReason = true
	}
	if responsesResp.Status == nil && hasCompletedFinishReason {
		responsesResp.Status = Ptr("completed")
	}

	responsesResp.ExtraFields = cr.ExtraFields
	responsesResp.ExtraFields.RequestType = ResponsesRequest

	return responsesResp
}

func (responsesResp *BifrostResponsesResponse) ToBifrostChatResponse() *BifrostChatResponse {
	if responsesResp == nil {
		return nil
	}

	chatResp := &BifrostChatResponse{
		Created:       responsesResp.CreatedAt,
		Object:        "chat.completion",
		Model:         responsesResp.Model,
		ServiceTier:   responsesResp.ServiceTier,
		Speed:         responsesResp.Speed,
		InferenceGeo:  responsesResp.InferenceGeo,
		Citations:     responsesResp.Citations,
		SearchResults: responsesResp.SearchResults,
		Videos:        responsesResp.Videos,
	}

	if responsesResp.ID != nil {
		chatResp.ID = *responsesResp.ID
	}

	if len(responsesResp.Output) > 0 {

		chatMessages := ToChatMessages(responsesResp.Output)

		finishReason := chatFinishReasonFromResponses(responsesResp.Status, responsesResp.IncompleteDetails, responsesResp.StopReason, responsesResp.Output)

		choices := make([]BifrostResponseChoice, 0, len(chatMessages))
		for i, chatMsg := range chatMessages {
			choice := BifrostResponseChoice{
				Index:        i,
				FinishReason: finishReason,
				ChatNonStreamResponseChoice: &ChatNonStreamResponseChoice{
					Message: &chatMsg,
				},
			}
			choices = append(choices, choice)
		}

		chatResp.Choices = choices
	}

	if responsesResp.Usage != nil {

		chatResp.Usage = responsesResp.Usage.ToBifrostLLMUsage()
	}

	chatResp.ExtraFields = responsesResp.ExtraFields
	chatResp.ExtraFields.RequestType = ChatCompletionRequest
	chatResp.ExtraFields.Provider = responsesResp.ExtraFields.Provider

	return chatResp
}

func AcquireChatToResponsesStreamState() *ChatToResponsesStreamState {
	state := chatToResponsesStreamStatePool.Get().(*ChatToResponsesStreamState)

	if state.ToolArgumentBuffers == nil {
		state.ToolArgumentBuffers = make(map[string]string)
	} else {
		clear(state.ToolArgumentBuffers)
	}
	if state.ItemIDs == nil {
		state.ItemIDs = make(map[string]string)
	} else {
		clear(state.ItemIDs)
	}
	if state.ToolCallNames == nil {
		state.ToolCallNames = make(map[string]string)
	} else {
		clear(state.ToolCallNames)
	}
	if state.ToolCallIndexToID == nil {
		state.ToolCallIndexToID = make(map[uint16]string)
	} else {
		clear(state.ToolCallIndexToID)
	}
	if state.ToolCallOutputIndices == nil {
		state.ToolCallOutputIndices = make(map[string]int)
	} else {
		clear(state.ToolCallOutputIndices)
	}

	state.CurrentOutputIndex = 0
	state.MessageID = nil
	state.Model = nil
	state.CreatedAt = int(time.Now().Unix())
	state.HasEmittedCreated = false
	state.HasEmittedInProgress = false
	state.TextItemAdded = false
	state.TextItemClosed = false
	state.TextItemHasContent = false
	state.TextBuffer = strings.Builder{}
	state.TextOutputIndex = 0
	state.ReasoningItemAdded = false
	state.ReasoningItemClosed = false
	state.ReasoningOutputIndex = 0
	state.ReasoningBuffer = strings.Builder{}
	state.SequenceNumber = 0
	state.ServiceTier = nil
	state.Speed = nil
	state.InferenceGeo = nil
	return state
}

func ReleaseChatToResponsesStreamState(state *ChatToResponsesStreamState) {
	if state != nil {

		if state.ToolArgumentBuffers != nil {
			clear(state.ToolArgumentBuffers)
		}
		if state.ItemIDs != nil {
			clear(state.ItemIDs)
		}
		if state.ToolCallNames != nil {
			clear(state.ToolCallNames)
		}
		if state.ToolCallIndexToID != nil {
			clear(state.ToolCallIndexToID)
		}
		if state.ToolCallOutputIndices != nil {
			clear(state.ToolCallOutputIndices)
		}

		state.CurrentOutputIndex = 0
		state.MessageID = nil
		state.Model = nil
		state.CreatedAt = int(time.Now().Unix())
		state.HasEmittedCreated = false
		state.HasEmittedInProgress = false
		state.TextItemAdded = false
		state.TextItemClosed = false
		state.TextItemHasContent = false
		state.TextBuffer = strings.Builder{}
		state.TextOutputIndex = 0
		state.ReasoningItemAdded = false
		state.ReasoningItemClosed = false
		state.ReasoningOutputIndex = 0
		state.ReasoningBuffer = strings.Builder{}
		state.SequenceNumber = 0
		state.ServiceTier = nil
		state.Speed = nil
		state.InferenceGeo = nil
		chatToResponsesStreamStatePool.Put(state)
	}
}

func (cr *BifrostChatResponse) ToBifrostResponsesStreamResponse(state *ChatToResponsesStreamState) []*BifrostResponsesStreamResponse {
	if cr == nil || state == nil {
		return nil
	}

	if cr.ServiceTier != nil {
		state.ServiceTier = cr.ServiceTier
	}
	if cr.Speed != nil {
		state.Speed = cr.Speed
	}
	if cr.InferenceGeo != nil {
		state.InferenceGeo = cr.InferenceGeo
	}

	if len(cr.Choices) == 0 {
		return nil
	}

	choice := cr.Choices[0]
	var delta *ChatStreamResponseChoiceDelta
	if choice.ChatStreamResponseChoice != nil {
		delta = choice.ChatStreamResponseChoice.Delta
	}
	if delta == nil {
		if choice.FinishReason == nil {
			return nil
		}

		delta = &ChatStreamResponseChoiceDelta{}
	}

	var responses []*BifrostResponsesStreamResponse

	if state.MessageID == nil && cr.ID != "" {
		state.MessageID = &cr.ID
	}
	if state.Model == nil && cr.Model != "" {
		state.Model = &cr.Model
	}

	if delta.Role != nil && !state.HasEmittedCreated {

		response := &BifrostResponsesResponse{
			ID:        state.MessageID,
			CreatedAt: state.CreatedAt,
		}
		responses = append(responses, &BifrostResponsesStreamResponse{
			Type:           ResponsesStreamResponseTypeCreated,
			SequenceNumber: state.SequenceNumber,
			Response:       response,
			ExtraFields:    cr.ExtraFields,
		})
		state.SequenceNumber++
		state.HasEmittedCreated = true

		response = &BifrostResponsesResponse{
			ID:        state.MessageID,
			CreatedAt: state.CreatedAt,
		}
		responses = append(responses, &BifrostResponsesStreamResponse{
			Type:           ResponsesStreamResponseTypeInProgress,
			SequenceNumber: state.SequenceNumber,
			Response:       response,
			ExtraFields:    cr.ExtraFields,
		})
		state.SequenceNumber++
		state.HasEmittedInProgress = true
	}

	hasContent := delta.Content != nil && *delta.Content != ""
	hasReasoning := delta.Reasoning != nil && *delta.Reasoning != ""

	closeReasoningItem := func() {
		if !state.ReasoningItemAdded || state.ReasoningItemClosed {
			return
		}
		itemID := state.ItemIDs["reasoning"]
		reasoningText := state.ReasoningBuffer.String()
		reasoningType := ResponsesMessageTypeReasoning
		role := ResponsesInputMessageRoleAssistant
		doneItem := &ResponsesMessage{
			Type: &reasoningType,
			Role: &role,
			Content: &ResponsesMessageContent{
				ContentBlocks: []ResponsesMessageContentBlock{
					{Type: ResponsesOutputMessageContentTypeReasoning, Text: &reasoningText},
				},
			},
		}
		if itemID != "" {
			doneItem.ID = &itemID
		}
		responses = append(responses, &BifrostResponsesStreamResponse{
			Type:           ResponsesStreamResponseTypeOutputItemDone,
			SequenceNumber: state.SequenceNumber,
			OutputIndex:    Ptr(state.ReasoningOutputIndex),
			Item:           doneItem,
			ExtraFields:    cr.ExtraFields,
		})
		state.SequenceNumber++
		state.ReasoningItemClosed = true
	}

	if hasReasoning && !state.ReasoningItemClosed {
		if !state.ReasoningItemAdded {
			outputIndex := state.CurrentOutputIndex
			state.CurrentOutputIndex++
			var itemID string
			if state.MessageID == nil {
				itemID = fmt.Sprintf("item_%d_reasoning", outputIndex)
			} else {
				itemID = fmt.Sprintf("msg_%s_reasoning", *state.MessageID)
			}
			state.ItemIDs["reasoning"] = itemID
			state.ReasoningOutputIndex = outputIndex

			reasoningType := ResponsesMessageTypeReasoning
			role := ResponsesInputMessageRoleAssistant
			item := &ResponsesMessage{
				ID:   &itemID,
				Type: &reasoningType,
				Role: &role,
				Content: &ResponsesMessageContent{
					ContentBlocks: []ResponsesMessageContentBlock{},
				},
			}
			responses = append(responses, &BifrostResponsesStreamResponse{
				Type:           ResponsesStreamResponseTypeOutputItemAdded,
				SequenceNumber: state.SequenceNumber,
				OutputIndex:    Ptr(outputIndex),
				Item:           item,
				ExtraFields:    cr.ExtraFields,
			})
			state.SequenceNumber++
			state.ReasoningItemAdded = true
		}

		state.ReasoningBuffer.WriteString(*delta.Reasoning)
		itemID := state.ItemIDs["reasoning"]
		reasoningDelta := &BifrostResponsesStreamResponse{
			Type:           ResponsesStreamResponseTypeReasoningSummaryTextDelta,
			SequenceNumber: state.SequenceNumber,
			OutputIndex:    Ptr(state.ReasoningOutputIndex),
			Delta:          delta.Reasoning,
			ExtraFields:    cr.ExtraFields,
		}
		if itemID != "" {
			reasoningDelta.ItemID = &itemID
		}
		responses = append(responses, reasoningDelta)
		state.SequenceNumber++
	}

	if hasContent && !state.TextItemClosed {
		closeReasoningItem()

		if !state.TextItemAdded {
			outputIndex := state.CurrentOutputIndex
			state.CurrentOutputIndex++
			state.TextOutputIndex = outputIndex

			var itemID string
			if state.MessageID == nil {
				itemID = fmt.Sprintf("item_%d", outputIndex)
			} else {
				itemID = fmt.Sprintf("msg_%s_item_%d", *state.MessageID, outputIndex)
			}
			state.ItemIDs["text"] = itemID

			messageType := ResponsesMessageTypeMessage
			role := ResponsesInputMessageRoleAssistant

			item := &ResponsesMessage{
				ID:   &itemID,
				Type: &messageType,
				Role: &role,
				Content: &ResponsesMessageContent{
					ContentBlocks: []ResponsesMessageContentBlock{},
				},
			}

			responses = append(responses, &BifrostResponsesStreamResponse{
				Type:           ResponsesStreamResponseTypeOutputItemAdded,
				SequenceNumber: state.SequenceNumber,
				OutputIndex:    Ptr(outputIndex),
				ContentIndex:   Ptr(0),
				Item:           item,
				ExtraFields:    cr.ExtraFields,
			})
			state.SequenceNumber++
			state.TextItemAdded = true

			emptyText := ""
			part := &ResponsesMessageContentBlock{
				Type: ResponsesOutputMessageContentTypeText,
				Text: &emptyText,
				ResponsesOutputMessageContentText: &ResponsesOutputMessageContentText{
					LogProbs:    []ResponsesOutputMessageContentTextLogProb{},
					Annotations: []ResponsesOutputMessageContentTextAnnotation{},
				},
			}
			responses = append(responses, &BifrostResponsesStreamResponse{
				Type:           ResponsesStreamResponseTypeContentPartAdded,
				SequenceNumber: state.SequenceNumber,
				OutputIndex:    Ptr(outputIndex),
				ContentIndex:   Ptr(0),
				ItemID:         &itemID,
				Part:           part,
				ExtraFields:    cr.ExtraFields,
			})
			state.SequenceNumber++
		}

		itemID := state.ItemIDs["text"]
		contentDelta := *delta.Content
		state.TextBuffer.WriteString(contentDelta)

		response := &BifrostResponsesStreamResponse{
			Type:           ResponsesStreamResponseTypeOutputTextDelta,
			SequenceNumber: state.SequenceNumber,
			OutputIndex:    Ptr(state.TextOutputIndex),
			ContentIndex:   Ptr(0),
			Delta:          &contentDelta,
			LogProbs:       []ResponsesOutputMessageContentTextLogProb{},
			ExtraFields:    cr.ExtraFields,
		}
		if itemID != "" {
			response.ItemID = &itemID
		}
		responses = append(responses, response)
		state.SequenceNumber++
		state.TextItemHasContent = true
	}

	if len(delta.ToolCalls) > 0 {

		toolCall := delta.ToolCalls[0]
		contentIndex := 1

		var toolCallID string
		if toolCall.ID != nil && *toolCall.ID != "" {
			toolCallID = *toolCall.ID
		} else {

			if id, exists := state.ToolCallIndexToID[toolCall.Index]; exists {
				toolCallID = id
			} else {

				return responses
			}
		}

		if toolCall.ID != nil && *toolCall.ID != "" {
			if _, exists := state.ToolCallOutputIndices[toolCallID]; !exists {

				closeReasoningItem()

				if state.TextItemAdded && !state.TextItemClosed && state.TextItemHasContent {
					outputIndex := state.TextOutputIndex
					itemID := state.ItemIDs["text"]

					finalText := state.TextBuffer.String()
					responses = append(responses, &BifrostResponsesStreamResponse{
						Type:           ResponsesStreamResponseTypeOutputTextDone,
						SequenceNumber: state.SequenceNumber,
						OutputIndex:    Ptr(outputIndex),
						ContentIndex:   Ptr(0),
						ItemID:         &itemID,
						Text:           &finalText,
						LogProbs:       []ResponsesOutputMessageContentTextLogProb{},
						ExtraFields:    cr.ExtraFields,
					})
					state.SequenceNumber++

					part := &ResponsesMessageContentBlock{
						Type: ResponsesOutputMessageContentTypeText,
						Text: &finalText,
						ResponsesOutputMessageContentText: &ResponsesOutputMessageContentText{
							LogProbs:    []ResponsesOutputMessageContentTextLogProb{},
							Annotations: []ResponsesOutputMessageContentTextAnnotation{},
						},
					}
					responses = append(responses, &BifrostResponsesStreamResponse{
						Type:           ResponsesStreamResponseTypeContentPartDone,
						SequenceNumber: state.SequenceNumber,
						OutputIndex:    Ptr(outputIndex),
						ContentIndex:   Ptr(0),
						ItemID:         &itemID,
						Part:           part,
						ExtraFields:    cr.ExtraFields,
					})
					state.SequenceNumber++

					statusCompleted := "completed"
					messageType := ResponsesMessageTypeMessage
					role := ResponsesInputMessageRoleAssistant
					textType := ResponsesOutputMessageContentTypeText
					doneItem := &ResponsesMessage{
						Type:   &messageType,
						Role:   &role,
						Status: &statusCompleted,
						Content: &ResponsesMessageContent{
							ContentBlocks: []ResponsesMessageContentBlock{
								{
									Type: textType,
									Text: &finalText,
									ResponsesOutputMessageContentText: &ResponsesOutputMessageContentText{
										LogProbs:    []ResponsesOutputMessageContentTextLogProb{},
										Annotations: []ResponsesOutputMessageContentTextAnnotation{},
									},
								},
							},
						},
					}
					if itemID != "" {
						doneItem.ID = &itemID
					}
					responses = append(responses, &BifrostResponsesStreamResponse{
						Type:           ResponsesStreamResponseTypeOutputItemDone,
						SequenceNumber: state.SequenceNumber,
						OutputIndex:    Ptr(outputIndex),
						ContentIndex:   Ptr(0),
						Item:           doneItem,
						ExtraFields:    cr.ExtraFields,
					})
					state.SequenceNumber++
					state.TextItemClosed = true
				}

				outputIndex := state.CurrentOutputIndex
				if outputIndex == 0 {
					outputIndex = 1
				}
				state.CurrentOutputIndex = outputIndex + 1
				state.ToolCallOutputIndices[toolCallID] = outputIndex

				state.ItemIDs[toolCallID] = toolCallID
				state.ToolCallIndexToID[toolCall.Index] = toolCallID
				if toolCall.Function.Name != nil {
					state.ToolCallNames[toolCallID] = *toolCall.Function.Name
				}

				state.ToolArgumentBuffers[toolCallID] = ""

				statusInProgress := "in_progress"
				item := &ResponsesMessage{
					ID:     &toolCallID,
					Type:   Ptr(ResponsesMessageTypeFunctionCall),
					Status: &statusInProgress,
					ResponsesToolMessage: &ResponsesToolMessage{
						CallID:    &toolCallID,
						Name:      toolCall.Function.Name,
						Arguments: Ptr(""),
					},
				}

				responses = append(responses, &BifrostResponsesStreamResponse{
					Type:           ResponsesStreamResponseTypeOutputItemAdded,
					SequenceNumber: state.SequenceNumber,
					OutputIndex:    Ptr(outputIndex),
					ContentIndex:   Ptr(contentIndex),
					Item:           item,
					ExtraFields:    cr.ExtraFields,
				})
				state.SequenceNumber++
			}
		}

		if toolCall.Function.Arguments != "" {
			outputIndex := state.ToolCallOutputIndices[toolCallID]
			state.ToolArgumentBuffers[toolCallID] += toolCall.Function.Arguments

			itemID := state.ItemIDs[toolCallID]
			response := &BifrostResponsesStreamResponse{
				Type:           ResponsesStreamResponseTypeFunctionCallArgumentsDelta,
				SequenceNumber: state.SequenceNumber,
				OutputIndex:    Ptr(outputIndex),
				ContentIndex:   Ptr(contentIndex),
				Delta:          &toolCall.Function.Arguments,
				ExtraFields:    cr.ExtraFields,
			}
			if itemID != "" {
				response.ItemID = &itemID
			}
			responses = append(responses, response)
			state.SequenceNumber++
		}
	}

	if delta.Refusal != nil && *delta.Refusal != "" {

		response := &BifrostResponsesStreamResponse{
			Type:           ResponsesStreamResponseTypeRefusalDelta,
			SequenceNumber: state.SequenceNumber,
			OutputIndex:    Ptr(0),
			Refusal:        delta.Refusal,
			ExtraFields:    cr.ExtraFields,
		}
		responses = append(responses, response)
		state.SequenceNumber++
	}

	if choice.FinishReason != nil {
		terminalEventType, terminalStatus, terminalIncompleteDetails := responsesTerminalFromChatFinishReason(choice.FinishReason)

		closeReasoningItem()

		if state.TextItemAdded && !state.TextItemClosed {
			outputIndex := state.TextOutputIndex
			itemID := state.ItemIDs["text"]

			finalText := state.TextBuffer.String()
			responses = append(responses, &BifrostResponsesStreamResponse{
				Type:           ResponsesStreamResponseTypeOutputTextDone,
				SequenceNumber: state.SequenceNumber,
				OutputIndex:    Ptr(outputIndex),
				ContentIndex:   Ptr(0),
				ItemID:         &itemID,
				Text:           &finalText,
				LogProbs:       []ResponsesOutputMessageContentTextLogProb{},
				ExtraFields:    cr.ExtraFields,
			})
			state.SequenceNumber++

			part := &ResponsesMessageContentBlock{
				Type: ResponsesOutputMessageContentTypeText,
				Text: &finalText,
				ResponsesOutputMessageContentText: &ResponsesOutputMessageContentText{
					LogProbs:    []ResponsesOutputMessageContentTextLogProb{},
					Annotations: []ResponsesOutputMessageContentTextAnnotation{},
				},
			}
			responses = append(responses, &BifrostResponsesStreamResponse{
				Type:           ResponsesStreamResponseTypeContentPartDone,
				SequenceNumber: state.SequenceNumber,
				OutputIndex:    Ptr(outputIndex),
				ContentIndex:   Ptr(0),
				ItemID:         &itemID,
				Part:           part,
				ExtraFields:    cr.ExtraFields,
			})
			state.SequenceNumber++

			statusFinal := terminalStatus
			messageType := ResponsesMessageTypeMessage
			role := ResponsesInputMessageRoleAssistant
			textType := ResponsesOutputMessageContentTypeText
			doneItem := &ResponsesMessage{
				Type:   &messageType,
				Role:   &role,
				Status: &statusFinal,
				Content: &ResponsesMessageContent{
					ContentBlocks: []ResponsesMessageContentBlock{
						{
							Type: textType,
							Text: &finalText,
							ResponsesOutputMessageContentText: &ResponsesOutputMessageContentText{
								LogProbs:    []ResponsesOutputMessageContentTextLogProb{},
								Annotations: []ResponsesOutputMessageContentTextAnnotation{},
							},
						},
					},
				},
			}
			if itemID != "" {
				doneItem.ID = &itemID
			}
			responses = append(responses, &BifrostResponsesStreamResponse{
				Type:           ResponsesStreamResponseTypeOutputItemDone,
				SequenceNumber: state.SequenceNumber,
				OutputIndex:    Ptr(outputIndex),
				ContentIndex:   Ptr(0),
				Item:           doneItem,
				ExtraFields:    cr.ExtraFields,
			})
			state.SequenceNumber++
			state.TextItemClosed = true
		}

		for toolCallID, args := range state.ToolArgumentBuffers {
			if args != "" {
				outputIndex := state.ToolCallOutputIndices[toolCallID]
				itemID := state.ItemIDs[toolCallID]
				contentIndex := 1
				argsCopy := args

				response := &BifrostResponsesStreamResponse{
					Type:           ResponsesStreamResponseTypeFunctionCallArgumentsDone,
					SequenceNumber: state.SequenceNumber,
					OutputIndex:    Ptr(outputIndex),
					ContentIndex:   Ptr(contentIndex),
					Arguments:      &argsCopy,
					ExtraFields:    cr.ExtraFields,
				}
				if itemID != "" {
					response.ItemID = &itemID
				}
				responses = append(responses, response)
				state.SequenceNumber++

				statusFinal := terminalStatus
				messageType := ResponsesMessageTypeFunctionCall
				callName, hasName := state.ToolCallNames[toolCallID]
				var callNamePtr *string
				if hasName && callName != "" {
					callNamePtr = &callName
				}
				argsValue := args
				outputItemDone := &ResponsesMessage{
					Type:   &messageType,
					Status: &statusFinal,
					ResponsesToolMessage: &ResponsesToolMessage{
						CallID:    &toolCallID,
						Name:      callNamePtr,
						Arguments: &argsValue,
					},
				}
				if itemID != "" {
					outputItemDone.ID = &itemID
				}
				responses = append(responses, &BifrostResponsesStreamResponse{
					Type:           ResponsesStreamResponseTypeOutputItemDone,
					SequenceNumber: state.SequenceNumber,
					OutputIndex:    Ptr(outputIndex),
					ContentIndex:   Ptr(contentIndex),
					Item:           outputItemDone,
					ExtraFields:    cr.ExtraFields,
				})
				state.SequenceNumber++
			}
		}

		var usage *ResponsesResponseUsage
		if cr.Usage != nil {
			usage = cr.Usage.ToResponsesResponseUsage()
		}

		responseStatus := terminalStatus

		response := &BifrostResponsesResponse{
			ID:                state.MessageID,
			CreatedAt:         state.CreatedAt,
			Usage:             usage,
			Status:            &responseStatus,
			StopReason:        responsesStopReasonFromChatFinishReason(choice.FinishReason),
			IncompleteDetails: terminalIncompleteDetails,
			ServiceTier:       state.ServiceTier,
			Speed:             state.Speed,
			InferenceGeo:      state.InferenceGeo,
		}

		if state.Model != nil {
			response.Model = *state.Model
		}
		var allOutput []ResponsesMessage

		if state.ReasoningItemAdded {
			reasoningType := ResponsesMessageTypeReasoning
			role := ResponsesInputMessageRoleAssistant
			reasoningText := state.ReasoningBuffer.String()
			itemID := state.ItemIDs["reasoning"]
			rMsg := ResponsesMessage{
				Type: &reasoningType,
				Role: &role,
				Content: &ResponsesMessageContent{
					ContentBlocks: []ResponsesMessageContentBlock{
						{Type: ResponsesOutputMessageContentTypeReasoning, Text: &reasoningText},
					},
				},
			}
			if itemID != "" {
				rMsg.ID = &itemID
			}
			allOutput = append(allOutput, rMsg)
		}

		if state.TextItemAdded {
			statusFinal := terminalStatus
			messageType := ResponsesMessageTypeMessage
			role := ResponsesInputMessageRoleAssistant
			textType := ResponsesOutputMessageContentTypeText
			finalText := state.TextBuffer.String()
			itemID := state.ItemIDs["text"]

			msg := ResponsesMessage{
				Type:   &messageType,
				Role:   &role,
				Status: &statusFinal,
				Content: &ResponsesMessageContent{
					ContentBlocks: []ResponsesMessageContentBlock{
						{
							Type: textType,
							Text: &finalText,
							ResponsesOutputMessageContentText: &ResponsesOutputMessageContentText{
								LogProbs:    []ResponsesOutputMessageContentTextLogProb{},
								Annotations: []ResponsesOutputMessageContentTextAnnotation{},
							},
						},
					},
				},
			}
			if itemID != "" {
				msg.ID = &itemID
			}
			allOutput = append(allOutput, msg)
		}

		type toolCallEntry struct {
			toolCallID  string
			outputIndex int
		}
		var toolCallEntries []toolCallEntry
		for toolCallID, outputIndex := range state.ToolCallOutputIndices {
			toolCallEntries = append(toolCallEntries, toolCallEntry{toolCallID: toolCallID, outputIndex: outputIndex})
		}
		sort.Slice(toolCallEntries, func(i, j int) bool {
			return toolCallEntries[i].outputIndex < toolCallEntries[j].outputIndex
		})

		for _, entry := range toolCallEntries {
			toolCallID := entry.toolCallID
			statusFinal := terminalStatus
			messageType := ResponsesMessageTypeFunctionCall
			callName, hasName := state.ToolCallNames[toolCallID]
			var callNamePtr *string
			if hasName && callName != "" {
				callNamePtr = &callName
			}
			args := state.ToolArgumentBuffers[toolCallID]
			fcMsg := ResponsesMessage{
				Type:   &messageType,
				Status: &statusFinal,
				ResponsesToolMessage: &ResponsesToolMessage{
					CallID:    &toolCallID,
					Name:      callNamePtr,
					Arguments: &args,
				},
			}
			itemID := state.ItemIDs[toolCallID]
			if itemID != "" {
				fcMsg.ID = &itemID
			}
			allOutput = append(allOutput, fcMsg)
		}

		if len(allOutput) > 0 {
			response.Output = allOutput
		}

		responses = append(responses, &BifrostResponsesStreamResponse{
			Type:           terminalEventType,
			SequenceNumber: state.SequenceNumber,
			Response:       response,
			ExtraFields:    cr.ExtraFields,
		})
		state.SequenceNumber++
	}

	for _, resp := range responses {
		if resp != nil {
			resp.ExtraFields.RequestType = ResponsesStreamRequest

			resp.SearchResults = cr.SearchResults
			resp.Videos = cr.Videos
			resp.Citations = cr.Citations
		}
	}

	return responses
}

func (rsr *BifrostResponsesStreamResponse) ToBifrostChatResponse() *BifrostChatResponse {
	if rsr == nil {
		return nil
	}

	extraFields := rsr.ExtraFields
	extraFields.RequestType = ChatCompletionStreamRequest

	resp := &BifrostChatResponse{
		Object:        "chat.completion.chunk",
		ExtraFields:   extraFields,
		SearchResults: rsr.SearchResults,
		Videos:        rsr.Videos,
		Citations:     rsr.Citations,
	}

	if rsr.Response != nil {
		if rsr.Response.ID != nil {
			resp.ID = *rsr.Response.ID
		}
		resp.Created = rsr.Response.CreatedAt
		resp.Model = rsr.Response.Model
		resp.ServiceTier = rsr.Response.ServiceTier
		resp.Speed = rsr.Response.Speed
		resp.InferenceGeo = rsr.Response.InferenceGeo
	}

	switch rsr.Type {
	case ResponsesStreamResponseTypeOutputTextDelta:
		resp.Choices = []BifrostResponseChoice{
			{
				Index: 0,
				ChatStreamResponseChoice: &ChatStreamResponseChoice{
					Delta: &ChatStreamResponseChoiceDelta{
						Content: rsr.Delta,
					},
				},
			},
		}
		return resp

	case ResponsesStreamResponseTypeReasoningSummaryTextDelta:
		resp.Choices = []BifrostResponseChoice{
			{
				Index: 0,
				ChatStreamResponseChoice: &ChatStreamResponseChoice{
					Delta: &ChatStreamResponseChoiceDelta{
						Reasoning: rsr.Delta,
					},
				},
			},
		}
		return resp

	case ResponsesStreamResponseTypeRefusalDelta:
		resp.Choices = []BifrostResponseChoice{
			{
				Index: 0,
				ChatStreamResponseChoice: &ChatStreamResponseChoice{
					Delta: &ChatStreamResponseChoiceDelta{
						Refusal: rsr.Refusal,
					},
				},
			},
		}
		return resp

	case ResponsesStreamResponseTypeOutputItemAdded:
		if rsr.Item == nil || rsr.Item.Type == nil {
			resp.Choices = []BifrostResponseChoice{
				{
					Index: 0,
					ChatStreamResponseChoice: &ChatStreamResponseChoice{
						Delta: &ChatStreamResponseChoiceDelta{},
					},
				},
			}
			return resp
		}

		switch *rsr.Item.Type {
		case ResponsesMessageTypeFunctionCall:
			if rsr.Item.ResponsesToolMessage == nil {
				resp.Choices = []BifrostResponseChoice{
					{
						Index: 0,
						ChatStreamResponseChoice: &ChatStreamResponseChoice{
							Delta: &ChatStreamResponseChoiceDelta{},
						},
					},
				}
				return resp
			}
			funcType := "function"
			var idx uint16
			if rsr.OutputIndex != nil && *rsr.OutputIndex > 0 {
				idx = uint16(*rsr.OutputIndex - 1)
			}
			resp.Choices = []BifrostResponseChoice{
				{
					Index: 0,
					ChatStreamResponseChoice: &ChatStreamResponseChoice{
						Delta: &ChatStreamResponseChoiceDelta{
							ToolCalls: []ChatAssistantMessageToolCall{
								{
									Index: idx,
									Type:  &funcType,
									ID:    rsr.Item.ResponsesToolMessage.CallID,
									Function: ChatAssistantMessageToolCallFunction{
										Name: rsr.Item.ResponsesToolMessage.Name,
									},
								},
							},
						},
					},
				},
			}
			return resp

		case ResponsesMessageTypeMessage:
			role := "assistant"
			resp.Choices = []BifrostResponseChoice{
				{
					Index: 0,
					ChatStreamResponseChoice: &ChatStreamResponseChoice{
						Delta: &ChatStreamResponseChoiceDelta{
							Role: &role,
						},
					},
				},
			}
			return resp

		default:

			resp.Choices = []BifrostResponseChoice{
				{
					Index: 0,
					ChatStreamResponseChoice: &ChatStreamResponseChoice{
						Delta: &ChatStreamResponseChoiceDelta{},
					},
				},
			}
			return resp
		}

	case ResponsesStreamResponseTypeFunctionCallArgumentsDelta:
		if rsr.Delta == nil {
			resp.Choices = []BifrostResponseChoice{
				{
					Index: 0,
					ChatStreamResponseChoice: &ChatStreamResponseChoice{
						Delta: &ChatStreamResponseChoiceDelta{},
					},
				},
			}
			return resp
		}
		var idx uint16
		if rsr.OutputIndex != nil && *rsr.OutputIndex > 0 {
			idx = uint16(*rsr.OutputIndex - 1)
		}

		resp.Choices = []BifrostResponseChoice{
			{
				Index: 0,
				ChatStreamResponseChoice: &ChatStreamResponseChoice{
					Delta: &ChatStreamResponseChoiceDelta{
						ToolCalls: []ChatAssistantMessageToolCall{
							{
								Index: idx,
								Function: ChatAssistantMessageToolCallFunction{
									Arguments: *rsr.Delta,
								},
							},
						},
					},
				},
			},
		}
		return resp

	case ResponsesStreamResponseTypeCompleted, ResponsesStreamResponseTypeIncomplete:
		status := ResponsesResponseStatusCompleted
		fallback := string(BifrostFinishReasonStop)
		if rsr.Type == ResponsesStreamResponseTypeIncomplete {
			status = ResponsesResponseStatusIncomplete
			fallback = string(BifrostFinishReasonLength)
		}

		var (
			incompleteDetails *ResponsesResponseIncompleteDetails
			stopReason        *string
			output            []ResponsesMessage
		)
		if rsr.Response != nil {
			incompleteDetails = rsr.Response.IncompleteDetails
			stopReason = rsr.Response.StopReason
			output = rsr.Response.Output
			if rsr.Response.Usage != nil {
				resp.Usage = rsr.Response.Usage.ToBifrostLLMUsage()
			}
		}

		finishReason := chatFinishReasonFromResponses(&status, incompleteDetails, stopReason, output)
		if finishReason == nil {
			finishReason = &fallback
		}

		resp.Choices = []BifrostResponseChoice{
			{
				Index:        0,
				FinishReason: finishReason,
				ChatStreamResponseChoice: &ChatStreamResponseChoice{
					Delta: &ChatStreamResponseChoiceDelta{},
				},
			},
		}
		return resp

	default:

		resp.Choices = []BifrostResponseChoice{
			{
				Index: 0,
				ChatStreamResponseChoice: &ChatStreamResponseChoice{
					Delta: &ChatStreamResponseChoiceDelta{},
				},
			},
		}
		return resp
	}
}

func (cr *BifrostChatResponse) ToBifrostTextCompletionResponse() *BifrostTextCompletionResponse {
	if cr == nil {
		return nil
	}

	if len(cr.Choices) == 0 {
		return &BifrostTextCompletionResponse{
			ID:                cr.ID,
			Model:             cr.Model,
			Created:           cr.Created,
			Object:            "text_completion",
			SystemFingerprint: cr.SystemFingerprint,
			Usage:             cr.Usage,
			ExtraFields: BifrostResponseExtraFields{
				RequestType:             TextCompletionRequest,
				ChunkIndex:              cr.ExtraFields.ChunkIndex,
				Provider:                cr.ExtraFields.Provider,
				OriginalModelRequested:  cr.ExtraFields.OriginalModelRequested,
				Latency:                 cr.ExtraFields.Latency,
				RawResponse:             cr.ExtraFields.RawResponse,
				CacheDebug:              cr.ExtraFields.CacheDebug,
				GuardrailDebug:          cr.ExtraFields.GuardrailDebug,
				ProviderResponseHeaders: cr.ExtraFields.ProviderResponseHeaders,
			},
		}
	}

	choice := cr.Choices[0]

	if choice.ChatStreamResponseChoice != nil && choice.ChatStreamResponseChoice.Delta != nil {
		return &BifrostTextCompletionResponse{
			ID:                cr.ID,
			Model:             cr.Model,
			Created:           cr.Created,
			Object:            "text_completion",
			SystemFingerprint: cr.SystemFingerprint,
			Choices: []BifrostResponseChoice{
				{
					Index: 0,
					TextCompletionResponseChoice: &TextCompletionResponseChoice{
						Text: choice.ChatStreamResponseChoice.Delta.Content,
					},
					FinishReason: choice.FinishReason,
					LogProbs:     choice.LogProbs,
				},
			},
			Usage: cr.Usage,
			ExtraFields: BifrostResponseExtraFields{
				RequestType:             TextCompletionRequest,
				ChunkIndex:              cr.ExtraFields.ChunkIndex,
				Provider:                cr.ExtraFields.Provider,
				OriginalModelRequested:  cr.ExtraFields.OriginalModelRequested,
				Latency:                 cr.ExtraFields.Latency,
				RawResponse:             cr.ExtraFields.RawResponse,
				CacheDebug:              cr.ExtraFields.CacheDebug,
				GuardrailDebug:          cr.ExtraFields.GuardrailDebug,
				ProviderResponseHeaders: cr.ExtraFields.ProviderResponseHeaders,
			},
		}
	}

	if choice.ChatNonStreamResponseChoice != nil {
		msg := choice.ChatNonStreamResponseChoice.Message
		var textContent *string
		if msg != nil && msg.Content != nil {
			if msg.Content.ContentStr != nil {
				textContent = msg.Content.ContentStr
			} else if len(msg.Content.ContentBlocks) > 0 {
				var sb strings.Builder
				for _, block := range msg.Content.ContentBlocks {
					if block.Text != nil {
						sb.WriteString(*block.Text)
					}
				}
				if sb.Len() > 0 {
					s := sb.String()
					textContent = &s
				}
			}
		}
		return &BifrostTextCompletionResponse{
			ID:                cr.ID,
			Model:             cr.Model,
			Created:           cr.Created,
			Object:            "text_completion",
			SystemFingerprint: cr.SystemFingerprint,
			Choices: []BifrostResponseChoice{
				{
					Index: 0,
					TextCompletionResponseChoice: &TextCompletionResponseChoice{
						Text: textContent,
					},
					FinishReason: choice.FinishReason,
					LogProbs:     choice.LogProbs,
				},
			},
			Usage: cr.Usage,
			ExtraFields: BifrostResponseExtraFields{
				RequestType:             TextCompletionRequest,
				ChunkIndex:              cr.ExtraFields.ChunkIndex,
				Provider:                cr.ExtraFields.Provider,
				OriginalModelRequested:  cr.ExtraFields.OriginalModelRequested,
				Latency:                 cr.ExtraFields.Latency,
				RawResponse:             cr.ExtraFields.RawResponse,
				CacheDebug:              cr.ExtraFields.CacheDebug,
				GuardrailDebug:          cr.ExtraFields.GuardrailDebug,
				ProviderResponseHeaders: cr.ExtraFields.ProviderResponseHeaders,
			},
		}
	}

	return &BifrostTextCompletionResponse{
		ID:                cr.ID,
		Model:             cr.Model,
		Created:           cr.Created,
		Object:            "text_completion",
		SystemFingerprint: cr.SystemFingerprint,
		Usage:             cr.Usage,
		ExtraFields: BifrostResponseExtraFields{
			RequestType:             TextCompletionRequest,
			ChunkIndex:              cr.ExtraFields.ChunkIndex,
			Provider:                cr.ExtraFields.Provider,
			OriginalModelRequested:  cr.ExtraFields.OriginalModelRequested,
			Latency:                 cr.ExtraFields.Latency,
			RawResponse:             cr.ExtraFields.RawResponse,
			CacheDebug:              cr.ExtraFields.CacheDebug,
			GuardrailDebug:          cr.ExtraFields.GuardrailDebug,
			ProviderResponseHeaders: cr.ExtraFields.ProviderResponseHeaders,
		},
	}
}
