package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import io.grpc.stub.StreamObserver;

/** Exact workspace service identity; neither member nor instance principals are accepted. */
public class IamGrpcManagedDevices extends ManagedDeviceServiceGrpc.ManagedDeviceServiceImplBase {
  private final IamManagedDevices devices;

  public IamGrpcManagedDevices(IamManagedDevices devices) {
    this.devices = devices;
  }

  private void caller() {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!p.isInternalService()
        || !"svc:workspace".equals(p.subject())
        || !TenantContext.PLATFORM_TENANT.equals(p.tenantId())
        || TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  @AuthenticatedOnly
  public void issueManagedDevice(
      IssueManagedDeviceRequest request, StreamObserver<IssueManagedDeviceResponse> observer) {
    caller();
    observer.onNext(devices.issue(request));
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void revokeManagedDevice(
      RevokeManagedDeviceRequest request, StreamObserver<RevokeManagedDeviceResponse> observer) {
    caller();
    devices.revoke(request);
    observer.onNext(RevokeManagedDeviceResponse.getDefaultInstance());
    observer.onCompleted();
  }
}
