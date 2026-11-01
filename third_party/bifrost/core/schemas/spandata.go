package schemas

type LLMSpanData struct {
	Provider      ModelProvider
	RequestType   RequestType
	RequestModel  string
	ResponseModel string
	Alias         string

	ResponseID        string
	Object            string
	SystemFingerprint string
	ServiceTier       string
	Created           int64
	FinishReasons     []string

	MessageCount   int
	InputMessages  []MessageSummary
	OutputMessages []MessageSummary
	ReasoningText  string

	Usage *BifrostLLMUsage

	Cost *BifrostCost

	RawRequest  string
	RawResponse string

	TimeToFirstChunkMs *float64
	TotalChunks        int

	Error *SpanError

	Chat           *ChatParameters
	Responses      *ResponsesParameters
	TextCompletion *TextCompletionParameters
	Embedding      *EmbeddingParameters
	Speech         *SpeechParameters
	Transcription  *TranscriptionParameters

	ResponsesEcho *ResponsesParameters

	TextPrompt     *TextCompletionInput
	EmbeddingInput EmbeddingInput
	SpeechInput    *SpeechInput

	Extra map[string]any
}
type AttachmentKind string
type ReasoningDetailSummary struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
type MessageSummary struct {
	Role             string                   `json:"role"`
	Content          string                   `json:"content"`
	Attachments      []AttachmentSummary      `json:"attachments,omitempty"`
	ToolCalls        []ToolCallSummary        `json:"tool_calls,omitempty"`
	Reasoning        string                   `json:"reasoning,omitempty"`
	ReasoningDetails []ReasoningDetailSummary `json:"reasoning_details,omitempty"`
	Audio            *AudioSummary            `json:"audio,omitempty"`
	Refusal          string                   `json:"refusal,omitempty"`
	ToolCallID       string                   `json:"tool_call_id,omitempty"`
}
type ToolCallSummary struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}
type AttachmentSummary struct {
	Kind      AttachmentKind `json:"kind"`
	MediaType string         `json:"media_type,omitempty"`
	URL       string         `json:"url,omitempty"`
	FileID    string         `json:"file_id,omitempty"`
	Filename  string         `json:"filename,omitempty"`
	Detail    string         `json:"detail,omitempty"`
	Format    string         `json:"format,omitempty"`

	Inline   bool `json:"inline,omitempty"`
	ByteSize int  `json:"byte_size,omitempty"`

	Data string `json:"data,omitempty"`
}
type AudioSummary struct {
	ID         string `json:"id,omitempty"`
	Transcript string `json:"transcript,omitempty"`
}
type SpanEnrichment struct {
	VirtualKeyID     string
	VirtualKeyName   string
	SelectedKeyID    string
	SelectedKeyName  string
	RoutingRuleID    string
	RoutingRuleName  string
	TeamID           string
	TeamName         string
	CustomerID       string
	CustomerName     string
	BusinessUnitID   string
	BusinessUnitName string
	ProjectID        string
	ProjectName      string
	PromptID         string
	PromptName       string
	PromptVersion    string
	UserID           string
	UserName         string
	UserEmail        string
	App              string

	TeamIDs           []string
	TeamNames         []string
	CustomerIDs       []string
	CustomerNames     []string
	BusinessUnitIDs   []string
	BusinessUnitNames []string

	RoutingEnginesUsed  []string
	ComplexityTier      string
	ComplexityMechanism string
	ComplexityScore     *float64

	Retries int

	FallbackIndex *int
}
type SpanError struct {
	Detail     *ErrorField
	StatusCode *int
	Type       string
}
