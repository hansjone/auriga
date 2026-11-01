// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

type EmbeddingContentPart struct {
	Type        EmbeddingContentPartType `json:"type"`
	Text        *string                  `json:"text,omitempty"`
	Image       *EmbeddingMediaPart      `json:"image,omitempty"`
	Audio       *EmbeddingMediaPart      `json:"audio,omitempty"`
	File        *EmbeddingMediaPart      `json:"file,omitempty"`
	Video       *EmbeddingMediaPart      `json:"video,omitempty"`
	VideoConfig *EmbeddingVideoConfig    `json:"video_config,omitempty"`
	Tokens      []int                    `json:"tokens,omitempty"`
}
type EmbeddingContent []EmbeddingContentPart
type EmbeddingVideoSegment struct {
	StartOffsetSec int `json:"start_offset_sec"`
	EndOffsetSec   int `json:"end_offset_sec"`
}
type EmbeddingData struct {
	Index        int                    `json:"index"`
	Object       string                 `json:"object"`
	Modality     EmbeddingModality      `json:"modality,omitempty"`
	VideoSegment *EmbeddingVideoSegment `json:"video_segment,omitempty"`
	Embedding    EmbeddingStruct        `json:"embedding"`

	EncodingFormat string `json:"encoding_format,omitempty"`
}
type EmbeddingStruct struct {
	EmbeddingStr        *string
	EmbeddingArray      []float64
	Embedding2DArray    [][]float64
	EmbeddingInt8Array  []int8
	EmbeddingInt32Array []int32
}
type EmbeddingModality string
type EmbeddingVideoConfig struct {
	StartOffsetSec *int `json:"start_offset_sec,omitempty"`
	EndOffsetSec   *int `json:"end_offset_sec,omitempty"`
	IntervalSec    *int `json:"interval_sec,omitempty"`
}
type EmbeddingContentPartType string
type BifrostEmbeddingRequest struct {
	Provider       ModelProvider        `json:"provider"`
	Model          string               `json:"model"`
	Input          []EmbeddingInputItem `json:"input,omitempty"`
	Params         *EmbeddingParameters `json:"params,omitempty"`
	Fallbacks      []Fallback           `json:"fallbacks,omitempty"`
	RawRequestBody []byte               `json:"-"`
}
type EmbeddingInput []EmbeddingInputItem
type BifrostEmbeddingResponse struct {
	Data        []EmbeddingData            `json:"data"`
	Model       string                     `json:"model"`
	Object      string                     `json:"object"`
	Usage       *BifrostLLMUsage           `json:"usage"`
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type EmbeddingInputItem struct {
	Content EmbeddingContent     `json:"content"`
	Params  *EmbeddingParameters `json:"params,omitempty"`
}
type EmbeddingParameters struct {
	EncodingFormat *string `json:"encoding_format,omitempty"`
	Dimensions     *int    `json:"dimensions,omitempty"`
	TaskType       *string `json:"task_type,omitempty"`
	Title          *string `json:"title,omitempty"`
	AutoTruncate   *bool   `json:"auto_truncate,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type EmbeddingMediaPart struct {
	Data     *string `json:"data,omitempty"`
	URL      *string `json:"url,omitempty"`
	MIMEType *string `json:"mime_type,omitempty"`
	Filename *string `json:"filename,omitempty"`
}

func (r *BifrostEmbeddingRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

func (i *EmbeddingInputItem) UnmarshalJSON(data []byte) error {
	var content EmbeddingContent
	if err := Unmarshal(data, &content); err == nil {
		i.Content = content
		i.Params = nil
		return nil
	}

	type alias EmbeddingInputItem
	return Unmarshal(data, (*alias)(i))
}

func (i EmbeddingInputItem) MarshalJSON() ([]byte, error) {
	if i.Params == nil {
		return MarshalSorted(i.Content)
	}
	type alias EmbeddingInputItem
	return MarshalSorted(alias(i))
}

func (i *EmbeddingInputItem) EffectiveParams(defaultParams *EmbeddingParameters) *EmbeddingParameters {
	if i.Params != nil {
		return i.Params.Clone()
	}
	return defaultParams.Clone()
}

func (in EmbeddingInput) Contents() []EmbeddingContent {
	contents := make([]EmbeddingContent, len(in))
	for i, item := range in {
		contents[i] = item.Content
	}
	return contents
}

func (in EmbeddingInput) Texts() []string {
	var out []string
	for _, item := range in {
		for _, part := range item.Content {
			if part.Text != nil {
				out = append(out, *part.Text)
			}
		}
	}
	return out
}

func (in EmbeddingInput) TokenIDs() [][]int {
	var out [][]int
	for _, item := range in {
		for _, part := range item.Content {
			if len(part.Tokens) > 0 {
				out = append(out, part.Tokens)
			}
		}
	}
	return out
}

func (in EmbeddingInput) RejectPerItemParams() error {
	for i, item := range in {
		if item.Params != nil {
			return fmt.Errorf("this provider applies one set of embedding parameters to the whole request; input item %d cannot carry its own params", i)
		}
	}
	return nil
}

func (r *BifrostEmbeddingResponse) BackfillParams(request *BifrostEmbeddingRequest) {
	if r == nil || request == nil {
		return
	}
	if strings.TrimSpace(r.Model) == "" && strings.TrimSpace(request.Model) != "" {
		r.Model = request.Model
	}
}

const (
	EmbeddingContentPartTypeText   EmbeddingContentPartType = "text"
	EmbeddingContentPartTypeImage  EmbeddingContentPartType = "image"
	EmbeddingContentPartTypeAudio  EmbeddingContentPartType = "audio"
	EmbeddingContentPartTypeFile   EmbeddingContentPartType = "file"
	EmbeddingContentPartTypeVideo  EmbeddingContentPartType = "video"
	EmbeddingContentPartTypeTokens EmbeddingContentPartType = "tokens"
)

func (m *EmbeddingMediaPart) Validate() error {
	if m == nil {
		return fmt.Errorf("embedding media payload is nil")
	}
	set := 0
	if m.Data != nil {
		if *m.Data == "" {
			return fmt.Errorf("embedding media data is empty")
		}
		set++
	}
	if m.URL != nil {
		if *m.URL == "" {
			return fmt.Errorf("embedding media url is empty")
		}
		set++
	}
	if set != 1 {
		return fmt.Errorf("embedding media payload must set exactly one of data or url")
	}
	return nil
}

func (p EmbeddingContentPart) Validate() error {
	set := 0
	if p.Text != nil {
		set++
	}
	if p.Image != nil {
		set++
	}
	if p.Audio != nil {
		set++
	}
	if p.File != nil {
		set++
	}
	if p.Video != nil {
		set++
	}
	if len(p.Tokens) > 0 {
		set++
	}
	if set != 1 {
		return fmt.Errorf("embedding content part must set exactly one modality")
	}

	switch p.Type {
	case EmbeddingContentPartTypeText:
		if p.Text == nil || *p.Text == "" {
			return fmt.Errorf("embedding content part type %q requires text payload", p.Type)
		}
	case EmbeddingContentPartTypeImage:
		if p.Image == nil {
			return fmt.Errorf("embedding content part type %q requires image payload", p.Type)
		}
		return p.Image.Validate()
	case EmbeddingContentPartTypeAudio:
		if p.Audio == nil {
			return fmt.Errorf("embedding content part type %q requires audio payload", p.Type)
		}
		return p.Audio.Validate()
	case EmbeddingContentPartTypeFile:
		if p.File == nil {
			return fmt.Errorf("embedding content part type %q requires file payload", p.Type)
		}
		return p.File.Validate()
	case EmbeddingContentPartTypeVideo:
		if p.Video == nil {
			return fmt.Errorf("embedding content part type %q requires video payload", p.Type)
		}
		return p.Video.Validate()
	case EmbeddingContentPartTypeTokens:
		if len(p.Tokens) == 0 {
			return fmt.Errorf("embedding content part type %q requires tokens payload", p.Type)
		}
	default:
		return fmt.Errorf("unsupported embedding content part type %q", p.Type)
	}

	return nil
}

func (c EmbeddingContent) Validate() error {
	if len(c) == 0 {
		return fmt.Errorf("embedding content is empty")
	}
	for _, part := range c {
		if err := part.Validate(); err != nil {
			return err
		}
	}
	return nil
}

const (
	EmbeddingEncodingFloat   = "float"
	EmbeddingEncodingInt8    = "int8"
	EmbeddingEncodingUint8   = "uint8"
	EmbeddingEncodingBinary  = "binary"
	EmbeddingEncodingUbinary = "ubinary"
	EmbeddingEncodingBase64  = "base64"
)

func (p *EmbeddingParameters) Clone() *EmbeddingParameters {
	if p == nil {
		return nil
	}
	clone := *p
	clone.ExtraParams = maps.Clone(p.ExtraParams)
	return &clone
}

func (d *EmbeddingData) UnmarshalJSON(data []byte) error {

	type alias EmbeddingData
	aux := &struct {
		*alias
		Embedding json.RawMessage `json:"embedding"`
	}{alias: (*alias)(d)}
	if err := Unmarshal(data, aux); err != nil {
		return err
	}
	if len(aux.Embedding) == 0 {
		return nil
	}

	switch d.EncodingFormat {
	case EmbeddingEncodingInt8, EmbeddingEncodingBinary:
		var values []int8
		if err := Unmarshal(aux.Embedding, &values); err == nil {
			d.Embedding = EmbeddingStruct{EmbeddingInt8Array: values}
			return nil
		}
	case EmbeddingEncodingUint8, EmbeddingEncodingUbinary:
		var values []int32
		if err := Unmarshal(aux.Embedding, &values); err == nil {
			d.Embedding = EmbeddingStruct{EmbeddingInt32Array: values}
			return nil
		}
	}
	return d.Embedding.UnmarshalJSON(aux.Embedding)
}

func (be EmbeddingStruct) MarshalJSON() ([]byte, error) {
	if be.EmbeddingStr != nil {
		return MarshalSorted(be.EmbeddingStr)
	}
	if be.EmbeddingArray != nil {
		return MarshalSorted(be.EmbeddingArray)
	}
	if be.Embedding2DArray != nil {
		return MarshalSorted(be.Embedding2DArray)
	}
	if be.EmbeddingInt8Array != nil {
		return Marshal(be.EmbeddingInt8Array)
	}
	if be.EmbeddingInt32Array != nil {
		return Marshal(be.EmbeddingInt32Array)
	}
	return nil, fmt.Errorf("no embedding found")
}

func (be *EmbeddingStruct) UnmarshalJSON(data []byte) error {

	var stringContent string
	if err := Unmarshal(data, &stringContent); err == nil {
		be.EmbeddingStr = &stringContent
		return nil
	}

	var arrayContent []float64
	if err := Unmarshal(data, &arrayContent); err == nil {
		be.EmbeddingArray = arrayContent
		return nil
	}

	var arrayContent2D [][]float64
	if err := Unmarshal(data, &arrayContent2D); err == nil {
		be.Embedding2DArray = arrayContent2D
		return nil
	}

	var int8Content []int8
	if err := Unmarshal(data, &int8Content); err == nil {
		be.EmbeddingInt8Array = int8Content
		return nil
	}

	var int32Content []int32
	if err := Unmarshal(data, &int32Content); err == nil {
		be.EmbeddingInt32Array = int32Content
		return nil
	}

	return fmt.Errorf("embedding field is neither a string, []float64, [][]float64, []int8, nor []int32")
}
