// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/tidwall/sjson"
	"github.com/valyala/fasthttp"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type geminiCachedContentList struct {
	CachedContents []geminiCachedContent `json:"cachedContents"`
	NextPageToken  string                `json:"nextPageToken,omitempty"`
}
type geminiCachedContent struct {
	Name              string         `json:"name,omitempty"`
	DisplayName       string         `json:"displayName,omitempty"`
	Model             string         `json:"model,omitempty"`
	SystemInstruction any            `json:"systemInstruction,omitempty"`
	Contents          []any          `json:"contents,omitempty"`
	Tools             []any          `json:"tools,omitempty"`
	ToolConfig        any            `json:"toolConfig,omitempty"`
	CreateTime        string         `json:"createTime,omitempty"`
	UpdateTime        string         `json:"updateTime,omitempty"`
	ExpireTime        string         `json:"expireTime,omitempty"`
	TTL               string         `json:"ttl,omitempty"`
	UsageMetadata     map[string]any `json:"usageMetadata,omitempty"`
}

func (g *geminiCachedContent) toBifrostObject() schemas.CachedContentObject {
	return schemas.CachedContentObject{
		Name:              g.Name,
		DisplayName:       g.DisplayName,
		Model:             g.Model,
		SystemInstruction: g.SystemInstruction,
		Contents:          g.Contents,
		Tools:             g.Tools,
		ToolConfig:        g.ToolConfig,
		CreateTime:        g.CreateTime,
		UpdateTime:        g.UpdateTime,
		ExpireTime:        g.ExpireTime,
		UsageMetadata:     g.UsageMetadata,
	}
}

func validateTTLExpireMutex(ttl, expireTime *string) *schemas.BifrostError {
	if ttl != nil && *ttl != "" && expireTime != nil && *expireTime != "" {
		return providerUtils.NewBifrostOperationError("ttl and expire_time are mutually exclusive", nil)
	}
	return nil
}

func (provider *GeminiProvider) CachedContentCreate(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCachedContentCreateRequest) (*schemas.BifrostCachedContentCreateResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.CachedContentCreateRequest); err != nil {
		return nil, err
	}
	if err := validateTTLExpireMutex(request.TTL, request.ExpireTime); err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, providerUtils.NewBifrostOperationError("model is required for cached content create", nil)
	}

	model := request.Model
	if !strings.HasPrefix(model, "models/") {
		model = "models/" + model
	}

	jsonBody, useRaw := providerUtils.CheckAndGetRawRequestBody(ctx, request)
	if useRaw && len(jsonBody) > 0 {
		var err error
		jsonBody, err = sjson.SetBytes(jsonBody, "model", model)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError("failed to set cached content model", err)
		}
	} else {
		body := geminiCachedContent{
			Model:             model,
			SystemInstruction: request.SystemInstruction,
			Contents:          request.Contents,
			Tools:             request.Tools,
			ToolConfig:        request.ToolConfig,
		}
		if request.DisplayName != nil {
			body.DisplayName = *request.DisplayName
		}
		if request.TTL != nil {
			body.TTL = *request.TTL
		}
		if request.ExpireTime != nil {
			body.ExpireTime = *request.ExpireTime
		}

		var err error
		jsonBody, err = sonic.Marshal(body)
		if err != nil {
			return nil, providerUtils.NewBifrostOperationError("failed to marshal cached content create body", err)
		}
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	requestURL := fmt.Sprintf("%s/cachedContents", provider.networkConfig.BaseURL)
	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodPost)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.SetBody(jsonBody)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, bifrostErr
	}
	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	respBody, decErr := providerUtils.CheckAndDecodeBody(resp)
	if decErr != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decErr)
	}

	var geminiResp geminiCachedContent
	if err := sonic.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	return &schemas.BifrostCachedContentCreateResponse{
		Name:              geminiResp.Name,
		DisplayName:       geminiResp.DisplayName,
		Model:             geminiResp.Model,
		SystemInstruction: geminiResp.SystemInstruction,
		Contents:          geminiResp.Contents,
		Tools:             geminiResp.Tools,
		ToolConfig:        geminiResp.ToolConfig,
		CreateTime:        geminiResp.CreateTime,
		UpdateTime:        geminiResp.UpdateTime,
		ExpireTime:        geminiResp.ExpireTime,
		UsageMetadata:     geminiResp.UsageMetadata,
		ExtraFields: schemas.BifrostResponseExtraFields{
			Latency: latency.Milliseconds(),
		},
	}, nil
}

