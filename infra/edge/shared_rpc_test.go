package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type sharedRPCFixture struct {
	workspacev1.SharedDshServiceClient
	pool    func(context.Context, *workspacev1.GetSharedDshPoolRequest) (*workspacev1.GetSharedDshPoolResponse, error)
	resolve func(context.Context, *workspacev1.ResolveSharedDshRequest) (*workspacev1.ResolveSharedDshResponse, error)
}

func (f *sharedRPCFixture) GetSharedDshPool(ctx context.Context, r *workspacev1.GetSharedDshPoolRequest, _ ...grpc.CallOption) (*workspacev1.GetSharedDshPoolResponse, error) {
	return f.pool(ctx, r)
}

func (f *sharedRPCFixture) ResolveSharedDsh(ctx context.Context, r *workspacev1.ResolveSharedDshRequest, _ ...grpc.CallOption) (*workspacev1.ResolveSharedDshResponse, error) {
	return f.resolve(ctx, r)
}

func sharedAuthorityFixture(t *testing.T, client *sharedRPCFixture) (*sharedWorkspaceAuthority, map[string]any) {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "service-token")
	if err := os.WriteFile(tokenFile, []byte("synthetic-service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	claims, _ := json.Marshal(sharedTestClaims(false, time.Now().Unix()))
	decoded, _ := sharedDecode(claims)
	binding := decoded["binding"].(map[string]any)
	routes := NewMemoryRouteStore()
	routes.Put(&InstanceRoute{InstanceID: sharedTestID, TenantID: sharedTestID, OwnerID: sharedTestID})
	return &sharedWorkspaceAuthority{
		rpc: &iamRPC{tokenFile: tokenFile, timeout: time.Second}, client: client, routes: routes, tenant: sharedTestID,
	}, binding
}

func TestSharedProductionModeUsesLivePoolAuthority(t *testing.T) {
	registered, mode, state := false, "", ""
	calls := 0
	client := &sharedRPCFixture{pool: func(ctx context.Context, r *workspacev1.GetSharedDshPoolRequest) (*workspacev1.GetSharedDshPoolResponse, error) {
		calls++
		meta, _ := metadata.FromOutgoingContext(ctx)
		if len(meta.Get("authorization")) != 1 || meta.Get("authorization")[0] != "Bearer synthetic-service-token" ||
			len(meta.Get("auriga-tenant-id")) != 1 || meta.Get("auriga-tenant-id")[0] != sharedTestID || r.PhysicalInstanceId != sharedTestID {
			t.Fatal("pool read lost authenticated service or trusted route tenant")
		}
		return &workspacev1.GetSharedDshPoolResponse{TenantId: r.TenantId, PhysicalInstanceId: r.PhysicalInstanceId, Registered: registered, Mode: mode, State: state}, nil
	}}
	authority, _ := sharedAuthorityFixture(t, client)
	entry := &SharedEntry{Authority: authority, LiveModes: true}
	if managed, err := entry.instanceShared(t.Context(), sharedTestID); err != nil || managed {
		t.Fatalf("legacy = %v, %v", managed, err)
	}
	registered, mode, state = true, "shared", "pending"
	if managed, err := entry.instanceShared(t.Context(), sharedTestID); err != nil || !managed {
		t.Fatalf("new shared pool reused legacy owner cache: %v, %v", managed, err)
	}
	mode, state = "dedicated", "ready"
	if managed, err := entry.instanceShared(t.Context(), sharedTestID); err != nil || !managed || calls != 3 {
		t.Fatalf("dedicated placement did not use entry authority: %v, %v, calls=%d", managed, err, calls)
	}
	client.pool = func(context.Context, *workspacev1.GetSharedDshPoolRequest) (*workspacev1.GetSharedDshPoolResponse, error) {
		return nil, errors.New("synthetic control-plane outage")
	}
	if _, err := entry.instanceShared(t.Context(), sharedTestID); err == nil {
		t.Fatal("mode authority outage reused an old authentication path")
	}
}

func TestSharedProductionResolveComparesEveryBindingAndCurrentAuthority(t *testing.T) {
	client := &sharedRPCFixture{}
	authority, binding := sharedAuthorityFixture(t, client)
	expected, err := sharedProtoBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	response := &workspacev1.ResolveSharedDshResponse{Binding: expected, State: "ready", AuthGeneration: 1}
	client.resolve = func(ctx context.Context, r *workspacev1.ResolveSharedDshRequest) (*workspacev1.ResolveSharedDshResponse, error) {
		if r.TenantId != expected.TenantId || r.MemberId != expected.MemberId || r.PlacementId != expected.PlacementId || r.PlatformSessionId != sharedTestID {
			t.Fatal("resolve accepted a caller-selected identity")
		}
		return response, nil
	}
	if err := authority.Check(t.Context(), binding, sharedTestID); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func(*workspacev1.ResolveSharedDshResponse){
		func(r *workspacev1.ResolveSharedDshResponse) { r.Binding.Epoch++ },
		func(r *workspacev1.ResolveSharedDshResponse) { r.Binding.Revision++ },
		func(r *workspacev1.ResolveSharedDshResponse) { r.Binding.MemberId = pathInstanceB },
		func(r *workspacev1.ResolveSharedDshResponse) { r.Binding.PhysicalInstanceId = pathInstanceB },
		func(r *workspacev1.ResolveSharedDshResponse) { r.State = "pending" },
		func(r *workspacev1.ResolveSharedDshResponse) { r.AuthGeneration = 0 },
	} {
		// Expire the ≤1s same-generation reuse so each mutation hits live W.
		time.Sleep(sharedResolveReuse + 10*time.Millisecond)
		response = &workspacev1.ResolveSharedDshResponse{Binding: proto.Clone(expected).(*nodev1.SharedDshBinding), State: "ready", AuthGeneration: 1}
		mutation(response)
		if err := authority.Check(t.Context(), binding, sharedTestID); err == nil {
			t.Fatal("changed live binding or authority was accepted")
		}
	}
}

func TestSharedResolveReusesSameGenerationWithinOneSecond(t *testing.T) {
	client := &sharedRPCFixture{}
	authority, binding := sharedAuthorityFixture(t, client)
	expected, err := sharedProtoBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	client.resolve = func(context.Context, *workspacev1.ResolveSharedDshRequest) (*workspacev1.ResolveSharedDshResponse, error) {
		calls.Add(1)
		return &workspacev1.ResolveSharedDshResponse{Binding: expected, State: "ready", AuthGeneration: 1}, nil
	}
	if err := authority.Check(t.Context(), binding, sharedTestID); err != nil {
		t.Fatal(err)
	}
	if err := authority.Check(t.Context(), binding, sharedTestID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one Resolve within reuse window, got %d", calls.Load())
	}
}

func TestSharedProductionWatchClosesOnAuthorityLoss(t *testing.T) {
	client := &sharedRPCFixture{}
	authority, binding := sharedAuthorityFixture(t, client)
	expected, err := sharedProtoBinding(binding)
	if err != nil {
		t.Fatal(err)
	}
	var unavailable atomic.Bool
	client.resolve = func(context.Context, *workspacev1.ResolveSharedDshRequest) (*workspacev1.ResolveSharedDshResponse, error) {
		if unavailable.Load() {
			return nil, errors.New("synthetic authority loss")
		}
		return &workspacev1.ResolveSharedDshResponse{Binding: expected, State: "ready", AuthGeneration: 1}, nil
	}
	revoked := make(chan struct{})
	stop, err := authority.Watch(t.Context(), binding, sharedTestID, func() { close(revoked) })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	unavailable.Store(true)
	select {
	case <-revoked:
	case <-time.After(3 * time.Second):
		t.Fatal("open stream survived loss of live Workspace authority")
	}
}
