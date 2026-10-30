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
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;

/** Online, tenant-scoped collab-only IAM adapter. Never falls back to cached permissions. */
public class NetworkAuthorityClient implements NetworkAuthority {
  private static final Logger LOG = LoggerFactory.getLogger(NetworkAuthorityClient.class);

  private enum Phase {
    RESOLVE,
    ACTIVE,
    CHECK,
    LIMITS,
    OTHER
  }

  private enum InboundRemaining {
    NONE,
    EXPIRED,
    LE_25_MS,
    LE_100_MS,
    LE_250_MS,
    LE_1000_MS,
    GT_1000_MS
  }

  private final NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub stub;
  private final long timeoutMillis;
  private final NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceBlockingStub sources;
  private static final ScopedValue<Long> SOURCE_DEADLINE = ScopedValue.newInstance();

  public <T> T sourceRequest(Supplier<T> work) {
    if (SOURCE_DEADLINE.isBound()) return work.get();
    return ScopedValue.where(SOURCE_DEADLINE, System.nanoTime() + TimeUnit.SECONDS.toNanos(30))
        .call(work::get);
  }

  private long remainingMillis() {
    long remaining = sourceRemainingMillis();
    if (remaining <= 0) throw new AurigaException(CommonErrors.UNAVAILABLE);
    return Math.min(timeoutMillis, remaining);
  }

  public static long sourceRemainingMillis() {
    return SOURCE_DEADLINE.isBound()
        ? Math.max(0, TimeUnit.NANOSECONDS.toMillis(SOURCE_DEADLINE.get() - System.nanoTime()))
        : 30000;
  }

  public NetworkAuthorityClient(Channel channel, Duration timeout) {
    if (timeout.isNegative() || timeout.isZero() || timeout.compareTo(Duration.ofSeconds(30)) > 0)
      throw new IllegalArgumentException("IAM timeout must be in (0,30s]");
    timeoutMillis = Math.max(1, timeout.toMillis());
    stub = NetworkAuthorityServiceGrpc.newBlockingStub(channel);
    sources = NetworkSourceAuthorityServiceGrpc.newBlockingStub(channel);
  }

  private NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub rpc() {
    return stub.withDeadlineAfter(remainingMillis(), TimeUnit.MILLISECONDS);
  }

