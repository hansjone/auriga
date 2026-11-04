// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"encoding/base64"

	"maps"
	"net/http"

	"strings"

	"github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

func (response *GenerateContentResponse) ToBifrostImageGenerationResponse() (*schemas.BifrostImageGenerationResponse, *schemas.BifrostError) {
	bifrostResp := &schemas.BifrostImageGenerationResponse{
		ID:    response.ResponseID,
		Model: response.ModelVersion,
		Data:  []schemas.ImageData{},
	}

	if len(response.Candidates) > 0 {
		candidate := response.Candidates[0]
		if candidate.Content != nil && len(candidate.Content.Parts) > 0 {
			var imageData []schemas.ImageData
			var imageMetadata []schemas.ImageGenerationResponseParameters

			for idx, part := range candidate.Content.Parts {

				if part != nil && part.InlineData != nil {
					imageData = append(imageData, schemas.ImageData{
						B64JSON: part.InlineData.Data,
						Index:   idx,
					})

					outputFormat := convertMimeTypeToExtension(part.InlineData.MIMEType)
					imageMetadata = append(imageMetadata, schemas.ImageGenerationResponseParameters{
						OutputFormat: outputFormat,
					})
				}
			}

			bifrostResp.Usage = convertGeminiUsageMetadataToImageUsage(response.UsageMetadata)

			if len(imageData) > 0 {
				bifrostResp.Data = imageData

				if len(imageMetadata) > 0 {
					bifrostResp.ImageGenerationResponseParameters = &imageMetadata[0]
				}
			}
		} else {
			return nil, &schemas.BifrostError{
				IsBifrostError: false,
				Error: &schemas.ErrorField{
					Message: candidate.FinishMessage,
					Code:    schemas.Ptr(string(candidate.FinishReason)),
				},
			}
		}
	} else {
		return nil, &schemas.BifrostError{
			IsBifrostError: false,
			Error: &schemas.ErrorField{
				Message: "No candidates found in response",
			},
		}
	}

	return bifrostResp, nil
}

func ToGeminiImageGenerationRequest(bifrostReq *schemas.BifrostImageGenerationRequest) *GeminiGenerationRequest {
	if bifrostReq == nil {
		return nil
	}

	bifrostReq.Model = NormalizeModelName(bifrostReq.Model)

	geminiReq := &GeminiGenerationRequest{
		Model: bifrostReq.Model,
	}

	geminiReq.GenerationConfig.ResponseModalities = []Modality{ModalityImage}

	if bifrostReq.Params != nil {

		geminiReq.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)

		imageConfig := &GeminiImageConfig{}
		if bifrostReq.Params.Size != nil && strings.ToLower(*bifrostReq.Params.Size) != "auto" {
			aspectRatio, imageSize := utils.ConvertSizeToAspectRatioAndResolution(*bifrostReq.Params.Size)
			imageConfig.AspectRatio = aspectRatio
			imageConfig.ImageSize = imageSize
		}
		if bifrostReq.Params.AspectRatio != nil && *bifrostReq.Params.AspectRatio != "" {
			imageConfig.AspectRatio = *bifrostReq.Params.AspectRatio
		}
		if imageConfig.AspectRatio != "" || imageConfig.ImageSize != "" {
			geminiReq.GenerationConfig.ImageConfig = imageConfig
		}

		if bifrostReq.Params.ExtraParams != nil {

			if safetySettings, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "safetySettings"); ok {
				delete(geminiReq.ExtraParams, "safetySettings")
				if settings, ok := SafeExtractSafetySettings(safetySettings); ok {
					geminiReq.SafetySettings = settings
				}
			} else if safetySettings, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "safety_settings"); ok {
				delete(geminiReq.ExtraParams, "safety_settings")
				if settings, ok := SafeExtractSafetySettings(safetySettings); ok {
					geminiReq.SafetySettings = settings
				}
			}

			if cachedContent, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["cachedContent"]); ok {
				delete(geminiReq.ExtraParams, "cachedContent")
				geminiReq.CachedContent = cachedContent
			} else if cachedContent, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["cached_content"]); ok {
				delete(geminiReq.ExtraParams, "cached_content")
				geminiReq.CachedContent = cachedContent
			}

			if labels, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "labels"); ok {
				switch m := labels.(type) {
				case map[string]string:
					delete(geminiReq.ExtraParams, "labels")
					geminiReq.Labels = m
				case map[string]interface{}:
					out := make(map[string]string, len(m))
					for k, v := range m {
						if s, ok := schemas.SafeExtractString(v); ok {
							out[k] = s
						}
					}
					if len(out) > 0 {
						delete(geminiReq.ExtraParams, "labels")
						geminiReq.Labels = out
					}
				}
			}
		}
	}

	if bifrostReq.Input == nil {
		return nil
	}

	parts := []*Part{
		{
			Text: bifrostReq.Input.Prompt,
		},
	}

	geminiReq.Contents = []Content{
		{
			Role:  RoleUser,
			Parts: parts,
		},
	}

	return geminiReq
}

