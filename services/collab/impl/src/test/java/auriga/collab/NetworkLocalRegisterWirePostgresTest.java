package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.collab.network.local.LocalRegistry;
import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.security.AccessTokenVerifier;
import auriga.contracts.collab.v1.*;
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import com.google.protobuf.ByteString;
import com.zaxxer.hikari.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.security.KeyPairGenerator;
import java.security.PublicKey;
import java.security.Signature;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Real PG and production RPC clients/wire. The IAM peer is a protocol fixture, not real IAM. */
@ExtendWith(PostgresExtension.class)
class NetworkLocalRegisterWirePostgresTest {
  static final UUID TENANT = UUID.randomUUID();
  static final AtomicInteger NODES = new AtomicInteger(200);
  static final String FINGERPRINT = "sha256:" + "a".repeat(64);
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static HikariDataSource pool;
  static ManagedChannel channel, mailboxChannel;
  static Server server;
  static GrpcServer mailboxServer;
  static NetworkSourceGrpcAuthority source;
  static NetworkMailboxWireService wire;
  static LocalRegistry registry;
  static NetworkSigningKeys keys;
  static PublicKey publicKey;
  static Peer peer;
  static Client iam;
  static JdbcTemplate observer;
  static final MutableClock CLOCK = new MutableClock();

