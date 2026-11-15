package bifrostbackend

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

// This metadata is set by the authenticated internal bridge. The ordinary
// device bridge overwrites caller metadata before forwarding. Backend member
// access additionally requires the deployment's internal BasicAuth credential.
func sharedOrigin(raw json.RawMessage, tenant string, key api.RelayKey, idempotencyKey string) (*sharedauth.Binding, error) {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if json.Unmarshal(raw, &fields) != nil {
		return nil, nil
	}
	if fields["auriga_shared_binding"] == nil && fields["auriga_shared_call"] == nil {
		return nil, nil
	}
	var document, callID string
	if json.Unmarshal(fields["auriga_shared_binding"], &document) != nil || json.Unmarshal(fields["auriga_shared_call"], &callID) != nil {
		return nil, invalid
	}
	var binding sharedauth.Binding
	decoder := json.NewDecoder(bytes.NewBufferString(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&binding) != nil || decoder.Decode(new(any)) != io.EOF || !binding.Valid() || !sharedauth.UUID(callID) || key.Source != "member" || key.Id != binding.MemberID || binding.TenantID != tenant || sharedmodel.CallKey(binding, callID) != idempotencyKey {
		return nil, invalid
	}
	return &binding, nil
}
