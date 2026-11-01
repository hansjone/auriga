package schemas

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
)

type ModelReasoning struct {
	Mandatory        *bool    `json:"mandatory,omitempty"`
	DefaultEnabled   *bool    `json:"default_enabled,omitempty"`
	SupportedEfforts []string `json:"supported_efforts,omitempty"`
	DefaultEffort    *string  `json:"default_effort,omitempty"`
}
type TopProvider struct {
	IsModerated         *bool `json:"is_moderated,omitempty"`
	ContextLength       *int  `json:"context_length,omitempty"`
	MaxCompletionTokens *int  `json:"max_completion_tokens,omitempty"`
}
type KeyStatus struct {
	KeyID    string        `json:"key_id"`
	Status   KeyStatusType `json:"status"`
	Provider ModelProvider `json:"provider"`
	Error    *BifrostError `json:"error,omitempty"`
}
type Architecture struct {
	Modality         *string  `json:"modality,omitempty"`
	Tokenizer        *string  `json:"tokenizer,omitempty"`
	InstructType     *string  `json:"instruct_type,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
}
type DefaultParameters struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
}
type BifrostListModelsResponse struct {
	Data          []Model                    `json:"data"`
	ExtraFields   BifrostResponseExtraFields `json:"extra_fields"`
	NextPageToken string                     `json:"next_page_token,omitempty"`

	KeyStatuses []KeyStatus `json:"key_statuses,omitempty"`

	FirstID *string `json:"-"`
	LastID  *string `json:"-"`
	HasMore *bool   `json:"-"`
}
type Model struct {
	ID                  string             `json:"id"`
	CanonicalSlug       *string            `json:"canonical_slug,omitempty"`
	Name                *string            `json:"name,omitempty"`
	NormalizedName      *string            `json:"normalized_name,omitempty"`
	Alias               *string            `json:"alias,omitempty"`
	Created             *int64             `json:"created,omitempty"`
	ContextLength       *int               `json:"context_length,omitempty"`
	MaxInputTokens      *int               `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     *int               `json:"max_output_tokens,omitempty"`
	Architecture        *Architecture      `json:"architecture,omitempty"`
	IsDeprecated        bool               `json:"is_deprecated,omitempty"`
	ShutdownDate        *string            `json:"shutdown_date,omitempty"`
	Pricing             *Pricing           `json:"pricing,omitempty"`
	TopProvider         *TopProvider       `json:"top_provider,omitempty"`
	PerRequestLimits    *PerRequestLimits  `json:"per_request_limits,omitempty"`
	SupportedParameters []string           `json:"supported_parameters,omitempty"`
	DefaultParameters   *DefaultParameters `json:"default_parameters,omitempty"`
	Reasoning           *ModelReasoning    `json:"reasoning,omitempty"`
	HuggingFaceID       *string            `json:"hugging_face_id,omitempty"`
	Description         *string            `json:"description,omitempty"`

	AdditionalAttributes map[string]string `json:"additional_attributes,omitempty"`

	OwnedBy          *string  `json:"owned_by,omitempty"`
	SupportedMethods []string `json:"supported_methods,omitempty"`

	ProviderExtra json.RawMessage `json:"-"`
}
type BifrostListModelsRequest struct {
	Provider ModelProvider `json:"provider"`

	PageSize int `json:"page_size"`

	PageToken string `json:"page_token"`

	Unfiltered bool `json:"-"`

	KeyID *string `json:"-"`

	ExtraParams map[string]interface{} `json:"-"`
}
type Pricing struct {
	Prompt            *string `json:"prompt,omitempty"`
	Completion        *string `json:"completion,omitempty"`
	Request           *string `json:"request,omitempty"`
	Image             *string `json:"image,omitempty"`
	WebSearch         *string `json:"web_search,omitempty"`
	InternalReasoning *string `json:"internal_reasoning,omitempty"`
	InputCacheRead    *string `json:"input_cache_read,omitempty"`
	InputCacheWrite   *string `json:"input_cache_write,omitempty"`
}
type paginationCursor struct {
	Offset int    `json:"o"`
	LastID string `json:"l,omitempty"`
}
type ListModelsByKeyResult struct {
	Response *BifrostListModelsResponse
	Err      *BifrostError
	KeyID    string
}
type PerRequestLimits struct {
	PromptTokens     *int `json:"prompt_tokens,omitempty"`
	CompletionTokens *int `json:"completion_tokens,omitempty"`
}
type BifrostModelRetrieveRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    string        `json:"model"`
}
type BifrostModelRetrieveResponse struct {
	Model
	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}

