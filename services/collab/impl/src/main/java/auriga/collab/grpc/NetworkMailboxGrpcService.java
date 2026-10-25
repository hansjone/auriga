package auriga.collab.grpc;

import auriga.collab.app.NetworkMailboxWireService;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthenticatedOnly;
import auriga.common.security.AuthorizationRules;
import auriga.contracts.collab.v1.*;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import com.google.protobuf.ByteString;
import io.grpc.stub.StreamObserver;

/** Served by the common authenticated internal gRPC server and its error mapper. */
public class NetworkMailboxGrpcService
    extends NetworkMailboxServiceGrpc.NetworkMailboxServiceImplBase {
  private final NetworkMailboxWireService service;

  public NetworkMailboxGrpcService(NetworkMailboxWireService service) {
    this.service = service;
  }

  private static NetworkAuthorityConnection connection(NetworkMailboxConnection request) {
    var caller =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!caller.isInternalService()
        || !"svc:ai-gateway".equals(caller.subject())
        || !TenantContext.PLATFORM_TENANT.equals(caller.tenantId())
        || TenantContext.current()
            .filter(
                t ->
                    !t.equals(TenantContext.PLATFORM_TENANT)
                        && t.toString().equals(request.getTenantId()))
            .isEmpty()) throw new AurigaException(CommonErrors.FORBIDDEN);
    return NetworkAuthorityConnection.newBuilder()
        .setTenantId(request.getTenantId())
        .setRealm(request.getRealm())
        .setNode(request.getNode())
        .setIngress(request.getIngress())
        .setServerId(request.getServerId())
        .setClientId(request.getClientId())
        .setClientAccount(request.getClientAccount())
        .build();
  }

  @Override
  @AuthenticatedOnly
  public void ready(
      NetworkMailboxServiceReadyRequest request,
      StreamObserver<NetworkMailboxServiceReadyResponse> observer) {
    observer.onNext(
        NetworkMailboxServiceReadyResponse.newBuilder()
            .setFrameJson(
                ByteString.copyFromUtf8(
                    service.ready(
                        connection(request.getConnection()), request.getFrameJson().toByteArray())))
            .build());
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void head(
      NetworkMailboxServiceHeadRequest request,
      StreamObserver<NetworkMailboxServiceHeadResponse> observer) {
    observer.onNext(
        NetworkMailboxServiceHeadResponse.newBuilder()
            .setFrameJson(
                ByteString.copyFromUtf8(service.head(connection(request.getConnection()))))
            .build());
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void fetch(
      NetworkMailboxServiceFetchRequest request,
      StreamObserver<NetworkMailboxServiceFetchResponse> observer) {
    observer.onNext(
        NetworkMailboxServiceFetchResponse.newBuilder()
            .setFrameJson(
                ByteString.copyFromUtf8(
                    service.fetch(
                        connection(request.getConnection()), request.getFrameJson().toByteArray())))
            .build());
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void receipt(
      NetworkMailboxServiceReceiptRequest request,
      StreamObserver<NetworkMailboxServiceReceiptResponse> observer) {
    observer.onNext(
        NetworkMailboxServiceReceiptResponse.newBuilder()
            .setFrameJson(
                ByteString.copyFromUtf8(
                    service.receipt(
                        connection(request.getConnection()), request.getFrameJson().toByteArray())))
            .build());
    observer.onCompleted();
  }
}
