package auriga.workspace.grpc;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.UnifiedResourceDownloadAuthority;
import io.grpc.stub.StreamObserver;

/**
 * The resource service must already authenticate the node's mTLS download channel.
 *
 * <p>Like the other gRPC endpoints, this class must allow the authorization advisor's subclass
 * proxy. Workspace transactions remain in the application service.
 */
public class UnifiedResourceApplyGrpc
    extends UnifiedResourceApplyServiceGrpc.UnifiedResourceApplyServiceImplBase {
  private final UnifiedResourceDownloadAuthority authority;

  public UnifiedResourceApplyGrpc(UnifiedResourceDownloadAuthority authority) {
    this.authority = authority;
  }

  @Override
  @AuthenticatedOnly
  public void checkUnifiedResourceDownload(
      CheckUnifiedResourceDownloadRequest request,
      StreamObserver<CheckUnifiedResourceDownloadResponse> observer) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isInternalService()
        || !principal.subject().equals("svc:resource")
        || !TenantContext.PLATFORM_TENANT.equals(principal.tenantId())
        || !TenantContext.require().toString().equals(request.getTenantId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    observer.onNext(authority.check(request));
    observer.onCompleted();
  }
}
