package schemas

import (
	"bytes"
	"fmt"
)

type BifrostImageGenerationStreamResponse struct {
	ID                string                     `json:"id,omitempty"`
	Type              ImageEventType             `json:"type,omitempty"`
	Index             int                        `json:"-"`
	ChunkIndex        int                        `json:"-"`
	PartialImageIndex *int                       `json:"partial_image_index,omitempty"`
	SequenceNumber    int                        `json:"sequence_number,omitempty"`
	B64JSON           string                     `json:"b64_json,omitempty"`
	URL               string                     `json:"url,omitempty"`
	CreatedAt         int64                      `json:"created_at,omitempty"`
	Size              string                     `json:"size,omitempty"`
	AspectRatio       string                     `json:"aspect_ratio,omitempty"`
	Quality           string                     `json:"quality,omitempty"`
	Background        string                     `json:"background,omitempty"`
	OutputFormat      string                     `json:"output_format,omitempty"`
	RevisedPrompt     string                     `json:"revised_prompt,omitempty"`
	Usage             *ImageUsage                `json:"usage,omitempty"`
	Error             *BifrostError              `json:"error,omitempty"`
	RawRequest        string                     `json:"-"`
	RawResponse       string                     `json:"-"`
	ExtraFields       BifrostResponseExtraFields `json:"extra_fields"`
}
type ImageEditParameters struct {
	Type              *string                `json:"type,omitempty"`
	Background        *string                `json:"background,omitempty"`
	InputFidelity     *string                `json:"input_fidelity,omitempty"`
	Mask              []byte                 `json:"mask,omitempty"`
	N                 *int                   `json:"n,omitempty"`
	OutputCompression *int                   `json:"output_compression,omitempty"`
	OutputFormat      *string                `json:"output_format,omitempty"`
	PartialImages     *int                   `json:"partial_images,omitempty"`
	Quality           *string                `json:"quality,omitempty"`
	ResponseFormat    *string                `json:"response_format,omitempty"`
	Size              *string                `json:"size,omitempty"`
	User              *string                `json:"user,omitempty"`
	NegativePrompt    *string                `json:"negative_prompt,omitempty"`
	Seed              *int                   `json:"seed,omitempty"`
	NumInferenceSteps *int                   `json:"num_inference_steps,omitempty"`
	UpscaleFactor     *int                   `json:"upscale_factor,omitempty"`
	TargetMegapixels  *int                   `json:"target_megapixels,omitempty"`
	AspectRatio       *string                `json:"aspect_ratio,omitempty"`
	ExtraParams       map[string]interface{} `json:"-"`
}
type ImageInput struct {
	Image []byte `json:"image"`
	URL   string `json:"url,omitempty"`
}
type ImageEventType string
type BifrostImageEditRequest struct {
	Provider       ModelProvider        `json:"provider"`
	Model          string               `json:"model"`
	Input          *ImageEditInput      `json:"input"`
	Params         *ImageEditParameters `json:"params,omitempty"`
	Fallbacks      []Fallback           `json:"fallbacks,omitempty"`
	RawRequestBody []byte               `json:"-"`
}
type ImageData struct {
	ID            string           `json:"id,omitempty"`
	URL           string           `json:"url,omitempty"`
	B64JSON       string           `json:"b64_json,omitempty"`
	RevisedPrompt string           `json:"revised_prompt,omitempty"`
	Index         int              `json:"index"`
	Detections    []ImageDetection `json:"detections,omitempty"`
}
type ImageGenerationResponseParameters struct {
	Background    string    `json:"background,omitempty"`
	OutputFormat  string    `json:"output_format,omitempty"`
	Quality       string    `json:"quality,omitempty"`
	Size          string    `json:"size,omitempty"`
	AspectRatio   string    `json:"aspect_ratio,omitempty"`
	FinishReasons []*string `json:"finish_reasons,omitempty"`
	Seeds         []int     `json:"seeds,omitempty"`
}
type ImageTokenDetails struct {
	NImages     int `json:"-"`
	ImageTokens int `json:"image_tokens,omitempty"`
	TextTokens  int `json:"text_tokens,omitempty"`

	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}
