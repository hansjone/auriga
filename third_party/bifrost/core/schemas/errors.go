// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type ErrorType string

const (
	RequestCancelled         = "request_cancelled"
	RequestTimedOut          = "request_timed_out"
	RequestDropped           = "request_dropped"
	ProviderConnectionFailed = "provider_connection_failed"

	NoKeySupportsModel = "no_key_supports_model"
)

const (
	ErrorTypeCallerModelNotAvailable ErrorType = "caller_model_not_available"
	ErrorTypeCallerModelUnknown      ErrorType = "caller_model_unknown"
	ErrorTypeCallerInvalidRequest    ErrorType = "caller_invalid_request"
	ErrorTypeCallerCancelled         ErrorType = "caller_cancelled"

	ErrorTypePolicyModelBlocked    ErrorType = "policy_model_blocked"
	ErrorTypePolicyProviderBlocked ErrorType = "policy_provider_blocked"
	ErrorTypePolicyAccessDenied    ErrorType = "policy_access_denied"
	ErrorTypePolicyBudgetExceeded  ErrorType = "policy_budget_exceeded"
	ErrorTypePolicyRateLimited     ErrorType = "policy_rate_limited"
	ErrorTypePolicyToolBlocked     ErrorType = "policy_tool_blocked"

	ErrorTypeProviderAuthFailed           ErrorType = "provider_auth_failed"
	ErrorTypeProviderBilling              ErrorType = "provider_billing"
	ErrorTypeProviderRateLimited          ErrorType = "provider_rate_limited"
	ErrorTypeProviderOverloaded           ErrorType = "provider_overloaded"
	ErrorTypeProviderServerError          ErrorType = "provider_server_error"
	ErrorTypeProviderTimeout              ErrorType = "provider_timeout"
	ErrorTypeProviderConnectionFailed     ErrorType = "provider_connection_failed"
	ErrorTypeProviderCredentialsExhausted ErrorType = "provider_credentials_exhausted"

	ErrorTypeBifrostDropped  ErrorType = "bifrost_dropped"
	ErrorTypeBifrostInternal ErrorType = "bifrost_internal"

	ErrorTypeOther ErrorType = "_OTHER"
)
