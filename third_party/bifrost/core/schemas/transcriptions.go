package schemas

import "encoding/json"

type TranscriptionSegment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogProb       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
}
type TranscriptionLogProb struct {
	Token   string  `json:"token"`
	LogProb float64 `json:"logprob"`
	Bytes   []int   `json:"bytes"`
}
type BifrostTranscriptionResponse struct {
	Duration         *float64                       `json:"duration,omitempty"`
	Language         *string                        `json:"language,omitempty"`
	LogProbs         []TranscriptionLogProb         `json:"logprobs,omitempty"`
	Segments         []TranscriptionSegment         `json:"segments,omitempty"`
	DiarizedSegments []TranscriptionDiarizedSegment `json:"-"`
	Task             *string                        `json:"task,omitempty"`
	Text             string                         `json:"text"`
	Usage            *TranscriptionUsage            `json:"usage,omitempty"`
	Words            []TranscriptionWord            `json:"words,omitempty"`
	ResponseFormat   *string                        `json:"-"`
	ExtraFields      BifrostResponseExtraFields     `json:"extra_fields"`
}
type TranscriptionWord struct {
	Word    string  `json:"word"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker *string `json:"speaker,omitempty"`
}
type TranscriptionUsageInputTokenDetails struct {
	TextTokens  int `json:"text_tokens"`
	AudioTokens int `json:"audio_tokens"`
}
type TranscriptionAdditionalFormat struct {
	Format                      TranscriptionExportOptions `json:"format"`
	IncludeSpeakers             *bool                      `json:"include_speakers,omitempty"`
	IncludeTimestamps           *bool                      `json:"include_timestamps,omitempty"`
	SegmentOnSilenceLongerThanS *float64                   `json:"segment_on_silence_longer_than_s,omitempty"`
	MaxSegmentDurationS         *float64                   `json:"max_segment_duration_s,omitempty"`
	MaxSegmentChars             *int                       `json:"max_segment_chars,omitempty"`
	MaxCharactersPerLine        *int                       `json:"max_characters_per_line,omitempty"`
}
type TranscriptionParameters struct {
	Language               *string  `json:"language,omitempty"`
	Prompt                 *string  `json:"prompt,omitempty"`
	ResponseFormat         *string  `json:"response_format,omitempty"`
	Temperature            *float64 `json:"temperature,omitempty"`
	TimestampGranularities []string `json:"timestamp_granularities,omitempty"`
	Include                []string `json:"include,omitempty"`
	Format                 *string  `json:"file_format,omitempty"`
	MaxLength              *int     `json:"max_length,omitempty"`
	MinLength              *int     `json:"min_length,omitempty"`
	MaxNewTokens           *int     `json:"max_new_tokens,omitempty"`
	MinNewTokens           *int     `json:"min_new_tokens,omitempty"`

	AdditionalFormats []TranscriptionAdditionalFormat `json:"additional_formats,omitempty"`
	WebhookMetadata   interface{}                     `json:"webhook_metadata,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type TranscriptionDiarizedSegment struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	Speaker string  `json:"speaker"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
}
type BifrostTranscriptionStreamResponse struct {
	Delta       *string                         `json:"delta,omitempty"`
	LogProbs    []TranscriptionLogProb          `json:"logprobs,omitempty"`
	Text        string                          `json:"text"`
	Type        TranscriptionStreamResponseType `json:"type"`
	Usage       *TranscriptionUsage             `json:"usage,omitempty"`
	ExtraFields BifrostResponseExtraFields      `json:"extra_fields"`
}
type TranscriptionUsage struct {
	Type              string                               `json:"type"`
	InputTokens       *int                                 `json:"input_tokens,omitempty"`
	InputTokenDetails *TranscriptionUsageInputTokenDetails `json:"input_token_details,omitempty"`
	OutputTokens      *int                                 `json:"output_tokens,omitempty"`
	TotalTokens       *int                                 `json:"total_tokens,omitempty"`
	Seconds           *float64                             `json:"seconds,omitempty"`

	Cost *BifrostCost `json:"cost,omitempty"`
}
type BifrostTranscriptionRequest struct {
	Provider       ModelProvider            `json:"provider"`
	Model          string                   `json:"model"`
	Input          *TranscriptionInput      `json:"input,omitempty"`
	Params         *TranscriptionParameters `json:"params,omitempty"`
	Fallbacks      []Fallback               `json:"fallbacks,omitempty"`
	RawRequestBody []byte                   `json:"-"`
}
type TranscriptionExportOptions string
type TranscriptionInput struct {
	File     []byte `json:"file"`
	Filename string `json:"filename,omitempty"`
}
type TranscriptionStreamResponseType string

func (r *BifrostTranscriptionRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

func (r BifrostTranscriptionResponse) MarshalJSON() ([]byte, error) {
	type Alias BifrostTranscriptionResponse
	if r.DiarizedSegments != nil {
		return json.Marshal(&struct {
			Segments   []TranscriptionDiarizedSegment `json:"segments"`
			IsDiarized bool                           `json:"is_diarized,omitempty"`
			Alias
		}{Segments: r.DiarizedSegments, IsDiarized: true, Alias: Alias(r)})
	}
	return json.Marshal(Alias(r))
}

func (r *BifrostTranscriptionResponse) UnmarshalJSON(data []byte) error {
	type Alias BifrostTranscriptionResponse
	aux := &struct {
		Segments   json.RawMessage `json:"segments"`
		IsDiarized bool            `json:"is_diarized,omitempty"`
		*Alias
	}{Alias: (*Alias)(r)}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	if len(aux.Segments) == 0 || string(aux.Segments) == "null" {
		if aux.IsDiarized {
			r.DiarizedSegments = []TranscriptionDiarizedSegment{}
		}
		return nil
	}

	if aux.IsDiarized {
		var diarized []TranscriptionDiarizedSegment
		if err := json.Unmarshal(aux.Segments, &diarized); err != nil {
			return err
		}
		r.DiarizedSegments = diarized
		return nil
	}

	var verbose []TranscriptionSegment
	if err := json.Unmarshal(aux.Segments, &verbose); err == nil {
		r.Segments = verbose
		return nil
	}

	var diarized []TranscriptionDiarizedSegment
	if err := json.Unmarshal(aux.Segments, &diarized); err != nil {
		return err
	}
	r.DiarizedSegments = diarized
	return nil
}

func (r *BifrostTranscriptionResponse) BackfillParams(req *BifrostTranscriptionRequest) {
	if r == nil || req == nil || req.Params == nil || req.Params.ResponseFormat == nil {
		return
	}
	r.ResponseFormat = req.Params.ResponseFormat
}

func IsPlainTextTranscriptionFormat(format *string) bool {
	if format == nil {
		return false
	}
	switch *format {
	case "text", "srt", "vtt":
		return true
	default:
		return false
	}
}

func IsDiarizedTranscriptionFormat(format *string) bool {
	return format != nil && *format == "diarized_json"
}

const (
	TranscriptionStreamResponseTypeDelta TranscriptionStreamResponseType = "transcript.text.delta"
	TranscriptionStreamResponseTypeDone  TranscriptionStreamResponseType = "transcript.text.done"
)
