package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority;
import io.grpc.stub.StreamObserver;
import java.util.*;
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

  @Override
  @AuthenticatedOnly
  public void batchNetworkNodesActive(
      BatchNetworkNodesActiveRequest request,
      StreamObserver<BatchNetworkNodesActiveResponse> observer) {
    gateway();
    respond(
        observer,
        () -> {
          if (request.getNodesCount() == 0 || request.getNodesCount() > 10000)
            throw new AurigaException(CommonErrors.VALIDATION_FAILED);
          var groups = new LinkedHashMap<UUID, LinkedHashSet<NetworkAuthority.NodeReference>>();
          for (var id : request.getNodesList()) {
            UUID tenant;
            try {
              tenant = UUID.fromString(id.getTenantId());
              if (!tenant.toString().equals(id.getTenantId())
                  || TenantContext.PLATFORM_TENANT.equals(tenant))
                throw new IllegalArgumentException();
            } catch (IllegalArgumentException invalid) {
              throw new AurigaException(CommonErrors.VALIDATION_FAILED);
            }
            groups
                .computeIfAbsent(tenant, k -> new LinkedHashSet<>())
                .add(new NetworkAuthority.NodeReference(id.getRealm(), id.getNode()));
          }
          var response = BatchNetworkNodesActiveResponse.newBuilder();
          // Exact gateway identity + the original delegated-tenant interceptor authorize
          // this trusted list; every query still enters its own FORCE RLS scope.
          groups.forEach(
              (tenant, nodes) ->
                  TenantContext.run(
                      tenant,
                      () -> {
                        if (io.grpc.Context.current().isCancelled())
                          throw new AurigaException(CommonErrors.UNAVAILABLE);
                        var statuses = authority.activeBatch(List.copyOf(nodes));
                        for (var node : nodes)
                          response.addNodes(
                              NetworkNodeStatus.newBuilder()
                                  .setIdentity(
                                      NetworkNodeIdentity.newBuilder()
                                          .setTenantId(tenant.toString())
                                          .setRealm(node.realm())
                                          .setNode(node.node()))
                                  .setActive(Boolean.TRUE.equals(statuses.get(node))));
                      }));
          return response.build();
        });
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
