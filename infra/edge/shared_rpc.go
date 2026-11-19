package main

import (
	"context"
	"errors"
	"time"

	nodev1 "github.com/hansjone/auriga/infra/gen/auriga/node/v1"
	workspacev1 "github.com/hansjone/auriga/infra/gen/auriga/workspace/v1"
	"google.golang.org/protobuf/proto"
)

// SharedDshConfig is an explicit installation adapter. No browser can choose its
// endpoint, credentials, trust roots, signing keys, or replay ledger.
type SharedDshConfig struct {
	RPC                  IAMRPCConfig `json:"rpc"`
	Hop                  IAMHopConfig `json:"hop"`
	NATSCredsFile        string       `json:"natsCredsFile"`
	NATSSeedFile         string       `json:"natsSeedFile,omitempty"`
	TicketPublicKeysFile string       `json:"ticketPublicKeysFile"`
	IdentityKeysFile     string       `json:"identityKeysFile"`
}

type sharedWorkspaceAuthority struct {
	rpc    *iamRPC
	client workspacev1.SharedDshServiceClient
	routes RouteStore
	tenant string
}

func (a *sharedWorkspaceAuthority) IsShared(ctx context.Context, id string) (bool, error) {
	if !sharedUUID.MatchString(id) {
		return false, errors.New("shared.binding_mismatch")
	}
	route, err := a.routes.Get(ctx, id)
	if !sharedUUID.MatchString(a.tenant) || (err != nil && !errors.Is(err, ErrRouteNotFound)) ||
		(err == nil && (route == nil || route.InstanceID != id || route.TenantID != a.tenant)) {
		return false, errors.New("common.unavailable")
	}
	// Resolve a newly registered pool even before its route projection appears.
	// Missing routing can never select the legacy owner authentication path.
	ctx, cancel, err := a.rpc.sessionContext(ctx, a.tenant)
	if err != nil {
		return false, errors.New("common.unavailable")
	}
	defer cancel()
	result, err := a.client.GetSharedDshPool(ctx, &workspacev1.GetSharedDshPoolRequest{
		TenantId: a.tenant, PhysicalInstanceId: id,
	})
	if err != nil || result == nil || result.TenantId != a.tenant || result.PhysicalInstanceId != id {
		return false, errors.New("common.unavailable")
	}
	if !result.Registered {
		if result.Mode != "" || result.State != "" {
			return false, errors.New("common.unavailable")
		}
		return false, nil
	}
	if (result.Mode != "shared" && result.Mode != "dedicated") ||
		(result.State != "pending" && result.State != "ready" && result.State != "blocked") {
		return false, errors.New("common.unavailable")
	}
	return true, nil
}

func sharedProtoBinding(binding map[string]any) (*nodev1.SharedDshBinding, error) {
	if !sharedBinding(binding) {
		return nil, errors.New("shared.binding_mismatch")
	}
	mode := nodev1.DshInstanceMode_DSH_INSTANCE_MODE_SHARED
	if binding["mode"] == "dedicated" {
		mode = nodev1.DshInstanceMode_DSH_INSTANCE_MODE_DEDICATED
	}
	return &nodev1.SharedDshBinding{
		TenantId: sharedString(binding["tenantId"]), MemberId: sharedString(binding["memberId"]),
		PlacementId: sharedString(binding["placementId"]), PhysicalInstanceId: sharedString(binding["physicalInstanceId"]),
		Mode: mode, Epoch: sharedInt(binding["epoch"]), Revision: sharedInt(binding["revision"]),
	}, nil
}

func (a *sharedWorkspaceAuthority) Check(ctx context.Context, binding map[string]any, session string) error {
	expected, err := sharedProtoBinding(binding)
	if err != nil || expected.TenantId != a.tenant || !sharedUUID.MatchString(session) {
		return errors.New("shared.binding_mismatch")
	}
	ctx, cancel, err := a.rpc.sessionContext(ctx, expected.TenantId)
	if err != nil {
		return errors.New("common.unavailable")
	}
	defer cancel()
	result, err := a.client.ResolveSharedDsh(ctx, &workspacev1.ResolveSharedDshRequest{
		TenantId: expected.TenantId, MemberId: expected.MemberId,
		PlacementId: expected.PlacementId, PlatformSessionId: session,
	})
	if err != nil {
		return errors.New("common.unavailable")
	}
	if result == nil || result.State != "ready" || result.AuthGeneration < 1 || !proto.Equal(result.Binding, expected) {
		return errors.New("shared.revoked")
	}
	return nil
}

func (a *sharedWorkspaceAuthority) Watch(ctx context.Context, binding map[string]any, session string, revoke context.CancelFunc) (func(), error) {
	if err := a.Check(ctx, binding, session); err != nil {
		return nil, err
	}
	watch, stop := context.WithCancel(ctx)
	// The authority is online on every request and once per second for an open
	// response/upgrade. Outages close the stream as well as explicit revocations.
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
				if a.Check(watch, binding, session) != nil {
					revoke()
					return
				}
			}
		}
	}()
	return stop, nil
}
