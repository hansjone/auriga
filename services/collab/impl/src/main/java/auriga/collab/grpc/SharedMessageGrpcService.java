package auriga.collab.grpc;

import auriga.collab.app.SharedMessageService;
import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.collab.v1.*;
import com.google.protobuf.ByteString;
import io.grpc.stub.StreamObserver;

/** The gateway supplies verified node mTLS identity and exact K claims. */
public class SharedMessageGrpcService
    extends SharedMessageServiceGrpc.SharedMessageServiceImplBase {
  private final SharedMessageService service;

  public SharedMessageGrpcService(SharedMessageService service) {
    this.service = service;
  }

  @Override
  @AuthenticatedOnly
  public void callSharedMessage(
      CallSharedMessageRequest r, StreamObserver<CallSharedMessageResponse> out) {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!p.isInternalService()
        || !"svc:ai-gateway".equals(p.subject())
        || !TenantContext.PLATFORM_TENANT.equals(p.tenantId())
        || TenantContext.PLATFORM_TENANT.equals(TenantContext.require())
        || !TenantContext.require().toString().equals(r.getCapability().getBinding().getTenantId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    out.onNext(
        CallSharedMessageResponse.newBuilder()
            .setResultJson(ByteString.copyFromUtf8(service.call(r)))
            .build());
    out.onCompleted();
  }
}
