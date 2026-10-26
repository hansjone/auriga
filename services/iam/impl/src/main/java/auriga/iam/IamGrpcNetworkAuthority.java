package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthenticatedOnly;
import auriga.common.security.AuthorizationRules;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority;
import io.grpc.stub.StreamObserver;
import java.time.Clock;
import java.util.function.Supplier;

/** Online collab authority. Recipient relationships and execution permission remain outside IAM. */
public class IamGrpcNetworkAuthority
    extends NetworkAuthorityServiceGrpc.NetworkAuthorityServiceImplBase {
  private final IamNetworkAuthority authority;
  private final Clock clock;
  private final IamNetworkSourceAuthority sources;

  IamGrpcNetworkAuthority(
      IamNetworkAuthority authority, Clock clock, IamNetworkSourceAuthority sources) {
    this.authority = authority;
    this.clock = clock;
    this.sources = sources;
  }

  private static void collab() {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isInternalService()
        || !"svc:collab".equals(principal.subject())
        || !TenantContext.PLATFORM_TENANT.equals(principal.tenantId())
        || TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  @AuthenticatedOnly
  public void checkNetwork(
      CheckNetworkRequest request, StreamObserver<CheckNetworkResponse> observer) {
    respond(
        observer,
        () -> {
          var sender =
              switch (request.getSender()) {
                case NETWORK_AUTHORITY_SENDER_HUMAN -> NetworkAuthority.Sender.HUMAN;
                case NETWORK_AUTHORITY_SENDER_AGENT -> NetworkAuthority.Sender.AGENT;
                default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
              };
          var operation =
              switch (request.getOperation()) {
                case NETWORK_AUTHORITY_OPERATION_CONNECT -> NetworkAuthority.Operation.CONNECT;
                case NETWORK_AUTHORITY_OPERATION_SEND -> NetworkAuthority.Operation.SEND;
                case NETWORK_AUTHORITY_OPERATION_RECEIVE -> NetworkAuthority.Operation.RECEIVE;
                case NETWORK_AUTHORITY_OPERATION_FIRST_CONTACT ->
                    NetworkAuthority.Operation.FIRST_CONTACT;
                case NETWORK_AUTHORITY_OPERATION_BROADCAST -> NetworkAuthority.Operation.BROADCAST;
                default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
              };
          positive(request.getRealm());
          positive(request.getSourceNode());
          // CONNECT has no target; all other operations must name a canonical positive node.
          if (operation != NetworkAuthority.Operation.CONNECT || !request.getTargetNode().isEmpty())
            positive(request.getTargetNode());
          if (!IamNetworkAuthority.validNumber(
                  request.getLocal(), sender == NetworkAuthority.Sender.HUMAN)
              || sender == NetworkAuthority.Sender.HUMAN && !"0".equals(request.getLocal())
              || request.getBodyBytes() < 0
              || request.getTargetNodes() < 0
              || request.getTargetLocals() < 0)
            throw new AurigaException(CommonErrors.VALIDATION_FAILED);
          var result =
              authority.check(
                  new NetworkAuthority.Check(
                      request.getRealm(),
                      request.getSourceNode(),
                      request.getTargetNode(),
                      request.getLocal(),
                      sender,
                      operation,
                      request.getBodyBytes(),
                      request.getTargetNodes(),
                      request.getTargetLocals()));
          return CheckNetworkResponse.newBuilder()
              .setAllowed(result.allowed())
              .setReason(result.allowed() ? "allowed" : "network_denied")
              .build();
        });
  }

  @Override
  @AuthenticatedOnly
  public void networkAuthorityActive(
      NetworkAuthorityActiveRequest request,
      StreamObserver<NetworkAuthorityActiveResponse> observer) {
    respond(
        observer,
        () -> {
          positive(request.getRealm());
          positive(request.getNode());
          return NetworkAuthorityActiveResponse.newBuilder()
              .setActive(authority.active(request.getRealm(), request.getNode()))
              .build();
        });
  }

  @Override
  @AuthenticatedOnly
  public void networkLimits(
      NetworkLimitsRequest request, StreamObserver<NetworkLimitsResponse> observer) {
    respond(
        observer,
        () -> {
          var limits = authority.effectiveLimits();
          if (limits.size() != NetworkAuthority.Limit.values().length)
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          var response = NetworkLimitsResponse.newBuilder();
          for (var key : NetworkAuthority.Limit.values()) {
            Long value = limits.get(key);
            if (value == null || value < 0 || value > key.defaultValue())
              throw new AurigaException(CommonErrors.UNAVAILABLE);
            response.putLimits(key.name(), value);
          }
          return response.build();
        });
  }

  @Override
  @AuthenticatedOnly
  public void resolveNetworkTarget(
      ResolveNetworkTargetRequest request, StreamObserver<ResolveNetworkTargetResponse> observer) {
    respond(
        observer,
        () -> {
          tenant(request.getConnection());
          if (!"0".equals(request.getLocal()))
            throw new AurigaException(CommonErrors.VALIDATION_FAILED);
          var c = request.getConnection();
          positive(c.getRealm());
          positive(c.getNode());
          // Opaque values are checked against the existing redeem format and exact persisted tuple.
          if (!c.getIngress().matches("[a-z2-7]{26}")
              || !c.getServerId().matches("N[A-Z2-7]{55}")
              || !IamNetworkAuthority.validNumber(c.getClientId(), false)
              || c.getClientAccount().isBlank()
              || c.getClientAccount().length() > 256)
            throw new AurigaException(CommonErrors.VALIDATION_FAILED);
          var expiry =
              authority.resolveTarget(
                  new IamNetworkAuthority.TargetBinding(
                      c.getRealm(),
                      c.getNode(),
                      c.getIngress(),
                      c.getServerId(),
                      c.getClientId(),
                      c.getClientAccount()));
          if (expiry == null || !clock.instant().isBefore(expiry))
            return ResolveNetworkTargetResponse.getDefaultInstance();
          return ResolveNetworkTargetResponse.newBuilder()
              .setAllowed(true)
              .setConnection(c)
              .setTargetType("system")
              .setLocal("0")
              .setValidUntil(expiry.toString())
              .build();
        });
  }

  @Override
  @AuthenticatedOnly
  public void authorizeNetworkDelivery(
      AuthorizeNetworkDeliveryRequest request,
      StreamObserver<AuthorizeNetworkDeliveryResponse> observer) {
    respond(
        observer,
        () -> {
          tenant(request.getConnection());
          return sources.authorizeDelivery(request);
        });
  }

  private static void tenant(NetworkAuthorityConnection connection) {
    if (!TenantContext.require().toString().equals(connection.getTenantId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static void positive(String value) {
    if (!IamNetworkAuthority.validNumber(value, false))
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  private static <T> void respond(StreamObserver<T> observer, Supplier<T> work) {
    collab();
    T response;
    try {
      if (io.grpc.Context.current().isCancelled())
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      response = work.get();
      if (io.grpc.Context.current().isCancelled())
        throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (AurigaException e) {
      // Never propagate authority params, causes, violations, or unexpected error codes.
      var code = e.errorCode().code();
      if (code.equals(CommonErrors.FORBIDDEN.code()))
        throw new AurigaException(CommonErrors.FORBIDDEN);
      if (code.equals(CommonErrors.VALIDATION_FAILED.code()))
        throw new AurigaException(CommonErrors.VALIDATION_FAILED);
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    observer.onNext(response);
    observer.onCompleted();
  }
}
