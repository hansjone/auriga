// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package anthropic

import (
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type AnthropicBatchRequestCounts struct {
	Processing int `json:"processing"`
	Succeeded  int `json:"succeeded"`
	Errored    int `json:"errored"`
	Canceled   int `json:"canceled"`
	Expired    int `json:"expired"`
}
type AnthropicBatchError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
type AnthropicBatchRequestItem struct {
	CustomID string         `json:"custom_id"`
	Params   map[string]any `json:"params"`
}
type AnthropicBatchResultData struct {
	Type    string                 `json:"type"`
	Message map[string]interface{} `json:"message,omitempty"`
	Error   *AnthropicBatchError   `json:"error,omitempty"`
}
type AnthropicBatchResultItem struct {
	CustomID string                   `json:"custom_id"`
	Result   AnthropicBatchResultData `json:"result"`
}
type AnthropicBatchListResponse struct {
	Data    []AnthropicBatchResponse `json:"data"`
	HasMore bool                     `json:"has_more"`
	FirstID *string                  `json:"first_id,omitempty"`
	LastID  *string                  `json:"last_id,omitempty"`
}
type AnthropicBatchCreateRequest struct {
	Requests []AnthropicBatchRequestItem `json:"requests"`
}
type AnthropicBatchResponse struct {
	ID                string                       `json:"id"`
	Type              string                       `json:"type"`
	ProcessingStatus  string                       `json:"processing_status"`
	RequestCounts     *AnthropicBatchRequestCounts `json:"request_counts,omitempty"`
	EndedAt           *string                      `json:"ended_at,omitempty"`
	CreatedAt         string                       `json:"created_at"`
	ExpiresAt         string                       `json:"expires_at"`
	ArchivedAt        *string                      `json:"archived_at,omitempty"`
	CancelInitiatedAt *string                      `json:"cancel_initiated_at,omitempty"`
	ResultsURL        *string                      `json:"results_url,omitempty"`
}

func ToBifrostBatchStatus(status string) schemas.BatchStatus {
	switch status {
	case "in_progress":
		return schemas.BatchStatusInProgress
	case "canceling":
		return schemas.BatchStatusCancelling
	case "ended":
		return schemas.BatchStatusEnded
	default:
		return schemas.BatchStatus(status)
	}
}

func parseAnthropicTimestamp(timestamp string) int64 {
	if timestamp == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return 0
	}
	return t.Unix()
}

func ToBifrostObjectType(anthropicType string) string {
	switch anthropicType {
	case "message_batch":
		return "batch"
	default:
		return anthropicType
	}
}

func (r *AnthropicBatchResponse) ToBifrostBatchCreateResponse(latency time.Duration, sendBackRawRequest bool, sendBackRawResponse bool, rawRequest interface{}, rawResponse interface{}) *schemas.BifrostBatchCreateResponse {
	expiresAt := parseAnthropicTimestamp(r.ExpiresAt)
	resp := &schemas.BifrostBatchCreateResponse{
		ID:               r.ID,
		Object:           ToBifrostObjectType(r.Type),
		Status:           ToBifrostBatchStatus(r.ProcessingStatus),
		ProcessingStatus: &r.ProcessingStatus,
		ResultsURL:       r.ResultsURL,
		CreatedAt:        parseAnthropicTimestamp(r.CreatedAt),
		ExpiresAt:        &expiresAt,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if r.RequestCounts != nil {
		resp.RequestCounts = schemas.BatchRequestCounts{
			Total:     r.RequestCounts.Processing + r.RequestCounts.Succeeded + r.RequestCounts.Errored + r.RequestCounts.Canceled + r.RequestCounts.Expired,
			Completed: r.RequestCounts.Succeeded,
			Failed:    r.RequestCounts.Errored,
			Succeeded: r.RequestCounts.Succeeded,
			Expired:   r.RequestCounts.Expired,
			Canceled:  r.RequestCounts.Canceled,
			Pending:   r.RequestCounts.Processing,
		}
	}

	if sendBackRawRequest {
		resp.ExtraFields.RawRequest = rawRequest
	}

	if sendBackRawResponse {
		resp.ExtraFields.RawResponse = rawResponse
	}

	return resp
}

func (r *AnthropicBatchResponse) ToBifrostBatchRetrieveResponse(latency time.Duration, sendBackRawRequest bool, sendBackRawResponse bool, rawRequest interface{}, rawResponse interface{}) *schemas.BifrostBatchRetrieveResponse {
	resp := &schemas.BifrostBatchRetrieveResponse{
		ID:               r.ID,
		Object:           ToBifrostObjectType(r.Type),
		Status:           ToBifrostBatchStatus(r.ProcessingStatus),
		ProcessingStatus: &r.ProcessingStatus,
		ResultsURL:       r.ResultsURL,
		CreatedAt:        parseAnthropicTimestamp(r.CreatedAt),
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}

	if sendBackRawRequest {
		resp.ExtraFields.RawRequest = rawRequest
	}

	expiresAt := parseAnthropicTimestamp(r.ExpiresAt)
	if expiresAt > 0 {
		resp.ExpiresAt = &expiresAt
	}

	if r.EndedAt != nil {
		endedAt := parseAnthropicTimestamp(*r.EndedAt)
		resp.CompletedAt = &endedAt
	}

	if r.ArchivedAt != nil {
		archivedAt := parseAnthropicTimestamp(*r.ArchivedAt)
		resp.ArchivedAt = &archivedAt
	}

	if r.CancelInitiatedAt != nil {
		cancellingAt := parseAnthropicTimestamp(*r.CancelInitiatedAt)
		resp.CancellingAt = &cancellingAt
	}

	if r.RequestCounts != nil {
		resp.RequestCounts = schemas.BatchRequestCounts{
			Total:     r.RequestCounts.Processing + r.RequestCounts.Succeeded + r.RequestCounts.Errored + r.RequestCounts.Canceled + r.RequestCounts.Expired,
			Completed: r.RequestCounts.Succeeded,
			Failed:    r.RequestCounts.Errored,
			Succeeded: r.RequestCounts.Succeeded,
			Expired:   r.RequestCounts.Expired,
			Canceled:  r.RequestCounts.Canceled,
			Pending:   r.RequestCounts.Processing,
		}
	}

	if sendBackRawResponse {
		resp.ExtraFields.RawResponse = rawResponse
	}

	return resp
}