const DefaultPageSize = 1000

func (k KeyStatus) MarshalJSON() ([]byte, error) {
	type Alias KeyStatus
	alias := Alias(k)
	if alias.Error != nil {
		errCopy := *alias.Error
		errCopy.ExtraFields.KeyStatuses = nil
		alias.Error = &errCopy
	}
	return MarshalSorted(alias)
}

func (response *BifrostListModelsResponse) ApplyPagination(pageSize int, pageToken string) *BifrostListModelsResponse {
	if response == nil {
		return nil
	}

	totalItems := len(response.Data)

	if pageSize <= 0 {
		return response
	}

	cursor := decodePaginationCursor(pageToken)
	offset := cursor.Offset

	if cursor.LastID != "" && !validatePaginationCursor(cursor, response.Data) {

		offset = resumeOffsetAfterLastID(response.Data, cursor.LastID, offset)
	}

	if offset >= totalItems {

		return &BifrostListModelsResponse{
			Data:          []Model{},
			ExtraFields:   response.ExtraFields,
			NextPageToken: "",
			KeyStatuses:   response.KeyStatuses,
		}
	}

	endIndex := offset + pageSize
	if endIndex > totalItems {
		endIndex = totalItems
	}

	paginatedData := response.Data[offset:endIndex]

	paginatedResponse := &BifrostListModelsResponse{
		Data:        paginatedData,
		ExtraFields: response.ExtraFields,
		KeyStatuses: response.KeyStatuses,
	}

	if endIndex < totalItems {

		var lastID string
		if len(paginatedData) > 0 {
			lastID = paginatedData[len(paginatedData)-1].ID
		}

		nextToken, err := encodePaginationCursor(endIndex, lastID)
		if err == nil {
			paginatedResponse.NextPageToken = nextToken
		}
	} else {
		paginatedResponse.NextPageToken = ""
	}

	return paginatedResponse
}

func encodePaginationCursor(offset int, lastID string) (string, error) {
	if offset <= 0 {
		return "", nil
	}

	cursor := paginationCursor{
		Offset: offset,
		LastID: lastID,
	}

	jsonData, err := MarshalSorted(cursor)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pagination cursor: %w", err)
	}

	encoded := base64.RawURLEncoding.EncodeToString(jsonData)
	return encoded, nil
}

func decodePaginationCursor(token string) paginationCursor {
	if token == "" {
		return paginationCursor{}
	}

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return paginationCursor{}
	}

	var cursor paginationCursor
	if err := Unmarshal(decoded, &cursor); err != nil {
		return paginationCursor{}
	}

	if cursor.Offset < 0 {
		return paginationCursor{}
	}

	return cursor
}

func validatePaginationCursor(cursor paginationCursor, data []Model) bool {
	if cursor.LastID == "" {
		return true
	}

	if cursor.Offset <= 0 || cursor.Offset > len(data) {
		return false
	}

	prevIndex := cursor.Offset - 1
	if prevIndex >= 0 && prevIndex < len(data) {
		return data[prevIndex].ID == cursor.LastID
	}

	return true
}

func resumeOffsetAfterLastID(data []Model, lastID string, fallback int) int {
	if sort.SliceIsSorted(data, func(i, j int) bool { return data[i].ID < data[j].ID }) {
		return sort.Search(len(data), func(i int) bool { return data[i].ID > lastID })
	}
	for i := range data {
		if data[i].ID == lastID {
			return i + 1
		}
	}
	if fallback > len(data) {
		return len(data)
	}
	return fallback
}
