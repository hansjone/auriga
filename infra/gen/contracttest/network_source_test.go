package contracttest

import (
	"testing"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// These checks exercise the generated public contract, not IAM authentication or persistence.
func TestNetworkSourceAuthorityDescriptorBoundary(t *testing.T) {
	file := iamv1.File_auriga_iam_v1_network_source_proto
	service := file.Services().ByName("NetworkSourceAuthorityService")
	if file.Services().Len() != 1 || service == nil || service.Methods().Len() != 5 {
		t.Fatal("source lifecycle must have a separate five-method service")
	}
	read := service.Methods().ByName("ReadAgentSourceReceipt")
	if read == nil || read.Input().FullName() != (&iamv1.ReadAgentSourceReceiptRequest{}).ProtoReflect().Descriptor().FullName() || read.Output().FullName() != (&iamv1.RegisterAgentSourceResponse{}).ProtoReflect().Descriptor().FullName() || read.IsStreamingClient() || read.IsStreamingServer() {
		t.Fatal("receipt read must be unary and reuse the complete original Register response")
	}
	original := read.Input().Fields().ByNumber(1)
	if read.Input().Fields().Len() != 1 || original == nil || original.Name() != "original_request" || original.Message().FullName() != (&iamv1.RegisterAgentSourceRequest{}).ProtoReflect().Descriptor().FullName() {
		t.Fatal("receipt wrapper must contain only the complete immutable original Register request")
	}
	fence := service.Methods().ByName("FenceAgentSourceReceipt")
	if fence == nil || fence.Input().FullName() != (&iamv1.FenceAgentSourceReceiptRequest{}).ProtoReflect().Descriptor().FullName() || fence.Output().FullName() != read.Output().FullName() || fence.IsStreamingClient() || fence.IsStreamingServer() {
		t.Fatal("receipt fence must be unary and reuse the complete original Register response")
	}
	frozen := fence.Input().Fields().ByNumber(1)
	if fence.Input().Fields().Len() != 1 || frozen == nil || frozen.Name() != "original_request" || frozen.Message().FullName() != original.Message().FullName() {
		t.Fatal("fence accepts only the frozen original Register request, with no current identity claims")
	}
	for _, name := range []protoreflect.Name{"RegisterAgentSource", "GetAgentSource", "RevokeAgentSource"} {
		method := service.Methods().ByName(name)
		if method == nil || method.Input().Name() != name+"Request" || method.Output().Name() != name+"Response" || method.IsStreamingClient() || method.IsStreamingServer() {
			t.Fatalf("missing independent unary request/response for %s", name)
		}
		for _, forbidden := range []protoreflect.Name{"owner_user_id", "device_id", "backing_session_id", "sender_kind", "host_session_id", "allowed"} {
			if method.Input().Fields().ByName(forbidden) != nil {
				t.Fatalf("request %s must not accept identity/permission claims: %s", name, forbidden)
			}
		}
	}
	witness := (&iamv1.NetworkAgentSourceRegistration{}).ProtoReflect().Descriptor()
	for _, name := range []protoreflect.Name{"registration_id", "allocation_op_id", "allocation_fingerprint", "registration_revision", "realm", "node", "local", "address_type"} {
		if witness.Fields().ByName(name) == nil {
			t.Fatalf("typed local witness missing %s", name)
		}
	}
	scope := (&iamv1.RegisterAgentSourceRequest{}).GetScope()
	if scope != iamv1.NetworkAgentSourceScope_NETWORK_AGENT_SOURCE_SCOPE_UNSPECIFIED {
		t.Fatal("empty request must not default to send scope")
	}
	if scope.Descriptor().Values().Len() != 2 || iamv1.NetworkAgentSourceState(0).String() != "NETWORK_AGENT_SOURCE_STATE_UNSPECIFIED" {
		t.Fatal("scope/state must retain explicit fail-closed zero values")
	}
	// The existing signing request retains exactly target connection + two bounded JSON blobs.
	delivery := (&iamv1.AuthorizeNetworkDeliveryRequest{}).ProtoReflect().Descriptor().Fields()
	if delivery.Len() != 3 || delivery.ByNumber(1).Name() != "connection" || delivery.ByNumber(2).Name() != "message_json" || delivery.ByNumber(3).Name() != "source_context_json" {
		t.Fatal("existing fifth RPC wire contract changed")
	}
}

func TestNetworkSourceAuthorityWireRoundTrip(t *testing.T) {
	const largest = uint64(18446744073709551615)
	input := &iamv1.GetAgentSourceResponse{
		Source: &iamv1.NetworkAgentSource{
			SourceBinding: "019a0000-0000-7000-8000-000000000001",
			Registration: &iamv1.NetworkAgentSourceRegistration{
				Realm: "2", Node: "63", Local: "18446744073709551615", AddressType: "session",
				RegistrationId: "019a0000-0000-7000-8000-000000000002", AllocationOpId: "allocate:1",
				AllocationFingerprint: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				RegistrationRevision:  largest,
			},
			Scope:    iamv1.NetworkAgentSourceScope_NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND,
			Revision: largest, State: iamv1.NetworkAgentSourceState_NETWORK_AGENT_SOURCE_STATE_REVOKED,
			NotBefore: "2026-10-26T00:00:00Z", ExpiresAt: "2026-10-26T00:05:00Z",
		},
	}
	encoded, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	output := &iamv1.GetAgentSourceResponse{}
	if err := proto.Unmarshal(encoded, output); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(input, output) {
		t.Fatal("opaque source witness, terminal state or uint64 revision changed on wire")
	}
}

// A successor has a named predecessor and its revision; operation_id is only
// idempotency. This verifies wire structure, not issuance, replay auth or CAS.
func TestNetworkSourceAuthoritySuccessorCASWire(t *testing.T) {
	request := &iamv1.RegisterAgentSourceRequest{
		Connection:  &iamv1.NetworkAuthorityConnection{TenantId: "tenant-a", Realm: "2", Node: "63", Ingress: "ingress-a"},
		OperationId: "source:second", PredecessorBinding: "source:first", ExpectedRevision: 2,
		Scope: iamv1.NetworkAgentSourceScope_NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND,
	}
	fields := request.ProtoReflect().Descriptor().Fields()
	predecessor := fields.ByNumber(6)
	if predecessor == nil || predecessor.Name() != "predecessor_binding" || predecessor.Kind() != protoreflect.StringKind || fields.ByNumber(5).Kind() != protoreflect.Uint64Kind {
		t.Fatal("successor must bind predecessor ID and uint64 revision without replacing existing fields")
	}
	source := &iamv1.NetworkAgentSource{SourceBinding: "source:second", PredecessorBinding: request.GetPredecessorBinding()}
	link := source.ProtoReflect().Descriptor().Fields().ByNumber(10)
	if link == nil || link.Name() != "predecessor_binding" || link.Kind() != protoreflect.StringKind {
		t.Fatal("saved successor must expose its immutable predecessor link")
	}
	for _, input := range []proto.Message{request, source} {
		encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		output := input.ProtoReflect().Type().New().Interface()
		if err := proto.Unmarshal(encoded, output); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(input, output) {
			t.Fatal("successor/replay request lost predecessor, revision or original connection")
		}
	}
	initial := &iamv1.RegisterAgentSourceRequest{}
	if initial.GetExpectedRevision() != 0 || initial.GetPredecessorBinding() != "" {
		t.Fatal("initial issuance must retain empty predecessor/zero CAS wire shape")
	}
}