func (provider *GeminiProvider) cachedContentListByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCachedContentListRequest) (*schemas.BifrostCachedContentListResponse, time.Duration, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	requestURL := fmt.Sprintf("%s/cachedContents", provider.networkConfig.BaseURL)
	queryArgs := url.Values{}
	if request.PageSize > 0 {
		queryArgs.Set("pageSize", strconv.Itoa(request.PageSize))
	}
	if request.PageToken != nil && *request.PageToken != "" {
		queryArgs.Set("pageToken", *request.PageToken)
	}
	if len(queryArgs) > 0 {
		requestURL += "?" + queryArgs.Encode()
	}

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, latency, bifrostErr
	}
	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, latency, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	respBody, decErr := providerUtils.CheckAndDecodeBody(resp)
	if decErr != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decErr)
	}

	var geminiList geminiCachedContentList
	if err := sonic.Unmarshal(respBody, &geminiList); err != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	bifrostObjects := make([]schemas.CachedContentObject, 0, len(geminiList.CachedContents))
	for i := range geminiList.CachedContents {
		bifrostObjects = append(bifrostObjects, geminiList.CachedContents[i].toBifrostObject())
	}

	return &schemas.BifrostCachedContentListResponse{
		CachedContents: bifrostObjects,
		NextPageToken:  geminiList.NextPageToken,
	}, latency, nil
}

func (provider *GeminiProvider) CachedContentList(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentListRequest) (*schemas.BifrostCachedContentListResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.CachedContentListRequest); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for cached content list", nil)
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {
		resp, latency, bifrostErr := provider.cachedContentListByKey(ctx, key, request)
		if bifrostErr == nil {
			resp.ExtraFields = schemas.BifrostResponseExtraFields{Latency: latency.Milliseconds()}
			return resp, nil
		}
		lastErr = bifrostErr
	}
	return nil, lastErr
}

func (provider *GeminiProvider) cachedContentRetrieveByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCachedContentRetrieveRequest) (*schemas.BifrostCachedContentRetrieveResponse, time.Duration, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	name, idErr := geminiResourcePath(request.Name, "cachedContents", "name")
	if idErr != nil {
		return nil, 0, idErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, name)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodGet)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, latency, bifrostErr
	}
	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, latency, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	respBody, decErr := providerUtils.CheckAndDecodeBody(resp)
	if decErr != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decErr)
	}

	var geminiResp geminiCachedContent
	if err := sonic.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	return &schemas.BifrostCachedContentRetrieveResponse{
		Name:              geminiResp.Name,
		DisplayName:       geminiResp.DisplayName,
		Model:             geminiResp.Model,
		SystemInstruction: geminiResp.SystemInstruction,
		Contents:          geminiResp.Contents,
		Tools:             geminiResp.Tools,
		ToolConfig:        geminiResp.ToolConfig,
		CreateTime:        geminiResp.CreateTime,
		UpdateTime:        geminiResp.UpdateTime,
		ExpireTime:        geminiResp.ExpireTime,
		UsageMetadata:     geminiResp.UsageMetadata,
	}, latency, nil
}

func (provider *GeminiProvider) CachedContentRetrieve(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentRetrieveRequest) (*schemas.BifrostCachedContentRetrieveResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.CachedContentRetrieveRequest); err != nil {
		return nil, err
	}
	if request.Name == "" {
		return nil, providerUtils.NewBifrostOperationError("name is required for cached content retrieve", nil)
	}
	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for cached content retrieve", nil)
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {
		resp, latency, bifrostErr := provider.cachedContentRetrieveByKey(ctx, key, request)
		if bifrostErr == nil {
			resp.ExtraFields = schemas.BifrostResponseExtraFields{Latency: latency.Milliseconds()}
			return resp, nil
		}
		lastErr = bifrostErr
	}
	return nil, lastErr
}

