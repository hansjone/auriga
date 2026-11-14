package auriga.workspace.grpc;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import io.grpc.stub.StreamObserver;
import java.util.*;

/** Mount behind the existing authenticated internal gRPC server, never a plaintext new listener. */
public class SharedDshGrpc extends SharedDshServiceGrpc.SharedDshServiceImplBase {
  private final SharedDshAuthority authority;
  private final Set<String> runtimeCallers;

  public SharedDshGrpc(SharedDshAuthority authority) {
    this(authority, Set.of("svc:workspace", "svc:agent"));
  }

  /**
   * Only authenticated node relays; plugins/browser peers never receive an internal service token.
   */
  public SharedDshGrpc(SharedDshAuthority authority, Set<String> runtimeCallers) {
    if (runtimeCallers.isEmpty()
        || !Set.of("svc:workspace", "svc:agent").containsAll(runtimeCallers))
      throw new IllegalArgumentException("Untrusted runtime relay");
    this.authority = authority;
    this.runtimeCallers = Set.copyOf(runtimeCallers);
  }

  private void caller(Set<String> allowed) {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!p.isInternalService()
        || !p.hasPermission("auriga.internal")
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
  public void resolveSharedDsh(
      ResolveSharedDshRequest r, StreamObserver<ResolveSharedDshResponse> o) {
    caller(Set.of("svc:edge", "svc:iam", "svc:ai-gateway", "svc:collab"));
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
