package sharedauth

import (
	"context"
	"crypto/ed25519"
	"time"

	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	node "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// RPCAuthority uses the normal authenticated IAM client. The transport owns its
// mTLS and rotated service JWT, while the trusted request supplies tenant scope.
type RPCAuthority struct {
	client iam.SharedTaskServiceClient
}

func NewRPCAuthority(client iam.SharedTaskServiceClient) (*RPCAuthority, error) {
	if client == nil {
		return nil, ErrInvalid
	}
	return &RPCAuthority{client: client}, nil
}

// NewRPCGate is the model/message runtime composition point. Enterprise signing
// keys and raw credentials never enter this process.
func NewRPCGate(client iam.SharedTaskServiceClient, keys map[string]ed25519.PublicKey, now func() time.Time) (*Gate, error) {
	authority, err := NewRPCAuthority(client)
	if err != nil {
		return nil, err
	}
	return New(authority, keys, now)
}

func (a *RPCAuthority) Resolve(ctx context.Context, query Lookup) (Snapshot, error) {
	if !UUID(query.TenantID) || !UUID(query.SessionID) || !UUID(query.TaskID) ||
		(query.Audience != Model && query.Audience != Message) || query.JTI != "" && !UUID(query.JTI) {
		return Snapshot{}, ErrInvalid
	}
	request, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	delegation, _ := metadata.FromOutgoingContext(request)
	delegation = delegation.Copy()
	delegation.Set("auriga-tenant-id", query.TenantID)
	request = metadata.NewOutgoingContext(request, delegation)
	response, err := a.client.ResolveSharedTask(request, &iam.ResolveSharedTaskRequest{
		TenantId: query.TenantID, SessionId: query.SessionID, TaskId: query.TaskID,
		Audience: query.Audience, Jti: query.JTI,
	})
	if err != nil {
		switch status.Code(err) {
		case codes.Unauthenticated, codes.PermissionDenied, codes.NotFound:
			return Snapshot{}, nil
		default:
			return Snapshot{}, ErrUnavailable
		}
	}
	if response == nil || request.Err() != nil {
		return Snapshot{}, ErrUnavailable
	}
	if !response.Active {
		return Snapshot{}, nil
	}
	binding, err := rpcBinding(response.Binding)
	if err != nil || binding.TenantID != query.TenantID || binding.SessionID != query.SessionID || !positive(response.CredentialVersion) {
		return Snapshot{}, ErrBinding
	}
	for _, scope := range response.Scopes {
		if !allowed(query.Audience, scope) {
			return Snapshot{}, ErrBinding
		}
	}
	return Snapshot{Binding: binding, CredentialVersion: response.CredentialVersion,
		Active: true, Scopes: append([]string(nil), response.Scopes...)}, nil
}

func rpcBinding(value *node.SharedDshSessionBinding) (Binding, error) {
	if value == nil || len(value.ProtoReflect().GetUnknown()) != 0 {
		return Binding{}, ErrBinding
	}
	mode := ""
	switch value.Mode {
	case node.DshInstanceMode_DSH_INSTANCE_MODE_SHARED:
		mode = "shared"
	case node.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED:
		mode = "dedicated"
	default:
		return Binding{}, ErrBinding
	}
	binding := Binding{TenantID: value.TenantId, MemberID: value.MemberId, PlacementID: value.PlacementId,
		PhysicalInstanceID: value.PhysicalInstanceId, Mode: mode, Epoch: value.Epoch, Revision: value.Revision,
		SessionID: value.SessionId, PlatformSessionID: value.PlatformSessionId, AuthGeneration: value.AuthGeneration}
	if !binding.Valid() {
		return Binding{}, ErrBinding
	}
	return binding, nil
}
