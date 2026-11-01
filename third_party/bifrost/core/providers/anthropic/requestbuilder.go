package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

var AnthropicProviderRequestDefaultsMap = map[schemas.ModelProvider]AnthropicProviderRequestDefaults{
	schemas.Anthropic: {},
	schemas.Azure:     {},

	schemas.Bedrock: {
		DeleteModelField:          true,
		DeleteStreamField:         true,
		AddAnthropicVersion:       true,
		AnthropicVersion:          "bedrock-2023-05-31",
		RemapToolVersions:         true,
		InjectBetaHeadersIntoBody: true,
		InlineURLSources:          true,
	},

	schemas.BedrockMantle: {
		RemapToolVersions: true,

		InlineURLSources: true,
	},
	schemas.DeepSeek: {},

	schemas.Vertex: {
		DeleteModelField:          true,
		DeleteRegionField:         true,
		AddAnthropicVersion:       true,
		AnthropicVersion:          "vertex-2023-10-16",
		StripCacheControlScope:    true,
		RemapToolVersions:         true,
		InjectBetaHeadersIntoBody: true,
	},
	schemas.SGL: {},
}

type AnthropicProviderRequestDefaults struct {
	DeleteModelField bool

	DeleteRegionField bool

	DeleteStreamField bool

	AddAnthropicVersion bool
	AnthropicVersion    string

	StripCacheControlScope bool

	RemapToolVersions bool

	InjectBetaHeadersIntoBody bool

	InlineURLSources bool
}
type AnthropicRequestBuildConfig struct {
	Provider schemas.ModelProvider

	Model string

	IsStreaming bool

	IsCountTokens bool

	ExcludeFields []string

	IncludeFields map[string]any

	ValidateTools bool

	BetaHeaderOverrides  map[string]bool
	ProviderExtraHeaders map[string]schemas.SecretVar

	ShouldSendBackRawRequest  bool
	ShouldSendBackRawResponse bool
}

