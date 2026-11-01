// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package openai

import (
	"fmt"
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type ErrorConverter func(resp *fasthttp.Response) *schemas.BifrostError

func responsesStreamError(response *schemas.BifrostResponsesStreamResponse) *schemas.BifrostError {
	eventType := string(response.Type)
	bifrostErr := &schemas.BifrostError{
		Type:           schemas.Ptr(eventType),
		IsBifrostError: false,
		Error:          &schemas.ErrorField{},
	}

	if response.Message != nil {
		bifrostErr.Error.Message = *response.Message
	}
	if response.Param != nil {
		bifrostErr.Error.Param = *response.Param
	}
	if response.Code != nil {
		bifrostErr.Error.Code = response.Code
	}

	if response.Error != nil {
		if response.Error.Type != "" {
			bifrostErr.Error.Type = schemas.Ptr(response.Error.Type)
		}
		if response.Error.Message != "" && bifrostErr.Error.Message == "" {
			bifrostErr.Error.Message = response.Error.Message
		}
		if response.Error.Code != "" && (bifrostErr.Error.Code == nil || *bifrostErr.Error.Code == "") {
			bifrostErr.Error.Code = schemas.Ptr(response.Error.Code)
		}
	}

	if response.Response != nil && response.Response.Error != nil {
		if response.Response.Error.Type != "" && bifrostErr.Error.Type == nil {
			bifrostErr.Error.Type = schemas.Ptr(response.Response.Error.Type)
		}
		if response.Response.Error.Message != "" && bifrostErr.Error.Message == "" {
			bifrostErr.Error.Message = response.Response.Error.Message
		}
		if response.Response.Error.Code != "" && (bifrostErr.Error.Code == nil || *bifrostErr.Error.Code == "") {
			bifrostErr.Error.Code = schemas.Ptr(response.Response.Error.Code)
		}
	}

	applyStreamErrorStatus(bifrostErr)

	if bifrostErr.Error.Message == "" {
		details := eventType
		if bifrostErr.Error.Type != nil && *bifrostErr.Error.Type != "" && *bifrostErr.Error.Type != eventType {
			details += ", type=" + *bifrostErr.Error.Type
		}
		if bifrostErr.Error.Code != nil && *bifrostErr.Error.Code != "" {
			details += ", code=" + *bifrostErr.Error.Code
		}
		bifrostErr.Error.Message = fmt.Sprintf("provider stream error (%s)", details)
	}

	return bifrostErr
}

func applyStreamErrorStatus(bifrostErr *schemas.BifrostError) {
	if bifrostErr == nil || bifrostErr.StatusCode != nil {
		return
	}
	bifrostErr.StatusCode = schemas.Ptr(streamErrorStatus(bifrostErr.Error))
}

func streamErrorStatus(field *schemas.ErrorField) int {
	var code, errType string
	if field != nil {
		if field.Code != nil {
			code = *field.Code
		}
		if field.Type != nil {
			errType = *field.Type
		}
	}
	switch {
	case matchesStreamErrorSignal(code, errType, "rate_limit_exceeded", "rate_limit_error", "too_many_requests", "insufficient_quota"):
		return fasthttp.StatusTooManyRequests
	case matchesStreamErrorSignal(code, errType, "context_length_exceeded", "invalid_prompt", "invalid_request_error"):
		return fasthttp.StatusBadRequest
	}

	return fasthttp.StatusBadGateway
}

func matchesStreamErrorSignal(code, errType string, want ...string) bool {
	for _, w := range want {
		if code == w || errType == w {
			return true
		}
	}
	return false
}

func ParseOpenAIError(resp *fasthttp.Response) *schemas.BifrostError {
	var errorResp schemas.BifrostError

	bifrostErr := providerUtils.HandleProviderAPIError(resp, &errorResp)

	if errorResp.EventID != nil {
		bifrostErr.EventID = errorResp.EventID
	}

	if errorResp.Error != nil {
		if bifrostErr.Error == nil {
			bifrostErr.Error = &schemas.ErrorField{}
		}
		bifrostErr.Error.Type = errorResp.Error.Type
		bifrostErr.Error.Code = errorResp.Error.Code
		if errorResp.Error.Message != "" {
			bifrostErr.Error.Message = errorResp.Error.Message
		}
		bifrostErr.Error.Param = errorResp.Error.Param
		if errorResp.Error.EventID != nil {
			bifrostErr.Error.EventID = errorResp.Error.EventID
		}
	}

	if bifrostErr.Error == nil {
		bifrostErr.Error = &schemas.ErrorField{}
	}
	if strings.TrimSpace(bifrostErr.Error.Message) == "" {
		if bifrostErr.StatusCode != nil {
			bifrostErr.Error.Message = fmt.Sprintf("provider API error (status %d)", *bifrostErr.StatusCode)
		} else {
			bifrostErr.Error.Message = "provider API error"
		}
	}

	return bifrostErr
}
