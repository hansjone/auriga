package sharednode

import (
	"errors"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/protobuf/proto"
)

// Ordinary execution has its own version and immutable task handle. It cannot
// be combined with company capabilities or turn into one on a later refresh.
func validTaskScopes(audience string, scopes []string) bool {
	if len(scopes) == 0 {
		return false
	}
	var allowed []string
	switch audience {
	case "auriga.shared.enterprise":
		if has(scopes, "task.execute") {
			return len(scopes) == 1
		}
		allowed = []string{"credentials", "outbound"}
	case "auriga.shared.model":
		allowed = []string{"model.invoke"}
	case "auriga.shared.message":
		allowed = []string{"message.send", "message.receive"}
	default:
		return false
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if seen[scope] || !has(allowed, scope) {
			return false
		}
		seen[scope] = true
	}
	return true
}

func checkAuthorization(cap *nodev1.SharedDshCapability, out *iamv1.AuthorizeSharedCapabilityResponse) error {
	if out == nil || !out.Active || !proto.Equal(cap.Binding, out.Binding) || cap.CredentialVersion != out.CredentialVersion {
		return errors.New("shared.revoked")
	}
	if cap.Audience == "auriga.shared.enterprise" {
		switch out.EnterpriseCredentialState {
		case iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_ABSENT:
			if !has(cap.Scopes, "task.execute") {
				return errors.New("common.forbidden")
			}
		case iamv1.SharedEnterpriseCredentialState_SHARED_ENTERPRISE_CREDENTIAL_STATE_AVAILABLE:
		default:
			return errors.New("common.unavailable")
		}
	}
	return nil
}
