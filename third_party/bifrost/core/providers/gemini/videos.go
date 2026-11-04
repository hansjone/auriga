// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"encoding/base64"

	"fmt"

	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

const defaultVideoContentType = "video/mp4"

func sizeToAspectRatio(size string) string {
	switch size {
	case "1280x720", "1792x1024":
		return "16:9"
	case "720x1280", "1024x1792":
		return "9:16"
	default:
		return "16:9"
	}
}

func videoImageDataFromExtraParam(value any) (*VideoImageData, bool) {
	if value == nil {
		return nil, false
	}

	switch v := value.(type) {
	case *VideoImageData:
		return v, v != nil
	case VideoImageData:
		return &v, true
	case map[string]any:
		image := &VideoImageData{}
		hasValue := false
		if bytesBase64Encoded, ok := schemas.SafeExtractStringPointer(v["bytesBase64Encoded"]); ok {
			image.BytesBase64Encoded = bytesBase64Encoded
			hasValue = true
		}
		if gcsURI, ok := schemas.SafeExtractStringPointer(v["gcsUri"]); ok {
			image.GCSURI = gcsURI
			hasValue = true
		}
		if mimeType, ok := schemas.SafeExtractStringPointer(v["mimeType"]); ok {
			image.MimeType = mimeType
			hasValue = true
		}
		if maskMode, ok := schemas.SafeExtractStringPointer(v["maskMode"]); ok {
			image.MaskMode = maskMode
			hasValue = true
		}
		if fileDataValue, exists := v["fileData"]; exists {
			if fileData, ok := fileDataFromExtraParam(fileDataValue); ok {
				image.FileData = fileData
				hasValue = true
			}
		}
		return image, hasValue
	default:
		if data, err := providerUtils.MarshalSorted(value); err == nil {
			var image VideoImageData
			if sonic.Unmarshal(data, &image) == nil && videoImageDataHasValue(&image) {
				return &image, true
			}
		}
	}
	return nil, false
}

func fileDataFromExtraParam(value any) (*FileData, bool) {
	if value == nil {
		return nil, false
	}

	switch v := value.(type) {
	case *FileData:
		return v, v != nil
	case FileData:
		return &v, true
	case map[string]any:
		fileData := &FileData{}
		hasValue := false
		if displayName, ok := schemas.SafeExtractString(v["displayName"]); ok {
			fileData.DisplayName = displayName
			hasValue = true
		}
		if fileURI, ok := schemas.SafeExtractString(v["fileUri"]); ok {
			fileData.FileURI = fileURI
			hasValue = true
		}
		if mimeType, ok := schemas.SafeExtractString(v["mimeType"]); ok {
			fileData.MIMEType = mimeType
			hasValue = true
		}
		return fileData, hasValue
	default:
		if data, err := providerUtils.MarshalSorted(value); err == nil {
			var fileData FileData
			if sonic.Unmarshal(data, &fileData) == nil && fileDataHasValue(&fileData) {
				return &fileData, true
			}
		}
	}
	return nil, false
}

func videoReferenceImagesFromExtraParam(value any) ([]VideoReferenceImage, bool) {
	switch v := value.(type) {
	case []VideoReferenceImage:
		return v, v != nil
	case []map[string]any:
		referenceImages := make([]VideoReferenceImage, 0, len(v))
		for _, item := range v {
			if referenceImage, ok := videoReferenceImageFromExtraParam(item); ok {
				referenceImages = append(referenceImages, referenceImage)
			}
		}
		return referenceImages, len(referenceImages) > 0
	case []any:
		referenceImages := make([]VideoReferenceImage, 0, len(v))
		for _, item := range v {
			if referenceImage, ok := videoReferenceImageFromExtraParam(item); ok {
				referenceImages = append(referenceImages, referenceImage)
			}
		}
		return referenceImages, len(referenceImages) > 0
	default:
		if data, err := providerUtils.MarshalSorted(value); err == nil {
			var referenceImages []VideoReferenceImage
			if sonic.Unmarshal(data, &referenceImages) == nil {
				return referenceImages, true
			}
		}
	}
	return nil, false
}