func ToImagenImageGenerationRequest(bifrostReq *schemas.BifrostImageGenerationRequest) *GeminiImagenRequest {
	if bifrostReq == nil || bifrostReq.Input == nil {
		return nil
	}

	bifrostReq.Model = NormalizeModelName(bifrostReq.Model)

	prompt := bifrostReq.Input.Prompt
	instances := []ImagenInstance{
		{
			Prompt: prompt,
		},
	}

	req := &GeminiImagenRequest{
		Instances:  instances,
		Parameters: GeminiImagenParameters{},
	}

	if bifrostReq.Params != nil {
		if bifrostReq.Params.N != nil {
			req.Parameters.SampleCount = bifrostReq.Params.N
		}

		if bifrostReq.Params.Size != nil && strings.ToLower(*bifrostReq.Params.Size) != "auto" {
			aspectRatio, imageSize := utils.ConvertSizeToAspectRatioAndResolution(*bifrostReq.Params.Size)
			if imageSize != "" {
				req.Parameters.SampleImageSize = &imageSize
			}
			if aspectRatio != "" {
				req.Parameters.AspectRatio = &aspectRatio
			}
		}

		if bifrostReq.Params.AspectRatio != nil && *bifrostReq.Params.AspectRatio != "" {
			req.Parameters.AspectRatio = bifrostReq.Params.AspectRatio
		}

		outputFormat := ""
		if bifrostReq.Params.OutputFormat != nil {
			outputFormat = *bifrostReq.Params.OutputFormat
		}

		if outputFormat != "" {
			mimeType := convertOutputFormatToMimeType(outputFormat)
			if mimeType != "" {
				if req.Parameters.OutputOptions == nil {
					req.Parameters.OutputOptions = &ImagenOutputOptions{}
				}
				req.Parameters.OutputOptions.MimeType = &mimeType
			}
		}

		if bifrostReq.Params.Seed != nil {
			req.Parameters.Seed = bifrostReq.Params.Seed
		}
		if bifrostReq.Params.NegativePrompt != nil {
			req.Parameters.NegativePrompt = bifrostReq.Params.NegativePrompt
		}

		if bifrostReq.Params.ExtraParams != nil {
			req.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)
			if addWatermark, ok := schemas.SafeExtractBoolPointer(bifrostReq.Params.ExtraParams["addWatermark"]); ok {
				delete(req.ExtraParams, "addWatermark")
				req.Parameters.AddWatermark = addWatermark
			}
			if sampleImageSize, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["sampleImageSize"]); ok {
				delete(req.ExtraParams, "sampleImageSize")
				req.Parameters.SampleImageSize = &sampleImageSize
			}

			if aspectRatio, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["aspectRatio"]); ok {
				delete(req.ExtraParams, "aspectRatio")
				req.Parameters.AspectRatio = &aspectRatio
			}

			if personGeneration, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["personGeneration"]); ok {
				delete(req.ExtraParams, "personGeneration")
				req.Parameters.PersonGeneration = &personGeneration
			}

			if language, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["language"]); ok {
				delete(req.ExtraParams, "language")
				req.Parameters.Language = &language
			}

			if enhancePrompt, ok := schemas.SafeExtractBoolPointer(bifrostReq.Params.ExtraParams["enhancePrompt"]); ok {
				delete(req.ExtraParams, "enhancePrompt")
				req.Parameters.EnhancePrompt = enhancePrompt
			}

			if safetySettings, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "safetySettings"); ok {
				if settings, ok := SafeExtractSafetySettings(safetySettings); ok {
					delete(req.ExtraParams, "safetySettings")
					req.Parameters.SafetySettings = settings
				}
			}
		}
	}

	return req
}

