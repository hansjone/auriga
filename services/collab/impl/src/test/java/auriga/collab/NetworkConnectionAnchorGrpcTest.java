package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.grpc.AurigaClientInterceptor;
import auriga.common.grpc.GrpcChannels;
import auriga.common.grpc.GrpcMetadata;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.UnknownFieldSet;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.UnaryOperator;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.transaction.support.TransactionSynchronizationManager;

/** IAM is a contract fixture; these are real generated gRPC calls, not ownership integration. */
class NetworkConnectionAnchorGrpcTest {
  static final UnknownFieldSet UNKNOWN =
      UnknownFieldSet.newBuilder()
          .addField(99, UnknownFieldSet.Field.newBuilder().addVarint(1).build())
          .build();

  static NetworkAuthorityConnection connection(UUID tenant, String ingress) {
    return NetworkAuthorityGrpcTest.connection(tenant).toBuilder()
        .setIngress(ingress)
        .setServerId("N" + "A".repeat(55))
        .build();
  }

  static NetworkConnectionAnchor anchor(NetworkAuthorityConnection c) {
    return NetworkConnectionAnchor.newBuilder()
        .setAnchorId(UuidV7.next().toString())
        .setConnection(c)
        .build();
  }

  @Test
  void resolveMustRejectPresentButInvalidAnchorInsteadOfIgnoringFieldSix() throws Exception {
    UUID tenant = UUID.randomUUID();
    var c = connection(tenant, "a".repeat(26));
    var malformed = anchor(c).toBuilder().setAnchorId("not-an-authoritative-id").build();
    var server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(
                new NetworkAuthorityServiceGrpc.NetworkAuthorityServiceImplBase() {
                  @Override
                  public void resolveNetworkTarget(
                      ResolveNetworkTargetRequest r,
                      StreamObserver<ResolveNetworkTargetResponse> o) {
                    o.onNext(
                        ResolveNetworkTargetResponse.newBuilder()
                            .setAllowed(true)
                            .setConnection(c)
                            .setLocal("0")
                            .setTargetType("system")
                            .setValidUntil(Instant.now().plusSeconds(20).toString())
                            .setConnectionAnchor(malformed)
                            .build());
                    o.onCompleted();
                  }
                })
            .build()
            .start();
    var channel = GrpcChannels.open("127.0.0.1:" + server.getPort());
    try {
      var client = new NetworkAuthorityClient(channel, Duration.ofSeconds(2));
      TenantContext.run(
          tenant,
          () ->
              assertEquals(
                  "common.unavailable",
                  assertThrows(AurigaException.class, () -> client.resolve(c, "0"))
                      .errorCode()
                      .code()));
    } finally {
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }
  }

  static class Peer extends NetworkAuthorityGrpcTest.Authority {
    final AtomicInteger reads = new AtomicInteger(), sourceCalls = new AtomicInteger();
    final java.util.Map<NetworkAuthorityConnection, NetworkConnectionAnchor> anchors =
        new java.util.concurrent.ConcurrentHashMap<>();
    volatile boolean metadata = true, hang;
    volatile Status failure;
    volatile long budgetCeiling = 30000;
    volatile UnaryOperator<ResolveNetworkTargetResponse> resolveReply = UnaryOperator.identity();
    volatile UnaryOperator<ReadNetworkConnectionAnchorResponse> readReply =
        UnaryOperator.identity();

    NetworkConnectionAnchor ref(NetworkAuthorityConnection c) {
      return anchors.computeIfAbsent(c, NetworkConnectionAnchorGrpcTest::anchor);
    }

    @Override
    public void resolveNetworkTarget(
        ResolveNetworkTargetRequest r, StreamObserver<ResolveNetworkTargetResponse> o) {
      var b =
          ResolveNetworkTargetResponse.newBuilder()
              .setAllowed(true)
              .setConnection(r.getConnection())
              .setLocal("0")
              .setTargetType("system")
              .setValidUntil(Instant.now().plusSeconds(17).toString());
      if (metadata) b.setConnectionAnchor(ref(r.getConnection()));
      o.onNext(resolveReply.apply(b.build()));
      o.onCompleted();
    }

