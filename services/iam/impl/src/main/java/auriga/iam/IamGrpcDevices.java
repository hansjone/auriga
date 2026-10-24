package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.Timestamp;
import io.grpc.stub.StreamObserver;

/** Separate exact-caller ACLs; deliberately does not widen edge's SessionService. */
public final class IamGrpcDevices extends DeviceAccessServiceGrpc.DeviceAccessServiceImplBase {
  private final DeviceAccessAuthority authority;

  public IamGrpcDevices(DeviceAccessAuthority authority) {
    this.authority = authority;
  }

  private static void caller(String subject) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isInternalService()
        || !principal.subject().equals(subject)
        || !TenantContext.PLATFORM_TENANT.equals(principal.tenantId())
        || TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  @AuthenticatedOnly
  public void verifyDeviceAccess(
      VerifyDeviceAccessRequest request, StreamObserver<VerifyDeviceAccessResponse> observer) {
    caller("svc:ai-gateway");
    try {
      var identity = authority.access(request.getAccessToken());
      var response = VerifyDeviceAccessResponse.newBuilder();
      if (identity != null)
        response
            .setActive(true)
            .setTenantId(identity.tenant())
            .setAccountId(identity.account())
            .setDeviceId(identity.device())
            .setExpiresAt(timestamp(identity));
      observer.onNext(response.build());
      observer.onCompleted();
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  @Override
  @AuthenticatedOnly
  public void verifyDeviceCredential(
      VerifyDeviceCredentialRequest request,
      StreamObserver<VerifyDeviceCredentialResponse> observer) {
    caller("svc:collab");
    try {
      var identity =
          authority.credential(
              request.getTenantId(), request.getAccountId(), request.getDeviceId());
      var response = VerifyDeviceCredentialResponse.newBuilder();
      if (identity != null)
        response
            .setActive(true)
            .setTenantId(identity.tenant())
            .setAccountId(identity.account())
            .setDeviceId(identity.device())
            .setExpiresAt(timestamp(identity));
      observer.onNext(response.build());
      observer.onCompleted();
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private static Timestamp timestamp(DeviceAccessAuthority.Identity identity) {
    return Timestamp.newBuilder()
        .setSeconds(identity.expiresAt().getEpochSecond())
        .setNanos(identity.expiresAt().getNano())
        .build();
  }
}