  static class MutableClock extends Clock {
    Duration offset = Duration.ZERO;

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return Instant.now().plus(offset);
    }
  }

  static class Peer extends NetworkAuthorityGrpcTest.Authority {
    volatile boolean unavailable;
    volatile Runnable onCheck = () -> {};

    @Override
    public void checkNetwork(CheckNetworkRequest r, StreamObserver<CheckNetworkResponse> o) {
      onCheck.run();
      if (unavailable) o.onError(Status.UNAVAILABLE.asRuntimeException());
      else super.checkNetwork(r, o);
    }
  }

  /** Fails on any production client RPC while collab still owns a transaction/connection. */
  static class Client extends NetworkAuthorityClient {
    Client(ManagedChannel c) {
      super(c, Duration.ofSeconds(5));
    }

    void outside() {
      assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
      assertFalse(TransactionSynchronizationManager.isSynchronizationActive());
      assertFalse(TransactionSynchronizationManager.hasResource(pool));
    }

    @Override
    public boolean active(String realm, String node) {
      outside();
      return super.active(realm, node);
    }

    @Override
    public Decision check(Check check) {
      outside();
      return super.check(check);
    }

    @Override
    public ResolveNetworkTargetResponse resolve(NetworkAuthorityConnection c, String local) {
      outside();
      return super.resolve(c, local);
    }
  }

  @BeforeAll
  static void setup() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkLocalRegisterWirePostgresTest.class,
            "register_wire_owner",
            "register_wire_app",
            "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    var config = new HikariConfig();
    config.setDataSource(roles.runtime().dataSource());
    config.setMaximumPoolSize(2);
    config.setMinimumIdle(0);
    pool = new HikariDataSource(config);
    jdbc = new JdbcTemplate(pool);
    manager = new FaultManager(pool);
    observer = new JdbcTemplate(roles.runtime().dataSource());
    peer = new Peer();
    server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(peer)
            .build()
            .start();
    channel =
        GrpcChannels.open(
            "127.0.0.1:" + server.getPort(),
            new AurigaClientInterceptor(() -> "collab-test", Duration.ofSeconds(5)));
    iam = new Client(channel);
    source = new NetworkSourceGrpcAuthority(iam, manager);
    keys = new NetworkSigningKeys(CLOCK, 1);
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    publicKey = pair.getPublic();
    keys.add(
        "2",
        "protocol_response",
        "ready-local-test",
        publicKey,
        pair.getPrivate(),
        Instant.now().minusSeconds(60),
        Instant.now().plusSeconds(3600),
        NetworkSigningKeys.State.ACTIVE);
    registry = spy(new LocalRegistry(jdbc, manager, source, iam, CLOCK));
    wire =
        new NetworkMailboxWireService(
            new NetworkMailboxService(jdbc, manager, source, keys, CLOCK, iam),
            registry,
            jdbc,
            manager,
            source,
            keys,
            CLOCK);
    var verifier = mock(AccessTokenVerifier.class);
    when(verifier.verify(anyString()))
        .thenReturn(NetworkMailboxGrpcTest.principal("svc:ai-gateway"));
    mailboxServer =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new NetworkMailboxGrpcService(wire)),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:ai-gateway")),
            null,
            false,
            Set.of());
    mailboxServer.start();
    mailboxChannel =
        GrpcChannels.open(
            "127.0.0.1:" + mailboxServer.port(),
            new AurigaClientInterceptor(() -> "gateway-test", Duration.ofSeconds(20)));
  }

  @AfterEach
  void resetProbes() {
    reset(registry);
    peer.allowed = true;
    peer.expired = false;
    peer.unavailable = false;
    peer.onCheck = () -> {};
    CLOCK.offset = Duration.ZERO;
    ((FaultManager) manager).failNext.set(false);
    ((FaultManager) manager).onCommit = () -> {};
  }

  @AfterAll
  static void close() throws Exception {
    if (mailboxChannel != null) mailboxChannel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (channel != null) channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (mailboxServer != null) mailboxServer.stop();
    if (server != null) server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (keys != null) keys.close();
    if (pool != null) pool.close();
  }

  record Fixture(NetworkAuthorityConnection c, ObjectNode ready) {}

  static Fixture fresh() {
    String node = Integer.toString(NODES.incrementAndGet());
    var c =
        NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
            .setNode(node)
            .setIngress("ingress-" + node)
            .build();
    return new Fixture(
        c,
        (ObjectNode)
            NetworkJson.parse(
                NetworkMailboxWirePostgresTest.ready(
                    "boot-" + node, "nonce-" + node, "ready-" + node)));
  }

  static Fixture reconnect(Fixture f) {
    var c = f.c().toBuilder().setIngress(f.c().getIngress() + "-next").build();
    var request = f.ready().deepCopy();
    request
        .put("boot_id", request.path("boot_id").asString() + "-next")
        .put("channel_nonce", request.path("channel_nonce").asString() + "-next")
        .put("request_id", request.path("request_id").asString() + "-next");
    return new Fixture(c, request);
  }

  static JsonNode ready(Fixture f) {
    return call(() -> NetworkJson.parse(wire.ready(f.c(), bytes(f.ready()))));
  }

  static byte[] bytes(JsonNode node) {
    return NetworkJson.canonical(node).getBytes(StandardCharsets.UTF_8);
  }

  static Map<String, Object> row(Fixture f) {
    return call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s ->
                        jdbc.queryForMap(
                            "SELECT * FROM collab.network_node WHERE tenant_id=? AND realm='2' AND node_id=?",
                            TENANT,
                            f.c().getNode())));
  }

  static void update(Fixture f, String assignments) {
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s ->
                        jdbc.update(
                            "UPDATE collab.network_node SET "
                                + assignments
                                + " WHERE tenant_id=? AND node_id=?",
                            TENANT,
                            f.c().getNode())));
  }

  static void blocked(Fixture f) {
    call(
        () -> {
          NetworkAuthorityGrpcTest.denied(() -> wire.head(f.c()), "network.recovery_conflict");
          return null;
        });
  }

  static <T> T call(Supplier<T> work) {
    return RequestContext.start(null, null)
        .withPrincipal(NetworkMailboxGrpcTest.principal("svc:ai-gateway"))
        .call(() -> TenantContext.call(TENANT, work::get));
  }

  static ObjectNode registration(Fixture f, String type) {
    return (ObjectNode)
        NetworkJson.parse(
            """
        {"app_version":"1.0","type":"local_register","request_id":"request-original",
         "epoch":"1","boot_id":"%s","candidate_local":"7","allocation_op_id":"opaque-op",
         "allocation_fingerprint":"%s","address_type":"%s","expected_registration_revision":"0"}
        """
                .formatted(f.ready().path("boot_id").asString(), FINGERPRINT, type));
  }

  static JsonNode rpc(Fixture f, byte[] frame) {
    var c = f.c();
    var connection =
        NetworkMailboxConnection.newBuilder()
            .setTenantId(c.getTenantId())
            .setRealm(c.getRealm())
            .setNode(c.getNode())
            .setIngress(c.getIngress())
            .setServerId(c.getServerId())
            .setClientId(c.getClientId())
            .setClientAccount(c.getClientAccount())
            .build();
    return TenantContext.call(
        TENANT,
        () ->
            NetworkJson.parse(
                NetworkMailboxServiceGrpc.newBlockingStub(mailboxChannel)
                    .localRegister(
                        NetworkMailboxServiceLocalRegisterRequest.newBuilder()
                            .setConnection(connection)
                            .setFrameJson(ByteString.copyFrom(frame))
                            .build())
                    .getFrameJson()
                    .toByteArray()));
  }

  @Test
  void realRpcRegistersBothCoarseTypesAndReturnsVerifiableOriginalOperation() throws Exception {
    for (String type : List.of("session", "subagent")) {
      var f = fresh();
      ready(f);
      var request = registration(f, type);
      var first = rpc(f, bytes(request));
      validateResult(first);
      assertEquals("protocol_response", first.path("server_auth").path("purpose").asString());
      assertEquals("2", first.path("server_auth").path("audience").path("realm").asString());
      assertEquals(
          f.c().getNode(), first.path("server_auth").path("audience").path("node").asString());
      assertEquals("1", first.path("server_auth").path("audience").path("epoch").asString());
      assertEquals("register_result", first.path("type").asString());
      assertEquals("registered", first.path("result").asString());
      assertEquals(type, first.path("address_type").asString());
      assertEquals("opaque-op", first.path("allocation_op_id").asString());
      assertEquals("1", first.path("registration_revision").asString());
      assertEquals("7", first.path("local_high_water").asString());
      assertFalse(first.has("epoch"));
      assertFalse(first.has("boot_id"));
      assertFalse(first.has("allocation_fingerprint"));
      var signed = (ObjectNode) first.deepCopy();
      String signature = signed.path("server_auth").path("signature").asString();
      ((ObjectNode) signed.path("server_auth")).remove("signature");
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(publicKey);
      verifier.update("Auriga.ServerEnvelope.v1\0".getBytes(StandardCharsets.UTF_8));
      verifier.update(NetworkJson.canonical(signed).getBytes(StandardCharsets.UTF_8));
      assertTrue(verifier.verify(Base64.getUrlDecoder().decode(signature)));
      assertEquals(
          f.ready().path("channel_nonce"), first.path("server_auth").path("channel_nonce"));
      assertEquals("request-original", first.path("server_auth").path("request_id").asString());
      var retry = rpc(f, bytes(request.put("request_id", "request-retry")));
      assertEquals("existing", retry.path("result").asString());
      assertEquals(first.path("registration_id"), retry.path("registration_id"));
      assertEquals("1", row(f).get("registration_revision").toString());
    }
  }

  static int count(Fixture f) {
    return call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s ->
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_local_registration WHERE tenant_id=? AND node_id=?",
                            Integer.class,
                            TENANT,
                            f.c().getNode())));
  }

  static void rejected(Fixture f, ObjectNode request, String code) {
    call(
        () -> {
          NetworkAuthorityGrpcTest.denied(() -> wire.localRegister(f.c(), bytes(request)), code);
          return null;
        });
  }

  static NetworkMailboxWireService cold(NetworkSigningKeys signingKeys) {
    return new NetworkMailboxWireService(
        new NetworkMailboxService(jdbc, manager, source, signingKeys, CLOCK, iam),
        new LocalRegistry(jdbc, manager, source, iam, CLOCK),
        jdbc,
        manager,
        source,
        signingKeys,
        CLOCK);
  }

  @Test
  void originalOperationRecoversAfterLostResponseAndColdInstanceAcrossReady() {
    var f = fresh();
    ready(f);
    var request = registration(f, "subagent");
    var original = rpc(f, bytes(request));
    var next = reconnect(f);
    next.ready().put("local_high_water", "7");
    next.ready()
        .putObject("pending_registration")
        .put("candidate_local", "7")
        .put("allocation_op_id", "opaque-op")
        .put("allocation_fingerprint", FINGERPRINT)
        .put("address_type", "subagent");
    assertEquals("ready", ready(next).path("status").asString());
    request.put("epoch", "2").put("boot_id", next.ready().path("boot_id").asString());
    var result = call(() -> NetworkJson.parse(cold(keys).localRegister(next.c(), bytes(request))));
    assertEquals("existing", result.path("result").asString());
    assertEquals(original.path("registration_id"), result.path("registration_id"));
    assertEquals("subagent", result.path("address_type").asString());
    assertEquals("1", result.path("registration_revision").asString());
    assertEquals("2", result.path("server_auth").path("audience").path("epoch").asString());
    assertEquals(1, count(f));
  }

  @Test
  void validOperationConflictsAndLegacyNullLeaveDurableDiagnosis() {
    for (String mutation : List.of("op", "fingerprint", "type", "null", "low")) {
      var f = fresh();
      ready(f);
      var request = registration(f, "session");
      rpc(f, bytes(request));
      if (mutation.equals("null"))
        call(
            () ->
                new TransactionTemplate(manager)
                    .execute(
                        s ->
                            jdbc.update(
                                "UPDATE collab.network_local_registration SET address_type=NULL WHERE tenant_id=? AND node_id=?",
                                TENANT,
                                f.c().getNode())));
      if (mutation.equals("op")) request.put("allocation_op_id", "different");
      if (mutation.equals("fingerprint"))
        request.put("allocation_fingerprint", "sha256:" + "b".repeat(64));
      if (mutation.equals("type")) request.put("address_type", "subagent");
      if (mutation.equals("low"))
        request.put("candidate_local", "6").put("allocation_op_id", "new-op");
      rejected(f, request, "network.local_allocation_conflict");
      assertEquals("node_identity_conflict", row(f).get("local_identity_status"));
      assertEquals("7", row(f).get("local_high_water").toString());
      assertEquals(1, count(f));
    }
  }

  @Test
  void wrongRevisionBootEpochAndUnknownIngressDoNotRegister() {
    for (String mutation : List.of("revision", "boot", "epoch", "ingress", "binding")) {
      var original = fresh();
      ready(original);
      var request = registration(original, "session");
      var f = original;
      if (mutation.equals("revision")) request.put("expected_registration_revision", "1");
      if (mutation.equals("boot")) request.put("boot_id", "wrong");
      if (mutation.equals("epoch")) request.put("epoch", "2");
      if (mutation.equals("ingress"))
        f = new Fixture(f.c().toBuilder().setIngress("unknown").build(), f.ready());
      if (mutation.equals("binding"))
        f = new Fixture(f.c().toBuilder().setClientId("other").build(), f.ready());
      rejected(
          f, request, mutation.equals("revision") ? "network.conflict" : "network.stale_epoch");
      assertEquals(0, count(original));
      assertEquals("healthy", row(original).get("local_identity_status"));
    }
  }

  @Test
  void strictRawJsonAndSchemaRejectBeforeRegistration() {
    var f = fresh();
    ready(f);
    var valid = registration(f, "session");
    var malformed = new ArrayList<byte[]>();
    malformed.add(new byte[] {(byte) 0xc3, (byte) 0x28});
    malformed.add(new byte[65537]);
    malformed.add((NetworkJson.canonical(valid) + " {}").getBytes(StandardCharsets.UTF_8));
    malformed.add(
        NetworkJson.canonical(valid)
            .replace(
                "\"type\":\"local_register\"",
                "\"type\":\"local_register\",\"type\":\"local_register\"")
            .getBytes(StandardCharsets.UTF_8));
    for (String field : List.of("type", "address_type", "candidate_local", "epoch", "boot_id")) {
      malformed.add(
          bytes(valid.deepCopy().put(field, field.equals("boot_id") ? "bad boot" : "invalid")));
    }
    var missing = valid.deepCopy();
    missing.remove("address_type");
    malformed.add(bytes(missing));
    malformed.add(bytes(valid.deepCopy().put("owner", "self-claimed")));
    for (var raw : malformed)
      NetworkMailboxGrpcTest.rejected(
          () -> rpc(f, raw), Status.Code.INVALID_ARGUMENT, "network.protocol_error");
    assertEquals(0, count(f));
  }

  @Test
  void missingReadyUnknownRecoveryAndReceiptConflictCannotRegister() {
    var missing = fresh();
    rejected(missing, registration(missing, "session"), "network.stale_epoch");
    for (String mode : List.of("unknown", "receipt", "rollback")) {
      var f = fresh();
      if (mode.equals("unknown"))
        f.ready()
            .putObject("pending_registration")
            .put("candidate_local", "7")
            .put("allocation_op_id", "unknown")
            .put("allocation_fingerprint", FINGERPRINT)
            .put("address_type", "session");
      if (mode.equals("receipt")) f.ready().put("resume_cursor", "1");
      if (mode.equals("rollback")) {
        ready(f);
        rpc(f, bytes(registration(f, "session")));
        f = reconnect(f); // Both client counters are now older than the durable witness.
      }
      assertNotEquals("ready", ready(f).path("status").asString());
      rejected(f, registration(f, "session"), "network.recovery_conflict");
      blocked(f);
      assertEquals(mode.equals("rollback") ? 1 : 0, count(f));
    }
  }

  @Test
  void signingFailureLeavesCommittedRegistrationRecoverable() {
    var f = fresh();
    ready(f);
    var request = registration(f, "session");
    try (var empty = new NetworkSigningKeys(CLOCK, 1)) {
      assertThrows(
          AurigaException.class,
          () -> call(() -> cold(empty).localRegister(f.c(), bytes(request))));
    }
    assertEquals(1, count(f));
    var recovered = rpc(f, bytes(request));
    assertEquals("existing", recovered.path("result").asString());
    assertEquals("1", recovered.path("registration_revision").asString());
  }

  @Test
  void lateCommittedResultCannotSignForNewReadyOrRevokedOrExpiredDecision() throws Exception {
    for (String mode : List.of("takeover", "revoked", "decision", "source", "health", "witness")) {
      var f = fresh();
      ready(f);
      var request = registration(f, "session");
      var entered = new CountDownLatch(1);
      var release = new CountDownLatch(1);
      doAnswer(
              i -> {
                var result = i.callRealMethod();
                entered.countDown();
                assertTrue(release.await(10, TimeUnit.SECONDS));
                return result;
              })
          .when(registry)
          .register(eq(TENANT), anyString(), any(Runnable.class));
      try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
        var future = executor.submit(() -> rpc(f, bytes(request)));
        try {
          assertTrue(entered.await(5, TimeUnit.SECONDS));
          assertEquals(1, count(f));
          assertFalse(future.isDone());
          if (mode.equals("takeover")) {
            var next = reconnect(f);
            next.ready().put("local_high_water", "7").put("registration_revision", "1");
            assertEquals("ready", ready(next).path("status").asString());
          }
          if (mode.equals("revoked")) peer.allowed = false;
          if (mode.equals("decision")) CLOCK.offset = Duration.ofSeconds(31);
          if (mode.equals("source")) CLOCK.offset = Duration.ofSeconds(61);
          if (mode.equals("health")) update(f, "local_identity_status='recovery_conflict'");
          if (mode.equals("witness"))
            call(
                () ->
                    new TransactionTemplate(manager)
                        .execute(
                            s ->
                                jdbc.update(
                                    "UPDATE collab.network_local_registration SET address_type='subagent' WHERE tenant_id=? AND node_id=?",
                                    TENANT,
                                    f.c().getNode())));
        } finally {
          release.countDown();
        }
        var failure =
            assertThrows(ExecutionException.class, () -> future.get(10, TimeUnit.SECONDS));
        var grpc = assertInstanceOf(StatusRuntimeException.class, failure.getCause());
        assertEquals(
            mode.equals("takeover")
                ? "network.stale_epoch"
                : mode.equals("health")
                    ? "network.recovery_conflict"
                    : mode.equals("witness") ? "network.conflict" : "network.unauthorized",
            grpc.getTrailers().get(GrpcMetadata.ERROR_CODE));
      } finally {
        resetProbes();
      }
      assertEquals(1, count(f));
    }
  }

  @Test
  void iamWaitOwnsNoConnectionOrNodeLockAndNewReadyFencesOldAllocation() throws Exception {
    var f = fresh();
    ready(f);
    var request = registration(f, "session");
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var checks = new AtomicInteger();
    peer.onCheck =
        () -> {
          if (checks.incrementAndGet() != 2) return;
          entered.countDown();
          try {
            assertTrue(release.await(10, TimeUnit.SECONDS));
          } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new AssertionError(e);
          }
        };
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var future = executor.submit(() -> rpc(f, bytes(request)));
      try {
        assertTrue(entered.await(5, TimeUnit.SECONDS));
        assertEquals(0, pool.getHikariPoolMXBean().getActiveConnections());
        call(
            () ->
                new TransactionTemplate(manager)
                    .execute(
                        s ->
                            jdbc.queryForMap(
                                "SELECT * FROM collab.network_node WHERE tenant_id=? AND node_id=? FOR UPDATE NOWAIT",
                                TENANT,
                                f.c().getNode())));
        var next = reconnect(f);
        assertEquals("ready", ready(next).path("status").asString());
      } finally {
        release.countDown();
      }
      var failure = assertThrows(ExecutionException.class, () -> future.get(10, TimeUnit.SECONDS));
      assertEquals(
          "network.stale_epoch",
          ((StatusRuntimeException) failure.getCause()).getTrailers().get(GrpcMetadata.ERROR_CODE));
    }
    assertEquals(0, count(f));
  }

  @Test
  void concurrentIdenticalOperationsReturnOneDurableWitness() throws Exception {
    var f = fresh();
    ready(f);
    var request = bytes(registration(f, "subagent"));
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var start = new CountDownLatch(1);
      var futures = new ArrayList<Future<JsonNode>>();
      for (int i = 0; i < 4; i++)
        futures.add(
            executor.submit(
                () -> {
                  assertTrue(start.await(5, TimeUnit.SECONDS));
                  return rpc(f, request);
                }));
      start.countDown();
      var ids = new HashSet<String>();
      int registered = 0;
      for (var future : futures) {
        var result = future.get(15, TimeUnit.SECONDS);
        ids.add(result.path("registration_id").asString());
        if (result.path("result").asString().equals("registered")) registered++;
      }
      assertEquals(1, ids.size());
      assertEquals(1, registered);
    }
    assertEquals(1, count(f));
  }

  @Test
  void inheritedTransactionFailsWithoutIamOrMutation() {
    var f = fresh();
    ready(f);
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s -> {
                      NetworkAuthorityGrpcTest.denied(
                          () -> wire.localRegister(f.c(), bytes(registration(f, "session"))),
                          "common.unavailable");
                      return null;
                    }));
    assertEquals(0, count(f));
  }

  static class FaultManager extends TenantTransactionManager {
    volatile Runnable onCommit = () -> {};
    final java.util.concurrent.atomic.AtomicBoolean failNext =
        new java.util.concurrent.atomic.AtomicBoolean();

    FaultManager(javax.sql.DataSource dataSource) {
      super(dataSource);
      setRollbackOnCommitFailure(true);
    }

    @Override
    protected void doCommit(DefaultTransactionStatus status) {
      if (failNext.getAndSet(false))
        throw new org.springframework.transaction.TransactionSystemException(
            "injected commit failure");
      var hook = onCommit;
      onCommit = () -> {};
      hook.run();
      super.doCommit(status);
    }
  }

  @Test
  void registrationOrFinalCommitFailureNeverReturnsSuccess() {
    for (boolean finalCommit : List.of(false, true)) {
      var f = fresh();
      ready(f);
      var request = registration(f, "session");
      doAnswer(
              i -> {
                if (!finalCommit) ((FaultManager) manager).failNext.set(true);
                var result = i.callRealMethod();
                if (finalCommit) ((FaultManager) manager).failNext.set(true);
                return result;
              })
          .when(registry)
          .register(eq(TENANT), anyString(), any(Runnable.class));
      NetworkMailboxGrpcTest.rejected(
          () -> rpc(f, bytes(request)), Status.Code.INTERNAL, "common.internal");
      assertEquals(finalCommit ? 1 : 0, count(f));
      reset(registry);
      var recovered = rpc(f, bytes(request));
      assertEquals(finalCommit ? "existing" : "registered", recovered.path("result").asString());
      assertEquals("1", recovered.path("registration_revision").asString());
    }
  }

  @Test
  void finalNodeLockWaitDoesNotExtendDecisionExpiry() throws Exception {
    var f = fresh();
    ready(f);
    var entered = new CountDownLatch(1);
    var proceed = new CountDownLatch(1);
    var locked = new CountDownLatch(1);
    var unlock = new CountDownLatch(1);
    doAnswer(
            i -> {
              var result = i.callRealMethod();
              entered.countDown();
              assertTrue(proceed.await(10, TimeUnit.SECONDS));
              return result;
            })
        .when(registry)
        .register(eq(TENANT), anyString(), any(Runnable.class));
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var response = executor.submit(() -> rpc(f, bytes(registration(f, "session"))));
      assertTrue(entered.await(5, TimeUnit.SECONDS));
      var holder =
          executor.submit(
              () ->
                  call(
                      () ->
                          new TransactionTemplate(manager)
                              .execute(
                                  s -> {
                                    jdbc.queryForMap(
                                        "SELECT * FROM collab.network_node WHERE tenant_id=? AND node_id=? FOR UPDATE",
                                        TENANT,
                                        f.c().getNode());
                                    locked.countDown();
                                    try {
                                      assertTrue(unlock.await(10, TimeUnit.SECONDS));
                                    } catch (InterruptedException e) {
                                      Thread.currentThread().interrupt();
                                      throw new AssertionError(e);
                                    }
                                    return null;
                                  })));
      try {
        assertTrue(locked.await(5, TimeUnit.SECONDS));
        proceed.countDown();
        long until = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        boolean waiting = false;
        while (System.nanoTime() < until) {
          waiting =
              observer.queryForObject(
                      "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%network_node%FOR UPDATE%'",
                      Integer.class)
                  > 0;
          if (waiting) break;
          Thread.yield();
        }
        assertTrue(waiting, "final transaction must actually wait on the PG node lock");
        assertFalse(response.isDone());
        CLOCK.offset = Duration.ofSeconds(31);
      } finally {
        proceed.countDown();
        unlock.countDown();
      }
      holder.get(10, TimeUnit.SECONDS);
      var failure =
          assertThrows(ExecutionException.class, () -> response.get(10, TimeUnit.SECONDS));
      assertEquals(
          "network.unauthorized",
          ((StatusRuntimeException) failure.getCause()).getTrailers().get(GrpcMetadata.ERROR_CODE));
    }
    assertEquals(1, count(f));
  }

  static void validateResult(JsonNode result) throws Exception {
    var resources = new HashMap<String, String>();
    String base = "https://auriga.example.com/contracts/network/";
    for (String name : List.of("protocol", "address", "envelope")) {
      try (var stream =
          NetworkLocalRegisterWirePostgresTest.class
              .getClassLoader()
              .getResourceAsStream("auriga/contracts/network/" + name + ".schema.json")) {
        assertNotNull(stream);
        resources.put(
            base + name + ".schema.json",
            new String(stream.readAllBytes(), StandardCharsets.UTF_8));
      }
    }
    var schemas =
        com.networknt.schema.SchemaRegistry.withDefaultDialect(
            com.networknt.schema.SpecificationVersion.DRAFT_2020_12,
            b ->
                b.schemaRegistryConfig(
                        com.networknt.schema.SchemaRegistryConfig.builder()
                            .formatAssertionsEnabled(true)
                            .build())
                    .schemas(resources));
    assertTrue(
        schemas
            .getSchema(
                com.networknt.schema.SchemaLocation.of(
                    base + "protocol.schema.json#/$defs/RegisterResult"))
            .validate(result)
            .isEmpty());
  }

  @Test
  void finalCommitCannotReturnAnExpiredSignedSuccess() {
    var f = fresh();
    ready(f);
    doAnswer(
            i -> {
              var result = i.callRealMethod();
              ((FaultManager) manager).onCommit = () -> CLOCK.offset = Duration.ofSeconds(31);
              return result;
            })
        .when(registry)
        .register(eq(TENANT), anyString(), any(Runnable.class));
    var failure =
        assertThrows(StatusRuntimeException.class, () -> rpc(f, bytes(registration(f, "session"))));
    assertEquals("network.unauthorized", failure.getTrailers().get(GrpcMetadata.ERROR_CODE));
    assertEquals(1, count(f));
    CLOCK.offset = Duration.ZERO;
    reset(registry);
    assertEquals("existing", rpc(f, bytes(registration(f, "session"))).path("result").asString());
  }
}
