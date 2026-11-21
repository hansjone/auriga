package bifrost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/observability"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/relaylimits"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
)

// SharedRelay uses the installation relay's existing member keys, model ACL,
// durable budget and usage accounting. Each invocation selects its own member.
type SharedRelay struct {
	client *Client
	http   *http.Client
}

func NewSharedRelay(origin, keyFile string, policy MemberPolicy) (*SharedRelay, error) {
	client, err := NewClient(origin, keyFile, policy)
	if err != nil {
		return nil, err
	}
	return &SharedRelay{client: client, http: &http.Client{
		Transport:     client.transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (r *SharedRelay) Close() { r.client.Close() }

const chatParameters = "model messages stream stream_options max_tokens max_completion_tokens temperature top_p tools tool_choice response_format seed stop n presence_penalty frequency_penalty logit_bias logprobs top_logprobs parallel_tool_calls reasoning_effort service_tier user metadata"

func sharedChatBody(call sharedmodel.SharedCall) ([]byte, bool, error) {
	if !call.Binding.Valid() || !sharedauth.UUID(call.CallID) || !sharedauth.UUID(call.TaskID) || len(call.Body) > int(relaylimits.ChatBodyBytes) {
		return nil, false, sharedauth.ErrInvalid
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(call.Body))
	if decoder.Decode(&fields) != nil || fields == nil || decoder.Decode(new(any)) != io.EOF || !relaylimits.DropStoreOptOut(fields) {
		return nil, false, sharedauth.ErrInvalid
	}
	allowed := make(map[string]bool)
	for _, name := range strings.Fields(chatParameters) {
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return nil, false, sharedauth.ErrInvalid
		}
	}
	var model string
	var messages []json.RawMessage
	var stream bool
	if json.Unmarshal(fields["model"], &model) != nil || model == "" || json.Unmarshal(fields["messages"], &messages) != nil || len(messages) == 0 {
		return nil, false, sharedauth.ErrInvalid
	}
	if value, ok := fields["stream"]; ok && (bytes.Equal(value, []byte("null")) || json.Unmarshal(value, &stream) != nil) {
		return nil, false, sharedauth.ErrInvalid
	}
	if stream {
		fields["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	fields["user"], _ = json.Marshal(call.Binding.TenantID + ":" + call.Binding.MemberID)
	bindingJSON, _ := json.Marshal(call.Binding)
	fields["metadata"], _ = json.Marshal(map[string]string{
		"tenant_id": call.Binding.TenantID, "account_id": call.Binding.MemberID,
		"placement_id": call.Binding.PlacementID, "session_id": call.Binding.SessionID, "call_id": call.CallID,
		"auriga_shared_binding": string(bindingJSON), "auriga_shared_call": call.CallID,
	})
	body, err := json.Marshal(fields)
	return body, stream, err
}

// sharedRelayKey fits the existing 128-character Idempotency-Key limit and
// remains stable for a member's call across placement and login changes.
func sharedRelayKey(call sharedmodel.SharedCall) string {
	return sharedmodel.CallKey(call.Binding, call.CallID)
}

func (r *SharedRelay) Stream(ctx context.Context, call sharedmodel.SharedCall, emit func([]byte) error) (sharedmodel.SharedUsage, error) {
	var usage sharedmodel.SharedUsage
	body, streaming, err := sharedChatBody(call)
	if err != nil || emit == nil {
		return usage, sharedauth.ErrInvalid
	}
	publicModel := sharedRequestModel(body)
	failStage := func(stage string) (sharedmodel.SharedUsage, error) {
		slog.WarnContext(ctx, "shared model relay failed", "stage", stage)
		return usage, sharedauth.ErrUnavailable
	}
	member, err := r.client.MemberKey(ctx, call.Binding.TenantID, call.Binding.MemberID)
	if err != nil {
		return usage, err
	}
	password, err := r.client.password()
	if err != nil {
		return failStage("relay_password")
	}
	target := *r.client.target
	target.Path = "/v1/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return failStage("relay_request")
	}
	// A lost response is reconciled using the durable call record. Disabling
	// GetBody prevents transport retries of a request that might have executed.
	request.GetBody = nil
	request.SetBasicAuth("auriga-member", password)
	request.Header.Set("X-Bf-Vk", member.Secret)
	request.Header.Set("Idempotency-Key", sharedRelayKey(call))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("traceparent", observability.Parent(ctx))
	response, err := r.http.Do(request)
	if err != nil {
		return failStage("upstream_transport")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		// Upstream errors may include keys or prompt material.
		return failStage("upstream_status")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return failStage("content_type")
	}
	rewrite := func(raw []byte) ([]byte, error) {
		out, err := rewriteSharedModel(raw, publicModel)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	if !streaming {
		if mediaType != "application/json" {
			return failStage("non_stream_media")
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, relaylimits.ChatBodyBytes+1))
		if err != nil || int64(len(raw)) > relaylimits.ChatBodyBytes {
			return failStage("non_stream_body")
		}
		raw, err = rewrite(raw)
		if err != nil {
			slog.WarnContext(ctx, "shared model relay failed", "stage", "model_rewrite")
			return usage, sharedauth.ErrUnavailable
		}
		if err := captureSharedUsage(raw, &usage); err != nil {
			slog.WarnContext(ctx, "shared model relay failed", "stage", usageFailureStage(err))
			return usage, mapUsageError(err)
		}
		return usage, emit(raw)
	}
	if mediaType != "text/event-stream" {
		return failStage("stream_media")
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), int(relaylimits.ChatBodyBytes))
	var event []byte
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(event) == 0 {
				continue
			}
			if string(event) == "[DONE]" {
				return usage, nil
			}
			rewritten, err := rewrite(event)
			if err != nil {
				slog.WarnContext(ctx, "shared model relay failed", "stage", "model_rewrite")
				return usage, sharedauth.ErrUnavailable
			}
			if err := captureSharedUsage(rewritten, &usage); err != nil {
				slog.WarnContext(ctx, "shared model relay failed", "stage", usageFailureStage(err))
				return usage, mapUsageError(err)
			}
			if err := emit(append([]byte(nil), rewritten...)); err != nil {
				return usage, err
			}
			event = nil
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if len(event) > 0 {
				event = append(event, '\n')
			}
			event = append(event, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
			if int64(len(event)) > relaylimits.ChatBodyBytes {
				return failStage("oversize_event")
			}
		}
	}
	// EOF without DONE is an uncertain stream, even when some tokens arrived.
	return failStage("missing_done")
}

func sharedRequestModel(body []byte) string {
	var fields struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &fields)
	return fields.Model
}

