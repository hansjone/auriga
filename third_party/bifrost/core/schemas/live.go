// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"fmt"
	"io"
)

type LiveContentResponse struct {
	SessionID     string        `json:"session_id"`
	Body          io.ReadCloser `json:"-"`
	ContentType   string        `json:"content_type,omitempty"`
	ContentLength int64         `json:"content_length,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type LiveAudioOutput struct {
	Voice *LiveVoice `json:"voice,omitempty"`
}
type LiveResponsesDelegation struct {
	Model             string                        `json:"model,omitempty"`
	Instructions      *string                       `json:"instructions,omitempty"`
	MaxOutputTokens   *int                          `json:"max_output_tokens,omitempty"`
	ParallelToolCalls *bool                         `json:"parallel_tool_calls,omitempty"`
	Reasoning         *ResponsesParametersReasoning `json:"reasoning,omitempty"`
	ServiceTier       *BifrostServiceTier           `json:"service_tier,omitempty"`
	Text              *ResponsesTextConfig          `json:"text,omitempty"`
	ToolChoice        *ResponsesToolChoice          `json:"tool_choice,omitempty"`
	Tools             []ResponsesTool               `json:"tools,omitempty"`
}
type BifrostLiveContentRequest struct {
	Provider  ModelProvider `json:"provider"`
	SessionID string        `json:"session_id"`
}
type LiveSessionLog struct {
	Transport         string               `json:"transport,omitempty"`
	ProviderSessionID string               `json:"provider_session_id,omitempty"`
	VoiceSeconds      float64              `json:"voice_seconds"`
	VoiceCost         *float64             `json:"voice_cost,omitempty"`
	BackendCost       *float64             `json:"backend_cost,omitempty"`
	Transcript        []LiveTranscriptLine `json:"transcript,omitempty"`
	Delegations       []LiveDelegationLog  `json:"delegations,omitempty"`
}
type LiveVoice struct {
	Name   *string
	Custom *LiveCustomVoice
}
type LiveAudioFormat struct {
	Type string `json:"type"`
	Rate int    `json:"rate"`
}
type LiveAudio struct {
	Format *LiveAudioFormat `json:"format,omitempty"`
	Output *LiveAudioOutput `json:"output,omitempty"`
}
type LiveEventPayload struct {
	Response *BifrostResponsesStreamResponse
	DTMF     *string
}
type LiveSession struct {
	ID        string `json:"id,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Status    string `json:"status,omitempty"`
	Type      string `json:"type,omitempty"`

	Model        string                `json:"model,omitempty"`
	Instructions *string               `json:"instructions,omitempty"`
	Audio        *LiveAudio            `json:"audio,omitempty"`
	Delegation   *LiveDelegationConfig `json:"delegation,omitempty"`
	Store        *bool                 `json:"store,omitempty"`
}
type LiveDelegationLog struct {
	DelegationID string             `json:"delegation_id,omitempty"`
	RequestID    string             `json:"request_id"`
	ResponseIDs  []string           `json:"response_ids,omitempty"`
	Model        string             `json:"model"`
	StartedMs    int64              `json:"started_ms,omitempty"`
	Usage        *BifrostLLMUsage   `json:"usage,omitempty"`
	Cost         *float64           `json:"cost,omitempty"`
	Output       []ResponsesMessage `json:"output,omitempty"`
	Error        string             `json:"error,omitempty"`
}
type LiveTranscriptLine struct {
	Role    string `json:"role"`
	Text    string `json:"text"`
	StartMs int64  `json:"start_ms,omitempty"`
	EndMs   int64  `json:"end_ms,omitempty"`
}
type LiveCustomVoice struct {
	ID string `json:"id"`
}
type LiveDelegationType string
type LiveDelegationConfig struct {
	Type      LiveDelegationType       `json:"type"`
	Responses *LiveResponsesDelegation `json:"responses,omitempty"`
}

func (v LiveVoice) MarshalJSON() ([]byte, error) {
	if v.Name != nil && v.Custom != nil {
		return nil, fmt.Errorf("both Name and Custom are set; only one should be non-nil")
	}
	if v.Name != nil {
		return MarshalSorted(v.Name)
	}
	if v.Custom != nil {
		return MarshalSorted(v.Custom)
	}
	return MarshalSorted(nil)
}

func (v *LiveVoice) UnmarshalJSON(data []byte) error {
	var name string
	if err := Unmarshal(data, &name); err == nil {
		v.Name = &name
		return nil
	}

	var custom LiveCustomVoice
	if err := Unmarshal(data, &custom); err == nil && custom.ID != "" {
		v.Custom = &custom
	}
	return nil
}

func (p LiveEventPayload) MarshalJSON() ([]byte, error) {
	if p.Response != nil && p.DTMF != nil {
		return nil, fmt.Errorf("both Response and DTMF are set; only one should be non-nil")
	}
	if p.Response != nil {
		return MarshalSorted(p.Response)
	}
	if p.DTMF != nil {
		return MarshalSorted(p.DTMF)
	}
	return MarshalSorted(nil)
}

func (p *LiveEventPayload) UnmarshalJSON(data []byte) error {
	var dtmf string
	if err := Unmarshal(data, &dtmf); err == nil {
		p.DTMF = &dtmf
		return nil
	}
	var response BifrostResponsesStreamResponse
	if err := Unmarshal(data, &response); err != nil {
		return err
	}
	p.Response = &response
	return nil
}
