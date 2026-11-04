// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package gemini

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/valyala/fasthttp"
)

type GeminiStreamAPIError struct {
	Err *GeminiGenerationErrorStruct
}

func (e *GeminiStreamAPIError) Error() string {
	return fmt.Sprintf("gemini api error: %d %s - %s", e.Err.Code, e.Err.Status, e.Err.Message)
}

func ApplyRetryInfo(bifrostErr *schemas.BifrostError, details []GeminiGenerationErrorDetails) {
	for _, detail := range details {
		if detail.Type != googleRetryInfoType {
			continue
		}
		if delay, err := time.ParseDuration(detail.RetryDelay); err == nil {
			providerUtils.SetRetryAfter(bifrostErr, delay)
		}
		return
	}
}

func toGeminiStreamBifrostError(err error) *schemas.BifrostError {
	bifrostErr := &schemas.BifrostError{
		Type:           schemas.Ptr("gemini_api_error"),
		IsBifrostError: false,
		Error: &schemas.ErrorField{
			Message: err.Error(),
			Error:   err,
		},
	}
	var apiErr *GeminiStreamAPIError
	if errors.As(err, &apiErr) {
		bifrostErr.StatusCode = schemas.Ptr(apiErr.Err.Code)
		bifrostErr.Error.Code = schemas.Ptr(strconv.Itoa(apiErr.Err.Code))
		bifrostErr.Error.Message = apiErr.Err.Message
		if apiErr.Err.Status != "" {
			bifrostErr.Error.Type = schemas.Ptr(apiErr.Err.Status)
		}
		ApplyRetryInfo(bifrostErr, apiErr.Err.Details)
	}

	if bifrostErr.StatusCode == nil || *bifrostErr.StatusCode == 0 {
		bifrostErr.StatusCode = schemas.Ptr(fasthttp.StatusBadGateway)
	}
	return bifrostErr
}

func parseGeminiError(resp *fasthttp.Response) *schemas.BifrostError {

	var errorResps []GeminiGenerationError
	bifrostErr := providerUtils.HandleProviderAPIError(resp, &errorResps)
	if len(errorResps) > 0 {
		var message string
		var firstError *GeminiGenerationErrorStruct
		for _, errorResp := range errorResps {
			if errorResp.Error != nil {
				if firstError == nil {
					firstError = errorResp.Error
				}
				message = message + errorResp.Error.Message + "\n"
			}
		}

		message = strings.TrimSuffix(message, "\n")
		if bifrostErr.Error == nil {
			bifrostErr.Error = &schemas.ErrorField{}
		}

		if firstError != nil {
			bifrostErr.Error.Code = schemas.Ptr(strconv.Itoa(firstError.Code))
			if firstError.Status != "" {
				bifrostErr.Error.Type = schemas.Ptr(firstError.Status)
			}
			ApplyRetryInfo(bifrostErr, firstError.Details)
		}

		bifrostErr.Error.Message = message
		return bifrostErr
	}

	var errorResp GeminiGenerationError
	bifrostErr = providerUtils.HandleProviderAPIError(resp, &errorResp)
	if errorResp.Error != nil {
		if bifrostErr.Error == nil {
			bifrostErr.Error = &schemas.ErrorField{}
		}
		bifrostErr.Error.Code = schemas.Ptr(strconv.Itoa(errorResp.Error.Code))
		bifrostErr.Error.Message = errorResp.Error.Message
		if errorResp.Error.Status != "" {
			bifrostErr.Error.Type = schemas.Ptr(errorResp.Error.Status)
		}
		ApplyRetryInfo(bifrostErr, errorResp.Error.Details)
	}
	return bifrostErr
}
