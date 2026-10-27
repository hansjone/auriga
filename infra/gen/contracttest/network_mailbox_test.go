package contracttest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	collabv1 "github.com/hansjone/auriga/infra/gen/auriga/collab/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Generated contract checks only: no ingress, IAM, database or signing implementation.
func TestNetworkMailboxLocalRegisterDescriptor(t *testing.T) {
	file := collabv1.File_auriga_collab_v1_network_mailbox_proto
	service := file.Services().ByName("NetworkMailboxService")
	names := []protoreflect.Name{"Submit", "ReceiveSet", "AttemptBegin", "ExecutionAdmit", "Ready", "Head", "Fetch", "Receipt", "LocalRegister", "SourceRegister", "SourceGet", "SourceRevoke"}
	if file.Services().Len() != 1 || file.Messages().Len() != 25 || service == nil || service.Methods().Len() != len(names) || len(collabv1.NetworkMailboxService_ServiceDesc.Methods) != len(names) || len(collabv1.NetworkMailboxService_ServiceDesc.Streams) != 0 {
		t.Fatal("expected twelve unary mailbox methods and twenty-five messages")
	}
	for i, name := range names {
		method := service.Methods().Get(i)
		if method.Name() != name || method.Input().Name() != "NetworkMailboxService"+name+"Request" || method.Output().Name() != "NetworkMailboxService"+name+"Response" || method.IsStreamingClient() || method.IsStreamingServer() || collabv1.NetworkMailboxService_ServiceDesc.Methods[i].MethodName != string(name) {
			t.Fatalf("unexpected method shape: %s", name)
		}
		input := method.Input().Fields()
		count := 2
		if name == "Head" {
			count = 1
		}
		if input.Len() != count || input.ByNumber(1).Name() != "connection" || input.ByNumber(1).Kind() != protoreflect.MessageKind || input.ByNumber(1).Cardinality() != protoreflect.Optional || input.ByNumber(1).ContainingOneof() != nil || input.ByNumber(1).Message().FullName() != "auriga.collab.v1.NetworkMailboxConnection" {
			t.Fatalf("unexpected connection shape: %s", name)
		}
		if count == 2 {
			if name == "Submit" {
				assertSubmitBytes(t, input.ByNumber(2), "submit_json")
			} else {
				assertMailboxBytes(t, input.ByNumber(2))
			}
		}
		if method.Output().Fields().Len() != 1 {
			t.Fatalf("unexpected response fields: %s", name)
		}
		if name == "Submit" {
			assertSubmitBytes(t, method.Output().Fields().ByNumber(1), "result_json")
		} else {
			assertMailboxBytes(t, method.Output().Fields().ByNumber(1))
		}
	}
	connection := file.Messages().ByName("NetworkMailboxConnection").Fields()
	if connection.Len() != 7 {
		t.Fatal("connection fields changed")
	}
	for i, name := range []protoreflect.Name{"tenant_id", "realm", "node", "ingress", "server_id", "client_id", "client_account"} {
		field := connection.ByNumber(protoreflect.FieldNumber(i + 1))
		if field == nil || field.Name() != name || field.Kind() != protoreflect.StringKind {
			t.Fatalf("connection field changed: %s", name)
		}
	}
	response, err := (collabv1.UnimplementedNetworkMailboxServiceServer{}).LocalRegister(context.Background(), &collabv1.NetworkMailboxServiceLocalRegisterRequest{})
	if response != nil || status.Code(err) != codes.Unimplemented {
		t.Fatal("unimplemented registration must fail closed")
	}
}

func assertSubmitBytes(t *testing.T, field protoreflect.FieldDescriptor, name protoreflect.Name) {
	t.Helper()
	if field == nil || field.Name() != name || field.Kind() != protoreflect.BytesKind || field.Cardinality() != protoreflect.Optional || field.ContainingOneof() != nil {
		t.Fatal("submit must preserve complete JSON bytes")
	}
}

func assertMailboxBytes(t *testing.T, field protoreflect.FieldDescriptor) {
	t.Helper()
	if field == nil || field.Name() != "frame_json" || field.Kind() != protoreflect.BytesKind || field.Cardinality() != protoreflect.Optional || field.ContainingOneof() != nil {
		t.Fatal("frame must remain a singular bytes field")
	}
}

func TestNetworkMailboxLocalRegisterWireRoundTrip(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/network/fixtures/frames.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Frames map[string]struct{ Value json.RawMessage } `json:"frames"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	frame := bytes.ReplaceAll(fixtures.Frames["local_register"].Value, []byte(`"42"`), []byte(`"18446744073709551615"`))
	frame = bytes.ReplaceAll(frame, []byte(`"419"`), []byte(`"18446744073709551615"`))
	frame = bytes.ReplaceAll(frame, []byte(`"418"`), []byte(`"18446744073709551615"`))
	if bytes.Count(frame, []byte(`"18446744073709551615"`)) != 3 {
		t.Fatal("complete request fixture must exercise three uint64 maximum strings")
	}
	request := &collabv1.NetworkMailboxServiceLocalRegisterRequest{
		Connection: &collabv1.NetworkMailboxConnection{TenantId: "tenant-a", Realm: "2", Node: "64", Ingress: "ingress-a", ServerId: "server-a", ClientId: "client-a", ClientAccount: "account-a"}, FrameJson: frame,
	}
	inputs := []proto.Message{request}
	for _, name := range []string{"register_result", "register_result_subagent", "error"} {
		if len(fixtures.Frames[name].Value) == 0 {
			t.Fatalf("missing complete signed fixture: %s", name)
		}
		inputs = append(inputs, &collabv1.NetworkMailboxServiceLocalRegisterResponse{FrameJson: fixtures.Frames[name].Value})
	}
	for _, input := range inputs {
		wire, err := proto.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		output := input.ProtoReflect().Type().New().Interface()
		if err := proto.Unmarshal(wire, output); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(input, output) {
			t.Fatal("complete frame bytes or authenticated connection changed on wire")
		}
	}
}
