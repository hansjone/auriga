package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkSourceAuthority.Target;
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
class NetworkReadyLocalRecoveryPostgresTest {
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
            NetworkReadyLocalRecoveryPostgresTest.class,
            "ready_local_owner",
            "ready_local_app",
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
    manager = new TenantTransactionManager(pool);
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
    var iam = new Client(channel);
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
            new AurigaClientInterceptor(() -> "gateway-test", Duration.ofSeconds(5)));
  }

  @AfterEach
  void resetProbes() {
    reset(registry);
    peer.allowed = true;
    peer.expired = false;
    peer.unavailable = false;
    peer.onCheck = () -> {};
    CLOCK.offset = Duration.ZERO;
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

  static Fixture typed() {
    var f = fresh();
    ready(f);
    register(f, "7", "0");
    var next = reconnect(f);
    next.ready().put("local_high_water", "7").put("registration_revision", "1");
    return next;
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

  static void pending(Fixture f, String type) {
    f.ready()
        .putObject("pending_registration")
        .put("candidate_local", "7")
        .put("allocation_op_id", "opaque-op")
        .put("allocation_fingerprint", FINGERPRINT)
        .put("address_type", type);
  }

  static LocalRegistry.Registration register(Fixture f, String local, String revision) {
    return call(
        () -> {
          var row = row(f);
          var target =
              new Target(
                  "2",
                  f.c().getNode(),
                  row.get("wire_incarnation").toString(),
                  ((Number) row.get("connection_epoch")).longValue(),
                  f.ready().path("channel_nonce").asString(),
                  "register",
                  CLOCK.instant().plusSeconds(60));
          String frame =
              """
          {"app_version":"1.0","type":"local_register","request_id":"register","epoch":"%s","boot_id":"%s","candidate_local":"%s","allocation_op_id":"opaque-op%s","allocation_fingerprint":"%s","address_type":"session","expected_registration_revision":"%s"}
          """
                  .formatted(
                      target.epoch(),
                      f.ready().path("boot_id").asString(),
                      local,
                      "7".equals(local) ? "" : local,
                      FINGERPRINT,
                      revision);
          return source.withTarget(f.c(), target, () -> registry.register(TENANT, frame));
        });
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

  @Test
  void productionReadyRpcReturnsSignedTypedCountersAndRejectsDelegationMismatch() throws Exception {
    var f = typed();
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
    var request =
        NetworkMailboxServiceReadyRequest.newBuilder()
            .setConnection(connection)
            .setFrameJson(ByteString.copyFrom(bytes(f.ready())))
            .build();
    var rpc = NetworkMailboxServiceGrpc.newBlockingStub(mailboxChannel);
    var result =
        TenantContext.call(
            TENANT, () -> NetworkJson.parse(rpc.ready(request).getFrameJson().toStringUtf8()));
    assertEquals("ready", result.path("status").asString());
    assertEquals("7", result.path("local_high_water").asString());
    assertEquals("1", result.path("registration_revision").asString());
    assertFalse(result.has("recovered_registration"));
    var unsigned = (ObjectNode) result.deepCopy();
    String signature = unsigned.path("server_auth").path("signature").asString();
    ((ObjectNode) unsigned.path("server_auth")).remove("signature");
    var verifier = Signature.getInstance("Ed25519");
    verifier.initVerify(publicKey);
    verifier.update("Auriga.ServerEnvelope.v1\0".getBytes(StandardCharsets.UTF_8));
    verifier.update(bytes(unsigned));
    assertTrue(verifier.verify(Base64.getUrlDecoder().decode(signature)));
    var error =
        assertThrows(
            StatusRuntimeException.class,
            () -> TenantContext.call(UUID.randomUUID(), () -> rpc.ready(request)));
    assertEquals(Status.Code.PERMISSION_DENIED, error.getStatus().getCode());
  }

  @Test
  void lostRegistrationResponseRestoresOnlyOriginalTypedFact() {
    var f = typed();
    f.ready().put("registration_revision", "0");
    pending(f, "session");
    assertEquals("ready", ready(f).path("status").asString());
    var original = register(f, "7", "0");
    assertEquals("existing", original.result());
    assertEquals(1, original.grantedEpoch());
    assertEquals("session", original.addressType());
    assertEquals(1, registrations(f));
  }

  static int registrations(Fixture f) {
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

  @Test
  void unknownPendingDoesNotRegisterOrGrantAnAddress() {
    var f = fresh();
    f.ready().put("local_high_water", "7");
    pending(f, "subagent");
    var result = ready(f);
    assertEquals("ready", result.path("status").asString());
    assertEquals("0", result.path("local_high_water").asString());
    assertEquals("0", result.path("registration_revision").asString());
    assertEquals(0, registrations(f));
    call(
        () -> {
          NetworkAuthorityGrpcTest.denied(
              () -> registry.requireRegistered(TENANT, "2", f.c().getNode(), "7", null, null),
              "network.local_registration_required");
          return null;
        });
  }

  @Test
  void nullOrDifferentTypeAndOpaqueConflictsStayDurablyIsolated() {
    for (String mutation : List.of("null", "type", "op", "fingerprint")) {
      var f = typed();
      f.ready().put("registration_revision", "0");
      pending(f, mutation.equals("type") ? "subagent" : "session");
      if (mutation.equals("op"))
        ((ObjectNode) f.ready().path("pending_registration")).put("allocation_op_id", "other");
      if (mutation.equals("fingerprint"))
        ((ObjectNode) f.ready().path("pending_registration"))
            .put("allocation_fingerprint", "sha256:" + "b".repeat(64));
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
      assertEquals("recovery_conflict", ready(f).path("status").asString());
      assertEquals("recovery_conflict", row(f).get("local_identity_status"));
      blocked(f);
      var next = reconnect(f);
      next.ready().putNull("pending_registration").put("registration_revision", "1");
      assertEquals("recovery_conflict", ready(next).path("status").asString());
      assertEquals(1, registrations(f));
    }
  }

  @Test
  void lowHighwaterOrRevisionIsIdentityRollback() {
    for (String field : List.of("local_high_water", "registration_revision")) {
      var f = typed();
      f.ready().put(field, "0");
      var result = ready(f);
      assertEquals("identity_rollback", result.path("status").asString());
      assertEquals("7", result.path("local_high_water").asString());
      assertEquals("identity_rollback", row(f).get("local_identity_status"));
      blocked(f);
    }
  }

  @Test
  void receiptCursorAndCheckpointRemainIndependentOfSuccessfulRegistrationRecovery() {
    for (String conflict : List.of("cursor", "checkpoint_seq", "checkpoint_revision", "evidence")) {
      var f = typed();
      if (conflict.equals("cursor")) f.ready().put("resume_cursor", "1");
      else if (conflict.equals("evidence"))
        f.ready()
            .putArray("evidence")
            .addObject()
            .put("delivery_id", "delivery")
            .put("mail_seq", "1")
            .put("recipient_id", "recipient")
            .put("message_digest", FINGERPRINT)
            .put("evidence_digest", FINGERPRINT)
            .put("accepted_by", "session");
      else ((ObjectNode) f.ready().path("receipt_checkpoint")).put(conflict, "1");
      var result = ready(f);
      assertEquals("recovery_conflict", result.path("status").asString());
      assertEquals("healthy", row(f).get("local_identity_status"));
      assertEquals("7", result.path("local_high_water").asString());
      blocked(f);
    }
  }

  @Test
  void exactReplayKeepsEpochAndDoesNotReconcileAnOldFrameAfterNewRegistrations() {
    var f = fresh();
    var first = ready(f);
    assertEquals(first.path("epoch"), ready(f).path("epoch"));
    register(f, "7", "0");
    assertEquals("7", ready(f).path("local_high_water").asString());
    assertEquals("healthy", row(f).get("local_identity_status"));
    var changed = new Fixture(f.c(), f.ready().deepCopy().put("request_id", "different"));
    call(
        () -> {
          NetworkAuthorityGrpcTest.denied(
              () -> wire.ready(changed.c(), bytes(changed.ready())), "network.conflict");
          return null;
        });
    var next = reconnect(f);
    next.ready().put("local_high_water", "7").put("registration_revision", "1");
    assertEquals("2", ready(next).path("epoch").asString());
    call(
        () -> {
          NetworkAuthorityGrpcTest.denied(
              () -> wire.ready(f.c(), bytes(f.ready())), "network.stale_epoch");
          return null;
        });
  }

  @Test
  void crashAfterPreparationLeavesBusinessClosedAndSameFrameCanResume() {
    var f = fresh();
    doThrow(new IllegalStateException("injected recovery crash"))
        .when(registry)
        .reconcileRegistration(eq(TENANT), anyString());
    assertThrows(IllegalStateException.class, () -> ready(f));
    assertEquals("reconciling", row(f).get("wire_status"));
    blocked(f);
    reset(registry);
    assertEquals("1", ready(f).path("epoch").asString());
    assertEquals("ready", row(f).get("wire_status"));
  }

  @Test
  void diagnosisSurvivesFailureAfterRecoveryAndBlocksCorrectedNewIngress() {
    var f = typed();
    f.ready().put("local_high_water", "0");
    doAnswer(
            i -> {
              i.callRealMethod();
              throw new IllegalStateException("injected after commit");
            })
        .when(registry)
        .reconcileRegistration(eq(TENANT), anyString());
    assertThrows(IllegalStateException.class, () -> ready(f));
    assertEquals("identity_rollback", row(f).get("local_identity_status"));
    assertEquals("reconciling", row(f).get("wire_status"));
    reset(registry);
    var next = reconnect(f);
    next.ready().put("local_high_water", "7");
    assertEquals("identity_rollback", ready(next).path("status").asString());
    blocked(next);
  }

  @Test
  void oldRecoveredResultCannotOpenANewerEpoch() throws Exception {
    var f = fresh();
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    doAnswer(
            i -> {
              var result = i.callRealMethod();
              if (i.getArgument(1, String.class)
                  .contains(f.ready().path("request_id").asString())) {
                entered.countDown();
                assertTrue(release.await(10, TimeUnit.SECONDS));
              }
              return result;
            })
        .when(registry)
        .reconcileRegistration(eq(TENANT), anyString());
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var old = executor.submit(() -> ready(f));
      try {
        assertTrue(entered.await(5, TimeUnit.SECONDS));
        assertEquals("reconciling", row(f).get("wire_status"));
        blocked(f);
        var next = reconnect(f);
        // Only pause the exact original frame, not this new ingress.
        reset(registry);
        assertEquals("2", ready(next).path("epoch").asString());
      } finally {
        release.countDown();
      }
      var failure = assertThrows(ExecutionException.class, () -> old.get(10, TimeUnit.SECONDS));
      assertEquals(
          "network.stale_epoch", ((AurigaException) failure.getCause()).errorCode().code());
    }
  }

  @Test
  void registrationVersionOrHealthChangeAfterRecoveryCannotOpenBusiness() {
    for (String mutation : List.of("registration", "health")) {
      var f = typed();
      doAnswer(
              i -> {
                var result = i.callRealMethod();
                if (mutation.equals("registration")) register(f, "8", "1");
                else update(f, "local_identity_status='recovery_conflict'");
                return result;
              })
          .when(registry)
          .reconcileRegistration(eq(TENANT), anyString());
      call(
          () -> {
            NetworkAuthorityGrpcTest.denied(
                () -> wire.ready(f.c(), bytes(f.ready())), "network.conflict");
            return null;
          });
      assertEquals("reconciling", row(f).get("wire_status"));
      blocked(f);
      reset(registry);
    }
  }

  @Test
  void expiredSourceOrDecisionAfterRecoveryCannotSignOrOpenBusiness() {
    for (Duration offset : List.of(Duration.ofSeconds(31), Duration.ofSeconds(61))) {
      var f = fresh();
      doAnswer(
              i -> {
                var result = i.callRealMethod();
                CLOCK.offset = offset;
                return result;
              })
          .when(registry)
          .reconcileRegistration(eq(TENANT), anyString());
      call(
          () -> {
            NetworkAuthorityGrpcTest.denied(
                () -> wire.ready(f.c(), bytes(f.ready())), "network.unauthorized");
            return null;
          });
      assertEquals("reconciling", row(f).get("wire_status"));
      CLOCK.offset = Duration.ZERO;
      blocked(f);
      reset(registry);
    }
  }

  @Test
  void iamDenialUnavailabilityAndExpiredBindingFailClosed() {
    for (String mode : List.of("denied", "unavailable", "expired")) {
      var f = fresh();
      peer.allowed = !mode.equals("denied");
      peer.unavailable = mode.equals("unavailable");
      peer.expired = mode.equals("expired");
      call(
          () -> {
            NetworkAuthorityGrpcTest.denied(
                () -> wire.ready(f.c(), bytes(f.ready())),
                mode.equals("unavailable") ? "common.unavailable" : "network.unauthorized");
            return null;
          });
      peer.allowed = true;
      peer.unavailable = peer.expired = false;
      assertEquals("ready", ready(f).path("status").asString());
    }
  }

  @Test
  void registrationIamDenialOrOutageAfterPreparationLeavesItsEpochClosed() {
    for (boolean unavailable : List.of(false, true)) {
      var f = fresh();
      var checks = new AtomicInteger();
      peer.onCheck =
          () -> {
            if (checks.incrementAndGet() == 3) {
              peer.allowed = unavailable;
              peer.unavailable = unavailable;
            }
          };
      call(
          () -> {
            NetworkAuthorityGrpcTest.denied(
                () -> wire.ready(f.c(), bytes(f.ready())),
                unavailable ? "common.unavailable" : "network.unauthorized");
            return null;
          });
      assertEquals("reconciling", row(f).get("wire_status"));
      assertEquals("healthy", row(f).get("local_identity_status"));
      assertEquals(1L, ((Number) row(f).get("connection_epoch")).longValue());
      peer.allowed = true;
      peer.unavailable = false;
      peer.onCheck = () -> {};
      blocked(f);
      assertEquals("ready", ready(f).path("status").asString());
      assertEquals("1", ready(f).path("epoch").asString());
    }
  }

  @Test
  void registryIamPhaseHoldsNeitherPoolConnectionNorNodeLock() throws Exception {
    var f = fresh();
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var checks = new AtomicInteger();
    peer.onCheck =
        () -> {
          // READY CONNECT+RECEIVE precede the registration CONNECT phase.
          if (checks.incrementAndGet() != 3) return;
          entered.countDown();
          try {
            assertTrue(release.await(10, TimeUnit.SECONDS));
          } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new AssertionError(e);
          }
        };
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var future = executor.submit(() -> ready(f));
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
        blocked(f);
      } finally {
        release.countDown();
      }
      assertEquals("ready", future.get(10, TimeUnit.SECONDS).path("status").asString());
    }
  }

  @Test
  void oldPreparedEpochCannotReconcileOrChangeNewBoot() {
    var f = fresh();
    var next = reconnect(f);
    var once = new java.util.concurrent.atomic.AtomicBoolean(true);
    doAnswer(
            i -> {
              if (once.getAndSet(false)) {
                assertEquals("reconciling", row(f).get("wire_status"));
                assertEquals("2", ready(next).path("epoch").asString());
              }
              return i.callRealMethod();
            })
        .when(registry)
        .reconcileRegistration(eq(TENANT), anyString());
    call(
        () -> {
          NetworkAuthorityGrpcTest.denied(
              () -> wire.ready(f.c(), bytes(f.ready())), "network.stale_epoch");
          return null;
        });
    assertEquals(next.ready().path("boot_id").asString(), row(f).get("local_boot_id"));
    assertEquals("ready", row(f).get("wire_status"));
    assertEquals("healthy", row(f).get("local_identity_status"));
  }

  @Test
  void laterRegistryIsolationClosesAnAlreadyReadySystemMailbox() {
    var f = fresh();
    ready(f);
    update(f, "local_identity_status='recovery_conflict'");
    call(
        () -> {
          NetworkAuthorityGrpcTest.denied(() -> wire.head(f.c()), "network.recovery_conflict");
          return null;
        });
    assertEquals("recovery_conflict", ready(f).path("status").asString());
  }

  @Test
  void inheritedTransactionFailsBeforePreparingOrContactingIam() {
    var f = fresh();
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s -> {
                      NetworkAuthorityGrpcTest.denied(
                          () -> wire.ready(f.c(), bytes(f.ready())), "common.unavailable");
                      return null;
                    }));
  }
}