func convertMimeTypeToExtension(mimeType string) string {
	if mimeType == "" {
		return ""
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if semi := strings.Index(mimeType, ";"); semi != -1 {
		mimeType = strings.TrimSpace(mimeType[:semi])
	}
	switch mimeType {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	default:

		if idx := strings.Index(mimeType, "/"); idx != -1 && idx+1 < len(mimeType) {
			return mimeType[idx+1:]
		}
		return ""
	}
}

func convertOutputFormatToMimeType(outputFormat string) string {
	format := strings.ToLower(strings.TrimSpace(outputFormat))
	switch format {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}

func (response *GeminiImagenResponse) ToBifrostImageGenerationResponse() *schemas.BifrostImageGenerationResponse {
	if response == nil {
		return nil
	}

	bifrostResp := &schemas.BifrostImageGenerationResponse{
		Data: make([]schemas.ImageData, len(response.Predictions)),
	}

	for i, prediction := range response.Predictions {
		bifrostResp.Data[i] = schemas.ImageData{
			B64JSON: prediction.BytesBase64Encoded,
			Index:   i,
		}

		if prediction.MimeType != "" && i == 0 {

			outputFormat := convertMimeTypeToExtension(prediction.MimeType)
			bifrostResp.ImageGenerationResponseParameters = &schemas.ImageGenerationResponseParameters{
				OutputFormat: outputFormat,
			}
		}
	}

	return bifrostResp
}

func ToGeminiImageEditRequest(bifrostReq *schemas.BifrostImageEditRequest) *GeminiGenerationRequest {
	if bifrostReq == nil || bifrostReq.Input == nil || len(bifrostReq.Input.Images) == 0 {
		return nil
	}

	bifrostReq.Model = NormalizeModelName(bifrostReq.Model)

	geminiReq := &GeminiGenerationRequest{
		Model: bifrostReq.Model,
	}

	geminiReq.GenerationConfig.ResponseModalities = []Modality{ModalityImage}

	if bifrostReq.Params != nil {
		geminiReq.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)

		imageConfig := &GeminiImageConfig{}
		if bifrostReq.Params.Size != nil && strings.ToLower(*bifrostReq.Params.Size) != "auto" {
			aspectRatio, imageSize := utils.ConvertSizeToAspectRatioAndResolution(*bifrostReq.Params.Size)
			imageConfig.AspectRatio = aspectRatio
			imageConfig.ImageSize = imageSize
		}
		if bifrostReq.Params.AspectRatio != nil && *bifrostReq.Params.AspectRatio != "" {
			imageConfig.AspectRatio = *bifrostReq.Params.AspectRatio
		}
		if imageConfig.AspectRatio != "" || imageConfig.ImageSize != "" {
			geminiReq.GenerationConfig.ImageConfig = imageConfig
		}

		if bifrostReq.Params.ExtraParams != nil {

			if safetySettings, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "safetySettings"); ok {
				delete(geminiReq.ExtraParams, "safetySettings")
				if settings, ok := SafeExtractSafetySettings(safetySettings); ok {
					geminiReq.SafetySettings = settings
				}
			} else if safetySettings, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "safety_settings"); ok {
				delete(geminiReq.ExtraParams, "safety_settings")
				if settings, ok := SafeExtractSafetySettings(safetySettings); ok {
					geminiReq.SafetySettings = settings
				}
			}

			if cachedContent, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["cachedContent"]); ok {
				delete(geminiReq.ExtraParams, "cachedContent")
				geminiReq.CachedContent = cachedContent
			} else if cachedContent, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["cached_content"]); ok {
				delete(geminiReq.ExtraParams, "cached_content")
				geminiReq.CachedContent = cachedContent
			}

			if labels, ok := schemas.SafeExtractFromMap(bifrostReq.Params.ExtraParams, "labels"); ok {
				switch m := labels.(type) {
				case map[string]string:
					delete(geminiReq.ExtraParams, "labels")
					geminiReq.Labels = m
				case map[string]interface{}:
					out := make(map[string]string, len(m))
					for k, v := range m {
						if s, ok := schemas.SafeExtractString(v); ok {
							out[k] = s
						}
					}
					if len(out) > 0 {
						delete(geminiReq.ExtraParams, "labels")
						geminiReq.Labels = out
					}
				}
			}
		}
	}

	if bifrostReq.Input == nil {
		return nil
	}

	parts := []*Part{
		{
			Text: bifrostReq.Input.Prompt,
		},
	}

	for _, image := range bifrostReq.Input.Images {
		if len(image.Image) == 0 {
			continue
		}

		mimeType := http.DetectContentType(image.Image)

		if mimeType == "application/octet-stream" || mimeType == "" {
			mimeType = "image/png"
		}

		parts = append(parts, &Part{
			InlineData: &Blob{
				MIMEType: mimeType,
				Data:     encodeBytesToBase64String(image.Image),
			},
		})
	}

	geminiReq.Contents = []Content{
		{
			Role:  RoleUser,
			Parts: parts,
		},
	}

	return geminiReq
}

