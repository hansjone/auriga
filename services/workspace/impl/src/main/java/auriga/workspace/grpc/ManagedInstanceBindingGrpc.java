package auriga.workspace.grpc;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.ManagedInstanceBindings;
import io.grpc.stub.StreamObserver;

public class ManagedInstanceBindingGrpc
    extends ManagedInstanceBindingServiceGrpc.ManagedInstanceBindingServiceImplBase {
  private final ManagedInstanceBindings bindings;

  public ManagedInstanceBindingGrpc(ManagedInstanceBindings bindings) {
    this.bindings = bindings;
  }

  @Override
  @AuthenticatedOnly
  public void getManagedInstanceBinding(
      GetManagedInstanceBindingRequest request,
      StreamObserver<GetManagedInstanceBindingResponse> observer) {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!p.isInternalService()
        || !"svc:iam".equals(p.subject())
        || !TenantContext.PLATFORM_TENANT.equals(p.tenantId())
        || TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
    observer.onNext(bindings.get(request));
    observer.onCompleted();
  }
}
