package gemini

import (
	"fmt"
	"maps"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

var geminiPerEntryEmbeddingExtraKeys = []string{"taskType", "title", "embedContentConfig"}

func mediaPartToGeminiPart(partType schemas.EmbeddingContentPartType, media *schemas.EmbeddingMediaPart, allowedURLSchemes ...string) (*Part, error) {
	if err := media.Validate(); err != nil {
		return nil, providerUtils.InvalidRequestErrorf("%s", err)
	}
	if len(allowedURLSchemes) == 0 {
		allowedURLSchemes = defaultGeminiImageURLSchemes
	}

	defaultMime := map[schemas.EmbeddingContentPartType]string{
		schemas.EmbeddingContentPartTypeImage: "image/jpeg",
		schemas.EmbeddingContentPartTypeAudio: "audio/mpeg",
		schemas.EmbeddingContentPartTypeFile:  "application/pdf",
		schemas.EmbeddingContentPartTypeVideo: "video/mp4",
	}[partType]

	if media.Data != nil {
		dataBytes, extractedMime := convertFileDataToBytes(*media.Data)
		if len(dataBytes) == 0 {
			return nil, providerUtils.InvalidRequestErrorf("empty media data for %s part", partType)
		}
		mimeType := defaultMime
		if media.MIMEType != nil && strings.TrimSpace(*media.MIMEType) != "" {
			mimeType = *media.MIMEType
		} else if extractedMime != "" {
			mimeType = extractedMime
		}
		return &Part{
			InlineData: &Blob{
				MIMEType: mimeType,
				Data:     encodeBytesToBase64String(dataBytes),
			},
		}, nil
	}

	mimeType := defaultMime
	if media.MIMEType != nil && strings.TrimSpace(*media.MIMEType) != "" {
		mimeType = *media.MIMEType
	}
	url := *media.URL
	if partType == schemas.EmbeddingContentPartTypeImage {
		sanitizedURL, err := schemas.SanitizeImageURLWithAllowedSchemes(url, allowedURLSchemes...)
		if err != nil {
			return nil, providerUtils.InvalidRequestErrorf("%s", err)
		}
		urlInfo := schemas.ExtractURLTypeInfo(sanitizedURL)
		if urlInfo.Type == schemas.ImageContentTypeBase64 {
			data := ""
			if urlInfo.DataURLWithoutPrefix != nil {
				data = *urlInfo.DataURLWithoutPrefix
			}
			decoded, err := decodeBase64StringToBytes(data)
			if err != nil {
				return nil, providerUtils.InvalidRequestErrorf("%s", err)
			}
			if urlInfo.MediaType != nil && (media.MIMEType == nil || *media.MIMEType == "") {
				mimeType = *urlInfo.MediaType
			}
			return &Part{
				InlineData: &Blob{
					MIMEType: mimeType,
					Data:     encodeBytesToBase64String(decoded),
				},
			}, nil
		}
		url = sanitizedURL
	}

	return &Part{
		FileData: &FileData{
			FileURI:  url,
			MIMEType: mimeType,
			DisplayName: func() string {
				if media.Filename != nil {
					return *media.Filename
				}
				return ""
			}(),
		},
	}, nil
}

func embeddingContentPartToGeminiPart(part schemas.EmbeddingContentPart, allowedURLSchemes ...string) (*Part, error) {
	if err := part.Validate(); err != nil {
		return nil, providerUtils.InvalidRequestErrorf("%s", err)
	}

	switch part.Type {
	case schemas.EmbeddingContentPartTypeText:
		return &Part{Text: *part.Text}, nil
	case schemas.EmbeddingContentPartTypeImage:
		return mediaPartToGeminiPart(part.Type, part.Image, allowedURLSchemes...)
	case schemas.EmbeddingContentPartTypeAudio:
		return mediaPartToGeminiPart(part.Type, part.Audio, allowedURLSchemes...)
	case schemas.EmbeddingContentPartTypeFile:
		return mediaPartToGeminiPart(part.Type, part.File, allowedURLSchemes...)
	case schemas.EmbeddingContentPartTypeVideo:
		return mediaPartToGeminiPart(part.Type, part.Video, allowedURLSchemes...)
	default:
		return nil, providerUtils.InvalidRequestErrorf("unsupported embedding content part type %q", part.Type)
	}
}

func EmbeddingContentToGeminiContent(content schemas.EmbeddingContent, allowedURLSchemes ...string) (*Content, error) {
	if err := content.Validate(); err != nil {
		return nil, providerUtils.InvalidRequestErrorf("%s", err)
	}
	parts := make([]*Part, 0, len(content))
	for _, contentPart := range content {
		part, err := embeddingContentPartToGeminiPart(contentPart, allowedURLSchemes...)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return &Content{Parts: parts}, nil
}

func ReadEmbedContentConfig(extra map[string]interface{}) *GeminiEmbedContentConfig {
	nested, ok := extra["embedContentConfig"].(map[string]interface{})
	if !ok {
		return nil
	}
	cfg := &GeminiEmbedContentConfig{}
	cfg.DocumentOCR, _ = schemas.SafeExtractBoolPointer(nested["documentOcr"])
	cfg.AudioTrackExtraction, _ = schemas.SafeExtractBoolPointer(nested["audioTrackExtraction"])
	if cfg.DocumentOCR == nil && cfg.AudioTrackExtraction == nil {
		return nil
	}
	return cfg
}

func applyGeminiEmbeddingParams(req *GeminiEmbeddingRequest, params *schemas.EmbeddingParameters) {
	if params == nil {
		return
	}
	req.OutputDimensionality = params.Dimensions
	req.TaskType = params.TaskType
	req.Title = params.Title

	if params.ExtraParams != nil {
		req.ExtraParams = params.ExtraParams
		if taskType, ok := schemas.SafeExtractStringPointer(params.ExtraParams["taskType"]); ok {
			delete(req.ExtraParams, "taskType")
			if req.TaskType == nil {
				req.TaskType = taskType
			}
		}
		if title, ok := schemas.SafeExtractStringPointer(params.ExtraParams["title"]); ok {
			delete(req.ExtraParams, "title")
			if req.Title == nil {
				req.Title = title
			}
		}
		req.EmbedContentConfig = ReadEmbedContentConfig(req.ExtraParams)
	}
}

func ToGeminiEmbeddingRequest(bifrostReq *schemas.BifrostEmbeddingRequest) (*GeminiBatchEmbeddingRequest, error) {
	if bifrostReq == nil || len(bifrostReq.Input) == 0 {
		return nil, providerUtils.InvalidRequestErrorf("bifrost request is nil or input is nil")
	}

	batchRequest := &GeminiBatchEmbeddingRequest{
		Requests: make([]GeminiEmbeddingRequest, 0, len(bifrostReq.Input)),
	}
	if bifrostReq.Params != nil && bifrostReq.Params.ExtraParams != nil {

		batchRequest.ExtraParams = maps.Clone(bifrostReq.Params.ExtraParams)
		for _, key := range geminiPerEntryEmbeddingExtraKeys {
			delete(batchRequest.ExtraParams, key)
		}
	}
	for _, item := range bifrostReq.Input {
		content, err := EmbeddingContentToGeminiContent(item.Content)
		if err != nil {
			return nil, fmt.Errorf("error converting embedding content to gemini content: %w", err)
		}
		req := GeminiEmbeddingRequest{
			Model:   "models/" + bifrostReq.Model,
			Content: content,
		}
		applyGeminiEmbeddingParams(&req, item.EffectiveParams(bifrostReq.Params))
		batchRequest.Requests = append(batchRequest.Requests, req)
	}
	return batchRequest, nil
}

func geminiResponseEmbeddings(resp *GeminiEmbeddingResponse) []GeminiEmbedding {
	if resp == nil {
		return nil
	}
	if len(resp.Embeddings) > 0 {
		return resp.Embeddings
	}
	if resp.Embedding != nil {
		return []GeminiEmbedding{*resp.Embedding}
	}
	return nil
}

func ToBifrostEmbeddingResponse(geminiResp *GeminiEmbeddingResponse, model string) *schemas.BifrostEmbeddingResponse {
	embeddings := geminiResponseEmbeddings(geminiResp)
	if len(embeddings) == 0 {
		return nil
	}

	bifrostResp := &schemas.BifrostEmbeddingResponse{
		Data:   make([]schemas.EmbeddingData, len(embeddings)),
		Model:  model,
		Object: "list",
	}

	for i, geminiEmbedding := range embeddings {
		bifrostResp.Data[i] = schemas.EmbeddingData{
			Index:  i,
			Object: "embedding",
			Embedding: schemas.EmbeddingStruct{
				EmbeddingArray: geminiEmbedding.Values,
			},
		}
	}

	hasStats := false
	for _, emb := range embeddings {
		if emb.Statistics != nil {
			hasStats = true
			break
		}
	}
	if um := geminiResp.UsageMetadata; um != nil && (um.PromptTokenCount > 0 || um.TotalTokenCount > 0) {
		prompt := int(um.PromptTokenCount)
		total := int(um.TotalTokenCount)
		if prompt == 0 {
			for _, emb := range embeddings {
				if emb.Statistics != nil {
					prompt += int(emb.Statistics.TokenCount)
				}
			}
			if prompt == 0 {
				prompt = total
			}
		}
		if total == 0 {
			total = prompt
		}
		bifrostResp.Usage = &schemas.BifrostLLMUsage{PromptTokens: prompt, TotalTokens: total}
	} else if geminiResp.Metadata != nil || hasStats {
		bifrostResp.Usage = &schemas.BifrostLLMUsage{}
		var totalTokens int
		for _, emb := range embeddings {
			if emb.Statistics != nil {
				totalTokens += int(emb.Statistics.TokenCount)
			}
		}
		if totalTokens > 0 {
			bifrostResp.Usage.PromptTokens = totalTokens
		} else if geminiResp.Metadata != nil {
			bifrostResp.Usage.PromptTokens = int(geminiResp.Metadata.BillableCharacterCount)
		}
		bifrostResp.Usage.TotalTokens = bifrostResp.Usage.PromptTokens
	}

	return bifrostResp
}
