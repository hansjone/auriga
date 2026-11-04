// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type FailureClass string

const (
	FailureClassCredential FailureClass = "credential"

	FailureClassModelAccess FailureClass = "model_access"

	FailureClassQuota FailureClass = "quota"

	FailureClassModelGone FailureClass = "model_gone"

	FailureClassRegionBlocked FailureClass = "region_blocked"

	FailureClassRateLimit FailureClass = "rate_limit"

	FailureClassCallerFault FailureClass = "caller_fault"

	FailureClassTransient FailureClass = "transient"

	FailureClassUnknown FailureClass = "unknown"

	FailureClassTimeout FailureClass = "timeout"

	FailureClassCancelled FailureClass = "cancelled"
)

func (c FailureClass) IsPerKey() bool {
	return c == FailureClassRateLimit || c.IsPermanentPerKey()
}

func (c FailureClass) IsPermanentPerKey() bool {
	switch c {
	case FailureClassCredential, FailureClassModelAccess, FailureClassQuota, FailureClassModelGone, FailureClassRegionBlocked:
		return true
	}
	return false
}

func (c FailureClass) FailReason() string {
	switch c {
	case FailureClassCredential:
		return "authentication_error"
	case FailureClassQuota:
		return "billing_error"
	case FailureClassRateLimit:
		return "rate_limit_error"
	case FailureClassModelAccess:
		return "model_access_error"
	case FailureClassModelGone:
		return "model_retired_error"
	case FailureClassRegionBlocked:
		return "region_blocked_error"
	}
	return ""
}
