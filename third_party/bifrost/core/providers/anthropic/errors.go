package anthropic

import (
	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

func streamErrorStatus(errType string) int {
	switch errType {
	case "invalid_request_error":
		return fasthttp.StatusBadRequest
	case "authentication_error":
		return fasthttp.StatusUnauthorized
	case "permission_error":
		return fasthttp.StatusForbidden
	case "not_found_error":
		return fasthttp.StatusNotFound
	case "request_too_large":
		return fasthttp.StatusRequestEntityTooLarge
	case "rate_limit_error":
		return fasthttp.StatusTooManyRequests
	case "api_error":
		return fasthttp.StatusInternalServerError
	case "overloaded_error":

		return 529
	}

	return fasthttp.StatusBadGateway
}

func ParseAnthropicError(resp *fasthttp.Response) *schemas.BifrostError {
	var errorResp AnthropicError
	bifrostErr := providerUtils.HandleProviderAPIError(resp, &errorResp)
	if errorResp.Error != nil {
		if bifrostErr.Error == nil {
			bifrostErr.Error = &schemas.ErrorField{}
		}
		bifrostErr.Error.Type = &errorResp.Error.Type
		bifrostErr.Error.Message = errorResp.Error.Message
	}
	return bifrostErr
}
