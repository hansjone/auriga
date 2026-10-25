package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority;
import io.grpc.stub.StreamObserver;
import java.util.function.Supplier;

/** Gateway-only wire adapter; shares the existing internal JWT/tenant/transport boundary. */
public class IamGrpcNetwork extends NetworkGatewayServiceGrpc.NetworkGatewayServiceImplBase {
  private final NetworkAuthority authority;

  public IamGrpcNetwork(NetworkAuthority authority) {
    this.authority = authority;
  }

  private static void gateway() {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isInternalService()
        || !principal.subject().equals("svc:ai-gateway")
        || !TenantContext.PLATFORM_TENANT.equals(principal.tenantId())
        || TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  @AuthenticatedOnly
  public void redeemNetworkTicket(
      RedeemNetworkTicketRequest request, StreamObserver<RedeemNetworkTicketResponse> observer) {
    gateway();
    respond(
        observer,
        () -> {
          var connection =
              authority.redeem(
                  request.getTicket(),
                  request.getNode(),
                  request.getDevice(),
                  request.getServerAudience(),
                  new NetworkAuthority.IngressBinding(
                      request.getServerId(),
                      request.getClientId(),
                      request.getUserNkey(),
                      request.getIngress(),
                      request.getClientAccount()));
          return RedeemNetworkTicketResponse.newBuilder()
              .setRealm(connection.identity().realm())
              .setNode(connection.identity().node())
              .setDevice(connection.identity().device())
              .setServerAudience(connection.serverAudience())
              .setExpiresAt(connection.expiresAt().toString())
              .setIngress(request.getIngress())
              .build();
        });
  }

  @Override
  @AuthenticatedOnly
  public void networkNodeActive(
      NetworkNodeActiveRequest request, StreamObserver<NetworkNodeActiveResponse> observer) {
    gateway();
    respond(
        observer,
        () ->
            NetworkNodeActiveResponse.newBuilder()
                .setActive(authority.active(request.getRealm(), request.getNode()))
                .build());
  }

  private static <T> void respond(StreamObserver<T> observer, Supplier<T> work) {
    T response;
    try {
      response = work.get();
    } catch (AurigaException denied) {
      throw denied;
    } catch (RuntimeException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    observer.onNext(response);
    observer.onCompleted();
  }
}