    @Override
    public void readNetworkConnectionAnchor(
        ReadNetworkConnectionAnchorRequest r,
        StreamObserver<ReadNetworkConnectionAnchorResponse> o) {
      reads.incrementAndGet();
      assertTrue(
          Context.current().getDeadline().timeRemaining(TimeUnit.MILLISECONDS)
              <= budgetCeiling + 5);
      if (failure != null) {
        o.onError(failure.asRuntimeException());
        return;
      }
      if (hang) return;
      o.onNext(
          readReply.apply(
              ReadNetworkConnectionAnchorResponse.newBuilder()
                  .setOriginal(r.getOriginal())
                  .setCurrent(r.getCurrent())
                  .build()));
      o.onCompleted();
    }
  }

  static final class Harness implements AutoCloseable {
    final Server server;
    final ManagedChannel channel;

    Harness(Peer peer, UUID tenant) throws Exception {
      server =
          NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
              .intercept(
                  new ServerInterceptor() {
                    @Override
                    public <Q, R> ServerCall.Listener<Q> interceptCall(
                        ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
                      assertEquals(
                          "Bearer collab-anchor-test", headers.get(GrpcMetadata.AUTHORIZATION));
                      assertEquals(tenant.toString(), headers.get(GrpcMetadata.TENANT_ID));
                      return next.startCall(call, headers);
                    }
                  })
              .addService(peer)
              .addService(
                  new NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase() {
                    @Override
                    public void registerAgentSource(
                        RegisterAgentSourceRequest r,
                        StreamObserver<RegisterAgentSourceResponse> o) {
                      peer.sourceCalls.incrementAndGet();
                      o.onError(Status.INTERNAL.asRuntimeException());
                    }

                    @Override
                    public void readAgentSourceReceipt(
                        ReadAgentSourceReceiptRequest r,
                        StreamObserver<RegisterAgentSourceResponse> o) {
                      peer.sourceCalls.incrementAndGet();
                      o.onError(Status.INTERNAL.asRuntimeException());
                    }

                    @Override
                    public void getAgentSource(
                        GetAgentSourceRequest r, StreamObserver<GetAgentSourceResponse> o) {
                      peer.sourceCalls.incrementAndGet();
                      o.onError(Status.INTERNAL.asRuntimeException());
                    }

                    @Override
                    public void revokeAgentSource(
                        RevokeAgentSourceRequest r, StreamObserver<RevokeAgentSourceResponse> o) {
                      o.onNext(RevokeAgentSourceResponse.getDefaultInstance());
                      o.onCompleted();
                    }
                  })
              .build()
              .start();
      channel =
          GrpcChannels.open(
              "127.0.0.1:" + server.getPort(),
              new AurigaClientInterceptor(() -> "collab-anchor-test", Duration.ofSeconds(30)));
    }

    @Override
    public void close() throws Exception {
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }
  }

  @Test
  void resolveCarriesOneResponseDeadlineAndOptionalAnchorAndPreservesOldRpcSemantics()
      throws Exception {
    UUID tenant = UUID.randomUUID();
    var peer = new Peer();
    var c = connection(tenant, "a".repeat(26));
    try (var h = new Harness(peer, tenant)) {
      var client = new NetworkAuthorityClient(h.channel, Duration.ofSeconds(2));
      var source =
          new NetworkSourceGrpcAuthority(
              client, new TenantTransactionManager(mock(DataSource.class)));
      TenantContext.run(
          tenant,
          () -> {
            var response = source.verifiedConnection(c);
            assertEquals(peer.ref(c), response.anchor());
            assertTrue(response.deadline().isBefore(Instant.now().plusSeconds(18)));
            peer.metadata = false;
            assertNull(source.verifiedConnection(c).anchor());
            assertTrue(source.verify(c).isAfter(Instant.now()));
            assertNotNull(
                source.revokeSource(
                    RevokeAgentSourceRequest.newBuilder().setConnection(c).build()));
          });
      assertEquals(0, peer.reads.get());
      assertEquals(0, peer.sourceCalls.get());
    }
  }