func videoReferenceImageFromExtraParam(value any) (VideoReferenceImage, bool) {
	switch v := value.(type) {
	case VideoReferenceImage:
		return v, true
	case map[string]any:
		referenceImage := VideoReferenceImage{}
		hasValue := false
		if imageValue, exists := v["image"]; exists {
			if image, ok := videoImageDataFromExtraParam(imageValue); ok {
				referenceImage.Image = *image
				hasValue = true
			}
		}
		if referenceType, ok := schemas.SafeExtractString(v["referenceType"]); ok {
			referenceImage.ReferenceType = referenceType
			hasValue = true
		}
		return referenceImage, hasValue
	default:
		if data, err := providerUtils.MarshalSorted(value); err == nil {
			var referenceImage VideoReferenceImage
			if sonic.Unmarshal(data, &referenceImage) == nil && videoReferenceImageHasValue(referenceImage) {
				return referenceImage, true
			}
		}
	}
	return VideoReferenceImage{}, false
}

func videoImageDataHasValue(image *VideoImageData) bool {
	return image != nil &&
		(image.BytesBase64Encoded != nil ||
			image.GCSURI != nil ||
			image.FileData != nil ||
			image.MimeType != nil ||
			image.MaskMode != nil)
}

func fileDataHasValue(fileData *FileData) bool {
	return fileData != nil &&
		(fileData.DisplayName != "" ||
			fileData.FileURI != "" ||
			fileData.MIMEType != "")
}

func videoReferenceImageHasValue(referenceImage VideoReferenceImage) bool {
	return referenceImage.ReferenceType != "" || videoImageDataHasValue(&referenceImage.Image)
}

func addVideoURLOutput(uri, contentType string) *schemas.VideoOutput {
	if uri == "" {
		return nil
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = defaultVideoContentType
	}
	return &schemas.VideoOutput{
		Type:        schemas.VideoOutputTypeURL,
		URL:         schemas.Ptr(uri),
		ContentType: contentType,
	}
}

func addVideoBase64Output(base64Value, contentType string) *schemas.VideoOutput {
	if base64Value == "" {
		return nil
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = defaultVideoContentType
	}
	return &schemas.VideoOutput{
		Type:        schemas.VideoOutputTypeBase64,
		Base64Data:  schemas.Ptr(base64Value),
		ContentType: contentType,
	}
}

