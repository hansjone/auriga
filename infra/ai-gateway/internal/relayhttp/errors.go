package relayhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/budget"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/modelprotocol"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

type fault string

func (e fault) Error() string { return string(e) }

type errorInfo struct {
	status       int
	kind, en, zh string
}

// Domain status/messages mirror contracts/catalog/m3.json. Contract tests detect
// drift; common mappings reuse the existing common HTTP boundary.
var errorCatalog = map[string]errorInfo{
	"common.validation_failed":   {400, "invalid_request_error", "Validation failed", "请求参数无效"},
	"common.unauthenticated":     {401, "authentication_error", "Unauthenticated", "请先登录"},
	"common.forbidden":           {403, "permission_error", "Forbidden", "无权执行此操作"},
	"common.not_found":           {404, "invalid_request_error", "Not found", "资源不存在"},
	"common.conflict":            {409, "conflict_error", "Conflict", "资源冲突"},
	"common.precondition_failed": {412, "conflict_error", "Precondition failed", "资源版本不匹配"},
	"common.rate_limited":        {429, "rate_limit_error", "Too many requests", "请求过于频繁"},
	"common.unavailable":         {503, "internal_error", "Service unavailable", "服务暂不可用"},
	"common.internal":            {500, "internal_error", "Internal error", "内部错误"},
	"ai.model_not_allowed":       {403, "permission_error", "This model is not authorized", "此模型未获授权"},
	"ai.budget_exceeded":         {429, "rate_limit_error", "Model budget is exhausted or held by unsettled usage", "模型额度不足或待结算用量占用额度"},
	"ai.budget_unavailable":      {503, "internal_error", "Budget or authorization cannot currently be verified", "暂时无法确认预算或授权"},
	"ai.request_in_progress":     {409, "conflict_error", "The same request is still executing", "同一请求仍在执行"},
	"ai.replay_unavailable":      {409, "conflict_error", "The original request is terminal and its body is not retained for replay", "原请求已终结，不保存正文供重放"},
	"ai.upstream_unavailable":    {502, "upstream_error", "The model provider is unavailable", "模型供应商暂时不可用"},
	"ai.upstream_timeout":        {504, "upstream_error", "The model provider timed out", "模型供应商响应超时"},
	"ai.upstream_rejected":       {502, "upstream_error", "The model provider rejected the request", "模型供应商拒绝了请求"},
	"ai.stream_interrupted":      {502, "upstream_error", "The model response stream was interrupted", "模型响应流中断"},
}

func classify(err error, problem bool) (string, *budget.Error) {
	code := "common.internal"
	var b *budget.Error
	var p *modelprotocol.Error
	switch {
	case errors.As(err, &b):
		code = b.Code
	case errors.As(err, &p):
		code = p.Code
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = "common.unavailable"
	default:
		if err != nil {
			if _, ok := errorCatalog[err.Error()]; ok {
				code = err.Error()
			}
		}
	}
	if _, ok := errorCatalog[code]; !ok {
		code = "common.internal"
	}
	// listOwnAiUsage declares common errors only. Authority's dependency outage
	// is the operation's existing common.unavailable, never a data-plane AiError.
	if problem && strings.HasPrefix(code, "ai.") {
		if code == "ai.budget_unavailable" {
			code = "common.unavailable"
		} else {
			code = "common.internal"
		}
	}
	return code, b
}

func (s *response) failure(ctx context.Context, id string, problem bool, err error) {
	code, b := classify(err, problem)
	i := errorCatalog[code]
	message := i.en
	// Accept-Language is copied into this private field by the handler, not used
	// as an identity or a source of error text.
	if strings.HasPrefix(strings.ToLower(s.language), "zh") {
		message = i.zh
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 100*time.Millisecond)
	defer cancel()
	if !s.committed && i.status == 401 {
		s.w.Header().Set("WWW-Authenticate", "Bearer")
	}
	if problem && !s.committed {
		p := api.NewProblem(http.StatusText(i.status), int32(i.status), code)
		p.Detail = &message
		p.TraceId = &id
		payload, _ := json.Marshal(p)
		_, _ = s.write(ctx, i.status, "application/problem+json", payload, false)
		return
	}
	e := api.AiError{RequestId: id, Error: api.AiErrorError{Code: code, Message: message, Type: i.kind}}
	if b != nil && (code == "ai.replay_unavailable" || code == "ai.request_in_progress") {
		if b.OriginalRequestID != "" {
			e.OriginalRequestId = &b.OriginalRequestID
		}
		switch b.Outcome {
		case "completed", "failed", "cancelled", "unknown":
			e.Outcome = &b.Outcome
		}
	}
	if s.committed {
		if s.errorSent {
			return
		}
		payload, _ := json.Marshal(e)
		s.errorSent = true
		_, _ = s.sink(ctx, append(append([]byte("data: "), payload...), '\n', '\n'))
		return
	}
	payload, _ := json.Marshal(e)
	_, _ = s.write(ctx, i.status, "application/json", payload, false)
}
