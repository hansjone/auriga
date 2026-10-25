package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import com.zaxxer.hikari.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.security.KeyPairGenerator;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

/** Real PG row locks and a real delayed IAM gRPC peer; no JDBC/core probes or sleeps. */
@ExtendWith(PostgresExtension.class)
class NetworkWireLockPostgresTest {
  static final UUID TENANT = UUID.randomUUID();
  static HikariDataSource pool;
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static NetworkMailboxService core;
  static NetworkMailboxWireService wire;
  static Server server;
  static ManagedChannel channel;
  static final AtomicReference<Gate> pause = new AtomicReference<>();

  record Gate(String phase, CountDownLatch entered, CountDownLatch release) {
    Gate(String phase) {
      this(phase, new CountDownLatch(1), new CountDownLatch(1));
    }
  }

  record Item(UUID delivery, UUID recipient, ObjectNode message) {}

  @BeforeAll
  static void setup() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkWireLockPostgresTest.class, "wire_lock_owner", "wire_lock_app", "collab");
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
    server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(
                new NetworkAuthorityGrpcTest.Authority() {
                  @Override
                  public void checkNetwork(
                      CheckNetworkRequest r, StreamObserver<CheckNetworkResponse> o) {
                    waitFor("check");
                    super.checkNetwork(r, o);
                  }

                  @Override
                  public void authorizeNetworkDelivery(
                      AuthorizeNetworkDeliveryRequest r,
                      StreamObserver<AuthorizeNetworkDeliveryResponse> o) {
                    waitFor("delivery");
                    super.authorizeNetworkDelivery(r, o);
                  }
                })
            .build()
            .start();
    channel =
        GrpcChannels.open(
            "127.0.0.1:" + server.getPort(),
            new AurigaClientInterceptor(() -> "collab-test", Duration.ofSeconds(10)));
    var iam = new NetworkAuthorityClient(channel, Duration.ofSeconds(10));
    var source = new NetworkSourceGrpcAuthority(iam, manager);
    var keys = new NetworkSigningKeys(Clock.systemUTC(), 1);
    for (String purpose : List.of("protocol_response", "business_delivery")) {
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      keys.add(
          "2",
          purpose,
          "lock-" + purpose,
          pair.getPublic(),
          pair.getPrivate(),
          Instant.now().minusSeconds(60),
          Instant.now().plusSeconds(3600),
          NetworkSigningKeys.State.ACTIVE);
    }
    core = new NetworkMailboxService(jdbc, manager, source, keys, Clock.systemUTC(), iam);
    wire = new NetworkMailboxWireService(core, jdbc, manager, source, keys, Clock.systemUTC());
  }

  static void waitFor(String phase) {
    var gate = pause.get();
    if (gate == null || !gate.phase().equals(phase) || !pause.compareAndSet(gate, null)) return;
    gate.entered().countDown();
    try {
      if (!gate.release().await(15, TimeUnit.SECONDS))
        throw new IllegalStateException("test IAM gate not released");
    } catch (InterruptedException error) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException(error);
    }
  }

  @AfterAll
  static void close() throws Exception {
    var gate = pause.getAndSet(null);
    if (gate != null) gate.release().countDown();
    if (channel != null) channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (server != null) server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    if (pool != null) pool.close();
  }

  @Test
  void slowIamForEachRpcLeavesPoolAndRowsAvailableAndTakeoverFencesOldDecision() throws Exception {
    for (String method : List.of("ready", "head", "fetch", "receipt")) {
      String node =
          switch (method) {
            case "ready" -> "71";
            case "head" -> "72";
            case "fetch" -> "73";
            default -> "74";
          };
      var c = connection(node, "ingress-" + method);
      call(
          () ->
              wire.ready(
                  c,
                  NetworkMailboxWirePostgresTest.ready(
                      "boot-" + node, "nonce-" + node, "ready-" + node)));
      var item = seed(node);
      var gate = new Gate("check");
      pause.set(gate);
      try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
        var request = executor.submit(() -> call(() -> invoke(method, c, item)));
        try {
          assertTrue(gate.entered().await(5, TimeUnit.SECONDS));
          assertEquals(
              0,
              pool.getHikariPoolMXBean().getActiveConnections(),
              "slow IAM must not hold a collab connection");
          assertNowait(node, item.recipient());
          var next = c.toBuilder().setIngress("next-" + method).build();
          call(
              () ->
                  wire.ready(
                      next,
                      NetworkMailboxWirePostgresTest.ready(
                          "new-" + node, "new-nonce-" + node, "new-ready-" + node)));
        } finally {
          gate.release().countDown();
        }
        var error = assertThrows(ExecutionException.class, () -> request.get(10, TimeUnit.SECONDS));
        assertEquals(
            "network.stale_epoch", ((AurigaException) error.getCause()).errorCode().code());
      }
      assertUnchanged(item, node);
    }
  }

  @Test
  void perItemDeliveryAuthorizationAlsoHoldsNeitherPoolConnectionNorMailboxLock() throws Exception {
    var c = connection("75", "ingress-delivery");
    call(
        () ->
            wire.ready(
                c,
                NetworkMailboxWirePostgresTest.ready(
                    "boot-delivery", "nonce-delivery", "ready-delivery")));
    var item = seed("75");
    var gate = new Gate("delivery");
    pause.set(gate);
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var request = executor.submit(() -> call(() -> invoke("fetch", c, item)));
      try {
        assertTrue(gate.entered().await(5, TimeUnit.SECONDS));
        assertEquals(0, pool.getHikariPoolMXBean().getActiveConnections());
        assertNowait("75", item.recipient());
        call(
            () ->
                wire.ready(
                    c.toBuilder().setIngress("next-delivery").build(),
                    NetworkMailboxWirePostgresTest.ready("next-boot", "next-nonce", "next-ready")));
      } finally {
        gate.release().countDown();
      }
      var error = assertThrows(ExecutionException.class, () -> request.get(10, TimeUnit.SECONDS));
      assertEquals("network.stale_epoch", ((AurigaException) error.getCause()).errorCode().code());
    }
    assertUnchanged(item, "75");
  }

  @Test
  void inheritedTransactionsFailClosedBeforeRemoteCallsRatherThanSuspendingTheirLocks() {
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    status -> {
                      var c = connection("76", "outer-transaction");
                      NetworkAuthorityGrpcTest.denied(() -> wire.head(c), "common.unavailable");
                      NetworkAuthorityGrpcTest.denied(
                          () -> core.fetchPage(TENANT, "2", "76", 0, 1, 65536),
                          "common.unavailable");
                      return null;
                    }));
  }

  static String invoke(String method, NetworkAuthorityConnection c, Item item) {
    return switch (method) {
      case "ready" ->
          wire.ready(
              c.toBuilder().setIngress("old-attempt-" + c.getNode()).build(),
              NetworkMailboxWirePostgresTest.ready(
                  "attempt-boot", "attempt-nonce", "attempt-ready"));
      case "head" -> wire.head(c);
      case "fetch" ->
          wire.fetch(
              c,
              NetworkMailboxWirePostgresTest.bytes(
                  "{\"app_version\":\"1.0\",\"type\":\"fetch\",\"request_id\":\"fetch-lock\",\"epoch\":\"1\",\"resume_cursor\":\"0\",\"continuation\":null,\"max_items\":1,\"max_bytes\":65536}"));
      default ->
          wire.receipt(
              c,
              NetworkMailboxWirePostgresTest.bytes(
                  NetworkMailboxWireFrames.receipt(
                      c.getNode(), item.delivery(), item.recipient(), item.message())));
    };
  }

  static NetworkAuthorityConnection connection(String node, String ingress) {
    return NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
        .setNode(node)
        .setIngress(ingress)
        .build();
  }

  static Item seed(String node) {
    return call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    status -> {
                      UUID message = UUID.randomUUID(),
                          delivery = UUID.randomUUID(),
                          recipient = UUID.randomUUID();
                      var data = NetworkMailboxWirePostgresTest.storedMessage(message, node, false);
                      data.put("created_at", Instant.now().minusSeconds(60).toString())
                          .put("expires_at", Instant.now().plusSeconds(3600).toString());
                      var source =
                          NetworkJson.parse(
                              "{\"realm\":\"2\",\"node\":\""
                                  + data.path("from").path("node").asString()
                                  + "\",\"user\":\"alice\",\"message_digest\":\""
                                  + NetworkJson.digest(data)
                                  + "\",\"sender_kind\":\"agent\",\"display_label\":\"agent\"}");
                      jdbc.update(
                          "INSERT INTO collab.network_message(id,tenant_id,realm,kind,body,source_node,source_local,message_json,source_context_json) VALUES (?,?,'2','message','hello',?,?,?,?)",
                          message,
                          TENANT,
                          data.path("from").path("node").asString(),
                          data.path("from").path("local").asString(),
                          data.toString(),
                          source.toString());
                      jdbc.update(
                          "INSERT INTO collab.network_mail_item(id,tenant_id,message_id,target_realm,target_node,mail_seq) VALUES (?,?,?,'2',?,1)",
                          delivery,
                          TENANT,
                          message,
                          node);
                      jdbc.update(
                          "INSERT INTO collab.network_recipient(id,tenant_id,delivery_id,target_local) VALUES (?,?,?,'0')",
                          recipient,
                          TENANT,
                          delivery);
                      jdbc.update(
                          "UPDATE collab.network_node SET head_seq=1 WHERE tenant_id=? AND node_id=?",
                          TENANT,
                          node);
                      return new Item(delivery, recipient, data);
                    }));
  }

  static void assertNowait(String node, UUID recipient) {
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    status -> {
                      assertEquals(
                          1,
                          jdbc.queryForList(
                                  "SELECT node_id FROM collab.network_node WHERE tenant_id=? AND realm='2' AND node_id=? FOR UPDATE NOWAIT",
                                  TENANT,
                                  node)
                              .size());
                      assertEquals(
                          1,
                          jdbc.queryForList(
                                  "SELECT id FROM collab.network_recipient WHERE tenant_id=? AND id=? FOR UPDATE NOWAIT",
                                  TENANT,
                                  recipient)
                              .size());
                      return null;
                    }));
  }

  static void assertUnchanged(Item item, String node) {
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    status -> {
                      assertEquals(
                          "pending",
                          jdbc.queryForObject(
                              "SELECT transport_status FROM collab.network_recipient WHERE tenant_id=? AND id=?",
                              String.class,
                              TENANT,
                              item.recipient()));
                      assertEquals(
                          0,
                          jdbc.queryForObject(
                              "SELECT count(*) FROM collab.network_acceptance_proof WHERE tenant_id=? AND recipient_id=?",
                              Integer.class,
                              TENANT,
                              item.recipient()));
                      assertEquals(
                          0,
                          jdbc.queryForObject(
                              "SELECT committed_cursor FROM collab.network_node WHERE tenant_id=? AND node_id=?",
                              Integer.class,
                              TENANT,
                              node));
                      return null;
                    }));
  }

  static <T> T call(Supplier<T> action) {
    return RequestContext.start(null, null)
        .withPrincipal(NetworkMailboxGrpcTest.principal("svc:ai-gateway"))
        .call(() -> TenantContext.call(TENANT, action::get));
  }
}
