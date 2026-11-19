// Command history-relay exposes the production Relay over loopback for the
// public Host enumeration regression. W and all identities are synthetic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/hansjone/auriga/infra/agent/internal/sharednode"
	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const tenant = "00000000-0000-4000-8000-000000000001"
const pool = "00000000-0000-4000-8000-000000000002"
const alive = "00000000-0000-4000-8000-000000000003"
const expired = "00000000-0000-4000-8000-000000000004"
const loggedOut = "00000000-0000-4000-8000-000000000005"
const outage = "00000000-0000-4000-8000-000000000006"
const unknown = "00000000-0000-4000-8000-000000000007"
const foreign = "00000000-0000-4000-8000-000000000008"

type authority struct {
	workspacev1.UnimplementedSharedDshServiceServer
}

func check(ctx context.Context) error {
	md, _ := metadata.FromIncomingContext(ctx)
	if strings.Join(md.Get("authorization"), ",") != "Bearer synthetic-service-token" || strings.Join(md.Get("auriga-tenant-id"), ",") != tenant {
		return status.Error(codes.Unauthenticated, "fixture-private-service-metadata")
	}
	return nil
}

func binding(member string) *nodev1.SharedDshBinding {
	return &nodev1.SharedDshBinding{TenantId: tenant, MemberId: member, PlacementId: member, PhysicalInstanceId: pool, Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1}
}

func (a *authority) ResolveSharedDsh(ctx context.Context, in *workspacev1.ResolveSharedDshRequest) (*workspacev1.ResolveSharedDshResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	if in.TenantId != tenant || (in.MemberId != alive && in.MemberId != foreign) || in.PlacementId != in.MemberId || in.PlatformSessionId != in.MemberId {
		return nil, status.Error(codes.NotFound, "fixture-private-placement")
	}
	return &workspacev1.ResolveSharedDshResponse{Binding: binding(in.MemberId), State: "ready", AuthGeneration: 1}, nil
}

func (a *authority) LookupSharedDshSession(ctx context.Context, in *workspacev1.LookupSharedDshSessionRequest) (*workspacev1.LookupSharedDshSessionResponse, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	if in.TenantId != tenant || in.PhysicalInstanceId != pool {
		return nil, status.Error(codes.PermissionDenied, "fixture-private-pool")
	}
	switch in.SessionId {
	case expired:
		return nil, status.Error(codes.NotFound, "common.not_found")
	case loggedOut:
		return nil, status.Error(codes.NotFound, "fixture-private-ticket")
	case outage:
		return nil, status.Error(codes.Unavailable, "fixture-private-outage")
	case unknown:
		return nil, status.Error(codes.Unknown, "common.not_found")
	case alive, foreign:
		return &workspacev1.LookupSharedDshSessionResponse{Binding: &nodev1.SharedDshSessionBinding{
			TenantId: tenant, MemberId: in.SessionId, PlacementId: in.SessionId, PhysicalInstanceId: pool,
			Mode: nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED, Epoch: 1, Revision: 1,
			SessionId: in.SessionId, PlatformSessionId: in.SessionId, AuthGeneration: 1,
		}}, nil
	default:
		return nil, status.Error(codes.NotFound, "common.not_found")
	}
}

func run() error {
	token, err := os.CreateTemp("", "auriga-history-synthetic-token-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(token.Name()) }()
	if _, err = token.WriteString("synthetic-service-token"); err != nil {
		_ = token.Close()
		return err
	}
	if err = token.Close(); err != nil {
		return err
	}
	listener := bufconn.Listen(1024 * 1024)
	defer func() { _ = listener.Close() }()
	server := grpc.NewServer()
	workspacev1.RegisterSharedDshServiceServer(server, &authority{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///synthetic-history", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	relay := sharednode.NewRelay(&sharednode.Client{Settings: sharednode.Settings{TenantID: tenant, ServiceTokenFile: token.Name()}, Workspace: conn}, pool)
	httpServer := httptest.NewServer(relay)
	defer httpServer.Close()
	if err = json.NewEncoder(os.Stdout).Encode(map[string]string{"url": httpServer.URL}); err != nil {
		return err
	}
	// The parent owns this fixture. EOF stops the server and cleans the token.
	_, err = io.Copy(io.Discard, os.Stdin)
	return err
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