  private <T> T call(Phase phase, Supplier<T> action) {
    tenant();
    long started = System.nanoTime();
    InboundRemaining inbound = inboundRemaining();
    try {
      return action.get();
    } catch (StatusRuntimeException e) {
      classifyFailure(phase, e.getStatus().getCode(), started, inbound);
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private static InboundRemaining inboundRemaining() {
    var deadline = io.grpc.Context.current().getDeadline();
    if (deadline == null) return InboundRemaining.NONE;
    long remaining = deadline.timeRemaining(TimeUnit.NANOSECONDS);
    if (remaining <= 0) return InboundRemaining.EXPIRED;
    if (remaining <= TimeUnit.MILLISECONDS.toNanos(25)) return InboundRemaining.LE_25_MS;
    if (remaining <= TimeUnit.MILLISECONDS.toNanos(100)) return InboundRemaining.LE_100_MS;
    if (remaining <= TimeUnit.MILLISECONDS.toNanos(250)) return InboundRemaining.LE_250_MS;
    if (remaining <= TimeUnit.MILLISECONDS.toNanos(1000)) return InboundRemaining.LE_1000_MS;
    return InboundRemaining.GT_1000_MS;
  }

  private static void classifyFailure(
      Phase phase, io.grpc.Status.Code code, long started, InboundRemaining inbound) {
    if (!LOG.isWarnEnabled()) return;
    // Do not implicitly attach identity/credential fields from the caller's MDC either.
    Map<String, String> previous = MDC.getCopyOfContextMap();
    try {
      MDC.clear();
      long elapsed = Math.max(0, TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started));
      boolean cancelled = io.grpc.Context.current().isCancelled();
      LOG.atWarn()
          .addKeyValue("phase", phase.name())
          .addKeyValue("grpc_code", code.name())
          .addKeyValue("elapsed_ms", elapsed)
          .addKeyValue("inbound_remaining", inbound.name())
          .addKeyValue("cancelled", cancelled)
          // Keep the same allowlisted values visible with Boot's default %m console pattern.
          .log(
              "collab_iam_rpc_failure phase={} grpc_code={} elapsed_ms={} inbound_remaining={} cancelled={}",
              phase.name(),
              code.name(),
              elapsed,
              inbound.name(),
              cancelled);
    } finally {
      if (previous == null) MDC.clear();
      else MDC.setContextMap(previous);
    }
  }

  // These methods deliberately do not require a live original transport: mutation receipt
  // recovery and narrowing revocation use the immutable retained request/ownership anchors.
  public RegisterAgentSourceResponse registerSource(RegisterAgentSourceRequest request) {
    return sourceCall(
        () ->
            sources
                .withDeadlineAfter(remainingMillis(), TimeUnit.MILLISECONDS)
                .registerAgentSource(request));
  }

  public RegisterAgentSourceResponse readSourceReceipt(RegisterAgentSourceRequest original) {
    // Validate the delegated tenant, not the liveness of this retained OLD connection.
    if (!original.hasConnection()
        || !tenant().toString().equals(original.getConnection().getTenantId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return sourceCall(
        () ->
            sources
                .withDeadlineAfter(remainingMillis(), TimeUnit.MILLISECONDS)
                .readAgentSourceReceipt(
                    ReadAgentSourceReceiptRequest.newBuilder()
                        .setOriginalRequest(original)
                        .build()));
  }

  public RegisterAgentSourceResponse fenceSourceReceipt(RegisterAgentSourceRequest original) {
    if (!original.hasConnection()
        || !tenant().toString().equals(original.getConnection().getTenantId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return sourceCall(
        () ->
            sources
                .withDeadlineAfter(remainingMillis(), TimeUnit.MILLISECONDS)
                .fenceAgentSourceReceipt(
                    FenceAgentSourceReceiptRequest.newBuilder()
                        .setOriginalRequest(original)
                        .build()));
  }

  /** Retains transport classification internally; overloaded alone never proves a read miss. */
  public static final class SourceRpcFailure extends AurigaException {
    private final io.grpc.Status.Code grpcCode;

    private SourceRpcFailure(StatusRuntimeException error) {
      super(
          switch (error.getStatus().getCode()) {
            case INVALID_ARGUMENT -> NetworkErrors.PROTOCOL_ERROR;
            case ABORTED, ALREADY_EXISTS -> NetworkErrors.CONFLICT;
            case PERMISSION_DENIED, UNAUTHENTICATED -> NetworkErrors.UNAUTHORIZED;
            default ->
                auriga.common.error.ErrorCode.of(
                    "network.overloaded", 503, io.grpc.Status.Code.UNAVAILABLE);
          });
      grpcCode = error.getStatus().getCode();
    }

    public io.grpc.Status.Code grpcCode() {
      return grpcCode;
    }
  }

  public GetAgentSourceResponse getSource(GetAgentSourceRequest request) {
    binding(request.getConnection());
    return sourceCall(
        () ->
            sources
                .withDeadlineAfter(remainingMillis(), TimeUnit.MILLISECONDS)
                .getAgentSource(request));
  }

  public RevokeAgentSourceResponse revokeSource(RevokeAgentSourceRequest request) {
    return sourceCall(
        () ->
            sources
                .withDeadlineAfter(remainingMillis(), TimeUnit.MILLISECONDS)
                .revokeAgentSource(request));
  }

  public ReadNetworkConnectionAnchorResponse readConnectionAnchor(
      NetworkConnectionAnchor original, NetworkConnectionAnchor current) {
    connectionAnchor(original);
    connectionAnchor(current);
    if (!original.getConnection().getRealm().equals(current.getConnection().getRealm())
        || !original.getConnection().getNode().equals(current.getConnection().getNode()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    var request =
        ReadNetworkConnectionAnchorRequest.newBuilder()
            .setOriginal(original)
            .setCurrent(current)
            .build();
    var response = call(Phase.OTHER, () -> rpc().readNetworkConnectionAnchor(request));
    if (!response.getUnknownFields().asMap().isEmpty()
        || !response.hasOriginal()
        || !response.hasCurrent()
        || !original.equals(response.getOriginal())
        || !current.equals(response.getCurrent()))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    return response;
  }

  /** Structural validation only; the pure IAM reader establishes historical ownership. */
  public static void connectionAnchor(NetworkConnectionAnchor anchor) {
    if (anchor == null
        || !anchor.getUnknownFields().asMap().isEmpty()
        || !anchor.hasConnection()
        || !anchor
            .getAnchorId()
            .matches("[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    var c = anchor.getConnection();
    binding(c);
    if (!c.getUnknownFields().asMap().isEmpty()
        || !c.getIngress().matches("[a-z2-7]{26}")
        || !c.getServerId().matches("N[A-Z2-7]{55}")
        || !c.getClientId().matches("[1-9][0-9]{0,19}")
        || new java.math.BigInteger(c.getClientId()).bitLength() > 64)
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private <T> T sourceCall(Supplier<T> action) {
    tenant();
    try {
      return action.get();
    } catch (StatusRuntimeException e) {
      throw new SourceRpcFailure(e);
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
    return call(
            Phase.ACTIVE,
            () ->
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
    var result = call(Phase.CHECK, () -> rpc().checkNetwork(r));
    if (!result.getAllowed()) return new Decision(false, result.getReason(), Map.of());
    return new Decision(true, result.getReason(), limits());
  }

  public Map<Limit, Long> limits() {
    var response =
        call(Phase.LIMITS, () -> rpc().networkLimits(NetworkLimitsRequest.getDefaultInstance()));
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
            Phase.RESOLVE,
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
    if (result.hasConnectionAnchor()) {
      try {
        connectionAnchor(result.getConnectionAnchor());
        if (!connection.equals(result.getConnectionAnchor().getConnection()))
          throw new AurigaException(CommonErrors.UNAVAILABLE);
      } catch (AurigaException e) {
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      }
    }
    return result;
  }

  public Instant authorize(NetworkAuthorityConnection connection, String message, String source) {
    binding(connection);
    var result =
        call(
            Phase.OTHER,
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