type ImageEditInput struct {
	Images []ImageInput `json:"images"`
	Prompt string       `json:"prompt"`
}
type ImageDetection struct {
	XMin int `json:"x_min"`
	YMin int `json:"y_min"`
	XMax int `json:"x_max"`
	YMax int `json:"y_max"`
}
type ImageVariationInput struct {
	Image ImageInput `json:"image"`
}
type ImageGenerationParameters struct {
	N                 *int                   `json:"n,omitempty"`
	Background        *string                `json:"background,omitempty"`
	Moderation        *string                `json:"moderation,omitempty"`
	PartialImages     *int                   `json:"partial_images,omitempty"`
	Size              *string                `json:"size,omitempty"`
	Quality           *string                `json:"quality,omitempty"`
	OutputCompression *int                   `json:"output_compression,omitempty"`
	OutputFormat      *string                `json:"output_format,omitempty"`
	Style             *string                `json:"style,omitempty"`
	Type              *string                `json:"type,omitempty"`
	ResponseFormat    *string                `json:"response_format,omitempty"`
	Seed              *int                   `json:"seed,omitempty"`
	NegativePrompt    *string                `json:"negative_prompt,omitempty"`
	NumInferenceSteps *int                   `json:"num_inference_steps,omitempty"`
	User              *string                `json:"user,omitempty"`
	InputImages       []string               `json:"input_images,omitempty"`
	AspectRatio       *string                `json:"aspect_ratio,omitempty"`
	ExtraParams       map[string]interface{} `json:"-"`
}
type ImageGenerationInput struct {
	Prompt string `json:"prompt"`
}
type BifrostImageVariationRequest struct {
	Provider       ModelProvider             `json:"provider"`
	Model          string                    `json:"model"`
	Input          *ImageVariationInput      `json:"input"`
	Params         *ImageVariationParameters `json:"params,omitempty"`
	Fallbacks      []Fallback                `json:"fallbacks,omitempty"`
	RawRequestBody []byte                    `json:"-"`
}
type ImageVariationParameters struct {
	N              *int                   `json:"n,omitempty"`
	ResponseFormat *string                `json:"response_format,omitempty"`
	Size           *string                `json:"size,omitempty"`
	User           *string                `json:"user,omitempty"`
	ExtraParams    map[string]interface{} `json:"-"`
}
type ImageUsage struct {
	InputTokens         int                `json:"input_tokens,omitempty"`
	InputTokensDetails  *ImageTokenDetails `json:"input_tokens_details,omitempty"`
	TotalTokens         int                `json:"total_tokens,omitempty"`
	OutputTokens        int                `json:"output_tokens,omitempty"`
	OutputTokensDetails *ImageTokenDetails `json:"output_tokens_details,omitempty"`
	NumInputImages      int                `json:"-"`
	Cost                *BifrostCost       `json:"cost,omitempty"`

	CostInUsdTicks *int64 `json:"cost_in_usd_ticks,omitempty"`
}
type BifrostImageGenerationResponse struct {
	ID      string      `json:"id,omitempty"`
	Created int64       `json:"created,omitempty"`
	Model   string      `json:"model,omitempty"`
	Data    []ImageData `json:"data"`

	*ImageGenerationResponseParameters

	Usage       *ImageUsage                `json:"usage,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostImageGenerationRequest struct {
	Provider       ModelProvider              `json:"provider"`
	Model          string                     `json:"model"`
	Input          *ImageGenerationInput      `json:"input"`
	Params         *ImageGenerationParameters `json:"params,omitempty"`
	Fallbacks      []Fallback                 `json:"fallbacks,omitempty"`
	RawRequestBody []byte                     `json:"-"`
}

const (
	ImageGenerationEventTypePartial   ImageEventType = "image_generation.partial_image"
	ImageGenerationEventTypeCompleted ImageEventType = "image_generation.completed"
	ImageGenerationEventTypeError     ImageEventType = "error"
	ImageEditEventTypePartial         ImageEventType = "image_edit.partial_image"
	ImageEditEventTypeCompleted       ImageEventType = "image_edit.completed"
	ImageEditEventTypeError           ImageEventType = "error"
)

func (b *BifrostImageGenerationRequest) GetRawRequestBody() []byte {
	return b.RawRequestBody
}

func (r *BifrostImageGenerationResponse) BackfillParams(req *BifrostRequest) {
	if r == nil || req == nil {
		return
	}
	numInputImages, size, quality, aspectRatio := getNumInputImagesSizeQualityAndAspectRatioFromRequest(req)

	if r.Model == "" {
		switch {
		case req.ImageGenerationRequest != nil:
			r.Model = req.ImageGenerationRequest.Model
		case req.ImageEditRequest != nil:
			r.Model = req.ImageEditRequest.Model
		case req.ImageVariationRequest != nil:
			r.Model = req.ImageVariationRequest.Model
		}
	}

	if numInputImages > 0 {
		if r.Usage == nil {
			r.Usage = &ImageUsage{}
		}
		r.Usage.NumInputImages = numInputImages
	}

	if size != "" && (r.ImageGenerationResponseParameters == nil || r.ImageGenerationResponseParameters.Size == "") {
		if r.ImageGenerationResponseParameters == nil {
			r.ImageGenerationResponseParameters = &ImageGenerationResponseParameters{}
		}
		r.ImageGenerationResponseParameters.Size = size
	}

	if quality != "" && (r.ImageGenerationResponseParameters == nil || r.ImageGenerationResponseParameters.Quality == "") {
		if r.ImageGenerationResponseParameters == nil {
			r.ImageGenerationResponseParameters = &ImageGenerationResponseParameters{}
		}
		r.ImageGenerationResponseParameters.Quality = quality
	}

	if aspectRatio != "" && (r.ImageGenerationResponseParameters == nil || r.ImageGenerationResponseParameters.AspectRatio == "") {
		if r.ImageGenerationResponseParameters == nil {
			r.ImageGenerationResponseParameters = &ImageGenerationResponseParameters{}
		}
		r.ImageGenerationResponseParameters.AspectRatio = aspectRatio
	}
}

func getNumInputImagesSizeQualityAndAspectRatioFromRequest(req *BifrostRequest) (numInputImages int, size string, quality string, aspectRatio string) {
	if req == nil {
		return 0, "", "", ""
	}

	switch {
	case req.ImageGenerationRequest != nil:
		if req.ImageGenerationRequest.Params != nil {
			p := req.ImageGenerationRequest.Params
			numInputImages = len(p.InputImages)
			if p.Size != nil {
				size = *p.Size
			}
			if p.Quality != nil {
				quality = normalizeImageQuality(*p.Quality)
			}
			if p.AspectRatio != nil {
				aspectRatio = *p.AspectRatio
			}
		}
	case req.ImageEditRequest != nil:
		if req.ImageEditRequest.Input != nil {
			numInputImages = len(req.ImageEditRequest.Input.Images)
		}
		if req.ImageEditRequest.Params != nil {
			p := req.ImageEditRequest.Params
			if p.Size != nil {
				size = *p.Size
			}
			if p.Quality != nil {
				quality = normalizeImageQuality(*p.Quality)
			}
			if p.AspectRatio != nil {
				aspectRatio = *p.AspectRatio
			}
		}
	case req.ImageVariationRequest != nil:
		if req.ImageVariationRequest.Input != nil {
			numInputImages = 1
		}
		if req.ImageVariationRequest.Params != nil && req.ImageVariationRequest.Params.Size != nil {
			size = *req.ImageVariationRequest.Params.Size
		}
	}
	return numInputImages, size, quality, aspectRatio
}

func normalizeImageQuality(q string) string {
	switch q {
	case "low", "medium", "high", "auto":
		return q
	default:
		return ""
	}
}

func (u *ImageUsage) DeepCopy() *ImageUsage {
	if u == nil {
		return nil
	}
	out := *u
	if u.InputTokensDetails != nil {
		details := *u.InputTokensDetails
		out.InputTokensDetails = &details
	}
	if u.OutputTokensDetails != nil {
		details := *u.OutputTokensDetails
		out.OutputTokensDetails = &details
	}
	if u.CostInUsdTicks != nil {
		out.CostInUsdTicks = new(*u.CostInUsdTicks)
	}
	if u.Cost != nil {
		out.Cost = new(*u.Cost)
	}
	return &out
}

func (r *BifrostImageGenerationStreamResponse) BackfillParams(req *BifrostRequest) {
	numInputImages, size, quality, aspectRatio := getNumInputImagesSizeQualityAndAspectRatioFromRequest(req)

	if numInputImages > 0 {
		if r.Usage == nil {
			r.Usage = &ImageUsage{}
		}
		r.Usage.NumInputImages = numInputImages
	}

	if size != "" && r.Size == "" {
		r.Size = size
	}

	if quality != "" && r.Quality == "" {
		r.Quality = quality
	}

	if aspectRatio != "" && r.AspectRatio == "" {
		r.AspectRatio = aspectRatio
	}
}

func (b *BifrostImageEditRequest) GetRawRequestBody() []byte {
	return b.RawRequestBody
}

func (i *ImageInput) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		i.Image = nil
		i.URL = ""
		return nil
	}

	var s string
	if err := Unmarshal(data, &s); err == nil {
		i.Image = nil
		i.URL = s
		return nil
	}
	type imageInput ImageInput
	var obj imageInput
	if err := Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("image input is neither a string nor an object")
	}
	*i = ImageInput(obj)
	return nil
}

func (b *BifrostImageVariationRequest) GetRawRequestBody() []byte {
	return b.RawRequestBody
}
