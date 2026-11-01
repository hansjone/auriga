package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

type DecisionInputPart struct {
	Type     string        `json:"type"`
	Text     *DecisionText `json:"text,omitempty"`
	ImageURL *string       `json:"image_url,omitempty"`
	Detail   *string       `json:"detail,omitempty"`
	ID       *string       `json:"id,omitempty"`
	Data     DecisionData  `json:"data,omitzero"`
	Name     *string       `json:"name,omitempty"`

	Refusal               *string                `json:"refusal,omitempty"`
	PromptCacheBreakpoint *PromptCacheBreakpoint `json:"prompt_cache_breakpoint,omitempty"`
}
type DecisionAnswer struct {
	Type          DecisionType          `json:"type"`
	Name          *string               `json:"name,omitempty"`
	Probability   *float64              `json:"probability,omitempty"`
	Choice        *DecisionScalar       `json:"choice,omitempty"`
	Score         *float64              `json:"score,omitempty"`
	Probabilities []DecisionProbability `json:"probabilities,omitempty"`
	Confidence    *float64              `json:"confidence,omitempty"`
	Legend        map[string]any        `json:"legend,omitempty"`
	Box           *DecisionBox          `json:"box,omitempty"`

	AnswerConfidence    *float64        `json:"answer_confidence,omitempty"`
	Action              json.RawMessage `json:"action,omitempty"`
	Abstention          *string         `json:"abstention,omitempty"`
	AbstentionThreshold *float64        `json:"abstention_threshold,omitempty"`
	LowConfidence       *bool           `json:"low_confidence,omitempty"`
}
type DecisionInputMessage struct {
	Type    *string              `json:"type,omitempty"`
	Role    string               `json:"role,omitempty"`
	Content DecisionInputContent `json:"content,omitzero"`
	Phase   *string              `json:"phase,omitempty"`

	ID        *string               `json:"id,omitempty"`
	CallID    *string               `json:"call_id,omitempty"`
	Name      *string               `json:"name,omitempty"`
	Namespace *string               `json:"namespace,omitempty"`
	Arguments *string               `json:"arguments,omitempty"`
	Input     *string               `json:"input,omitempty"`
	Output    *DecisionInputContent `json:"output,omitempty"`
	Status    *string               `json:"status,omitempty"`
}
type DecisionInput struct {
	Text       *string
	Messages   []DecisionInputMessage
	Structured interface{}
}
type DecisionText struct {
	Text       *string
	Structured interface{}
}
type DecisionScalar struct {
	Str  *string
	Bool *bool
	Num  *float64
}
type DecisionData struct {
	File       []byte
	Structured interface{}
}
type DecisionCriteria struct {
	True  *DecisionText `json:"true,omitempty"`
	False *DecisionText `json:"false,omitempty"`
}
type BifrostDecisionRequest struct {
	Provider         ModelProvider          `json:"provider"`
	Model            string                 `json:"model"`
	Input            DecisionInput          `json:"input"`
	Questions        []DecisionQuestion     `json:"questions"`
	SafetyIdentifier *string                `json:"safety_identifier,omitempty"`
	Fallbacks        []Fallback             `json:"fallbacks,omitempty"`
	RawRequestBody   []byte                 `json:"-"`
	ExtraParams      map[string]interface{} `json:"-"`

	PromptCacheKey     *string             `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions *PromptCacheOptions `json:"prompt_cache_options,omitempty"`
}
type BifrostDecisionResponse struct {
	ID             string                     `json:"id,omitempty"`
	Model          string                     `json:"model"`
	Answers        []DecisionAnswer           `json:"answers"`
	Usage          *BifrostLLMUsage           `json:"usage,omitempty"`
	ExtraFields    BifrostResponseExtraFields `json:"extra_fields"`
	NativeResponse json.RawMessage            `json:"-"`

	Routing json.RawMessage `json:"routing,omitempty"`
}
type DecisionLevel struct {
	Label       string        `json:"label"`
	Description *DecisionText `json:"description,omitempty"`
}
type DecisionType string
type DecisionInputContent struct {
	Text  *string
	Parts []DecisionInputPart
}
type DecisionChoice struct {
	Value       DecisionScalar `json:"value"`
	Description *DecisionText  `json:"description,omitempty"`
}
type DecisionBox struct {
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
}
type DecisionProbability struct {
	Value       DecisionScalar `json:"value"`
	Label       *string        `json:"label,omitempty"`
	Probability float64        `json:"probability"`
}
type DecisionQuestion struct {
	Type         DecisionType      `json:"type"`
	Name         *string           `json:"name,omitempty"`
	Instructions *DecisionText     `json:"instructions,omitempty"`
	Criteria     *DecisionCriteria `json:"criteria,omitempty"`
	Choices      []DecisionChoice  `json:"choices,omitempty"`
	Levels       []DecisionLevel   `json:"levels,omitempty"`
}

func (r *BifrostDecisionRequest) GetRawRequestBody() []byte {
	return r.RawRequestBody
}

const (
	DecisionTypePredicate DecisionType = "predicate"

	DecisionTypeChoice DecisionType = "choice"

	DecisionTypeScore DecisionType = "score"

	DecisionTypeBox DecisionType = "box"

	DecisionTypeRefusal DecisionType = "refusal"
)

func (t DecisionType) IsQuestionType() bool {
	return t == DecisionTypePredicate || t == DecisionTypeChoice || t == DecisionTypeScore || t == DecisionTypeBox
}

func (in DecisionInput) IsEmpty() bool {
	return in.Text == nil && in.Messages == nil && in.Structured == nil
}

func (in DecisionInput) Validate() error {
	set := 0
	for _, present := range []bool{in.Text != nil, in.Messages != nil, in.Structured != nil} {
		if present {
			set++
		}
	}
	if set > 1 {
		return fmt.Errorf("decision input carries more than one of text, messages, and structured input; send exactly one")
	}
	return nil
}

func (in DecisionInput) MarshalJSON() ([]byte, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	switch {
	case in.Text != nil:
		return Marshal(*in.Text)
	case in.Messages != nil:
		return Marshal(in.Messages)
	case in.Structured != nil:
		return Marshal(in.Structured)
	}
	return Marshal(nil)
}

func (in *DecisionInput) UnmarshalJSON(data []byte) error {
	*in = DecisionInput{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := Unmarshal(trimmed, &text); err != nil {
			return err
		}
		in.Text = &text
		return nil
	case '[':
		var messages []DecisionInputMessage
		if err := Unmarshal(trimmed, &messages); err != nil {
			return fmt.Errorf("decision input array must be a list of messages: %w", err)
		}
		in.Messages = messages
		return nil
	case '{':
		var structured map[string]interface{}
		if err := Unmarshal(trimmed, &structured); err != nil {
			return err
		}
		in.Structured = structured
		return nil
	}
	return fmt.Errorf("decision input must be a string, an array of messages, or an object")
}

const (
	DecisionInputItemTypeMessage              = "message"
	DecisionInputItemTypeFunctionCall         = "function_call"
	DecisionInputItemTypeFunctionCallOutput   = "function_call_output"
	DecisionInputItemTypeCustomToolCall       = "custom_tool_call"
	DecisionInputItemTypeCustomToolCallOutput = "custom_tool_call_output"
)

func (c DecisionInputContent) MarshalJSON() ([]byte, error) {
	if c.Text != nil && c.Parts != nil {
		return nil, fmt.Errorf("both decision message text and parts are set; only one should be non-nil")
	}
	if c.Text != nil {
		return Marshal(*c.Text)
	}
	if c.Parts != nil {
		return Marshal(c.Parts)
	}
	return Marshal(nil)
}

func (c *DecisionInputContent) UnmarshalJSON(data []byte) error {
	*c = DecisionInputContent{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := Unmarshal(trimmed, &text); err == nil {
			c.Text = &text
			return nil
		}
	} else if trimmed[0] == '[' {
		var parts []DecisionInputPart
		if err := Unmarshal(trimmed, &parts); err == nil {
			c.Parts = parts
			return nil
		}
	}
	return fmt.Errorf("decision message content must be a string or an array of parts")
}

const (
	DecisionInputPartTypeText = "input_text"

	DecisionInputPartTypeImage = "input_image"

	DecisionInputPartTypeJSON = "input_json"

	DecisionInputPartTypeOutputText = "output_text"

	DecisionInputPartTypeRefusal = "refusal"
)

func (d DecisionData) MarshalJSON() ([]byte, error) {
	if d.File != nil && d.Structured != nil {
		return nil, fmt.Errorf("both decision part file and structured data are set; only one should be non-nil")
	}
	if d.Structured != nil {
		return MarshalSorted(d.Structured)
	}
	return Marshal(d.File)
}

func (d *DecisionData) UnmarshalJSON(data []byte) error {
	*d = DecisionData{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.UseNumber()
		return decoder.Decode(&d.Structured)
	}
	return Unmarshal(trimmed, &d.File)
}

func (p DecisionInputPart) IsTextual() bool {
	switch p.Type {
	case DecisionInputPartTypeText, DecisionInputPartTypeOutputText, DecisionInputPartTypeRefusal, DecisionInputPartTypeJSON:
		return true
	}
	return false
}

func NewDecisionText(text string) *DecisionText {
	return &DecisionText{Text: &text}
}

func (t *DecisionText) Value() interface{} {
	if t == nil {
		return nil
	}
	if t.Text != nil {
		return *t.Text
	}
	return t.Structured
}

func (t DecisionText) MarshalJSON() ([]byte, error) {
	if t.Text != nil && t.Structured != nil {
		return nil, fmt.Errorf("both decision text and structured value are set; only one should be non-nil")
	}
	return Marshal(t.Value())
}

func (t *DecisionText) UnmarshalJSON(data []byte) error {
	*t = DecisionText{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	switch trimmed[0] {
	case '"':
		var text string
		if err := Unmarshal(trimmed, &text); err != nil {
			return err
		}
		t.Text = &text
		return nil
	case '{', '[':
		var structured interface{}
		if err := Unmarshal(trimmed, &structured); err != nil {
			return err
		}
		t.Structured = structured
		return nil
	}
	return fmt.Errorf("decision text must be a string, object, or array")
}

func (c *DecisionCriteria) UnmarshalJSON(data []byte) error {
	var sides map[string]json.RawMessage
	if err := Unmarshal(data, &sides); err != nil {
		return err
	}
	*c = DecisionCriteria{}
	for key, side := range map[string]**DecisionText{"true": &c.True, "false": &c.False} {
		raw, present := sides[key]
		if !present {
			continue
		}
		text := &DecisionText{}
		if err := text.UnmarshalJSON(raw); err != nil {
			return fmt.Errorf("criteria %q: %w", key, err)
		}
		*side = text
	}
	return nil
}

func (c DecisionChoice) Key() (string, error) {
	switch {
	case c.Value.Str != nil && c.Value.Bool == nil && c.Value.Num == nil:
		return *c.Value.Str, nil
	case c.Value.Bool != nil && c.Value.Str == nil && c.Value.Num == nil:
		return strconv.FormatBool(*c.Value.Bool), nil
	}
	return "", fmt.Errorf("choice value must be exactly one of a string or a boolean")
}

func (s DecisionScalar) Key() (string, bool) {
	switch {
	case s.Str != nil:
		return *s.Str, true
	case s.Bool != nil:
		return strconv.FormatBool(*s.Bool), true
	case s.Num != nil:
		return strconv.FormatFloat(*s.Num, 'f', -1, 64), true
	}
	return "", false
}

func (s DecisionScalar) MarshalJSON() ([]byte, error) {
	set := 0
	for _, present := range []bool{s.Str != nil, s.Bool != nil, s.Num != nil} {
		if present {
			set++
		}
	}
	if set > 1 {
		return nil, fmt.Errorf("decision scalar has more than one value set; only one should be non-nil")
	}
	switch {
	case s.Str != nil:
		return Marshal(*s.Str)
	case s.Bool != nil:
		return Marshal(*s.Bool)
	case s.Num != nil:
		return Marshal(*s.Num)
	}
	return Marshal(nil)
}

func (s *DecisionScalar) UnmarshalJSON(data []byte) error {
	*s = DecisionScalar{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var str string
	if err := Unmarshal(trimmed, &str); err == nil {
		s.Str = &str
		return nil
	}
	var b bool
	if err := Unmarshal(trimmed, &b); err == nil {
		s.Bool = &b
		return nil
	}
	var n float64
	if err := Unmarshal(trimmed, &n); err == nil {
		s.Num = &n
		return nil
	}
	return fmt.Errorf("decision scalar must be a string, boolean, or number")
}
