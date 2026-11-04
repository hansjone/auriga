// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package minimax

type MiniMaxSpeechResponse struct {
	Data *struct {
		Audio  string `json:"audio"`
		Status int    `json:"status"`
	} `json:"data,omitempty"`
	ExtraInfo *struct {
		AudioLength     int64 `json:"audio_length"`
		UsageCharacters int   `json:"usage_characters"`
	} `json:"extra_info,omitempty"`
	TraceID  string           `json:"trace_id,omitempty"`
	BaseResp *MiniMaxBaseResp `json:"base_resp,omitempty"`
}
type MiniMaxTranscriptionSegment struct {
	ID      int     `json:"id"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
}
type MiniMaxVideoGenerationRequest struct {
	Model       string                    `json:"model"`
	Content     []MiniMaxVideoContentItem `json:"content"`
	Resolution  string                    `json:"resolution"`
	Duration    int                       `json:"duration"`
	Ratio       *string                   `json:"ratio,omitempty"`
	CallbackURL *string                   `json:"callback_url,omitempty"`
	ExtraParams map[string]interface{}    `json:"-"`
}
type MiniMaxImageGenerationRequest struct {
	Model           string                 `json:"model"`
	Prompt          string                 `json:"prompt"`
	AspectRatio     *string                `json:"aspect_ratio,omitempty"`
	Width           *int                   `json:"width,omitempty"`
	Height          *int                   `json:"height,omitempty"`
	N               *int                   `json:"n,omitempty"`
	ResponseFormat  *string                `json:"response_format,omitempty"`
	Seed            *int                   `json:"seed,omitempty"`
	PromptOptimizer *bool                  `json:"prompt_optimizer,omitempty"`
	ExtraParams     map[string]interface{} `json:"-"`
}
type MiniMaxTranscriptionStreamEvent struct {
	Index    int      `json:"index"`
	Delta    string   `json:"delta"`
	Finish   bool     `json:"finish"`
	Duration *float64 `json:"duration,omitempty"`
}
type MiniMaxVideoTaskStatus string
type MiniMaxImageGenerationResponse struct {
	ID   string `json:"id"`
	Data struct {
		ImageURLs   []string `json:"image_urls,omitempty"`
		ImageBase64 []string `json:"image_base64,omitempty"`
	} `json:"data"`
	Metadata struct {
		SuccessCount any `json:"success_count,omitempty"`
		FailedCount  any `json:"failed_count,omitempty"`
	} `json:"metadata"`
	BaseResp *MiniMaxBaseResp `json:"base_resp,omitempty"`
}
type MiniMaxVideoMediaURL struct {
	URL string `json:"url"`
}
type MiniMaxVoiceSetting struct {
	VoiceID string   `json:"voice_id"`
	Speed   *float64 `json:"speed,omitempty"`
	Vol     *float64 `json:"vol,omitempty"`
	Pitch   *int     `json:"pitch,omitempty"`
	Emotion *string  `json:"emotion,omitempty"`
}
type MiniMaxSpeechStreamOptions struct {
	ExcludeAggregatedAudio bool `json:"exclude_aggregated_audio"`
}
type MiniMaxVideoContentItem struct {
	Type     string                `json:"type"`
	Role     string                `json:"role,omitempty"`
	Text     string                `json:"text,omitempty"`
	ImageURL *MiniMaxVideoMediaURL `json:"image_url,omitempty"`
	VideoURL *MiniMaxVideoMediaURL `json:"video_url,omitempty"`
	AudioURL *MiniMaxVideoMediaURL `json:"audio_url,omitempty"`
}
type MiniMaxVideoTaskCreationResponse struct {
	TaskID   string           `json:"task_id"`
	BaseResp *MiniMaxBaseResp `json:"base_resp,omitempty"`
}
type MiniMaxTranscriptionResponse struct {
	Text      string                        `json:"text"`
	Duration  *float64                      `json:"duration,omitempty"`
	NSpeakers int                           `json:"n_speakers,omitempty"`
	Segments  []MiniMaxTranscriptionSegment `json:"segments,omitempty"`
	TraceID   string                        `json:"trace_id,omitempty"`
}
type MiniMaxVideoDeleteResponse struct {
	TaskID string `json:"task_id"`
	Action string `json:"action"`
	Status string `json:"status"`
}
type MiniMaxSpeechRequest struct {
	Model         string                      `json:"model"`
	Text          string                      `json:"text"`
	Stream        bool                        `json:"stream,omitempty"`
	StreamOptions *MiniMaxSpeechStreamOptions `json:"stream_options,omitempty"`
	VoiceSetting  MiniMaxVoiceSetting         `json:"voice_setting"`
	AudioSetting  *MiniMaxAudioSetting        `json:"audio_setting,omitempty"`
	LanguageBoost *string                     `json:"language_boost,omitempty"`
	OutputFormat  *string                     `json:"output_format,omitempty"`
	ExtraParams   map[string]interface{}      `json:"-"`
}
type MiniMaxBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}
type MiniMaxTaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type MiniMaxVideoTaskResponse struct {
	Task     *MiniMaxVideoTask `json:"task"`
	BaseResp *MiniMaxBaseResp  `json:"base_resp,omitempty"`
}
type MiniMaxVideoTask struct {
	ID        string                 `json:"id"`
	Model     string                 `json:"model"`
	Status    MiniMaxVideoTaskStatus `json:"status"`
	Error     *MiniMaxTaskError      `json:"error,omitempty"`
	CreatedAt int64                  `json:"created_at"`
	UpdatedAt int64                  `json:"updated_at"`
	Content   *struct {
		URL    string `json:"url,omitempty"`
		Prompt string `json:"prompt,omitempty"`
	} `json:"content,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	Ratio      string `json:"ratio,omitempty"`
	Usage      *struct {
		TotalSeconds  float64 `json:"total_seconds,omitempty"`
		OutputSeconds float64 `json:"output_seconds,omitempty"`
	} `json:"usage,omitempty"`
}
type MiniMaxAudioSetting struct {
	Format     *string `json:"format,omitempty"`
	SampleRate *int    `json:"sample_rate,omitempty"`
	Bitrate    *int    `json:"bitrate,omitempty"`
	Channel    *int    `json:"channel,omitempty"`
}

func (r *MiniMaxImageGenerationRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

func (r *MiniMaxVideoGenerationRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}

const (
	MiniMaxVideoTaskStatusQueued    MiniMaxVideoTaskStatus = "queued"
	MiniMaxVideoTaskStatusRunning   MiniMaxVideoTaskStatus = "running"
	MiniMaxVideoTaskStatusSucceeded MiniMaxVideoTaskStatus = "succeeded"
	MiniMaxVideoTaskStatusFailed    MiniMaxVideoTaskStatus = "failed"
	MiniMaxVideoTaskStatusCancelled MiniMaxVideoTaskStatus = "cancelled"
)

func (r *MiniMaxSpeechRequest) GetExtraParams() map[string]interface{} {
	return r.ExtraParams
}
