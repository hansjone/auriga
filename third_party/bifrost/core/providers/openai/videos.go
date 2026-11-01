// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

var validOpenAIVideoEditMimeTypes = map[string]bool{
	"video/mp4":       true,
	"video/webm":      true,
	"video/quicktime": true,
}
var openAIVideoEditExtensions = map[string]string{
	"video/mp4":       ".mp4",
	"video/webm":      ".webm",
	"video/quicktime": ".mov",
}

func ToOpenAIVideoGenerationRequest(bifrostReq *schemas.BifrostVideoGenerationRequest) (*OpenAIVideoGenerationRequest, error) {
	if bifrostReq == nil || bifrostReq.Input == nil || bifrostReq.Input.Prompt == "" {
		return nil, fmt.Errorf("bifrost request, input, or prompt is nil/empty")
	}

	req := &OpenAIVideoGenerationRequest{
		Model:  bifrostReq.Model,
		Prompt: bifrostReq.Input.Prompt,
	}

	if bifrostReq.Input.InputReference != nil {

		sanitizedURL, err := schemas.SanitizeImageURL(*bifrostReq.Input.InputReference)
		if err != nil {
			return nil, fmt.Errorf("invalid input reference: %w", err)
		}
		urlInfo := schemas.ExtractURLTypeInfo(sanitizedURL)
		if urlInfo.DataURLWithoutPrefix != nil {
			bytes, err := base64.StdEncoding.DecodeString(*urlInfo.DataURLWithoutPrefix)
			if err != nil {
				return nil, fmt.Errorf("failed to decode base64 input reference: %w", err)
			}
			req.InputReference = bytes
		} else {
			return nil, fmt.Errorf("input_reference must be a base64 data URL (e.g. data:image/png;base64,...)")
		}
	}

	if bifrostReq.Params != nil {
		if bifrostReq.Params.Seconds != nil {
			req.Seconds = bifrostReq.Params.Seconds
		}

		if bifrostReq.Params.Size != "" {

			if ValidOpenAIVideoSizes[bifrostReq.Params.Size] {
				req.Size = bifrostReq.Params.Size
			} else {

				req.Size = string(DefaultOpenAIVideoSize)
			}
		} else {

			req.Size = string(DefaultOpenAIVideoSize)
		}

		req.ExtraParams = bifrostReq.Params.ExtraParams
	}

	return req, nil
}

func ToOpenAIVideoEditRequest(bifrostReq *schemas.BifrostVideoEditRequest) (*OpenAIVideoEditRequest, error) {
	if bifrostReq == nil || bifrostReq.Input == nil || bifrostReq.Input.Prompt == "" {
		return nil, fmt.Errorf("bifrost request, input, or prompt is nil/empty")
	}

	req := &OpenAIVideoEditRequest{
		Model:  bifrostReq.Model,
		Prompt: bifrostReq.Input.Prompt,
	}

	source := bifrostReq.Input.Video
	switch {
	case len(source.Video) > 0:
		req.Video.Bytes = source.Video
	case source.ID != "":
		req.Video.ID = providerUtils.StripVideoIDProviderSuffix(source.ID, schemas.OpenAI)
	case source.URL != "":
		return nil, fmt.Errorf("openai video edit accepts an uploaded video or a video id, not a url")
	default:
		return nil, fmt.Errorf("source video is required")
	}

	if bifrostReq.Params != nil {
		req.ExtraParams = bifrostReq.Params.ExtraParams
	}

	return req, nil
}

func parseVideoEditFormDataBodyFromRequest(writer *multipart.Writer, openaiReq *OpenAIVideoEditRequest) *schemas.BifrostError {
	if err := writer.WriteField("prompt", openaiReq.Prompt); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write prompt field", err)
	}

	if openaiReq.Model != "" {
		if err := writer.WriteField("model", openaiReq.Model); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write model field", err)
		}
	}

	mimeType := http.DetectContentType(openaiReq.Video.Bytes)
	if !validOpenAIVideoEditMimeTypes[mimeType] {
		mimeType = "video/mp4"
	}
	part, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="video"; filename="video%s"`, openAIVideoEditExtensions[mimeType])},
		"Content-Type":        {mimeType},
	})
	if err != nil {
		return providerUtils.NewBifrostOperationError("failed to create form part for video", err)
	}
	if _, err := part.Write(openaiReq.Video.Bytes); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write video file data", err)
	}

	if err := writer.Close(); err != nil {
		return providerUtils.NewBifrostOperationError("failed to close multipart writer", err)
	}

	return nil
}

func ToOpenAIVideoRemixRequest(bifrostReq *schemas.BifrostVideoRemixRequest) (*OpenAIVideoRemixRequest, error) {
	if bifrostReq == nil || bifrostReq.Input == nil || bifrostReq.Input.Prompt == "" {
		return nil, fmt.Errorf("bifrost request, input, or prompt is nil/empty")
	}

	req := &OpenAIVideoRemixRequest{
		Prompt: bifrostReq.Input.Prompt,
	}

	return req, nil
}

func parseVideoGenerationFormDataBodyFromRequest(writer *multipart.Writer, openaiReq *OpenAIVideoGenerationRequest, providerName schemas.ModelProvider) *schemas.BifrostError {

	if openaiReq.Prompt == "" {
		return providerUtils.NewBifrostOperationError("prompt is required", nil)
	}
	if err := writer.WriteField("prompt", openaiReq.Prompt); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write prompt field", err)
	}

	if openaiReq.Model != "" {
		if err := writer.WriteField("model", openaiReq.Model); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write model field", err)
		}
	}

	if openaiReq.Seconds != nil {
		if err := writer.WriteField("seconds", *openaiReq.Seconds); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write seconds field", err)
		}
	}

	if openaiReq.Size != "" {
		if err := writer.WriteField("size", openaiReq.Size); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write size field", err)
		}
	}

	if len(openaiReq.InputReference) > 0 {

		mimeType := http.DetectContentType(openaiReq.InputReference)

		validMimeTypes := map[string]bool{
			"image/jpeg": true,
			"image/png":  true,
			"image/webp": true,
			"video/mp4":  true,
		}

		if !validMimeTypes[mimeType] {

			mimeType = "image/png"
		}

		var filename string
		switch mimeType {
		case "image/jpeg":
			filename = "input_reference.jpg"
		case "image/webp":
			filename = "input_reference.webp"
		case "video/mp4":
			filename = "input_reference.mp4"
		default:
			filename = "input_reference.png"
		}

		part, err := writer.CreatePart(map[string][]string{
			"Content-Disposition": {fmt.Sprintf(`form-data; name="input_reference"; filename="%s"`, filename)},
			"Content-Type":        {mimeType},
		})
		if err != nil {
			return providerUtils.NewBifrostOperationError("failed to create form part for input_reference", err)
		}
		if _, err := part.Write(openaiReq.InputReference); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write input_reference file data", err)
		}
	}

	if err := writer.Close(); err != nil {
		return providerUtils.NewBifrostOperationError("failed to close multipart writer", err)
	}

	return nil
}
