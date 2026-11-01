package deepseek

import "github.com/maximhq/bifrost/core/schemas"

const unsupportedDocumentCode = "deepseek_unsupported_document_content"

func rejectUnsupportedChatContent(request *schemas.BifrostChatRequest) *schemas.BifrostError {
	if request == nil {
		return nil
	}
	for i := range request.Input {
		content := request.Input[i].Content
		if content == nil {
			continue
		}
		for j := range content.ContentBlocks {
			if content.ContentBlocks[j].Type == schemas.ChatContentBlockTypeFile {
				return newUnsupportedDocumentError()
			}
		}
	}
	return nil
}

func rejectUnsupportedResponsesContent(request *schemas.BifrostResponsesRequest) *schemas.BifrostError {
	if request == nil {
		return nil
	}
	for i := range request.Input {
		message := &request.Input[i]
		if message.Content != nil && hasResponsesFileBlock(message.Content.ContentBlocks) {
			return newUnsupportedDocumentError()
		}
		if message.ResponsesToolMessage == nil {
			continue
		}
		if message.ResponsesToolMessage.Output != nil &&
			hasResponsesFileBlock(message.ResponsesToolMessage.Output.ResponsesFunctionToolCallOutputBlocks) {
			return newUnsupportedDocumentError()
		}

		if wf := message.ResponsesToolMessage.ResponsesWebFetchCall; wf != nil && wf.Document != nil &&
			emittedWebFetchDocumentBlockType(wf.Document) == webFetchDocumentBlockType {
			return newUnsupportedDocumentError()
		}
	}
	return nil
}

const webFetchDocumentBlockType = "document"

func emittedWebFetchDocumentBlockType(doc *schemas.ResponsesWebFetchDocument) string {
	if doc == nil || doc.Type == "" {
		return webFetchDocumentBlockType
	}
	return doc.Type
}

func hasResponsesFileBlock(blocks []schemas.ResponsesMessageContentBlock) bool {
	for i := range blocks {
		if blocks[i].Type == schemas.ResponsesInputMessageContentBlockTypeFile {
			return true
		}
	}
	return false
}

func newUnsupportedDocumentError() *schemas.BifrostError {
	statusCode := 400
	errorType := "invalid_request_error"
	allowFallbacks := true
	return &schemas.BifrostError{
		IsBifrostError: true,
		StatusCode:     &statusCode,
		AllowFallbacks: &allowFallbacks,
		Error: &schemas.ErrorField{
			Type:    &errorType,
			Code:    schemas.Ptr(unsupportedDocumentCode),
			Message: "deepseek's anthropic-compatible api does not support document content blocks; the request was not sent upstream",
		},
	}
}