func ToGeminiVideoGenerationRequest(bifrostReq *schemas.BifrostVideoGenerationRequest) (*GeminiVideoGenerationRequest, error) {
	if bifrostReq == nil || bifrostReq.Input == nil {
		return nil, fmt.Errorf("bifrost request or input is nil")
	}

	bifrostReq.Model = NormalizeModelName(bifrostReq.Model)

	instance := &GeminiVideoGenerationInstance{
		Prompt: bifrostReq.Input.Prompt,
	}

	if bifrostReq.Input.InputReference != nil && *bifrostReq.Input.InputReference != "" {

		sanitizedURL, err := schemas.SanitizeImageURLWithAllowedSchemes(*bifrostReq.Input.InputReference, defaultGeminiImageURLSchemes...)
		if err != nil {
			return nil, fmt.Errorf("invalid input reference: %w", err)
		}
		urlInfo := schemas.ExtractURLTypeInfo(sanitizedURL)

		image := &VideoImageData{}

		if urlInfo.DataURLWithoutPrefix != nil {
			image.BytesBase64Encoded = urlInfo.DataURLWithoutPrefix
		}
		image.MimeType = schemas.Ptr("image/png")
		if urlInfo.MediaType != nil {
			image.MimeType = urlInfo.MediaType
		}

		instance.Image = image
	}

	if bifrostReq.Input != nil && bifrostReq.Input.VideoURI != nil {
		instance.Video = &VideoGenerationVideoInput{
			URI: bifrostReq.Input.VideoURI,
		}
	}

	req := &GeminiVideoGenerationRequest{
		Instances: []GeminiVideoGenerationInstance{*instance},
	}

	if bifrostReq.Params != nil {
		params := &VideoGenerationParameters{}

		if bifrostReq.Params.NegativePrompt != nil {
			params.NegativePrompt = bifrostReq.Params.NegativePrompt
		}

		if bifrostReq.Params.Seconds != nil {
			seconds, err := strconv.Atoi(*bifrostReq.Params.Seconds)
			if err != nil {
				return nil, fmt.Errorf("invalid seconds value: %w", err)
			}
			params.DurationSeconds = &seconds
		}

		if bifrostReq.Params.Seed != nil {
			params.Seed = bifrostReq.Params.Seed
		}

		if bifrostReq.Params.Audio != nil {
			params.GenerateAudio = bifrostReq.Params.Audio
		}

		if bifrostReq.Params.ExtraParams != nil {
			req.ExtraParams = bifrostReq.Params.ExtraParams
			if aspectRatio, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["aspectRatio"]); ok {
				params.AspectRatio = aspectRatio
			}
			if resolution, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["resolution"]); ok {
				params.Resolution = resolution
			}

			if sampleCount, ok := schemas.SafeExtractIntPointer(bifrostReq.Params.ExtraParams["sampleCount"]); ok {
				params.SampleCount = sampleCount
			}

			if personGeneration, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["personGeneration"]); ok {
				params.PersonGeneration = personGeneration
			}

			if numberOfVideos, ok := schemas.SafeExtractIntPointer(bifrostReq.Params.ExtraParams["numberOfVideos"]); ok {
				params.NumberOfVideos = numberOfVideos
			}
			if storageURI, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["storageURI"]); ok {
				params.StorageURI = storageURI
			}
			if compressionQuality, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["compressionQuality"]); ok {
				params.CompressionQuality = compressionQuality
			}
			if enhancePrompt, ok := schemas.SafeExtractBoolPointer(bifrostReq.Params.ExtraParams["enhancePrompt"]); ok {
				params.EnhancePrompt = enhancePrompt
			}
			if resizeMode, ok := schemas.SafeExtractStringPointer(bifrostReq.Params.ExtraParams["resizeMode"]); ok {
				params.ResizeMode = resizeMode
			}
			if referenceImages, ok := bifrostReq.Params.ExtraParams["referenceImages"]; ok {
				if referenceImages, ok := videoReferenceImagesFromExtraParam(referenceImages); ok {
					req.Instances[0].ReferenceImages = referenceImages
				}
			}
			if lastFrame, ok := bifrostReq.Params.ExtraParams["lastFrame"]; ok {
				if lastFrame, ok := videoImageDataFromExtraParam(lastFrame); ok {
					req.Instances[0].LastFrame = lastFrame
				}
			}
		}

		if params.AspectRatio == nil && bifrostReq.Params.Size != "" {
			aspectRatio := sizeToAspectRatio(bifrostReq.Params.Size)
			if aspectRatio != "" {
				params.AspectRatio = &aspectRatio
			}
		}

		req.Parameters = params
	}

	return req, nil
}

