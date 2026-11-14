package bifrostbackend

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedauth"
	"github.com/hansjone/auriga/infra/ai-gateway/internal/sharedmodel"
	api "github.com/hansjone/auriga/infra/gen/aiapi"
)

func TestSharedOriginMatchesMemberBindingAndCall(t *testing.T) {
	binding := sharedauth.Binding{TenantID: uuid.NewString(), MemberID: uuid.NewString(), PlacementID: uuid.NewString(), PhysicalInstanceID: uuid.NewString(), Mode: "shared", Epoch: 1, Revision: 1, SessionID: uuid.NewString(), PlatformSessionID: uuid.NewString(), AuthGeneration: 1}
	callID := uuid.NewString()
	document, _ := json.Marshal(binding)
	metadata, _ := json.Marshal(map[string]string{"auriga_shared_binding": string(document), "auriga_shared_call": callID})
	key := api.RelayKey{Id: binding.MemberID, Source: "member"}
	origin, err := sharedOrigin(metadata, binding.TenantID, key, sharedmodel.CallKey(binding, callID))
	if err != nil || origin == nil || *origin != binding {
		t.Fatalf("%+v %v", origin, err)
	}
	key.Id = uuid.NewString()
	if _, err := sharedOrigin(metadata, binding.TenantID, key, sharedmodel.CallKey(binding, callID)); err == nil {
		t.Fatal("different member accepted")
	}
	key.Id = binding.MemberID
	if _, err := sharedOrigin(metadata, binding.TenantID, key, "old-call"); err == nil {
		t.Fatal("call key mismatch")
	}
	if _, err := sharedOrigin(metadata, uuid.NewString(), key, sharedmodel.CallKey(binding, callID)); err == nil {
		t.Fatal("different tenant accepted")
	}
	if origin, err := sharedOrigin(json.RawMessage(`{"device_id":"original"}`), binding.TenantID, key, ""); err != nil || origin != nil {
		t.Fatal("existing member path changed")
	}
}
