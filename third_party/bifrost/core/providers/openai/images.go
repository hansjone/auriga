// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"strconv"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func ToOpenAIImageGenerationRequest(bifrostReq *schemas.BifrostImageGenerationRequest) *OpenAIImageGenerationRequest {
	if bifrostReq == nil || bifrostReq.Input == nil || bifrostReq.Input.Prompt == "" {
		return nil
	}

	req := &OpenAIImageGenerationRequest{
		Model:  bifrostReq.Model,
		Prompt: bifrostReq.Input.Prompt,
	}

	if bifrostReq.Params != nil {
		req.ImageGenerationParameters = *bifrostReq.Params
	}

	switch bifrostReq.Provider {
	case schemas.XAI:
		filterXAISpecificParameters(req)
	case schemas.OpenAI, schemas.Azure:
		filterOpenAISpecificParameters(req)
	}
	if bifrostReq.Params != nil {
		req.ExtraParams = bifrostReq.Params.ExtraParams
	}
	return req
}

func filterXAISpecificParameters(req *OpenAIImageGenerationRequest) {
	req.ImageGenerationParameters.Quality = nil
	req.ImageGenerationParameters.Style = nil
	req.ImageGenerationParameters.Size = nil
	req.ImageGenerationParameters.OutputCompression = nil
}

func filterOpenAISpecificParameters(req *OpenAIImageGenerationRequest) {
	req.ImageGenerationParameters.Seed = nil
	req.NumInferenceSteps = nil
	req.NegativePrompt = nil
}

func ToOpenAIImageEditRequest(bifrostReq *schemas.BifrostImageEditRequest) *OpenAIImageEditRequest {
	if bifrostReq == nil || bifrostReq.Input == nil || bifrostReq.Input.Images == nil || bifrostReq.Input.Prompt == "" {
		return nil
	}

	req := &OpenAIImageEditRequest{
		Model: bifrostReq.Model,
		Input: bifrostReq.Input,
	}

	if bifrostReq.Params != nil {
		req.ImageEditParameters = *bifrostReq.Params
	}

	if bifrostReq.Params != nil {
		req.ExtraParams = bifrostReq.Params.ExtraParams
	}

	return req
}

func parseImageEditFormDataBodyFromRequest(writer *multipart.Writer, openaiReq *OpenAIImageEditRequest, providerName schemas.ModelProvider) *schemas.BifrostError {

	if err := writer.WriteField("model", openaiReq.Model); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write model field", err)
	}

	if err := writer.WriteField("prompt", openaiReq.Input.Prompt); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write prompt field", err)
	}

	if openaiReq.Stream != nil && *openaiReq.Stream {
		if err := writer.WriteField("stream", "true"); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write stream field", err)
		}
	}

	if openaiReq.N != nil {
		if err := writer.WriteField("n", strconv.Itoa(*openaiReq.N)); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write n field", err)
		}
	}

	if openaiReq.Size != nil {
		if err := writer.WriteField("size", *openaiReq.Size); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write size field", err)
		}
	}

	if openaiReq.ResponseFormat != nil {
		if err := writer.WriteField("response_format", *openaiReq.ResponseFormat); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write response_format field", err)
		}
	}

	if openaiReq.Quality != nil {
		if err := writer.WriteField("quality", *openaiReq.Quality); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write quality field", err)
		}
	}

	if openaiReq.Background != nil {
		if err := writer.WriteField("background", *openaiReq.Background); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write background field", err)
		}
	}

	if openaiReq.InputFidelity != nil {
		if err := writer.WriteField("input_fidelity", *openaiReq.InputFidelity); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write input_fidelity field", err)
		}
	}

	if openaiReq.PartialImages != nil {
		if err := writer.WriteField("partial_images", strconv.Itoa(*openaiReq.PartialImages)); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write partial_images field", err)
		}
	}

	if openaiReq.OutputFormat != nil {
		if err := writer.WriteField("output_format", *openaiReq.OutputFormat); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write output_format field", err)
		}
	}

	if openaiReq.OutputCompression != nil {
		if err := writer.WriteField("output_compression", strconv.Itoa(*openaiReq.OutputCompression)); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write output_compression field", err)
		}
	}

	if openaiReq.User != nil {
		if err := writer.WriteField("user", *openaiReq.User); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write user field", err)
		}
	}

	for i, imageInput := range openaiReq.Input.Images {
		fieldName := "image[]"

		mimeType := http.DetectContentType(imageInput.Image)

		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = "image/png"
		}

		var filename string
		switch mimeType {
		case "image/jpeg":
			filename = fmt.Sprintf("image%d.jpg", i)
		case "image/webp":
			filename = fmt.Sprintf("image%d.webp", i)
		default:
			filename = fmt.Sprintf("image%d.png", i)
		}

		part, err := writer.CreatePart(map[string][]string{
			"Content-Disposition": {fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, filename)},
			"Content-Type":        {mimeType},
		})
		if err != nil {
			return providerUtils.NewBifrostOperationError(fmt.Sprintf("failed to create form part for image %d", i), err)
		}
		if _, err := part.Write(imageInput.Image); err != nil {
			return providerUtils.NewBifrostOperationError(fmt.Sprintf("failed to write image %d data", i), err)
		}
	}

	if len(openaiReq.Mask) > 0 {

		maskMimeType := http.DetectContentType(openaiReq.Mask)
		if maskMimeType != "image/png" && maskMimeType != "image/jpeg" && maskMimeType != "image/webp" {
			maskMimeType = "image/png"
		}

		var maskFilename string
		switch maskMimeType {
		case "image/jpeg":
			maskFilename = "mask.jpg"
		case "image/webp":
			maskFilename = "mask.webp"
		default:
			maskFilename = "mask.png"
		}

		maskPart, err := writer.CreatePart(map[string][]string{
			"Content-Disposition": {`form-data; name="mask"; filename="` + maskFilename + `"`},
			"Content-Type":        {maskMimeType},
		})
		if err != nil {
			return providerUtils.NewBifrostOperationError("failed to create mask form part", err)
		}
		if _, err := maskPart.Write(openaiReq.Mask); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write mask data", err)
		}
	}

	if err := writer.Close(); err != nil {
		return providerUtils.NewBifrostOperationError("failed to close multipart writer", err)
	}

	return nil
}

