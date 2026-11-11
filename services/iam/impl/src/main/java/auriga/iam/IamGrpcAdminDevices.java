package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import io.grpc.stub.StreamObserver;

public class IamGrpcAdminDevices
    extends AdminDeviceBindingsServiceGrpc.AdminDeviceBindingsServiceImplBase {
  private final IamAdminDevices devices;

  public IamGrpcAdminDevices(IamAdminDevices devices) {
    this.devices = devices;
  }

  @Override
  @AuthenticatedOnly
  public void readBindings(
      ReadAdminDeviceBindingsRequest request,
      StreamObserver<ReadAdminDeviceBindingsResponse> observer) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isInternalService() || !principal.subject().equals("svc:edge"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
    var response = ReadAdminDeviceBindingsResponse.newBuilder();
    for (var row : devices.connectionBindings().path("items"))
      response.addItems(
          AdminDeviceConnectionBinding.newBuilder()
              .setDevice(row.path("device").asString())
              .setServerId(row.path("serverId").asString())
              .setClientId(row.path("clientId").asString())
              .setAccount(row.path("account").asString())
              .setIngress(row.path("ingress").asString()));
    observer.onNext(response.build());
    observer.onCompleted();
  }
}