func ToBifrostVideoGenerationResponse(operation *GenerateVideosOperation, model string) (*schemas.BifrostVideoGenerationResponse, *schemas.BifrostError) {
	model = NormalizeModelName(model)

	if operation == nil {
		return nil, providerUtils.NewBifrostOperationError("operation is nil", nil)
	}

	response := &schemas.BifrostVideoGenerationResponse{
		ID:        operation.Name,
		Object:    "video",
		CreatedAt: time.Now().Unix(),
	}
	if model != "" {
		response.Model = model
	}

	if !operation.Done {
		response.Status = schemas.VideoStatusInProgress
		if operation.Metadata != nil {
			if p := providerUtils.GetJSONField([]byte(operation.Metadata), "progress"); p.Exists() {
				progress := p.Float()
				response.Progress = &progress
			}
		}
	} else if operation.Error != nil {
		response.Status = schemas.VideoStatusFailed
		code := providerUtils.GetJSONField(operation.Error, "code").String()
		message := providerUtils.GetJSONField(operation.Error, "message").String()
		if code == "" {
			code = "video_generation_failed"
		}
		if message == "" {
			message = string(operation.Error)
		}
		response.Error = &schemas.VideoCreateError{
			Code:    code,
			Message: message,
		}
	} else if operation.Response != nil {

		if genVideoResp := operation.Response.GenerateVideoResponse; genVideoResp != nil {

			if genVideoResp.RAIMediaFilteredCount > 0 {
				response.Status = schemas.VideoStatusFailed
				response.ContentFilter = &schemas.ContentFilterInfo{
					FilteredCount: int(genVideoResp.RAIMediaFilteredCount),
					Reasons:       genVideoResp.RAIMediaFilteredReasons,
				}
				errorMsg := "Content filtered by safety policies"
				if len(genVideoResp.RAIMediaFilteredReasons) > 0 {
					errorMsg = genVideoResp.RAIMediaFilteredReasons[0]
				}
				response.Error = &schemas.VideoCreateError{
					Code:    "content_filtered",
					Message: errorMsg,
				}
			} else {
				response.Status = schemas.VideoStatusCompleted

				var videos []schemas.VideoOutput

				if len(genVideoResp.GeneratedSamples) > 0 {
					for _, sample := range genVideoResp.GeneratedSamples {
						if sample == nil || sample.Video == nil {
							continue
						}

						if sample.Video.URI != "" {
							videoOutput := addVideoURLOutput(sample.Video.URI, sample.Video.MIMEType)
							if videoOutput != nil {
								videos = append(videos, *videoOutput)
							}
						}
						if len(sample.Video.VideoBytes) > 0 {
							videoOutput := addVideoBase64Output(
								base64.StdEncoding.EncodeToString(sample.Video.VideoBytes),
								sample.Video.MIMEType,
							)
							if videoOutput != nil {
								videos = append(videos, *videoOutput)
							}
						}
					}
				}

				if len(videos) > 0 {
					response.Videos = videos
				}
			}
		} else if len(operation.Response.GeneratedVideos) > 0 {

			response.Status = schemas.VideoStatusCompleted
			var videos []schemas.VideoOutput
			for _, genVideo := range operation.Response.GeneratedVideos {
				if genVideo == nil || genVideo.Video == nil {
					continue
				}
				if genVideo.Video.URI != "" {
					videoOutput := addVideoURLOutput(genVideo.Video.URI, genVideo.Video.MIMEType)
					if videoOutput != nil {
						videos = append(videos, *videoOutput)
					}
				}
				if len(genVideo.Video.VideoBytes) > 0 {
					videoOutput := addVideoBase64Output(
						base64.StdEncoding.EncodeToString(genVideo.Video.VideoBytes),
						genVideo.Video.MIMEType,
					)
					if videoOutput != nil {
						videos = append(videos, *videoOutput)
					}
				}
			}
			if len(videos) > 0 {
				response.Videos = videos
			}
		} else if len(operation.Response.Videos) > 0 {
			response.Status = schemas.VideoStatusCompleted
			var videos []schemas.VideoOutput
			for _, video := range operation.Response.Videos {
				if video.GCSURI != nil && *video.GCSURI != "" {
					mimeType := defaultVideoContentType
					if video.MIMEType != nil && *video.MIMEType != "" {
						mimeType = *video.MIMEType
					}
					videoOutput := addVideoURLOutput(*video.GCSURI, mimeType)
					if videoOutput != nil {
						videos = append(videos, *videoOutput)
					}
				} else if video.BytesBase64Encoded != nil && *video.BytesBase64Encoded != "" {
					mimeType := defaultVideoContentType
					if video.MIMEType != nil && *video.MIMEType != "" {
						mimeType = *video.MIMEType
					}
					videoOutput := addVideoBase64Output(*video.BytesBase64Encoded, mimeType)
					if videoOutput != nil {
						videos = append(videos, *videoOutput)
					}
				}
			}
			if len(videos) > 0 {
				response.Videos = videos
			}
		} else {
			response.Status = schemas.VideoStatusCompleted
		}
	} else {
		response.Status = schemas.VideoStatusCompleted
	}

	if operation.Metadata != nil {
		if ct := providerUtils.GetJSONField([]byte(operation.Metadata), "createTime"); ct.Exists() {
			if t, err := time.Parse(time.RFC3339, ct.String()); err == nil {
				response.CreatedAt = t.Unix()
			}
		}
		if ut := providerUtils.GetJSONField([]byte(operation.Metadata), "updateTime"); ut.Exists() {
			if t, err := time.Parse(time.RFC3339, ut.String()); err == nil && operation.Done {
				response.CompletedAt = schemas.Ptr(t.Unix())
			}
		}
	}

	return response, nil
}
