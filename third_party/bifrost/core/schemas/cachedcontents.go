package schemas

type BifrostCachedContentCreateRequest struct {
	Provider          ModelProvider `json:"provider"`
	Model             string        `json:"model"`
	DisplayName       *string       `json:"display_name,omitempty"`
	SystemInstruction any           `json:"system_instruction,omitempty"`
	Contents          []any         `json:"contents,omitempty"`
	Tools             []any         `json:"tools,omitempty"`
	ToolConfig        any           `json:"tool_config,omitempty"`
	TTL               *string       `json:"ttl,omitempty"`
	ExpireTime        *string       `json:"expire_time,omitempty"`

	RawRequestBody []byte         `json:"-"`
	ExtraParams    map[string]any `json:"-"`
}
type BifrostCachedContentUpdateRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	Name string `json:"name"`

	TTL        *string `json:"ttl,omitempty"`
	ExpireTime *string `json:"expire_time,omitempty"`

	RawRequestBody []byte         `json:"-"`
	ExtraParams    map[string]any `json:"-"`
}
type BifrostCachedContentRetrieveRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	Name string `json:"name"`

	RawRequestBody []byte         `json:"-"`
	ExtraParams    map[string]any `json:"-"`
}
type BifrostCachedContentDeleteRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	Name string `json:"name"`

	RawRequestBody []byte         `json:"-"`
	ExtraParams    map[string]any `json:"-"`
}
type BifrostCachedContentUpdateResponse struct {
	Name              string         `json:"name"`
	DisplayName       string         `json:"display_name,omitempty"`
	Model             string         `json:"model"`
	SystemInstruction any            `json:"system_instruction,omitempty"`
	Contents          []any          `json:"contents,omitempty"`
	Tools             []any          `json:"tools,omitempty"`
	ToolConfig        any            `json:"tool_config,omitempty"`
	CreateTime        string         `json:"create_time,omitempty"`
	UpdateTime        string         `json:"update_time,omitempty"`
	ExpireTime        string         `json:"expire_time,omitempty"`
	UsageMetadata     map[string]any `json:"usage_metadata,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostCachedContentCreateResponse struct {
	Name              string         `json:"name"`
	DisplayName       string         `json:"display_name,omitempty"`
	Model             string         `json:"model"`
	SystemInstruction any            `json:"system_instruction,omitempty"`
	Contents          []any          `json:"contents,omitempty"`
	Tools             []any          `json:"tools,omitempty"`
	ToolConfig        any            `json:"tool_config,omitempty"`
	CreateTime        string         `json:"create_time,omitempty"`
	UpdateTime        string         `json:"update_time,omitempty"`
	ExpireTime        string         `json:"expire_time,omitempty"`
	UsageMetadata     map[string]any `json:"usage_metadata,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostCachedContentDeleteResponse struct {
	Name    string `json:"name,omitempty"`
	Deleted bool   `json:"deleted"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostCachedContentListResponse struct {
	CachedContents []CachedContentObject `json:"cached_contents"`
	NextPageToken  string                `json:"next_page_token,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostCachedContentRetrieveResponse struct {
	Name              string         `json:"name"`
	DisplayName       string         `json:"display_name,omitempty"`
	Model             string         `json:"model"`
	SystemInstruction any            `json:"system_instruction,omitempty"`
	Contents          []any          `json:"contents,omitempty"`
	Tools             []any          `json:"tools,omitempty"`
	ToolConfig        any            `json:"tool_config,omitempty"`
	CreateTime        string         `json:"create_time,omitempty"`
	UpdateTime        string         `json:"update_time,omitempty"`
	ExpireTime        string         `json:"expire_time,omitempty"`
	UsageMetadata     map[string]any `json:"usage_metadata,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostCachedContentListRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	PageSize  int     `json:"page_size,omitempty"`
	PageToken *string `json:"page_token,omitempty"`

	RawRequestBody []byte         `json:"-"`
	ExtraParams    map[string]any `json:"-"`
}
type CachedContentObject struct {
	Name              string         `json:"name"`
	DisplayName       string         `json:"display_name,omitempty"`
	Model             string         `json:"model"`
	SystemInstruction any            `json:"system_instruction,omitempty"`
	Contents          []any          `json:"contents,omitempty"`
	Tools             []any          `json:"tools,omitempty"`
	ToolConfig        any            `json:"tool_config,omitempty"`
	CreateTime        string         `json:"create_time,omitempty"`
	UpdateTime        string         `json:"update_time,omitempty"`
	ExpireTime        string         `json:"expire_time,omitempty"`
	UsageMetadata     map[string]any `json:"usage_metadata,omitempty"`
}

func (r *BifrostCachedContentCreateRequest) GetRawRequestBody() []byte { return r.RawRequestBody }

func (r *BifrostCachedContentListRequest) GetRawRequestBody() []byte { return r.RawRequestBody }

func (r *BifrostCachedContentRetrieveRequest) GetRawRequestBody() []byte { return r.RawRequestBody }

func (r *BifrostCachedContentUpdateRequest) GetRawRequestBody() []byte { return r.RawRequestBody }

func (r *BifrostCachedContentDeleteRequest) GetRawRequestBody() []byte { return r.RawRequestBody }
