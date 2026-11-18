package auriga.workspace.grpc;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.api.SharedDshRuntimeResources;
import auriga.workspace.app.SharedDshEntryQueries;
import io.grpc.stub.StreamObserver;
import java.util.*;

/** Mount behind the existing authenticated internal gRPC server, never a plaintext new listener. */
public class SharedDshGrpc extends SharedDshServiceGrpc.SharedDshServiceImplBase {
  private final SharedDshAuthority authority;
  private final Set<String> runtimeCallers;
  private final SharedDshEntryQueries entry;
  private final SharedDshRuntimeResources resources;

  public SharedDshGrpc(SharedDshAuthority authority) {
    this(authority, Set.of("svc:workspace", "svc:agent"));
  }

  /**
   * Only authenticated node relays; plugins/browser peers never receive an internal service token.
   */
  public SharedDshGrpc(SharedDshAuthority authority, Set<String> runtimeCallers) {
    this(authority, runtimeCallers, null);
  }

  public SharedDshGrpc(
      SharedDshAuthority authority, Set<String> runtimeCallers, SharedDshEntryQueries entry) {
    this(authority, runtimeCallers, entry, null);
  }

  public SharedDshGrpc(
      SharedDshAuthority authority,
      Set<String> runtimeCallers,
      SharedDshEntryQueries entry,
      SharedDshRuntimeResources resources) {
    if (runtimeCallers.isEmpty()
        || !Set.of("svc:workspace", "svc:agent").containsAll(runtimeCallers))
      throw new IllegalArgumentException("Untrusted runtime relay");
    this.authority = authority;
    this.runtimeCallers = Set.copyOf(runtimeCallers);
    this.entry = entry;
    this.resources = resources;
  }

  private void caller(Set<String> allowed) {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    // The internal verifier authenticates the token's auriga.internal audience. Service JWTs
    // intentionally carry no user permission claims; caller authority is this exact service list.
    if (!p.isInternalService()
        || !allowed.contains(p.subject())
        || !TenantContext.PLATFORM_TENANT.equals(p.tenantId())
        || TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private void tenant(String tenant) {
    if (!TenantContext.require().toString().equals(tenant))
      throw new AurigaException(CommonErrors.NOT_FOUND);
  }

  @Override
  @AuthenticatedOnly
  public void getSharedDshPool(
      GetSharedDshPoolRequest r, StreamObserver<GetSharedDshPoolResponse> o) {
    caller(Set.of("svc:edge", "svc:agent"));
    tenant(r.getTenantId());
    if (entry == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    o.onNext(entry.pool(UUID.fromString(r.getPhysicalInstanceId())));
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void lookupSharedDshSession(
      LookupSharedDshSessionRequest r, StreamObserver<LookupSharedDshSessionResponse> o) {
    caller(runtimeCallers);
    tenant(r.getTenantId());
    if (entry == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    o.onNext(
        LookupSharedDshSessionResponse.newBuilder()
            .setBinding(
                entry.lookup(
                    UUID.fromString(r.getPhysicalInstanceId()), UUID.fromString(r.getSessionId())))
            .build());
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void resolveSharedDsh(
      ResolveSharedDshRequest r, StreamObserver<ResolveSharedDshResponse> o) {
    caller(Set.of("svc:edge", "svc:agent", "svc:iam", "svc:ai-gateway", "svc:collab"));
    tenant(r.getTenantId());
    var p =
        authority.resolve(
            UUID.fromString(r.getMemberId()),
            UUID.fromString(r.getPlacementId()),
            UUID.fromString(r.getPlatformSessionId()));
    o.onNext(
        ResolveSharedDshResponse.newBuilder()
            .setBinding(p.binding())
            .setState(p.state())
            .setAuthGeneration(p.authGeneration())
            .build());
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void registerSharedDshSession(
      RegisterSharedDshSessionRequest r, StreamObserver<RegisterSharedDshSessionResponse> o) {
    caller(runtimeCallers);
    tenant(r.getBinding().getTenantId());
    o.onNext(
        RegisterSharedDshSessionResponse.newBuilder()
            .setBinding(authority.register(r.getBinding(), r.getParentSessionId()))
            .build());
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void revokeSharedDsh(RevokeSharedDshRequest r, StreamObserver<RevokeSharedDshResponse> o) {
    caller(Set.of("svc:iam"));
    tenant(r.getRevocation().getTenantId());
    if (r.getRevocation().getSchemaVersion() != 1)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    // The caller supplies invalidation reason, never chooses a new authorization generation.
    o.onNext(
        RevokeSharedDshResponse.newBuilder()
            .setRevision(authority.revoke(r.getRevocation()))
            .build());
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void getSharedDshResourceView(
      GetSharedDshResourceViewRequest r, StreamObserver<GetSharedDshResourceViewResponse> o) {
    caller(runtimeCallers);
    tenant(r.getBinding().getTenantId());
    o.onNext(
        GetSharedDshResourceViewResponse.newBuilder()
            .setView(authority.resourceView(r.getBinding()))
            .build());
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void getSharedDshResourceSnapshot(
      GetSharedDshResourceSnapshotRequest r,
      StreamObserver<GetSharedDshResourceSnapshotResponse> o) {
    caller(runtimeCallers);
    tenant(r.getBinding().getTenantId());
    if (resources == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var view = authority.resourceView(r.getBinding());
    var snapshot = resources.snapshot(r.getBinding());
    if (!snapshot.hasView() || !snapshot.getView().equals(view) || !snapshot.hasRevocations())
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    o.onNext(snapshot);
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void reportSharedDshResource(
      ReportSharedDshResourceRequest r, StreamObserver<ReportSharedDshResourceResponse> o) {
    caller(runtimeCallers);
    tenant(r.getReport().getBinding().getTenantId());
    o.onNext(
        ReportSharedDshResourceResponse.newBuilder()
            .setRevision(authority.reportResource(r.getReport()))
            .build());
    o.onCompleted();
  }
}
