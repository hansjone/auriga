package contracttest

import (
	"testing"

	iamv1 "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"google.golang.org/protobuf/proto"
)

func TestConnectionAnchorReadIsAdditiveAndContainsOnlyReferences(t *testing.T) {
	t.Parallel()
	service := iamv1.File_auriga_iam_v1_network_authority_proto.Services().ByName("NetworkAuthorityService")
	method := service.Methods().ByName("ReadNetworkConnectionAnchor")
	if method == nil || method.IsStreamingClient() || method.IsStreamingServer() {
		t.Fatal("missing unary internal anchor read")
	}
	for _, message := range []proto.Message{&iamv1.ReadNetworkConnectionAnchorRequest{}, &iamv1.ReadNetworkConnectionAnchorResponse{}} {
		fields := message.ProtoReflect().Descriptor().Fields()
		if fields.Len() != 2 || fields.ByNumber(1).Name() != "original" || fields.ByNumber(2).Name() != "current" {
			t.Fatal("anchor read must contain only the two exact references")
		}
	}
	anchor := (&iamv1.NetworkConnectionAnchor{}).ProtoReflect().Descriptor()
	if anchor.Fields().Len() != 2 || anchor.Fields().ByNumber(1).Name() != "anchor_id" || anchor.Fields().ByNumber(2).Message().FullName() != "auriga.iam.v1.NetworkAuthorityConnection" {
		t.Fatal("anchor exposes fields beyond opaque ID and complete C")
	}
	resolve := (&iamv1.ResolveNetworkTargetResponse{}).ProtoReflect().Descriptor().Fields().ByNumber(6)
	if resolve == nil || resolve.Name() != "connection_anchor" || resolve.Message() != anchor {
		t.Fatal("Resolve field 6 must be the optional anchor")
	}
	if iamv1.File_auriga_iam_v1_network_proto.Services().ByName("NetworkGatewayService").Methods().ByName("ReadNetworkConnectionAnchor") != nil {
		t.Fatal("gateway allowlist expanded")
	}
}