  @Test
  void presentReferencesAreStrictAndMustMatchTheCompleteResolvedConnection() throws Exception {
    UUID tenant = UUID.randomUUID();
    var peer = new Peer();
    var c = connection(tenant, "a".repeat(26));
    try (var h = new Harness(peer, tenant)) {
      var client = new NetworkAuthorityClient(h.channel, Duration.ofSeconds(2));
      TenantContext.run(
          tenant,
          () -> {
            for (var bad : invalidReferences(peer.ref(c))) {
              peer.resolveReply = r -> r.toBuilder().setConnectionAnchor(bad).build();
              denied("common.unavailable", () -> client.resolve(c, "0"));
            }
            var different =
                peer.ref(c).toBuilder()
                    .setConnection(c.toBuilder().setClientAccount("another-account"))
                    .build();
            peer.resolveReply = r -> r.toBuilder().setConnectionAnchor(different).build();
            denied("common.unavailable", () -> client.resolve(c, "0"));
          });
    }
  }

  static List<NetworkConnectionAnchor> invalidReferences(NetworkConnectionAnchor ref) {
    var c = ref.getConnection();
    return List.of(
        ref.toBuilder().setAnchorId(UUID.randomUUID().toString()).build(),
        ref.toBuilder().clearConnection().build(),
        ref.toBuilder().setUnknownFields(UNKNOWN).build(),
        ref.toBuilder().setConnection(c.toBuilder().setUnknownFields(UNKNOWN)).build(),
        ref.toBuilder()
            .setConnection(c.toBuilder().setTenantId(UUID.randomUUID().toString()))
            .build(),
        ref.toBuilder().setConnection(c.toBuilder().setRealm("02")).build(),
        ref.toBuilder().setConnection(c.toBuilder().setNode("18446744073709551616")).build(),
        ref.toBuilder().setConnection(c.toBuilder().setIngress("not-an-ingress")).build(),
        ref.toBuilder().setConnection(c.toBuilder().setServerId("server-test")).build(),
        ref.toBuilder().setConnection(c.toBuilder().setClientId("01")).build(),
        ref.toBuilder().setConnection(c.toBuilder().setClientAccount("")).build());
  }

