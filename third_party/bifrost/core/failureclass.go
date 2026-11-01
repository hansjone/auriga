package bifrost

import (
	"slices"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

var callerFaultStatusCodes = []int{400, 409, 413, 415, 422}
var credentialTypes = []string{
	"authentication_error",
	"UNAUTHENTICATED",
	"UnrecognizedClientException",
	"InvalidSignatureException",
	"ExpiredTokenException",
	"IncompleteSignatureException",
}
var retiredModelMarkers = []string{
	"deprecated",
	"deprecation",
	"decommissioned",
	"retired",
	"end of its life",
	"end-of-life",
	"no longer available",
	"was removed",
	"has been removed",
}
var rateLimitPatterns = []string{
	"rate limit",
	"rate_limit",
	"ratelimit",
	"too many requests",
	"quota exceeded",
	"quota_exceeded",
	"request limit",
	"throttled",
	"throttling",
	"rate exceeded",
	"limit exceeded",
	"requests per",
	"rpm exceeded",
	"tpm exceeded",
	"tokens per minute",
	"requests per minute",
	"requests per second",
	"api rate limit",
	"usage limit",
	"concurrent requests limit",
	"burst_rate",
	"rate increased",
}
var bedrockModelMarkers = []string{
	"model id",
	"on-demand throughput",
	"inference profile",
}
var regionMarkers = []string{
	"unsupported countries",
	"location is not supported",
	"not available in your country",
}
var transientServerStatusCodes = []int{500, 502, 503, 504, 529}
var googleModelMarkers = []string{"models/", "publisher model"}
var retiredModelCodes = []string{"model_decommissioned", "ServiceModelDeprecating"}
var openRouterModelMarkers = []string{"no endpoints found", "no allowed providers"}
var perKeyStatusCodes = []int{401, 402, 403, 429}
var quotaCodes = []string{"insufficient_quota", "blocked_api_access"}
var credentialMarkers = []string{
	"api key not valid",
	"api_key_invalid",
	"api key expired",
	"api key is valid",
	"invalid api key",
	"security token",
}
var googleModelTypes = []string{"NOT_FOUND", "PERMISSION_DENIED", "FAILED_PRECONDITION"}
var quotaMarkers = []string{
	"credit balance",
	"spend limit",
	"api usage limits",
	"prepayment credits",
	"credits are depleted",
	"spending cap",
	"requires billing to be enabled",
	"key limit exceeded",
	"account is not active",
}
var modelAccessTypes = []string{
	"model_not_found",
	"invalid_model",
	"permission_error",
	"AccessDeniedException",
}
var anthropicModelMarkers = []string{"model:", "is not available"}
var credentialCodes = []string{
	"invalid_api_key",
	"organization_restricted",
}
var modelAccessCodes = []string{
	"model_not_found",
	"DeploymentNotFound",
	"unknown_model",
	"model_terms_required",
}
var credentialSharedTypes = []string{
	"INVALID_ARGUMENT",
	"AccessDeniedException",
	"permission_error",
}

func IsRateLimitErrorMessage(errorMessage string) bool {
	if errorMessage == "" {
		return false
	}
	return containsAny(strings.ToLower(errorMessage), rateLimitPatterns)
}

func ClassifyFailure(err *schemas.BifrostError) schemas.FailureClass {
	if err == nil {
		return ""
	}
	if err.Error != nil && err.Error.Type != nil {
		switch *err.Error.Type {
		case schemas.RequestTimedOut:
			return schemas.FailureClassTimeout
		case schemas.RequestCancelled:
			return schemas.FailureClassCancelled
		}
	}
	if err.IsBifrostError {
		return schemas.FailureClassUnknown
	}
	status := 0
	if err.StatusCode != nil {
		status = *err.StatusCode
	}
	var message, errType, code string
	hasParam := false
	if err.Error != nil {
		message = err.Error.Message
		if err.Error.Type != nil {
			errType = *err.Error.Type
		}
		if err.Error.Code != nil {
			code = *err.Error.Code
		}
		if s, ok := err.Error.Param.(string); ok {
			hasParam = s != ""
		} else {
			hasParam = err.Error.Param != nil
		}
	}
	if errType == "" && err.Type != nil {
		errType = *err.Type
	}
	lower := strings.ToLower(message)
	body := hasBody(message)
	transient := slices.Contains(transientServerStatusCodes, status)

	if message == schemas.ErrProviderDoRequest || message == schemas.ErrProviderNetworkError {
		return schemas.FailureClassTransient
	}

	if !transient && (status == 402 || slices.Contains(quotaCodes, code) || errType == "insufficient_quota" || containsAny(lower, quotaMarkers)) {
		return schemas.FailureClassQuota
	}

	if status == 429 || (status != 401 && status != 403 && hasRateLimitSignal(err)) {
		return schemas.FailureClassRateLimit
	}

	if transient {
		return schemas.FailureClassTransient
	}

	if code == "unsupported_country_region_territory" || ((status == 400 || status == 403) && containsAny(lower, regionMarkers)) {
		return schemas.FailureClassRegionBlocked
	}

	if status == 401 || slices.Contains(credentialTypes, errType) || slices.Contains(credentialCodes, code) ||
		(slices.Contains(credentialSharedTypes, errType) && containsAny(lower, credentialMarkers)) {
		return schemas.FailureClassCredential
	}

	if isModel, retired := modelSignals(status, errType, code, lower, body); isModel {
		if retired {
			return schemas.FailureClassModelGone
		}
		return schemas.FailureClassModelAccess
	}

	if status == 403 {
		return schemas.FailureClassCredential
	}

	if slices.Contains(callerFaultStatusCodes, status) && (errType != "" || code != "" || body) {
		return schemas.FailureClassCallerFault
	}
	if status == 404 && hasParam {
		return schemas.FailureClassCallerFault
	}
	return schemas.FailureClassUnknown
}

func modelSignals(status int, errType, code, lower string, body bool) (isModel, retired bool) {
	retired = slices.Contains(retiredModelCodes, code) || containsAny(lower, retiredModelMarkers)
	if status == 410 {
		return body || retired, true
	}
	if slices.Contains(modelAccessCodes, code) || slices.Contains(retiredModelCodes, code) || strings.HasPrefix(code, "model_permission_blocked") {
		return true, retired
	}
	switch {
	case slices.Contains(modelAccessTypes, errType):
		return true, retired
	case errType == "ResourceNotFoundException":
		return strings.Contains(lower, "model"), retired
	case errType == "ValidationException":

		retired = retired && strings.Contains(lower, "model")
		return retired || containsAny(lower, bedrockModelMarkers), retired
	case errType == "not_found_error":
		return containsAny(lower, anthropicModelMarkers), retired
	case slices.Contains(googleModelTypes, errType):
		return containsAny(lower, googleModelMarkers), retired
	}
	if status == 404 {
		if containsAny(lower, openRouterModelMarkers) {
			return true, retired
		}
		if strings.Contains(lower, "model") && (strings.Contains(lower, "not found") || retired) {
			return true, retired
		}
	}
	return false, false
}

func hasRateLimitSignal(err *schemas.BifrostError) bool {
	if err == nil || err.Error == nil {
		return false
	}
	if IsRateLimitErrorMessage(err.Error.Message) {
		return true
	}
	if err.Error.Type != nil && IsRateLimitErrorMessage(*err.Error.Type) {
		return true
	}
	return err.Error.Code != nil && IsRateLimitErrorMessage(*err.Error.Code)
}

func retriesWithoutRotation(err *schemas.BifrostError, class schemas.FailureClass) bool {
	switch class {
	case schemas.FailureClassTransient, schemas.FailureClassRateLimit:
		return true
	case schemas.FailureClassRegionBlocked:
		return false
	}
	if err == nil || !class.IsPermanentPerKey() {
		return false
	}
	return (err.StatusCode != nil && slices.Contains(perKeyStatusCodes, *err.StatusCode)) || hasRateLimitSignal(err)
}

func hasBody(message string) bool {
	return message != "" &&
		message != schemas.ErrProviderResponseHTML &&
		!strings.HasPrefix(message, schemas.ErrProviderResponseEmpty) &&
		!strings.HasPrefix(message, "provider API error")
}

func containsAny(lower string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
