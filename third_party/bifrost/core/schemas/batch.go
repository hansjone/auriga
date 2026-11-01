// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type BatchRequestItem struct {
	CustomID string         `json:"custom_id"`
	Method   string         `json:"method,omitempty"`
	URL      string         `json:"url,omitempty"`
	Body     map[string]any `json:"body,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}
type BifrostBatchRetrieveResponse struct {
	ID               string             `json:"id"`
	Object           string             `json:"object,omitempty"`
	DisplayName      *string            `json:"display_name,omitempty"`
	Endpoint         string             `json:"endpoint,omitempty"`
	InputFileID      string             `json:"input_file_id,omitempty"`
	CompletionWindow string             `json:"completion_window,omitempty"`
	Status           BatchStatus        `json:"status"`
	RequestCounts    BatchRequestCounts `json:"request_counts,omitempty"`
	Metadata         map[string]string  `json:"metadata,omitempty"`
	CreatedAt        int64              `json:"created_at,omitempty"`
	ExpiresAt        *int64             `json:"expires_at,omitempty"`
	InProgressAt     *int64             `json:"in_progress_at,omitempty"`
	FinalizingAt     *int64             `json:"finalizing_at,omitempty"`
	CompletedAt      *int64             `json:"completed_at,omitempty"`
	FailedAt         *int64             `json:"failed_at,omitempty"`
	ExpiredAt        *int64             `json:"expired_at,omitempty"`
	CancellingAt     *int64             `json:"cancelling_at,omitempty"`
	CancelledAt      *int64             `json:"cancelled_at,omitempty"`

	OutputFileID *string      `json:"output_file_id,omitempty"`
	ErrorFileID  *string      `json:"error_file_id,omitempty"`
	Errors       *BatchErrors `json:"errors,omitempty"`

	ProcessingStatus *string `json:"processing_status,omitempty"`
	ResultsURL       *string `json:"results_url,omitempty"`
	ArchivedAt       *int64  `json:"archived_at,omitempty"`

	OperationName *string `json:"operation_name,omitempty"`
	Done          *bool   `json:"done,omitempty"`
	Progress      *int    `json:"progress,omitempty"`

	InputBlob  *string `json:"input_blob,omitempty"`
	OutputBlob *string `json:"output_blob,omitempty"`
	ErrorBlob  *string `json:"error_blob,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostBatchListResponse struct {
	Object  string                         `json:"object,omitempty"`
	Data    []BifrostBatchRetrieveResponse `json:"data"`
	FirstID *string                        `json:"first_id,omitempty"`
	LastID  *string                        `json:"last_id,omitempty"`
	HasMore bool                           `json:"has_more,omitempty"`

	NextCursor *string `json:"next_cursor,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostBatchCreateResponse struct {
	ID               string             `json:"id"`
	Object           string             `json:"object,omitempty"`
	DisplayName      *string            `json:"display_name,omitempty"`
	Endpoint         string             `json:"endpoint,omitempty"`
	InputFileID      string             `json:"input_file_id,omitempty"`
	CompletionWindow string             `json:"completion_window,omitempty"`
	Status           BatchStatus        `json:"status"`
	RequestCounts    BatchRequestCounts `json:"request_counts"`
	Metadata         map[string]string  `json:"metadata,omitempty"`
	CreatedAt        int64              `json:"created_at,omitempty"`
	ExpiresAt        *int64             `json:"expires_at,omitempty"`

	OutputFileID *string `json:"output_file_id,omitempty"`
	ErrorFileID  *string `json:"error_file_id,omitempty"`

	ProcessingStatus *string `json:"processing_status,omitempty"`
	ResultsURL       *string `json:"results_url,omitempty"`

	OperationName *string `json:"operation_name,omitempty"`

	InputBlob  *string `json:"input_blob,omitempty"`
	OutputBlob *string `json:"output_blob,omitempty"`
	ErrorBlob  *string `json:"error_blob,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostBatchRetrieveRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`
	BatchID  string        `json:"batch_id"`

	RawRequestBody []byte `json:"-"`

	ExtraParams map[string]any `json:"-"`
}
type BifrostBatchCreateRequest struct {
	Provider       ModelProvider `json:"provider"`
	Model          *string       `json:"model,omitempty"`
	RawRequestBody []byte        `json:"-"`

	InputFileID string `json:"input_file_id,omitempty"`

	Requests []BatchRequestItem `json:"requests,omitempty"`

	InputBlob    *string            `json:"input_blob,omitempty"`
	OutputFolder *BatchOutputFolder `json:"output_folder,omitempty"`

	DisplayName        *string            `json:"display_name,omitempty"`
	Endpoint           BatchEndpoint      `json:"endpoint,omitempty"`
	CompletionWindow   string             `json:"completion_window,omitempty"`
	Metadata           map[string]string  `json:"metadata,omitempty"`
	OutputExpiresAfter *BatchExpiresAfter `json:"output_expires_after,omitempty"`

	ExtraParams map[string]any `json:"-"`
}
type BifrostBatchListRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	Limit      int     `json:"limit,omitempty"`
	After      *string `json:"after,omitempty"`
	BeforeID   *string `json:"before_id,omitempty"`
	AfterID    *string `json:"after_id,omitempty"`
	PageToken  *string `json:"page_token,omitempty"`
	PageSize   int     `json:"page_size,omitempty"`
	NextCursor *string `json:"next_cursor,omitempty"`

	ExtraParams map[string]any `json:"-"`
}
type BifrostBatchDeleteRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`
	BatchID  string        `json:"batch_id"`

	RawRequestBody []byte `json:"-"`

	ExtraParams map[string]any `json:"-"`
}
type BatchResultData struct {
	Type    string         `json:"type"`
	Message map[string]any `json:"message,omitempty"`
}
type BatchErrors struct {
	Object string       `json:"object,omitempty"`
	Data   []BatchError `json:"data,omitempty"`
}
type BatchAccountingDebug struct {
	ModelBreakdowns map[string]BatchModelBreakdown `json:"model_breakdowns,omitempty"`
	Cost            *float64                       `json:"cost,omitempty"`

	ParseErrorCount int `json:"parse_error_count,omitempty"`

	Echo bool `json:"echo,omitempty"`

	Incomplete bool `json:"incomplete,omitempty"`
}
type BifrostBatchDebug struct {
	BatchID string `json:"batch_id,omitempty"`

	Status string `json:"status,omitempty"`

	Endpoint      string                `json:"endpoint,omitempty"`
	RequestCounts *BatchRequestCounts   `json:"request_counts,omitempty"`
	Accounting    *BatchAccountingDebug `json:"accounting,omitempty"`
}
type BifrostBatchDeleteResponse struct {
	ID            string             `json:"id"`
	Object        string             `json:"object,omitempty"`
	Status        BatchStatus        `json:"status"`
	RequestCounts BatchRequestCounts `json:"request_counts,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BatchResultItem struct {
	CustomID string `json:"custom_id"`

	Response *BatchResultResponse `json:"response,omitempty"`
	Result   *BatchResultData     `json:"result,omitempty"`

	Error *BatchResultError `json:"error,omitempty"`
}
type BatchStatus string
type BifrostBatchCancelRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`
	BatchID  string        `json:"batch_id"`

	RawRequestBody []byte `json:"-"`

	ExtraParams map[string]any `json:"-"`
}
type BatchExpiresAfter struct {
	Anchor  string `json:"anchor"`
	Seconds int    `json:"seconds"`
}
type BifrostBatchResultsResponse struct {
	BatchID  string            `json:"batch_id"`
	Endpoint BatchEndpoint     `json:"-"`
	Results  []BatchResultItem `json:"results"`

	HasMore    bool    `json:"has_more,omitempty"`
	NextCursor *string `json:"next_cursor,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BatchModelBreakdown struct {
	Model        string          `json:"model"`
	RequestCount int             `json:"request_count"`
	Usage        BifrostLLMUsage `json:"usage"`
	Cost         *float64        `json:"cost,omitempty"`
}
type BifrostBatchCancelResponse struct {
	ID            string             `json:"id"`
	Object        string             `json:"object,omitempty"`
	Status        BatchStatus        `json:"status"`
	RequestCounts BatchRequestCounts `json:"request_counts,omitempty"`
	CancellingAt  *int64             `json:"cancelling_at,omitempty"`
	CancelledAt   *int64             `json:"cancelled_at,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BatchResultResponse struct {
	StatusCode int            `json:"status_code"`
	RequestID  string         `json:"request_id,omitempty"`
	Body       map[string]any `json:"body,omitempty"`
}
type BatchEndpoint string
type BatchOutputFolder struct {
	URL string `json:"url"`
}
type BatchRequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Succeeded int `json:"succeeded,omitempty"`
	Expired   int `json:"expired,omitempty"`
	Canceled  int `json:"canceled,omitempty"`
	Pending   int `json:"pending,omitempty"`
}
type BatchResultError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}
type BatchError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Param   string `json:"param,omitempty"`
	Line    *int   `json:"line,omitempty"`
}
type BifrostBatchResultsRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`
	BatchID  string        `json:"batch_id"`

	RawRequestBody []byte `json:"-"`

	ExtraParams map[string]any `json:"-"`
}