func (provider *GeminiProvider) cachedContentUpdateByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCachedContentUpdateRequest) (*schemas.BifrostCachedContentUpdateResponse, time.Duration, *schemas.BifrostError) {
	body := geminiCachedContent{}
	updateMaskFields := []string{}
	if request.TTL != nil && *request.TTL != "" {
		body.TTL = *request.TTL
		updateMaskFields = append(updateMaskFields, "ttl")
	}
	if request.ExpireTime != nil && *request.ExpireTime != "" {
		body.ExpireTime = *request.ExpireTime
		updateMaskFields = append(updateMaskFields, "expireTime")
	}

	jsonBody, useRaw := providerUtils.CheckAndGetRawRequestBody(ctx, request)
	if !useRaw || len(jsonBody) == 0 {
		var marshalErr error
		jsonBody, marshalErr = sonic.Marshal(body)
		if marshalErr != nil {
			return nil, 0, providerUtils.NewBifrostOperationError("failed to marshal cached content update body", marshalErr)
		}
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	name, idErr := geminiResourcePath(request.Name, "cachedContents", "name")
	if idErr != nil {
		return nil, 0, idErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, name)
	if len(updateMaskFields) > 0 {
		requestURL += "?updateMask=" + strings.Join(updateMaskFields, ",")
	}

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodPatch)
	req.Header.SetContentType("application/json")
	setGeminiAuthHeader(req, key.Value.GetValue())
	req.SetBody(jsonBody)

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, latency, bifrostErr
	}
	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, latency, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	respBody, decErr := providerUtils.CheckAndDecodeBody(resp)
	if decErr != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseDecode, decErr)
	}

	var geminiResp geminiCachedContent
	if err := sonic.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, latency, providerUtils.NewBifrostOperationError(schemas.ErrProviderResponseUnmarshal, err)
	}

	return &schemas.BifrostCachedContentUpdateResponse{
		Name:              geminiResp.Name,
		DisplayName:       geminiResp.DisplayName,
		Model:             geminiResp.Model,
		SystemInstruction: geminiResp.SystemInstruction,
		Contents:          geminiResp.Contents,
		Tools:             geminiResp.Tools,
		ToolConfig:        geminiResp.ToolConfig,
		CreateTime:        geminiResp.CreateTime,
		UpdateTime:        geminiResp.UpdateTime,
		ExpireTime:        geminiResp.ExpireTime,
		UsageMetadata:     geminiResp.UsageMetadata,
	}, latency, nil
}

func (provider *GeminiProvider) CachedContentUpdate(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentUpdateRequest) (*schemas.BifrostCachedContentUpdateResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.CachedContentUpdateRequest); err != nil {
		return nil, err
	}
	if request.Name == "" {
		return nil, providerUtils.NewBifrostOperationError("name is required for cached content update", nil)
	}
	if err := validateTTLExpireMutex(request.TTL, request.ExpireTime); err != nil {
		return nil, err
	}
	if (request.TTL == nil || *request.TTL == "") && (request.ExpireTime == nil || *request.ExpireTime == "") {
		return nil, providerUtils.NewBifrostOperationError("either ttl or expire_time must be set for cached content update", nil)
	}
	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for cached content update", nil)
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {
		resp, latency, bifrostErr := provider.cachedContentUpdateByKey(ctx, key, request)
		if bifrostErr == nil {
			resp.ExtraFields = schemas.BifrostResponseExtraFields{Latency: latency.Milliseconds()}
			return resp, nil
		}
		lastErr = bifrostErr
	}
	return nil, lastErr
}

func (provider *GeminiProvider) cachedContentDeleteByKey(ctx *schemas.BifrostContext, key schemas.Key, request *schemas.BifrostCachedContentDeleteRequest) (*schemas.BifrostCachedContentDeleteResponse, time.Duration, *schemas.BifrostError) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	name, idErr := geminiResourcePath(request.Name, "cachedContents", "name")
	if idErr != nil {
		return nil, 0, idErr
	}
	requestURL := fmt.Sprintf("%s/%s", provider.networkConfig.BaseURL, name)

	providerUtils.SetExtraHeaders(ctx, req, provider.networkConfig.ExtraHeaders, nil)
	req.SetRequestURI(requestURL)
	req.Header.SetMethod(http.MethodDelete)
	setGeminiAuthHeader(req, key.Value.GetValue())

	latency, bifrostErr, wait := providerUtils.MakeRequestWithContext(ctx, provider.client, req, resp)
	defer wait()
	if bifrostErr != nil {
		return nil, latency, bifrostErr
	}
	if resp.StatusCode() != fasthttp.StatusOK {
		return nil, latency, providerUtils.SetErrorLatency(parseGeminiError(resp), latency)
	}

	return &schemas.BifrostCachedContentDeleteResponse{
		Name:    name,
		Deleted: true,
	}, latency, nil
}

func (provider *GeminiProvider) CachedContentDelete(ctx *schemas.BifrostContext, keys []schemas.Key, request *schemas.BifrostCachedContentDeleteRequest) (*schemas.BifrostCachedContentDeleteResponse, *schemas.BifrostError) {
	if err := providerUtils.CheckOperationAllowed(schemas.Gemini, provider.customProviderConfig, schemas.CachedContentDeleteRequest); err != nil {
		return nil, err
	}
	if request.Name == "" {
		return nil, providerUtils.NewBifrostOperationError("name is required for cached content delete", nil)
	}
	if len(keys) == 0 {
		return nil, providerUtils.NewBifrostOperationError("no keys provided for cached content delete", nil)
	}

	var lastErr *schemas.BifrostError
	for _, key := range keys {
		resp, latency, bifrostErr := provider.cachedContentDeleteByKey(ctx, key, request)
		if bifrostErr == nil {
			resp.ExtraFields = schemas.BifrostResponseExtraFields{Latency: latency.Milliseconds()}
			return resp, nil
		}
		lastErr = bifrostErr
	}
	return nil, lastErr
}