// rewriteSharedModel keeps the caller-authorized public model id on every
// chunk. Auto pools route to upstream ids (MiniMax/DeepSeek); agent validates
// against the requested model string.
func rewriteSharedModel(raw []byte, publicModel string) ([]byte, error) {
	if publicModel == "" {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, sharedauth.ErrUnavailable
	}
	if _, ok := fields["model"]; ok {
		fields["model"], _ = json.Marshal(publicModel)
	}
	return json.Marshal(fields)
}

func usageFailureStage(err error) string {
	if errors.Is(err, errUsageMismatch) {
		return "usage_mismatch"
	}
	return "usage_invalid"
}

var errUsageMismatch = errors.New("usage_mismatch")

func captureSharedUsage(raw []byte, usage *sharedmodel.SharedUsage) error {
	var response struct {
		Error json.RawMessage `json:"error"`
		Usage *struct {
			Input  *int64 `json:"prompt_tokens"`
			Output *int64 `json:"completion_tokens"`
			Total  *int64 `json:"total_tokens"`
			CompletionDetails *struct {
				Reasoning *int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Error) > 0 && string(response.Error) != "null" {
		return sharedauth.ErrUnavailable
	}
	if response.Usage == nil {
		return nil
	}
	u := response.Usage
	const maxTokens = 9007199254740991
	if u.Input == nil || u.Output == nil || u.Total == nil || *u.Input < 0 || *u.Output < 0 || *u.Total < 0 || *u.Input > maxTokens || *u.Output > maxTokens || *u.Total > maxTokens {
		return sharedauth.ErrUnavailable
	}
	input, output, total := *u.Input, *u.Output, *u.Total
	// Reasoning models often put reasoning tokens into total without counting
	// them in completion_tokens. Bill by upstream total: fold the gap into
	// output so Input+Output remains the durable accounting identity.
	if input+output != total {
		var reasoning int64
		if u.CompletionDetails != nil && u.CompletionDetails.Reasoning != nil {
			reasoning = *u.CompletionDetails.Reasoning
		}
		if reasoning < 0 || reasoning > maxTokens || input+output+reasoning != total {
			return errUsageMismatch
		}
		output += reasoning
	}
	*usage = sharedmodel.SharedUsage{InputTokens: input, OutputTokens: output, Known: true}
	return nil
}

func mapUsageError(err error) error {
	if errors.Is(err, errUsageMismatch) {
		return sharedauth.ErrUnavailable
	}
	return err
}