func extractIntArray(v interface{}) []int {
	if v == nil {
		return nil
	}

	if arr, ok := v.([]interface{}); ok {
		result := make([]int, 0, len(arr))
		for _, item := range arr {
			switch val := item.(type) {
			case int:
				result = append(result, val)
			case int64:
				result = append(result, int(val))
			case float64:
				result = append(result, int(val))
			}
		}
		return result
	}

	if arr, ok := v.([]int); ok {
		return arr
	}

	if arr, ok := v.([]int64); ok {
		result := make([]int, len(arr))
		for i, val := range arr {
			result[i] = int(val)
		}
		return result
	}

	return nil
}

func mapTypeToImagenEditMode(editType string) string {
	switch strings.ToLower(editType) {
	case "inpainting":
		return "EDIT_MODE_INPAINT_INSERTION"
	case "outpainting":
		return "EDIT_MODE_OUTPAINT"
	case "inpaint_removal":
		return "EDIT_MODE_INPAINT_REMOVAL"
	case "bgswap":
		return "EDIT_MODE_BGSWAP"
	default:
		return ""
	}
}

func ToImagenImageEditRequest(bifrostReq *schemas.BifrostImageEditRequest) *GeminiImagenRequest {
	if bifrostReq == nil || bifrostReq.Input == nil || len(bifrostReq.Input.Images) == 0 {
		return nil
	}

	bifrostReq.Model = NormalizeModelName(bifrostReq.Model)

	req := &GeminiImagenRequest{
		Parameters: GeminiImagenParameters{},
	}

	var refImages []ImagenReferenceImage
	refID := 1

	for _, img := range bifrostReq.Input.Images {
		refImages = append(refImages, ImagenReferenceImage{
			ReferenceType: "REFERENCE_TYPE_RAW",
			ReferenceID:   refID,
			ReferenceImage: ImagenReferenceData{
				BytesBase64Encoded: base64.StdEncoding.EncodeToString(img.Image),
			},
		})
		refID++
	}

	if bifrostReq.Params != nil {
		var maskMode string
		var hasMaskData bool
		var dilation *float64
		var maskClasses []int
		req.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)

		if len(bifrostReq.Params.Mask) > 0 {
			hasMaskData = true
			maskMode = "MASK_MODE_USER_PROVIDED"
		}

		if bifrostReq.Params.ExtraParams != nil {

			if v, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["maskMode"]); ok {
				delete(req.ExtraParams, "maskMode")
				maskMode = v
			}

			if v, ok := schemas.SafeExtractFloat64Pointer(bifrostReq.Params.ExtraParams["dilation"]); ok {

				if *v >= 0 && *v <= 1 {
					delete(req.ExtraParams, "dilation")
					dilation = v
				}
			}

			if v, ok := bifrostReq.Params.ExtraParams["maskClasses"]; ok {
				delete(req.ExtraParams, "maskClasses")
				maskClasses = extractIntArray(v)
			}
		}

		if hasMaskData || maskMode != "" {
			maskRef := ImagenReferenceImage{
				ReferenceType: "REFERENCE_TYPE_MASK",
				ReferenceID:   refID,
				MaskImageConfig: &ImagenMaskImageConfig{
					MaskMode:    maskMode,
					Dilation:    dilation,
					MaskClasses: maskClasses,
				},
			}

			if hasMaskData {
				maskRef.ReferenceImage = ImagenReferenceData{
					BytesBase64Encoded: base64.StdEncoding.EncodeToString(bifrostReq.Params.Mask),
				}
			}

			refImages = append(refImages, maskRef)
		}
	}

	req.Instances = append(req.Instances, ImagenInstance{
		ReferenceImages: refImages,
		Prompt:          bifrostReq.Input.Prompt,
	})

	if bifrostReq.Params != nil {
		if bifrostReq.Params.N != nil {
			req.Parameters.SampleCount = bifrostReq.Params.N
		}

		if bifrostReq.Params.Size != nil && strings.ToLower(*bifrostReq.Params.Size) != "auto" {
			aspectRatio, imageSize := utils.ConvertSizeToAspectRatioAndResolution(*bifrostReq.Params.Size)
			if imageSize != "" {
				req.Parameters.SampleImageSize = &imageSize
			}
			if aspectRatio != "" {
				req.Parameters.AspectRatio = &aspectRatio
			}
		}

		if bifrostReq.Params.OutputFormat != nil {
			mimeType := convertOutputFormatToMimeType(*bifrostReq.Params.OutputFormat)
			if mimeType != "" {
				req.Parameters.OutputOptions = &ImagenOutputOptions{MimeType: &mimeType}
			}
		}
		if bifrostReq.Params.OutputCompression != nil {
			if req.Parameters.OutputOptions == nil {
				req.Parameters.OutputOptions = &ImagenOutputOptions{}
			}
			req.Parameters.OutputOptions.CompressionQuality = bifrostReq.Params.OutputCompression
		}

		if bifrostReq.Params.Type != nil {
			editMode := mapTypeToImagenEditMode(*bifrostReq.Params.Type)
			if editMode != "" {
				req.Parameters.EditMode = &editMode
			}
		}

		if bifrostReq.Params.NegativePrompt != nil {
			req.Parameters.NegativePrompt = bifrostReq.Params.NegativePrompt
		}

		if bifrostReq.Params.Seed != nil {
			req.Parameters.Seed = bifrostReq.Params.Seed
		}

		if bifrostReq.Params.ExtraParams != nil {

			if bifrostReq.Params.Type == nil {
				if v, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["editMode"]); ok {
					delete(req.ExtraParams, "editMode")
					req.Parameters.EditMode = &v
				}
			}
			if v, ok := schemas.SafeExtractIntPointer(bifrostReq.Params.ExtraParams["guidanceScale"]); ok {
				delete(req.ExtraParams, "guidanceScale")
				req.Parameters.GuidanceScale = v
			}
			if v, ok := schemas.SafeExtractIntPointer(bifrostReq.Params.ExtraParams["baseSteps"]); ok {
				delete(req.ExtraParams, "baseSteps")
				req.Parameters.BaseSteps = v
			}
			if v, ok := schemas.SafeExtractBoolPointer(bifrostReq.Params.ExtraParams["addWatermark"]); ok {
				delete(req.ExtraParams, "addWatermark")
				req.Parameters.AddWatermark = v
			}
			if v, ok := schemas.SafeExtractBoolPointer(bifrostReq.Params.ExtraParams["includeRaiReason"]); ok {
				delete(req.ExtraParams, "includeRaiReason")
				req.Parameters.IncludeRaiReason = v
			}
			if v, ok := schemas.SafeExtractBoolPointer(bifrostReq.Params.ExtraParams["includeSafetyAttributes"]); ok {
				delete(req.ExtraParams, "includeSafetyAttributes")
				req.Parameters.IncludeSafetyAttributes = v
			}
			if v, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["personGeneration"]); ok {
				delete(req.ExtraParams, "personGeneration")
				req.Parameters.PersonGeneration = &v
			}
			if v, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["language"]); ok {
				delete(req.ExtraParams, "language")
				req.Parameters.Language = &v
			}
			if v, ok := schemas.SafeExtractString(bifrostReq.Params.ExtraParams["storageUri"]); ok {
				delete(req.ExtraParams, "storageUri")
				req.Parameters.StorageUri = &v
			}
			if v, ok := SafeExtractSafetySettings(bifrostReq.Params.ExtraParams["safetySettings"]); ok {
				delete(req.ExtraParams, "safetySettings")
				req.Parameters.SafetySettings = v
			}
		}
	}

	return req
}