func BuildAnthropicResponsesRequestBody(ctx *schemas.BifrostContext, request *schemas.BifrostResponsesRequest, cfg AnthropicRequestBuildConfig) ([]byte, *schemas.BifrostError) {
	if providerUtils.IsLargePayloadPassthroughEnabled(ctx) {
		return nil, nil
	}

	capModel := schemas.ResolveCanonicalModel(ctx, request.Model)

	defaults := AnthropicProviderRequestDefaultsMap[cfg.Provider]

	newErr := func(msg string, err error, reqBody []byte) *schemas.BifrostError {
		bifrostErr := providerUtils.NewBifrostOperationError(msg, err)
		if badRequest, ok := providerUtils.AsBifrostBadRequestError(err); ok {
			bifrostErr = badRequest
		}
		return providerUtils.EnrichError(
			ctx,
			bifrostErr,
			reqBody,
			nil,
			cfg.ShouldSendBackRawRequest,
			cfg.ShouldSendBackRawResponse,
		)
	}

	var jsonBody []byte
	var err error

	if useRawBody, ok := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); ok && useRawBody {
		jsonBody = request.GetRawRequestBody()

		if providerUtils.JSONFieldExists(jsonBody, "thread") {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "thread")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		if cfg.IsCountTokens {

			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "max_tokens")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "temperature")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "model", cfg.Model)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		} else {

			if cfg.Model != "" {
				if defaults.DeleteModelField {

					jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "model")
					if err != nil {
						return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
					}
				} else {

					jsonBody, err = providerUtils.SetJSONField(jsonBody, "model", cfg.Model)
					if err != nil {
						return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
					}
				}
			} else {

				jsonBody, err = providerUtils.SetJSONField(jsonBody, "model", request.Model)
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
			}

			if !providerUtils.JSONFieldExists(jsonBody, "max_tokens") {
				modelForTokens := cfg.Model
				if modelForTokens == "" {
					if r := providerUtils.GetJSONField(jsonBody, "model"); r.Exists() {
						modelForTokens = r.String()
					}
				}
				jsonBody, err = providerUtils.SetJSONField(jsonBody, "max_tokens", providerUtils.GetMaxOutputTokensOrDefault(cfg.Provider, modelForTokens, AnthropicDefaultMaxTokens))
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
			}
			if maxTokens := providerUtils.GetJSONField(jsonBody, "max_tokens"); maxTokens.Exists() {
				if clamped := clampToModelOutputCeiling(schemas.ResolveModelCaps(cfg.Provider, capModel), int(maxTokens.Int())); int64(clamped) != maxTokens.Int() {
					jsonBody, err = providerUtils.SetJSONField(jsonBody, "max_tokens", clamped)
					if err != nil {
						return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
					}
					jsonBody, err = fitRawThinkingBudget(jsonBody, clamped)
					if err != nil {
						return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
					}
				}
			}

		}

		if cfg.IsStreaming {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "stream", true)
		} else {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "stream")
		}
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		jsonBody, err = StripAutoInjectableTools(jsonBody)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		if defaults.RemapToolVersions {

			jsonBody, err = RemapRawToolVersionsForProvider(jsonBody, cfg.Provider, capModel)
			if err != nil {
				return nil, newErr(err.Error(), nil, jsonBody)
			}
		}

		if defaults.DeleteRegionField {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "region")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		jsonBody, err = StripUnsupportedFieldsFromRawBody(jsonBody, cfg.Provider, capModel)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		jsonBody, err = ApplyDefaultEagerInputStreamingToRawBody(jsonBody, cfg.Provider, capModel)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		if defaults.AddAnthropicVersion && !providerUtils.JSONFieldExists(jsonBody, "anthropic_version") {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "anthropic_version", defaults.AnthropicVersion)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		var probe AnthropicMessageRequest
		if unmarshalErr := schemas.Unmarshal(jsonBody, &probe); unmarshalErr == nil {

			probe.Model = capModel
			_ = AddMissingBetaHeadersToContext(ctx, &probe, cfg.Provider)
		}

		for _, field := range cfg.ExcludeFields {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, field)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
		for field, value := range cfg.IncludeFields {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, field, value)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
	} else {
		if cfg.ValidateTools && request.Params != nil && request.Params.Tools != nil {

			if keep, dropped := ValidateResponsesToolsForProvider(request.Params.Tools, schemas.ResolveModelCaps(cfg.Provider, capModel)); len(dropped) > 0 {
				reqCopy := *request
				paramsCopy := *request.Params
				paramsCopy.Tools = keep
				reqCopy.Params = &paramsCopy
				request = &reqCopy
			}
		}

		ct, ch := providerUtils.StartPhaseSpan(ctx, "convertor")
		reqBody, convErr := ToAnthropicResponsesRequest(ctx, request)
		if convErr != nil {
			if ct != nil {
				ct.EndSpan(ch, schemas.SpanStatusError, convErr.Error())
			}
			if errors.Is(convErr, ErrReasoningMaxTokensTooLow) {
				return nil, providerUtils.EnrichError(
					ctx,
					providerUtils.NewBifrostBadRequestError(convErr.Error()),
					jsonBody,
					nil,
					cfg.ShouldSendBackRawRequest,
					cfg.ShouldSendBackRawResponse,
				)
			}
			return nil, newErr(schemas.ErrRequestBodyConversion, convErr, jsonBody)
		}
		if reqBody == nil {
			if ct != nil {
				ct.EndSpan(ch, schemas.SpanStatusError, "request body is not provided")
			}
			return nil, newErr("request body is not provided", nil, jsonBody)
		}
		if ct != nil {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}

		if cfg.Model != "" {
			reqBody.Model = cfg.Model
		}

		if defaults.StripCacheControlScope {
			reqBody.SetStripCacheControlScope(true)
		}

		if cfg.IsStreaming {
			reqBody.Stream = schemas.Ptr(true)
		}

		stripUnsupportedAnthropicFields(reqBody, cfg.Provider, capModel)
		applyDefaultEagerInputStreaming(reqBody, cfg.Provider, capModel)

		restoreNativeAnthropicRequestSurface(ctx, reqBody, request.Params, cfg.Provider, capModel)

		AddMissingBetaHeadersToContext(ctx, reqBody, cfg.Provider)

		mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
		jsonBody, err = providerUtils.MarshalProviderRequest(reqBody)
		if mt != nil {
			if err != nil {
				mt.EndSpan(mh, schemas.SpanStatusError, err.Error())
			} else {
				mt.EndSpan(mh, schemas.SpanStatusOk, "")
			}
		}
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, fmt.Errorf("failed to marshal request body: %w", err), jsonBody)
		}

		if ctx.Value(schemas.BifrostContextKeyPassthroughExtraParams) == true {
			extraParams := reqBody.GetExtraParams()
			if len(extraParams) > 0 {
				jsonBody, err = providerUtils.MergeExtraParamsIntoJSON(jsonBody, extraParams)
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
			}
		}

		if defaults.AddAnthropicVersion && !providerUtils.JSONFieldExists(jsonBody, "anthropic_version") {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "anthropic_version", defaults.AnthropicVersion)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		if cfg.IsCountTokens {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "max_tokens")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "temperature")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		} else if defaults.DeleteModelField {

			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "model")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		if defaults.DeleteRegionField {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "region")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		for _, field := range cfg.ExcludeFields {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, field)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
		for field, value := range cfg.IncludeFields {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, field, value)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
	}

	jsonBody, err = StripEmptyThinkingBlocks(jsonBody)
	if err != nil {
		return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
	}

	jsonBody, err = stripBifrostFallbacksFromBody(jsonBody, cfg.Provider)
	if err != nil {
		return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
	}

	if cfg.IsCountTokens {

		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "fallback_credit_token")
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}
	}

	if defaults.DeleteStreamField {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "stream")
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}
	}

	jsonBody, err = normalizeBase64TextSources(jsonBody)
	if err != nil {
		return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
	}

	if defaults.InlineURLSources {
		jsonBody, err = InlineURLContentSources(ctx, jsonBody)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}
	}

	if defaults.InjectBetaHeadersIntoBody {
		if betaHeaders := FilterBetaHeadersForProvider(MergeBetaHeaders(ctx, cfg.ProviderExtraHeaders), cfg.Provider, cfg.BetaHeaderOverrides); len(betaHeaders) > 0 {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "anthropic_beta", betaHeaders)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
	}

	return jsonBody, nil
}