  @Test
  void pureReaderUsesBothExactReferencesAndRejectsAllIncompleteOrChangedEchoes() throws Exception {
    UUID tenant = UUID.randomUUID();
    var peer = new Peer();
    var original = peer.ref(connection(tenant, "a".repeat(26)));
    var current = peer.ref(connection(tenant, "b".repeat(26)));
    try (var h = new Harness(peer, tenant)) {
      var client = new NetworkAuthorityClient(h.channel, Duration.ofSeconds(30));
      TenantContext.run(
          tenant,
          () ->
              client.sourceRequest(
                  () -> {
                    var first = client.readConnectionAnchor(original, current);
                    assertEquals(original, first.getOriginal());
                    assertEquals(current, first.getCurrent());
                    long remaining = NetworkAuthorityClient.sourceRemainingMillis();
                    peer.budgetCeiling = remaining;
                    client.sourceRequest(
                        () -> {
                          assertTrue(NetworkAuthorityClient.sourceRemainingMillis() <= remaining);
                          return client.readConnectionAnchor(original, current);
                        });
                    for (UnaryOperator<ReadNetworkConnectionAnchorResponse> bad :
                        List.<UnaryOperator<ReadNetworkConnectionAnchorResponse>>of(
                            r -> ReadNetworkConnectionAnchorResponse.getDefaultInstance(),
                            r -> r.toBuilder().clearOriginal().build(),
                            r -> r.toBuilder().clearCurrent().build(),
                            r -> r.toBuilder().setOriginal(current).build(),
                            r -> r.toBuilder().setCurrent(original).build(),
                            r -> r.toBuilder().setUnknownFields(UNKNOWN).build(),
                            r ->
                                r.toBuilder()
                                    .setOriginal(original.toBuilder().setUnknownFields(UNKNOWN))
                                    .build())) {
                      peer.readReply = bad;
                      denied(
                          "common.unavailable",
                          () -> client.readConnectionAnchor(original, current));
                    }
                    int calls = peer.reads.get();
                    for (var bad : invalidReferences(original))
                      assertThrows(
                          AurigaException.class, () -> client.readConnectionAnchor(bad, current));
                    denied(
                        "common.forbidden",
                        () ->
                            client.readConnectionAnchor(
                                original,
                                current.toBuilder()
                                    .setConnection(
                                        current.getConnection().toBuilder().setNode("21"))
                                    .build()));
                    denied(
                        "common.forbidden",
                        () ->
                            client.readConnectionAnchor(
                                original,
                                current.toBuilder()
                                    .setConnection(
                                        current.getConnection().toBuilder().setRealm("3"))
                                    .build()));
                    assertEquals(calls, peer.reads.get());
                    return null;
                  }));
      TenantContext.run(
          UUID.randomUUID(),
          () -> denied("common.forbidden", () -> client.readConnectionAnchor(original, current)));
      TenantContext.run(
          TenantContext.PLATFORM_TENANT,
          () -> denied("common.forbidden", () -> client.readConnectionAnchor(original, current)));
      assertEquals(0, peer.sourceCalls.get());
    }
  }

  @Test
  void pureReaderIsOutsideEveryDatabaseContextAndNeverFallsBackOnFailures() throws Exception {
    UUID tenant = UUID.randomUUID();
    var peer = new Peer();
    var ref = peer.ref(connection(tenant, "a".repeat(26)));
    try (var h = new Harness(peer, tenant)) {
      var dataSource = mock(DataSource.class);
      var source =
          new NetworkSourceGrpcAuthority(
              new NetworkAuthorityClient(h.channel, Duration.ofMillis(100)),
              new TenantTransactionManager(dataSource));
      TenantContext.run(
          tenant,
          () -> {
            TransactionSynchronizationManager.setActualTransactionActive(true);
            try {
              denied("common.unavailable", () -> source.readConnectionAnchor(ref, ref));
            } finally {
              TransactionSynchronizationManager.setActualTransactionActive(false);
            }
            TransactionSynchronizationManager.initSynchronization();
            try {
              denied("common.unavailable", () -> source.readConnectionAnchor(ref, ref));
            } finally {
              TransactionSynchronizationManager.clearSynchronization();
            }
            TransactionSynchronizationManager.bindResource(dataSource, new Object());
            try {
              denied("common.unavailable", () -> source.readConnectionAnchor(ref, ref));
            } finally {
              TransactionSynchronizationManager.unbindResource(dataSource);
            }
            assertEquals(0, peer.reads.get());
            for (var status :
                List.of(
                    Status.PERMISSION_DENIED,
                    Status.UNAVAILABLE,
                    Status.NOT_FOUND,
                    Status.UNIMPLEMENTED,
                    Status.CANCELLED,
                    Status.DEADLINE_EXCEEDED)) {
              peer.failure = status;
              denied(
                  "common.unavailable",
                  () -> source.sourceRequest(() -> source.readConnectionAnchor(ref, ref)));
            }
            peer.failure = null;
            peer.hang = true;
            denied(
                "common.unavailable",
                () -> source.sourceRequest(() -> source.readConnectionAnchor(ref, ref)));
            try (var cancelled = Context.current().withCancellation()) {
              cancelled.cancel(null);
              cancelled.run(
                  () ->
                      denied(
                          "common.unavailable",
                          () -> source.sourceRequest(() -> source.readConnectionAnchor(ref, ref))));
            }
          });
      assertEquals(0, peer.sourceCalls.get());
    }
  }

  private static void denied(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }
}
