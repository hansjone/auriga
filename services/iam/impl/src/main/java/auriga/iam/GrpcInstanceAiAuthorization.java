package auriga.iam;

import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import java.util.concurrent.TimeUnit;

/** The supplied stub must use the normal internal JWT, tenant metadata and mTLS transport. */
public final class GrpcInstanceAiAuthorization implements InstanceAiAuthorization {
  private final AuthorizationServiceGrpc.AuthorizationServiceBlockingStub stub;

  public GrpcInstanceAiAuthorization(
      AuthorizationServiceGrpc.AuthorizationServiceBlockingStub stub) {
    this.stub = java.util.Objects.requireNonNull(stub);
  }

  @Override
  public GetEffectivePermissionsResponse getEffectivePermissions(
      GetEffectivePermissionsRequest request) {
    try {
      return stub.withDeadlineAfter(5, TimeUnit.SECONDS).getEffectivePermissions(request);
    } catch (io.grpc.StatusRuntimeException e) {
      throw new auriga.common.error.AurigaException(
          switch (e.getStatus().getCode()) {
            case NOT_FOUND -> auriga.common.error.CommonErrors.NOT_FOUND;
            case PERMISSION_DENIED -> auriga.common.error.CommonErrors.FORBIDDEN;
            default -> auriga.common.error.CommonErrors.UNAVAILABLE;
          });
    }
  }
}