func BuildAnthropicChatRequestBody(ctx *schemas.BifrostContext, request *schemas.BifrostChatRequest, cfg AnthropicRequestBuildConfig) ([]byte, *schemas.BifrostError) {
	if providerUtils.IsLargePayloadPassthroughEnabled(ctx) {
		return nil, nil
	}

	capModel := schemas.ResolveCanonicalModel(ctx, request.Model)

	defaults := AnthropicProviderRequestDefaultsMap[cfg.Provider]

	newErr := func(msg string, err error, reqBody []byte) *schemas.BifrostError {
		bifrostErr := providerUtils.NewBifrostOperationError(msg, err)
		if badRequest, ok := providerUtils.AsBifrostBadRequestError(err); ok {
			bifrostErr = badRequest
		}
		return providerUtils.EnrichError(
			ctx,
			bifrostErr,
			reqBody,
			nil,
			cfg.ShouldSendBackRawRequest,
			cfg.ShouldSendBackRawResponse,
		)
	}

	var jsonBody []byte
	var err error

	if useRawBody, ok := ctx.Value(schemas.BifrostContextKeyUseRawRequestBody).(bool); ok && useRawBody {
		jsonBody = request.GetRawRequestBody()

		if cfg.Model != "" {
			if defaults.DeleteModelField {
				jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "model")
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
			} else {
				jsonBody, err = providerUtils.SetJSONField(jsonBody, "model", cfg.Model)
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
			}
		} else {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "model", request.Model)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		if !providerUtils.JSONFieldExists(jsonBody, "max_tokens") {
			modelForTokens := cfg.Model
			if modelForTokens == "" {
				if r := providerUtils.GetJSONField(jsonBody, "model"); r.Exists() {
					modelForTokens = r.String()
				}
			}
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "max_tokens", providerUtils.GetMaxOutputTokensOrDefault(cfg.Provider, modelForTokens, AnthropicDefaultMaxTokens))
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
		if maxTokens := providerUtils.GetJSONField(jsonBody, "max_tokens"); maxTokens.Exists() {
			if clamped := clampToModelOutputCeiling(schemas.ResolveModelCaps(cfg.Provider, capModel), int(maxTokens.Int())); int64(clamped) != maxTokens.Int() {
				jsonBody, err = providerUtils.SetJSONField(jsonBody, "max_tokens", clamped)
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
				jsonBody, err = fitRawThinkingBudget(jsonBody, clamped)
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
			}
		}

		if cfg.IsStreaming {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "stream", true)
		} else {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "stream")
		}
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		jsonBody, err = StripAutoInjectableTools(jsonBody)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		if defaults.RemapToolVersions {
			jsonBody, err = RemapRawToolVersionsForProvider(jsonBody, cfg.Provider, capModel)
			if err != nil {
				return nil, newErr(err.Error(), nil, jsonBody)
			}
		}

		if defaults.DeleteRegionField {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "region")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		jsonBody, err = StripUnsupportedFieldsFromRawBody(jsonBody, cfg.Provider, capModel)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		jsonBody, err = ApplyDefaultEagerInputStreamingToRawBody(jsonBody, cfg.Provider, capModel)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}

		if defaults.AddAnthropicVersion && !providerUtils.JSONFieldExists(jsonBody, "anthropic_version") {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "anthropic_version", defaults.AnthropicVersion)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		var probe AnthropicMessageRequest
		if unmarshalErr := schemas.Unmarshal(jsonBody, &probe); unmarshalErr == nil {
			probe.Model = capModel

			_ = AddMissingBetaHeadersToContext(ctx, &probe, cfg.Provider)
		}

		for _, field := range cfg.ExcludeFields {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, field)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
		for field, value := range cfg.IncludeFields {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, field, value)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
	} else {
		ct, ch := providerUtils.StartPhaseSpan(ctx, "convertor")
		reqBody, convErr := ToAnthropicChatRequest(ctx, request)
		if convErr != nil {
			if ct != nil {
				ct.EndSpan(ch, schemas.SpanStatusError, convErr.Error())
			}
			if errors.Is(convErr, ErrReasoningMaxTokensTooLow) {
				return nil, providerUtils.EnrichError(
					ctx,
					providerUtils.NewBifrostBadRequestError(convErr.Error()),
					jsonBody,
					nil,
					cfg.ShouldSendBackRawRequest,
					cfg.ShouldSendBackRawResponse,
				)
			}
			return nil, newErr(schemas.ErrRequestBodyConversion, convErr, jsonBody)
		}
		if reqBody == nil {
			if ct != nil {
				ct.EndSpan(ch, schemas.SpanStatusError, "request body is not provided")
			}
			return nil, newErr("request body is not provided", nil, jsonBody)
		}
		if ct != nil {
			ct.EndSpan(ch, schemas.SpanStatusOk, "")
		}

		if cfg.Model != "" {
			reqBody.Model = cfg.Model
		}

		if defaults.StripCacheControlScope {
			reqBody.SetStripCacheControlScope(true)
		}

		if cfg.IsStreaming {
			reqBody.Stream = schemas.Ptr(true)
		}

		stripUnsupportedAnthropicFields(reqBody, cfg.Provider, capModel)
		applyDefaultEagerInputStreaming(reqBody, cfg.Provider, capModel)

		AddMissingBetaHeadersToContext(ctx, reqBody, cfg.Provider)

		mt, mh := providerUtils.StartPhaseSpan(ctx, "request-marshal")
		jsonBody, err = providerUtils.MarshalProviderRequest(reqBody)
		if mt != nil {
			if err != nil {
				mt.EndSpan(mh, schemas.SpanStatusError, err.Error())
			} else {
				mt.EndSpan(mh, schemas.SpanStatusOk, "")
			}
		}
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, fmt.Errorf("failed to marshal request body: %w", err), jsonBody)
		}

		if ctx.Value(schemas.BifrostContextKeyPassthroughExtraParams) == true {
			extraParams := reqBody.GetExtraParams()
			if len(extraParams) > 0 {
				jsonBody, err = providerUtils.MergeExtraParamsIntoJSON(jsonBody, extraParams)
				if err != nil {
					return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
				}
			}
		}

		if defaults.AddAnthropicVersion && !providerUtils.JSONFieldExists(jsonBody, "anthropic_version") {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "anthropic_version", defaults.AnthropicVersion)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		if defaults.DeleteModelField {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "model")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		if defaults.DeleteRegionField {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "region")
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}

		for _, field := range cfg.ExcludeFields {
			jsonBody, err = providerUtils.DeleteJSONField(jsonBody, field)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
		for field, value := range cfg.IncludeFields {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, field, value)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
	}

	jsonBody, err = StripEmptyThinkingBlocks(jsonBody)
	if err != nil {
		return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
	}

	jsonBody, err = stripBifrostFallbacksFromBody(jsonBody, cfg.Provider)
	if err != nil {
		return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
	}

	if defaults.DeleteStreamField {
		jsonBody, err = providerUtils.DeleteJSONField(jsonBody, "stream")
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}
	}

	jsonBody, err = normalizeBase64TextSources(jsonBody)
	if err != nil {
		return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
	}

	if defaults.InlineURLSources {
		jsonBody, err = InlineURLContentSources(ctx, jsonBody)
		if err != nil {
			return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
		}
	}

	if defaults.InjectBetaHeadersIntoBody {
		if betaHeaders := FilterBetaHeadersForProvider(MergeBetaHeaders(ctx, cfg.ProviderExtraHeaders), cfg.Provider, cfg.BetaHeaderOverrides); len(betaHeaders) > 0 {
			jsonBody, err = providerUtils.SetJSONField(jsonBody, "anthropic_beta", betaHeaders)
			if err != nil {
				return nil, newErr(schemas.ErrProviderRequestMarshal, err, jsonBody)
			}
		}
	}

	return jsonBody, nil
}

const (
	BedrockInvokeGuardrailActionKey = "invoke_guardrail_action"
	BedrockInvokeGuardrailTraceKey  = "invoke_guardrail_trace"
)

func bedrockInvokeGuardrailOutcome(body []byte) (action string, trace json.RawMessage) {
	if a := providerUtils.GetJSONField(body, "amazon-bedrock-guardrailAction"); a.Exists() {
		action = a.String()
	}
	if t := providerUtils.GetJSONField(body, "amazon-bedrock-trace"); t.Exists() {
		trace = json.RawMessage(t.Raw)
	}
	return action, trace
}

func setBedrockInvokeGuardrailOutcome(resp *schemas.BifrostResponsesResponse, action string, trace json.RawMessage) {
	if action == "" && len(trace) == 0 {
		return
	}
	if resp.ProviderExtraFields == nil {
		resp.ProviderExtraFields = make(map[string]interface{}, 2)
	}
	if action != "" {
		resp.ProviderExtraFields[BedrockInvokeGuardrailActionKey] = action
	}
	if len(trace) > 0 {
		resp.ProviderExtraFields[BedrockInvokeGuardrailTraceKey] = trace
	}
}