func ToOpenAIImageVariationRequest(bifrostReq *schemas.BifrostImageVariationRequest) *OpenAIImageVariationRequest {
	if bifrostReq == nil || bifrostReq.Input == nil || bifrostReq.Input.Image.Image == nil || len(bifrostReq.Input.Image.Image) == 0 {
		return nil
	}

	req := &OpenAIImageVariationRequest{
		Model: bifrostReq.Model,
		Input: bifrostReq.Input,
	}

	if bifrostReq.Params != nil {
		req.ImageVariationParameters = *bifrostReq.Params
	}

	if bifrostReq.Params != nil {
		req.ExtraParams = bifrostReq.Params.ExtraParams
	}

	return req
}

func parseImageVariationFormDataBodyFromRequest(writer *multipart.Writer, openaiReq *OpenAIImageVariationRequest, providerName schemas.ModelProvider) *schemas.BifrostError {

	if err := writer.WriteField("model", openaiReq.Model); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write model field", err)
	}

	if openaiReq.Input == nil || openaiReq.Input.Image.Image == nil || len(openaiReq.Input.Image.Image) == 0 {
		return providerUtils.NewBifrostOperationError("image is required", nil)
	}

	if openaiReq.N != nil {
		if err := writer.WriteField("n", strconv.Itoa(*openaiReq.N)); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write n field", err)
		}
	}

	if openaiReq.ResponseFormat != nil {
		if err := writer.WriteField("response_format", *openaiReq.ResponseFormat); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write response_format field", err)
		}
	}

	if openaiReq.Size != nil {
		if err := writer.WriteField("size", *openaiReq.Size); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write size field", err)
		}
	}

	if openaiReq.User != nil {
		if err := writer.WriteField("user", *openaiReq.User); err != nil {
			return providerUtils.NewBifrostOperationError("failed to write user field", err)
		}
	}

	mimeType := http.DetectContentType(openaiReq.Input.Image.Image)

	if mimeType == "application/octet-stream" || mimeType == "" {
		mimeType = "image/png"
	}

	filename := "image"
	part, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {fmt.Sprintf(`form-data; name="image"; filename="%s"`, filename)},
		"Content-Type":        {mimeType},
	})
	if err != nil {
		return providerUtils.NewBifrostOperationError("failed to create image part", err)
	}

	if _, err := part.Write(openaiReq.Input.Image.Image); err != nil {
		return providerUtils.NewBifrostOperationError("failed to write image data", err)
	}

	if err := writer.Close(); err != nil {
		return providerUtils.NewBifrostOperationError("failed to close multipart writer", err)
	}

	return nil
}
