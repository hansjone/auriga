package sharedauth

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	node "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type rpcAuthorityPeer struct {
	iam.UnimplementedSharedTaskServiceServer
	resolve func(context.Context, *iam.ResolveSharedTaskRequest) (*iam.ResolveSharedTaskResponse, error)
}

func (p *rpcAuthorityPeer) ResolveSharedTask(ctx context.Context, request *iam.ResolveSharedTaskRequest) (*iam.ResolveSharedTaskResponse, error) {
	return p.resolve(ctx, request)
}

func authorityClient(t *testing.T, peer *rpcAuthorityPeer) iam.SharedTaskServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	iam.RegisterSharedTaskServiceServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///shared-authority",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close(); server.Stop(); _ = listener.Close() })
	return iam.NewSharedTaskServiceClient(connection)
}

func authorityBinding() *node.SharedDshSessionBinding {
	return &node.SharedDshSessionBinding{TenantId: uuid.NewString(), MemberId: uuid.NewString(),
		PlacementId: uuid.NewString(), PhysicalInstanceId: uuid.NewString(),
		Mode: node.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1,
		SessionId: uuid.NewString(), PlatformSessionId: uuid.NewString(), AuthGeneration: 1}
}

func TestRPCAuthorityDelegatesTheTrustedTenantAndReadsCurrentSnapshot(t *testing.T) {
	binding := authorityBinding()
	query := Lookup{TenantID: binding.TenantId, SessionID: binding.SessionId, TaskID: uuid.NewString(), Audience: Model, JTI: uuid.NewString()}
	client := authorityClient(t, &rpcAuthorityPeer{resolve: func(ctx context.Context, request *iam.ResolveSharedTaskRequest) (*iam.ResolveSharedTaskResponse, error) {
		incoming, _ := metadata.FromIncomingContext(ctx)
		if scope := incoming.Get("auriga-tenant-id"); len(scope) != 1 || scope[0] != query.TenantID ||
			request.TenantId != query.TenantID || request.SessionId != query.SessionID ||
			request.TaskId != query.TaskID || request.Audience != query.Audience || request.Jti != query.JTI {
			return nil, status.Error(codes.PermissionDenied, "wrong delegation")
		}
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) > 5*time.Second {
			return nil, status.Error(codes.InvalidArgument, "unbounded authority call")
		}
		return &iam.ResolveSharedTaskResponse{Active: true, Binding: binding, CredentialVersion: 7, Scopes: []string{"model.invoke"}}, nil
	}})
	authority, err := NewRPCAuthority(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs("auriga-tenant-id", uuid.NewString()))
	value, err := authority.Resolve(ctx, query)
	if err != nil || !value.Active || value.Binding.TenantID != binding.TenantId || value.CredentialVersion != 7 {
		t.Fatalf("current authority snapshot missing: %v", err)
	}
}

func TestRPCAuthorityFailsClosedForRevocationOutageOrMismatchedBinding(t *testing.T) {
	for _, scenario := range []string{"revoked", "unavailable", "wrong-tenant", "wrong-scope"} {
		t.Run(scenario, func(t *testing.T) {
			binding := authorityBinding()
			query := Lookup{TenantID: binding.TenantId, SessionID: binding.SessionId, TaskID: uuid.NewString(), Audience: Model, JTI: uuid.NewString()}
			client := authorityClient(t, &rpcAuthorityPeer{resolve: func(context.Context, *iam.ResolveSharedTaskRequest) (*iam.ResolveSharedTaskResponse, error) {
				response := &iam.ResolveSharedTaskResponse{Active: true, Binding: binding, CredentialVersion: 1, Scopes: []string{"model.invoke"}}
				switch scenario {
				case "revoked":
					return nil, status.Error(codes.PermissionDenied, "revoked")
				case "unavailable":
					return nil, status.Error(codes.Unavailable, "authority unavailable")
				case "wrong-tenant":
					response.Binding = authorityBinding()
				case "wrong-scope":
					response.Scopes = []string{"credentials"}
				}
				return response, nil
			}})
			authority, err := NewRPCAuthority(client)
			if err != nil {
				t.Fatal(err)
			}
			value, err := authority.Resolve(t.Context(), query)
			switch scenario {
			case "revoked":
				if err != nil || value.Active {
					t.Fatal("revoked authority accepted")
				}
			case "unavailable":
				if !errors.Is(err, ErrUnavailable) {
					t.Fatal("outage did not fail closed")
				}
			default:
				if !errors.Is(err, ErrBinding) {
					t.Fatal("mismatched authority response accepted")
				}
			}
		})
	}
}
