package auriga.collab.grpc;

import auriga.collab.app.NetworkErrors;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority;
import io.grpc.Channel;
import io.grpc.StatusRuntimeException;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;

/** Online, tenant-scoped collab-only IAM adapter. Never falls back to cached permissions. */
public class NetworkAuthorityClient implements NetworkAuthority {
  private final NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub stub;
  private final long timeoutMillis;

  public NetworkAuthorityClient(Channel channel, Duration timeout) {
    if (timeout.isNegative() || timeout.isZero() || timeout.compareTo(Duration.ofSeconds(30)) > 0)
      throw new IllegalArgumentException("IAM timeout must be in (0,30s]");
    timeoutMillis = Math.max(1, timeout.toMillis());
    stub = NetworkAuthorityServiceGrpc.newBlockingStub(channel);
  }

  private NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub rpc() {
    return stub.withDeadlineAfter(timeoutMillis, TimeUnit.MILLISECONDS);
  }

  private <T> T call(Supplier<T> action) {
    tenant();
    try {
      return action.get();
    } catch (StatusRuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private static UUID tenant() {
    UUID tenant =
        TenantContext.current().orElseThrow(() -> new AurigaException(CommonErrors.FORBIDDEN));
    if (TenantContext.PLATFORM_TENANT.equals(tenant))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return tenant;
  }

  @Override
  public boolean active(String realm, String node) {
    return call(() ->
            rpc()
                .networkAuthorityActive(
                    NetworkAuthorityActiveRequest.newBuilder()
                        .setRealm(realm)
                        .setNode(node)
                        .build()))
        .getActive();
  }

  @Override
  public Decision check(Check request) {
    var r =
        CheckNetworkRequest.newBuilder()
            .setRealm(request.realm())
            .setSourceNode(request.sourceNode())
            .setTargetNode(request.targetNode())
            .setLocal(request.local())
            .setSender(
                NetworkAuthoritySender.valueOf(
                    "NETWORK_AUTHORITY_SENDER_" + request.sender().name()))
            .setOperation(
                NetworkAuthorityOperation.valueOf(
                    "NETWORK_AUTHORITY_OPERATION_" + request.operation().name()))
            .setBodyBytes(request.bodyBytes())
            .setTargetNodes(request.targetNodes())
            .setTargetLocals(request.targetLocals())
            .build();
    var result = call(() -> rpc().checkNetwork(r));
    if (!result.getAllowed()) return new Decision(false, result.getReason(), Map.of());
    return new Decision(true, result.getReason(), limits());
  }

  public Map<Limit, Long> limits() {
    var response = call(() -> rpc().networkLimits(NetworkLimitsRequest.getDefaultInstance()));
    var result = new EnumMap<Limit, Long>(Limit.class);
    try {
      for (var entry : response.getLimitsMap().entrySet()) {
        Limit key = Limit.valueOf(entry.getKey());
        if (entry.getValue() < 0) throw new IllegalArgumentException();
        result.put(key, Math.min(key.defaultValue(), entry.getValue()));
      }
      if (result.size() != Limit.values().length) throw new IllegalArgumentException();
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    return Map.copyOf(result);
  }

  public ResolveNetworkTargetResponse resolve(NetworkAuthorityConnection connection, String local) {
    binding(connection);
    if (!"0".equals(local)) throw new AurigaException(CommonErrors.FORBIDDEN);
    var result =
        call(
            () ->
                rpc()
                    .resolveNetworkTarget(
                        ResolveNetworkTargetRequest.newBuilder()
                            .setConnection(connection)
                            .setLocal(local)
                            .build()));
    if (!result.getAllowed()) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    if (!connection.equals(result.getConnection())
        || !local.equals(result.getLocal())
        || !"system".equals(result.getTargetType()))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    deadline(result.getValidUntil());
    return result;
  }

  public Instant authorize(NetworkAuthorityConnection connection, String message, String source) {
    binding(connection);
    var result =
        call(
            () ->
                rpc()
                    .authorizeNetworkDelivery(
                        AuthorizeNetworkDeliveryRequest.newBuilder()
                            .setConnection(connection)
                            .setMessageJson(com.google.protobuf.ByteString.copyFromUtf8(message))
                            .setSourceContextJson(
                                com.google.protobuf.ByteString.copyFromUtf8(source))
                            .build()));
    if (!result.getAllowed()) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    if (!connection.equals(result.getConnection()))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    return deadline(result.getValidUntil());
  }

  public static Instant deadline(String value) {
    try {
      Instant result = Instant.parse(value);
      if (!result.isAfter(Instant.now())) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
      return result;
    } catch (java.time.format.DateTimeParseException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  public static void binding(NetworkAuthorityConnection connection) {
    if (!connection.getTenantId().equals(tenant().toString()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    for (String id : List.of(connection.getRealm(), connection.getNode())) {
      if (!id.matches("[1-9][0-9]{0,19}") || new java.math.BigInteger(id).bitLength() > 64)
        throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    }
    for (String id :
        List.of(
            connection.getIngress(),
            connection.getServerId(),
            connection.getClientId(),
            connection.getClientAccount()))
      if (id.isBlank() || id.length() > 256 || id.chars().anyMatch(c -> c <= 32 || c >= 127))
        throw new AurigaException(NetworkErrors.UNAUTHORIZED);
  }

  @Override
  public Map<NodeReference, Boolean> activeBatch(List<NodeReference> nodes) {
    if (nodes.size() > 10000) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    var result = new LinkedHashMap<NodeReference, Boolean>();
    for (var node : nodes) result.computeIfAbsent(node, n -> active(n.realm(), n.node()));
    return Map.copyOf(result);
  }

  private static AurigaException unsupported() {
    return new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  public Node register(String token) {
    throw unsupported();
  }

  @Override
  public Ticket prepare(String token, String node, String audience) {
    throw unsupported();
  }

  @Override
  public Connection redeem(
      String ticket, String node, String device, String audience, IngressBinding binding) {
    throw unsupported();
  }

  @Override
  public void configure(Configuration configuration) {
    throw unsupported();
  }

  @Override
  public void nodePolicy(String source, String target, boolean allowed) {
    throw unsupported();
  }

  @Override
  public void grant(String node, Sender sender, String local, Operation operation, Instant expiry) {
    throw unsupported();
  }

  @Override
  public void withdraw(String node, Sender sender, String local, Operation operation) {
    throw unsupported();
  }

  @Override
  public void revoke(String node) {
    throw unsupported();
  }
}
