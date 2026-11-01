package schemas

import (
	"fmt"
	"unicode/utf8"
)

type SpeechStreamResponseType string
type SpeechInput struct {
	Input string `json:"input"`
}
type BifrostSpeechResponse struct {
	Audio               []byte                     `json:"audio"`
	Usage               *SpeechUsage               `json:"usage"`
	Alignment           *SpeechAlignment           `json:"alignment,omitempty"`
	NormalizedAlignment *SpeechAlignment           `json:"normalized_alignment,omitempty"`
	AudioBase64         *string                    `json:"audio_base64,omitempty"`
	ExtraFields         BifrostResponseExtraFields `json:"extra_fields"`
}
type SpeechUsageInputTokenDetails struct {
	TextTokens  int `json:"text_tokens,omitempty"`
	AudioTokens int `json:"audio_tokens,omitempty"`
}
type SpeechVoiceInput struct {
	Voice            *string
	MultiVoiceConfig []VoiceConfig
}
type SpeechPronunciationDictionaryLocator struct {
	PronunciationDictionaryID string  `json:"pronunciation_dictionary_id"`
	VersionID                 *string `json:"version_id,omitempty"`
}
type BifrostSpeechRequest struct {
	Provider       ModelProvider     `json:"provider"`
	Model          string            `json:"model"`
	Input          *SpeechInput      `json:"input,omitempty"`
	Params         *SpeechParameters `json:"params,omitempty"`
	Fallbacks      []Fallback        `json:"fallbacks,omitempty"`
	RawRequestBody []byte            `json:"-"`
}
type BifrostSpeechStreamResponse struct {
	Type        SpeechStreamResponseType   `json:"type"`
	Audio       []byte                     `json:"audio"`
	Usage       *SpeechUsage               `json:"usage"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type SpeechAlignment struct {
	CharStartTimesMs []float64 `json:"char_start_times_ms"`
	CharEndTimesMs   []float64 `json:"char_end_times_ms"`
	Characters       []string  `json:"characters"`
}
type SpeechUsage struct {
	InputTokens       int                           `json:"input_tokens"`
	InputChars        int                           `json:"input_chars,omitempty"`
	InputTokenDetails *SpeechUsageInputTokenDetails `json:"input_token_details,omitempty"`
	OutputTokens      int                           `json:"output_tokens"`
	TotalTokens       int                           `json:"total_tokens"`

	AudioSeconds int `json:"audio_seconds,omitempty"`

	Cost *BifrostCost `json:"cost,omitempty"`
}
type SpeechParameters struct {
	VoiceConfig    *SpeechVoiceInput `json:"voice"`
	Instructions   string            `json:"instructions,omitempty"`
	ResponseFormat string            `json:"response_format,omitempty"`
	Speed          *float64          `json:"speed,omitempty"`

	LanguageCode                    *string                                `json:"language_code,omitempty"`
	PronunciationDictionaryLocators []SpeechPronunciationDictionaryLocator `json:"pronunciation_dictionary_locators,omitempty"`
	EnableLogging                   *bool                                  `json:"enable_logging,omitempty"`
	OptimizeStreamingLatency        *bool                                  `json:"optimize_streaming_latency,omitempty"`
	WithTimestamps                  *bool                                  `json:"with_timestamps,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type VoiceConfig struct {
	Speaker string `json:"speaker"`
	Voice   string `json:"voice"`
}

func (r *BifrostSpeechRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

func (r *BifrostSpeechResponse) BackfillParams(request *BifrostSpeechRequest) {
	if r == nil || request == nil || request.Input == nil {
		return
	}
	if r.Usage == nil {
		r.Usage = &SpeechUsage{}
	}

	if r.Usage.InputChars == 0 {
		r.Usage.InputChars = utf8.RuneCountInString(request.Input.Input)
	}
}

func (vi *SpeechVoiceInput) MarshalJSON() ([]byte, error) {

	if vi.Voice != nil && len(vi.MultiVoiceConfig) > 0 {
		return nil, fmt.Errorf("both Voice and MultiVoiceConfig are set; only one should be non-nil")
	}

	if vi.Voice != nil {
		return MarshalSorted(*vi.Voice)
	}
	if len(vi.MultiVoiceConfig) > 0 {
		return MarshalSorted(vi.MultiVoiceConfig)
	}

	return MarshalSorted(nil)
}

func (vi *SpeechVoiceInput) UnmarshalJSON(data []byte) error {

	vi.Voice = nil
	vi.MultiVoiceConfig = nil

	var stringContent string
	if err := Unmarshal(data, &stringContent); err == nil {
		vi.Voice = &stringContent
		return nil
	}

	var voiceConfigs []VoiceConfig
	if err := Unmarshal(data, &voiceConfigs); err == nil {

		validConfigs := make([]VoiceConfig, 0, len(voiceConfigs))
		for _, config := range voiceConfigs {
			if config.Voice == "" {
				return fmt.Errorf("voice config has empty voice field")
			}
			validConfigs = append(validConfigs, config)
		}
		vi.MultiVoiceConfig = validConfigs
		return nil
	}

	return fmt.Errorf("voice field is neither a string, nor an array of VoiceConfig objects")
}

const (
	SpeechStreamResponseTypeDelta SpeechStreamResponseType = "speech.audio.delta"
	SpeechStreamResponseTypeDone  SpeechStreamResponseType = "speech.audio.done"
)

func (r *BifrostSpeechStreamResponse) BackfillParams(request *BifrostSpeechRequest) {
	if r == nil || request == nil || request.Input == nil {
		return
	}
	if r.Usage == nil {
		r.Usage = &SpeechUsage{}
	}
	r.Usage.InputChars = utf8.RuneCountInString(request.Input.Input)
}