const (
	BatchStatusValidating BatchStatus = "validating"
	BatchStatusFailed     BatchStatus = "failed"
	BatchStatusInProgress BatchStatus = "in_progress"
	BatchStatusFinalizing BatchStatus = "finalizing"
	BatchStatusCompleted  BatchStatus = "completed"
	BatchStatusExpired    BatchStatus = "expired"
	BatchStatusCancelling BatchStatus = "cancelling"
	BatchStatusCancelled  BatchStatus = "cancelled"
	BatchStatusEnded      BatchStatus = "ended"
	BatchStatusDeleted    BatchStatus = "deleted"
)

const (
	BatchEndpointChatCompletions BatchEndpoint = "/v1/chat/completions"
	BatchEndpointEmbeddings      BatchEndpoint = "/v1/embeddings"
	BatchEndpointCompletions     BatchEndpoint = "/v1/completions"
	BatchEndpointResponses       BatchEndpoint = "/v1/responses"
	BatchEndpointMessages        BatchEndpoint = "/v1/messages"
)

func (c BatchRequestCounts) IsZero() bool {
	return c.Total == 0 &&
		c.Completed == 0 &&
		c.Failed == 0 &&
		c.Succeeded == 0 &&
		c.Expired == 0 &&
		c.Canceled == 0 &&
		c.Pending == 0
}

func (d *BifrostBatchDebug) IsZero() bool {
	if d == nil {
		return true
	}
	return d.BatchID == "" && d.Status == "" && d.Endpoint == "" && d.RequestCounts == nil && d.Accounting == nil
}

func (request *BifrostBatchCreateRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (request *BifrostBatchRetrieveRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (request *BifrostBatchCancelRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (request *BifrostBatchDeleteRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (request *BifrostBatchResultsRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (i BatchResultItem) Failed() bool {
	if i.Error != nil {
		return true
	}
	if i.Response != nil && (i.Response.StatusCode < 200 || i.Response.StatusCode >= 300) {
		return true
	}
	if i.Result != nil && i.Result.Type != "" && i.Result.Type != "succeeded" {
		return true
	}

	return i.Response == nil && i.Result == nil
}
