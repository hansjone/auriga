package auriga.common.events;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.MissingTenantException;
import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.observability.EventMetrics;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.testing.MutableClock;
import auriga.testsupport.PostgresExtension;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

@ExtendWith(PostgresExtension.class)
class OutboxTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final UUID TENANT = UUID.randomUUID();
  private static PostgresExtension.ServiceRole role;
  private static JdbcTemplate jdbc;
  private static TransactionTemplate tx;
  private static JdbcTemplate otherJdbc;
  private static TransactionTemplate otherTx;

  private MutableClock clock;
  private SimpleMeterRegistry registry;
  private EventMetrics metrics;

  @BeforeAll
  static void schema() {
    role = PostgresExtension.serviceRole(OutboxTest.class, "obx_app", "obx");
    CommonMigrations.migrate(role.dataSource(), "obx");
    jdbc = new JdbcTemplate(role.dataSource());
    tx = new TransactionTemplate(new TenantTransactionManager(role.dataSource()));
    var otherRole = PostgresExtension.serviceRole(OutboxTest.class, "obx_other_app", "obx_other");
    CommonMigrations.migrate(otherRole.dataSource(), "obx_other");
    otherJdbc = new JdbcTemplate(otherRole.dataSource());
    otherTx = new TransactionTemplate(new TenantTransactionManager(otherRole.dataSource()));
  }

  @BeforeEach
  void clean() {
    jdbc.update("DELETE FROM obx.outbox_event");
    otherJdbc.update("DELETE FROM obx_other.outbox_event");
    clock = new MutableClock(Instant.parse("2026-10-19T08:00:00Z"));
    registry = new SimpleMeterRegistry();
    metrics = new EventMetrics(registry, "obx");
  }

  private OutboxEventPublisher publisher(boolean validate) {
    return new OutboxEventPublisher(
        jdbc, "obx", "org", validate ? new EventSchemas() : null, JSON, clock, null);
  }

  private OutboxForwarder forwarder(EventTransport transport) {
    return new OutboxForwarder(
        role.dataSource(),
        "obx",
        transport,
        metrics,
        clock,
        new OutboxForwarder.Settings(
            10,
            Duration.ofMillis(50),
            Duration.ofSeconds(1),
            Duration.ofSeconds(60),
            Duration.ofDays(7)));
  }

  private UUID publish(OutboxEventPublisher publisher, String type, String subject, Object data) {
    return TenantContext.call(
        TENANT, () -> tx.execute(s -> publisher.publish(type, subject, data)));
  }

  private long outboxRows() {
    return jdbc.queryForObject("SELECT count(*) FROM obx.outbox_event", Long.class);
  }

  @Test
  void writesValidCloudEventInTheBusinessTransaction() {
    OutboxEventPublisher publisher = publisher(true);
    RequestContext request =
        RequestContext.start("req-1", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01");
    UUID id =
        request.call(
            () -> publish(publisher, "user.created", "user-1", EventSchemasTest.userCreated()));
    assertEquals(7, id.version());
    Map<String, Object> row = jdbc.queryForMap("SELECT * FROM obx.outbox_event WHERE id = ?", id);
    assertEquals("auriga.evt.user.created", row.get("nats_subject"));
    assertEquals("user-1", row.get("subject_key"));
    assertEquals(TENANT, row.get("tenant_id"));
    JsonNode envelope = JSON.readTree((String) row.get("envelope"));
    assertEquals("auriga/org", envelope.get("source").asString());
    assertEquals(TENANT.toString(), envelope.get("tenantid").asString());
    assertEquals("1", envelope.get("dataversion").asString());
    assertTrue(envelope.get("traceparent").asString().contains("4bf92f3577b34da6a3ce929d0e0e4736"));
    assertDoesNotThrow(() -> new EventSchemas().validate(envelope));
  }

  @Test
  void rollbackDiscardsTheEvent() {
    OutboxEventPublisher publisher = publisher(false);
    assertThrows(
        IllegalStateException.class,
        () ->
            TenantContext.run(
                TENANT,
                () ->
                    tx.executeWithoutResult(
                        s -> {
                          publisher.publish("test.happened", "s", Map.of("n", 1));
                          throw new IllegalStateException("business failure");
                        })));
    assertEquals(0, outboxRows());
    List<String> sent = new ArrayList<>();
    assertEquals(0, forwarder((subject, id, payload) -> sent.add(id)).forwardOnce());
    assertTrue(sent.isEmpty());
  }

  @Test
  void requiresTransactionAndTenant() {
    OutboxEventPublisher publisher = publisher(false);
    assertThrows(
        IllegalStateException.class,
        () -> TenantContext.run(TENANT, () -> publisher.publish("test.happened", "s", Map.of())));
    assertThrows(
        MissingTenantException.class,
        () -> tx.executeWithoutResult(s -> publisher.publish("test.happened", "s", Map.of())));
    // explicit system scope publishes a platform event
    TenantContext.runAsSystem(
        () -> tx.executeWithoutResult(s -> publisher.publish("test.happened", "s", Map.of())));
    assertEquals(
        TenantContext.PLATFORM_TENANT,
        jdbc.queryForObject("SELECT tenant_id FROM obx.outbox_event", UUID.class));
  }

  @Test
  void invalidEventIsRejectedAndRolledBack() {
    OutboxEventPublisher publisher = publisher(true);
    assertThrows(
        InvalidEventException.class,
        () -> publish(publisher, "user.created", "u", Map.of("via", "admin")));
    assertThrows(
        InvalidEventException.class, () -> publish(publisher, "nosuch.type", "u", Map.of()));
    assertEquals(0, outboxRows());
  }

  @Test
  void auditGoesToAuditSubject() {
    OutboxEventPublisher publisher = publisher(false);
    publish(publisher, "audit.recorded", "x", Map.of("action", "org.member.disable"));
    assertEquals(
        "auriga.audit.org",
        jdbc.queryForObject("SELECT nats_subject FROM obx.outbox_event", String.class));
  }

  @Test
  void forwardsWithMessageIdAndMarksPublished() {
    OutboxEventPublisher publisher = publisher(false);
    UUID id = publish(publisher, "test.happened", "s1", Map.of("n", 1));
    Map<String, String> sent = new HashMap<>();
    OutboxForwarder forwarder = forwarder((subject, msgId, payload) -> sent.put(msgId, subject));
    forwarder.refreshBacklog();
    assertEquals(1, metrics.backlog());
    clock.advance(Duration.ofMillis(250));
    assertEquals(1, forwarder.forwardOnce());
    assertEquals(Map.of(id.toString(), "auriga.evt.test.happened"), sent);
    assertNotNull(jdbc.queryForObject("SELECT published_at FROM obx.outbox_event", Object.class));
    assertEquals(0, forwarder.forwardOnce());
    assertEquals(0, metrics.backlog());
    assertEquals(1, registry.get("auriga.outbox.forward.latency").timer().count());
    assertEquals(
        250,
        registry
            .get("auriga.outbox.forward.latency")
            .timer()
            .totalTime(java.util.concurrent.TimeUnit.MILLISECONDS),
        1);
  }

  @Test
  void failuresBackOffAndKeepPerSubjectOrder() {
    OutboxEventPublisher publisher = publisher(false);
    UUID first = publish(publisher, "test.happened", "s1", Map.of("n", 1));
    UUID second = publish(publisher, "test.happened", "s1", Map.of("n", 2));
    UUID other = publish(publisher, "test.happened", "s2", Map.of("n", 3));
    AtomicInteger failuresLeft = new AtomicInteger(2);
    List<String> sent = Collections.synchronizedList(new ArrayList<>());
    OutboxForwarder forwarder =
        forwarder(
            (subject, msgId, payload) -> {
              if (msgId.equals(first.toString()) && failuresLeft.getAndDecrement() > 0) {
                throw new IllegalStateException("broker down");
              }
              sent.add(msgId);
            });
    assertEquals(1, forwarder.forwardOnce());
    assertEquals(List.of(other.toString()), sent);
    Map<String, Object> row =
        jdbc.queryForMap(
            "SELECT attempts, next_attempt_at, last_error FROM obx.outbox_event WHERE id = ?",
            first);
    assertEquals(1, row.get("attempts"));
    assertEquals(
        clock.instant().plusSeconds(1),
        ((java.sql.Timestamp) row.get("next_attempt_at")).toInstant());
    assertTrue(((String) row.get("last_error")).contains("broker down"));
    assertEquals(1.0, registry.get("auriga.outbox.publish.failures").counter().count());

    // not due yet: nothing happens, second event of s1 stays behind the first
    assertEquals(0, forwarder.forwardOnce());
    clock.advance(Duration.ofSeconds(1));
    assertEquals(0, forwarder.forwardOnce()); // second failure, backoff now 2s
    assertEquals(
        2,
        jdbc.queryForObject(
            "SELECT attempts FROM obx.outbox_event WHERE id = ?", Integer.class, first));
    clock.advance(Duration.ofSeconds(2));
    assertEquals(1, forwarder.forwardOnce());
    assertEquals(1, forwarder.forwardOnce());
    assertEquals(List.of(other.toString(), first.toString(), second.toString()), sent);
  }

  @Test
  void backoffIsExponentialAndCapped() {
    OutboxForwarder forwarder = forwarder((s, i, p) -> {});
    assertEquals(Duration.ofSeconds(1), forwarder.backoff(1));
    assertEquals(Duration.ofSeconds(8), forwarder.backoff(4));
    assertEquals(Duration.ofSeconds(60), forwarder.backoff(10));
    assertEquals(Duration.ofSeconds(60), forwarder.backoff(1000));
  }

  @Test
  void concurrentForwardersNeverDuplicateAndKeepSubjectOrder() throws Exception {
    OutboxEventPublisher publisher = publisher(false);
    Map<String, List<String>> expected = new HashMap<>();
    for (int i = 0; i < 200; i++) {
      String subject = "subject-" + (i % 20);
      UUID id = publish(publisher, "test.happened", subject, Map.of("n", i));
      expected.computeIfAbsent(subject, k -> new ArrayList<>()).add(id.toString());
    }
    Map<String, List<String>> delivered = new ConcurrentHashMap<>();
    Set<String> all = ConcurrentHashMap.newKeySet();
    AtomicInteger duplicates = new AtomicInteger();
    EventTransport transport =
        (subject, msgId, payload) -> {
          String s = JSON.readTree(payload).get("subject").asString();
          if (!all.add(msgId)) duplicates.incrementAndGet();
          delivered
              .computeIfAbsent(s, k -> Collections.synchronizedList(new ArrayList<>()))
              .add(msgId);
          Thread.sleep(1);
        };
    int replicas = 4;
    CountDownLatch start = new CountDownLatch(1);
    try (var pool = Executors.newFixedThreadPool(replicas)) {
      List<Future<?>> futures = new ArrayList<>();
      for (int r = 0; r < replicas; r++) {
        OutboxForwarder f = forwarder(transport);
        futures.add(
            pool.submit(
                () -> {
                  start.await();
                  long deadline = System.nanoTime() + Duration.ofSeconds(60).toNanos();
                  while (all.size() < 200 && System.nanoTime() < deadline) f.forwardOnce();
                  return null;
                }));
      }
      start.countDown();
      for (Future<?> f : futures) f.get();
    }
    assertEquals(0, duplicates.get());
    assertEquals(200, all.size());
    assertEquals(expected, new HashMap<>(delivered));
    assertEquals(
        0L,
        jdbc.queryForObject(
            "SELECT count(*) FROM obx.outbox_event WHERE published_at IS NULL", Long.class));
    assertEquals(new HashSet<>(all).size(), 200);
  }

  @Test
  void backgroundLoopDeliversAfterWakeUpAndStopsCleanly() throws Exception {
    List<String> sent = Collections.synchronizedList(new ArrayList<>());
    OutboxForwarder forwarder = forwarder((s, id, p) -> sent.add(id));
    OutboxEventPublisher publisher =
        new OutboxEventPublisher(jdbc, "obx", "org", null, JSON, clock, forwarder::wakeUp);
    forwarder.start();
    try {
      UUID id = publish(publisher, "test.happened", "s", Map.of("n", 1));
      long deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos();
      while (sent.isEmpty() && System.nanoTime() < deadline) Thread.sleep(10);
      assertEquals(List.of(id.toString()), sent);
    } finally {
      forwarder.stop();
    }
    assertFalse(forwarder.isRunning());
  }

  @Test
  void purgesOldPublishedRows() {
    OutboxEventPublisher publisher = publisher(false);
    publish(publisher, "test.happened", "s", Map.of());
    OutboxForwarder forwarder = forwarder((s, i, p) -> {});
    forwarder.forwardOnce();
    assertEquals(0, forwarder.purgePublished());
    clock.advance(Duration.ofDays(8));
    assertEquals(1, forwarder.purgePublished());
  }

  @Test
  void switchingTenantInsideATransactionFailsAndRequiresNewWorks() {
    OutboxEventPublisher publisher = publisher(false);
    UUID other = UUID.randomUUID();
    assertThrows(
        TenantTransactionManager.TenantMismatchException.class,
        () ->
            TenantContext.run(
                TENANT,
                () ->
                    tx.executeWithoutResult(
                        s ->
                            TenantContext.run(
                                other, () -> publisher.publish("test.happened", "s", Map.of())))));
    // the tenant line handler refuses as well
    var handler = new auriga.common.persistence.AurigaTenantLineHandler(Set.of());
    assertThrows(
        TenantTransactionManager.TenantMismatchException.class,
        () ->
            TenantContext.run(
                TENANT,
                () ->
                    tx.executeWithoutResult(s -> TenantContext.run(other, handler::getTenantId))));
    assertThrows(
        TenantTransactionManager.TenantMismatchException.class,
        () ->
            TenantContext.run(
                TENANT,
                () ->
                    tx.executeWithoutResult(
                        s -> TenantContext.runAsSystem(() -> handler.ignoreTable("member")))));
    assertEquals(0, outboxRows());
    // a new transaction for the other tenant is the supported way; the outer one keeps its tenant
    TransactionTemplate requiresNew =
        new TransactionTemplate(new TenantTransactionManager(role.dataSource()));
    requiresNew.setPropagationBehavior(TransactionTemplate.PROPAGATION_REQUIRES_NEW);
    TenantContext.run(
        TENANT,
        () ->
            tx.executeWithoutResult(
                s -> {
                  publisher.publish("test.happened", "outer-1", Map.of());
                  TenantContext.run(
                      other,
                      () ->
                          requiresNew.executeWithoutResult(
                              s2 -> publisher.publish("test.happened", "inner", Map.of())));
                  publisher.publish("test.happened", "outer-2", Map.of());
                }));
    assertEquals(
        Map.of("outer-1", TENANT, "inner", other, "outer-2", TENANT),
        Map.copyOf(
            jdbc.query(
                "SELECT subject_key, tenant_id FROM obx.outbox_event",
                rs -> {
                  Map<String, UUID> m = new HashMap<>();
                  while (rs.next()) m.put(rs.getString(1), rs.getObject(2, UUID.class));
                  return m;
                })));
  }

  @Test
  void nestedPublishersInDifferentSchemasDoNotWaitForTheirCaller() {
    var outer = publisher(false);
    var inner = new OutboxEventPublisher(otherJdbc, "obx_other", "org", null, JSON, clock, null);
    assertDoesNotThrow(
        () ->
            TenantContext.run(
                TENANT,
                () ->
                    tx.executeWithoutResult(
                        outerStatus -> {
                          outer.publish("test.happened", "same-account", Map.of("n", 1));
                          otherTx.executeWithoutResult(
                              innerStatus -> {
                                otherJdbc.execute("SET LOCAL lock_timeout = '1s'");
                                inner.publish("test.happened", "same-account", Map.of("n", 2));
                              });
                          outer.publish("test.happened", "same-account", Map.of("n", 3));
                        })));
    assertEquals(
        List.of(1, 3),
        jdbc.queryForList(
            "SELECT (envelope::jsonb -> 'data' ->> 'n')::int FROM obx.outbox_event ORDER BY seq",
            Integer.class));
    assertEquals(
        List.of(2),
        otherJdbc.queryForList(
            "SELECT (envelope::jsonb -> 'data' ->> 'n')::int FROM obx_other.outbox_event ORDER BY seq",
            Integer.class));
    assertEquals(
        "same-account",
        otherJdbc.queryForObject("SELECT subject_key FROM obx_other.outbox_event", String.class));
  }

  @Test
  void sameSubjectPublishersAreSerializedUntilCommit() throws Exception {
    OutboxEventPublisher publisher = publisher(false);
    var secondPublisher =
        new OutboxEventPublisher(jdbc, "obx", "workspace", null, JSON, clock, null);
    CountDownLatch firstPublished = new CountDownLatch(1);
    CountDownLatch releaseFirst = new CountDownLatch(1);
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      Future<?> first =
          pool.submit(
              () ->
                  publish(
                      publisher,
                      () -> {
                        publisher.publish("test.happened", "order-1", Map.of("n", 1));
                        firstPublished.countDown();
                        await(releaseFirst);
                      }));
      assertTrue(firstPublished.await(10, java.util.concurrent.TimeUnit.SECONDS));
      Future<?> second =
          pool.submit(
              () ->
                  publish(
                      publisher,
                      () -> secondPublisher.publish("test.happened", "order-1", Map.of("n", 2))));
      // a different subject is not blocked
      pool.submit(
              () ->
                  publish(
                      publisher,
                      () -> publisher.publish("test.happened", "order-2", Map.of("n", 3))))
          .get(10, java.util.concurrent.TimeUnit.SECONDS);
      Thread.sleep(300);
      assertFalse(second.isDone(), "same subject must wait for the first transaction to commit");
      releaseFirst.countDown();
      first.get(10, java.util.concurrent.TimeUnit.SECONDS);
      second.get(10, java.util.concurrent.TimeUnit.SECONDS);
    }
    List<Integer> order =
        jdbc.queryForList(
            "SELECT (envelope::jsonb #>> '{data,n}')::int FROM obx.outbox_event"
                + " WHERE subject_key = 'order-1' ORDER BY seq",
            Integer.class);
    assertEquals(List.of(1, 2), order);
  }

  private static void publish(OutboxEventPublisher publisher, Runnable body) {
    TenantContext.run(TENANT, () -> tx.executeWithoutResult(s -> body.run()));
  }

  private static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(10, java.util.concurrent.TimeUnit.SECONDS));
    } catch (InterruptedException e) {
      throw new IllegalStateException(e);
    }
  }
}
