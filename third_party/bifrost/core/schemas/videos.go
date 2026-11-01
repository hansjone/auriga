// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type VideoEditInput struct {
	Video  VideoInput `json:"video"`
	Prompt string     `json:"prompt"`
}
type BifrostVideoListResponse struct {
	Object      string                     `json:"object"`
	Data        []VideoObject              `json:"data"`
	FirstID     *string                    `json:"first_id,omitempty"`
	HasMore     *bool                      `json:"has_more,omitempty"`
	LastID      *string                    `json:"last_id,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostVideoRetrieveRequest = BifrostVideoReferenceRequest
type BifrostVideoDebug struct {
	VideoID string `json:"video_id,omitempty"`

	Status VideoStatus `json:"status,omitempty"`

	Accounting *VideoAccountingDebug `json:"accounting,omitempty"`

	SettledByRequestID string `json:"settled_by_request_id,omitempty"`
}
type BifrostVideoDownloadRequest struct {
	Provider    ModelProvider         `json:"provider"`
	ID          string                `json:"id"`
	Variant     *VideoDownloadVariant `json:"variant,omitempty"`
	ExtraParams map[string]any        `json:"-"`
}
type BifrostVideoListRequest struct {
	Provider ModelProvider `json:"provider"`
	After    *string       `json:"after,omitempty"`
	Limit    *int          `json:"limit,omitempty"`
	Order    *string       `json:"order,omitempty"`
}
type BifrostVideoGenerationResponse struct {
	ID                 string             `json:"id,omitempty"`
	CompletedAt        *int64             `json:"completed_at,omitempty"`
	CreatedAt          int64              `json:"created_at,omitempty"`
	Error              *VideoCreateError  `json:"error,omitempty"`
	ExpiresAt          *int64             `json:"expires_at,omitempty"`
	Model              string             `json:"model,omitempty"`
	Object             string             `json:"object,omitempty"`
	Progress           *float64           `json:"progress,omitempty"`
	Prompt             string             `json:"prompt,omitempty"`
	RemixedFromVideoID *string            `json:"remixed_from_video_id,omitempty"`
	Seconds            *string            `json:"seconds,omitempty"`
	Size               string             `json:"size,omitempty"`
	Status             VideoStatus        `json:"status,omitempty"`
	Videos             []VideoOutput      `json:"videos,omitempty"`
	ContentFilter      *ContentFilterInfo `json:"content_filter,omitempty"`
	Usage              *VideoUsage        `json:"usage,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields,omitempty"`
}
type BifrostVideoGenerationRequest struct {
	Provider       ModelProvider              `json:"provider"`
	Model          string                     `json:"model"`
	Input          *VideoGenerationInput      `json:"input"`
	Params         *VideoGenerationParameters `json:"params,omitempty"`
	Fallbacks      []Fallback                 `json:"fallbacks,omitempty"`
	RawRequestBody []byte                     `json:"-"`
}
type BifrostVideoDeleteResponse struct {
	ID          string                     `json:"id"`
	Deleted     bool                       `json:"deleted"`
	Object      string                     `json:"object,omitempty"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type VideoAccountingDebug struct {
	Seconds *int   `json:"seconds,omitempty"`
	Size    string `json:"size,omitempty"`

	OutputCount int `json:"output_count,omitempty"`

	OutputTokens int `json:"output_tokens,omitempty"`

	Incomplete bool `json:"incomplete,omitempty"`

	RequestType RequestType `json:"request_type,omitempty"`

	ProviderCost *float64 `json:"provider_cost,omitempty"`
}
type VideoUsage struct {
	Cost         *BifrostCost `json:"cost,omitempty"`
	OutputTokens int          `json:"output_tokens,omitempty"`
}
type BifrostVideoEditRequest struct {
	Provider       ModelProvider        `json:"provider"`
	Model          string               `json:"model,omitempty"`
	Input          *VideoEditInput      `json:"input"`
	Params         *VideoEditParameters `json:"params,omitempty"`
	Fallbacks      []Fallback           `json:"fallbacks,omitempty"`
	RawRequestBody []byte               `json:"-"`
}
type BifrostVideoDeleteRequest = BifrostVideoReferenceRequest
type VideoEditParameters struct {
	Type         *string        `json:"type,omitempty"`
	Seed         *int           `json:"seed,omitempty"`
	OutputFormat *string        `json:"output_format,omitempty"`
	ExtraParams  map[string]any `json:"-"`

	UpscaleFactor    *int `json:"upscale_factor,omitempty"`
	TargetMegapixels *int `json:"target_megapixels,omitempty"`
}
type ContentFilterInfo struct {
	FilteredCount int      `json:"filtered_count,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
}
type BifrostVideoDownloadResponse struct {
	VideoID     string `json:"video_id"`
	Content     []byte `json:"-"`
	ContentType string `json:"content_type,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type VideoOutputType string
type VideoGenerationInput struct {
	Prompt         string  `json:"prompt"`
	InputReference *string `json:"input_reference,omitempty"`
	VideoURI       *string `json:"video_uri,omitempty"`
}
type VideoCreateError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
type VideoStatus string
type VideoOutput struct {
	ID          string          `json:"id,omitempty"`
	Type        VideoOutputType `json:"type"`
	URL         *string         `json:"url,omitempty"`
	Base64Data  *string         `json:"base64,omitempty"`
	ContentType string          `json:"content_type"`
}
type BifrostVideoRemixRequest struct {
	ID             string                `json:"id"`
	Provider       ModelProvider         `json:"provider"`
	Input          *VideoGenerationInput `json:"input"`
	ExtraParams    map[string]any        `json:"-"`
	RawRequestBody []byte                `json:"-"`
}
type BifrostVideoReferenceRequest struct {
	Provider ModelProvider `json:"provider"`
	ID       string        `json:"id"`
}
type VideoObject struct {
	ID                 string            `json:"id"`
	Object             string            `json:"object"`
	Model              string            `json:"model"`
	Status             VideoStatus       `json:"status"`
	CreatedAt          int64             `json:"created_at"`
	CompletedAt        *int64            `json:"completed_at,omitempty"`
	ExpiresAt          *int64            `json:"expires_at,omitempty"`
	Progress           *float64          `json:"progress,omitempty"`
	Prompt             string            `json:"prompt"`
	RemixedFromVideoID *string           `json:"remixed_from_video_id,omitempty"`
	Seconds            *string           `json:"seconds"`
	Size               string            `json:"size"`
	Error              *VideoCreateError `json:"error,omitempty"`
}
type VideoDownloadVariant string
type BifrostVideoEditResponse = BifrostVideoGenerationResponse
type VideoGenerationParameters struct {
	Seconds *string `json:"seconds,omitempty"`
	Size    string  `json:"size,omitempty"`

	NegativePrompt *string        `json:"negative_prompt,omitempty"`
	Seed           *int           `json:"seed,omitempty"`
	Type           *string        `json:"type,omitempty"`
	OutputFormat   *string        `json:"output_format,omitempty"`
	Audio          *bool          `json:"audio,omitempty"`
	ExtraParams    map[string]any `json:"-"`

	UpscaleFactor    *int `json:"upscale_factor,omitempty"`
	TargetMegapixels *int `json:"target_megapixels,omitempty"`
}
type VideoInput struct {
	Video []byte `json:"video,omitempty"`
	URL   string `json:"url,omitempty"`
	ID    string `json:"id,omitempty"`
}

const (
	VideoStatusQueued     VideoStatus = "queued"
	VideoStatusInProgress VideoStatus = "in_progress"
	VideoStatusCompleted  VideoStatus = "completed"
	VideoStatusFailed     VideoStatus = "failed"
)

const (
	VideoOutputTypeBase64 VideoOutputType = "base64"
	VideoOutputTypeURL    VideoOutputType = "url"
)

func (b *BifrostVideoGenerationRequest) GetRawRequestBody() []byte {
	return b.RawRequestBody
}

func (b *BifrostVideoGenerationRequest) GetExtraParams() map[string]interface{} {
	if b == nil || b.Params == nil {
		return nil
	}
	return b.Params.ExtraParams
}

const DefaultVideoDuration = "8"

func getSecondsFromVideoRequest(req *BifrostRequest) *string {
	if req == nil {
		return nil
	}
	useDefaultForSeconds := func(p ModelProvider) bool {
		return p == Gemini || p == Vertex
	}
	if req.VideoGenerationRequest != nil {
		var seconds *string
		if req.VideoGenerationRequest.Params != nil {
			seconds = req.VideoGenerationRequest.Params.Seconds
		}
		if seconds == nil && useDefaultForSeconds(req.VideoGenerationRequest.Provider) {
			seconds = Ptr(DefaultVideoDuration)
		}
		return seconds
	}
	if req.VideoRemixRequest != nil && useDefaultForSeconds(req.VideoRemixRequest.Provider) {
		return Ptr(DefaultVideoDuration)
	}
	return nil
}

func (r *BifrostVideoGenerationResponse) BackfillParams(req *BifrostRequest) {
	if r == nil || req == nil {
		return
	}
	seconds := getSecondsFromVideoRequest(req)
	if seconds != nil {
		r.Seconds = seconds
	}
	if r.Model == "" {
		switch {
		case req.VideoGenerationRequest != nil:
			r.Model = req.VideoGenerationRequest.Model
		case req.VideoEditRequest != nil:
			r.Model = req.VideoEditRequest.Model
		}
	}
}

func (b *BifrostVideoEditRequest) GetRawRequestBody() []byte {
	return b.RawRequestBody
}

func (b *BifrostVideoEditRequest) GetExtraParams() map[string]interface{} {
	if b == nil || b.Params == nil {
		return nil
	}
	return b.Params.ExtraParams
}

func (b *BifrostVideoRemixRequest) GetRawRequestBody() []byte {
	return b.RawRequestBody
}

func (b *BifrostVideoRemixRequest) GetExtraParams() map[string]interface{} {
	if b == nil {
		return nil
	}
	return b.ExtraParams
}

func (d *BifrostVideoDebug) IsZero() bool {
	if d == nil {
		return true
	}
	return d.VideoID == "" && d.Status == "" && d.Accounting == nil
}
