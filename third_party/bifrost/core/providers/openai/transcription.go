package openai

import (
	"fmt"
	"mime/multipart"
	"sort"

	"github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func ToOpenAITranscriptionRequest(bifrostReq *schemas.BifrostTranscriptionRequest) *OpenAITranscriptionRequest {
	if bifrostReq == nil || bifrostReq.Input.File == nil {
		return nil
	}

	transcriptionInput := bifrostReq.Input
	params := bifrostReq.Params

	openaiReq := &OpenAITranscriptionRequest{
		Model:    bifrostReq.Model,
		File:     transcriptionInput.File,
		Filename: transcriptionInput.Filename,
	}

	if params != nil {
		openaiReq.TranscriptionParameters = *params
		openaiReq.ExtraParams = params.ExtraParams
	}

	return openaiReq
}

func ParseTranscriptionFormDataBodyFromRequest(writer *multipart.Writer, openaiReq *OpenAITranscriptionRequest, providerName schemas.ModelProvider) *schemas.BifrostError {

	if err := writer.WriteField("model", openaiReq.Model); err != nil {
		return utils.NewBifrostOperationError("failed to write model field", err)
	}

	if openaiReq.Language != nil {
		if err := writer.WriteField("language", *openaiReq.Language); err != nil {
			return utils.NewBifrostOperationError("failed to write language field", err)
		}
	}

	if openaiReq.Prompt != nil {
		if err := writer.WriteField("prompt", *openaiReq.Prompt); err != nil {
			return utils.NewBifrostOperationError("failed to write prompt field", err)
		}
	}

	if openaiReq.ResponseFormat != nil {
		if err := writer.WriteField("response_format", *openaiReq.ResponseFormat); err != nil {
			return utils.NewBifrostOperationError("failed to write response_format field", err)
		}
	}

	if openaiReq.Temperature != nil {
		if err := writer.WriteField("temperature", fmt.Sprintf("%g", *openaiReq.Temperature)); err != nil {
			return utils.NewBifrostOperationError("failed to write temperature field", err)
		}
	}

	for _, granularity := range openaiReq.TimestampGranularities {
		if err := writer.WriteField("timestamp_granularities[]", granularity); err != nil {
			return utils.NewBifrostOperationError("failed to write timestamp_granularities field", err)
		}
	}

	for _, include := range openaiReq.Include {
		if err := writer.WriteField("include[]", include); err != nil {
			return utils.NewBifrostOperationError("failed to write include field", err)
		}
	}

	if openaiReq.Stream != nil && *openaiReq.Stream {
		if err := writer.WriteField("stream", "true"); err != nil {
			return utils.NewBifrostOperationError("failed to write stream field", err)
		}
	}

	if len(openaiReq.ExtraParams) > 0 {
		extraKeys := make([]string, 0, len(openaiReq.ExtraParams))
		for key := range openaiReq.ExtraParams {
			extraKeys = append(extraKeys, key)
		}
		sort.Strings(extraKeys)
		for _, key := range extraKeys {
			value := openaiReq.ExtraParams[key]
			var fieldValue string
			switch v := value.(type) {
			case string:
				fieldValue = v
			default:
				encoded, err := schemas.MarshalSorted(v)
				if err != nil {
					return utils.NewBifrostOperationError(fmt.Sprintf("failed to encode %s field", key), err)
				}
				fieldValue = string(encoded)
			}
			if err := writer.WriteField(key, fieldValue); err != nil {
				return utils.NewBifrostOperationError(fmt.Sprintf("failed to write %s field", key), err)
			}
		}
	}

	filename := openaiReq.Filename
	if filename == "" {
		filename = utils.AudioFilenameFromBytes(openaiReq.File)
	}
	fileWriter, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return utils.NewBifrostOperationError("failed to create form file", err)
	}
	if _, err := fileWriter.Write(openaiReq.File); err != nil {
		return utils.NewBifrostOperationError("failed to write file data", err)
	}

	if err := writer.Close(); err != nil {
		return utils.NewBifrostOperationError("failed to close multipart writer", err)
	}

	return nil
}
