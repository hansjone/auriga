package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.CollabAdmission;
import auriga.collab.app.CollabService;
import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import auriga.contracts.collab.v1.*;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

@ExtendWith(PostgresExtension.class)
class CollabPostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static CollabService service;
  static PostgresExtension.ServiceRoles roles;
  static final AurigaPrincipal GATEWAY =
      new AurigaPrincipal(
          "svc:ai-gateway",
          TenantContext.PLATFORM_TENANT,
          PrincipalType.INTERNAL_SERVICE,
          TokenContext.SERVICE,
          null,
          null,
          null,
          List.of(),
          Set.of(),
          null,
          null,
          "test");

  @BeforeAll
  static void migrate() {
    roles =
        PostgresExtension.serviceRoles(
            CollabPostgresTest.class, "collab_owner", "collab_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
    service = new CollabService(jdbc, manager, (actor, operation) -> {});
  }

  static <T> T call(Actor a, Supplier<T> action) {
    return RequestContext.start(null, null)
        .withPrincipal(GATEWAY)
        .call(() -> TenantContext.call(UUID.fromString(a.getTenantId()), action::get));
  }

  static Actor actor(UUID tenant) {
    return Actor.newBuilder()
        .setTenantId(tenant.toString())
        .setAccountId(UUID.randomUUID().toString())
        .setCredentialKind("DEVICE")
        .setCredentialSubject(UUID.randomUUID().toString())
        .build();
  }

  static Actor endpoint(Actor a) {
    Endpoint e =
        call(
            a,
            () ->
                service.registerEndpoint(
                    RegisterEndpointRequest.newBuilder()
                        .setActor(a)
                        .setLabel("member")
                        .setReceiveEnabled(true)
                        .build()));
    return a.toBuilder().setEndpointId(e.getId()).build();
  }

  static Actor bind(Actor a, String session, String thread) {
    Actor input = a.toBuilder().setHostSessionId(session).build();
    SessionBinding b =
        call(
            input,
            () ->
                service.bindSession(
                    BindSessionRequest.newBuilder().setActor(input).setThreadId(thread).build()));
    return input.toBuilder().setBindingId(b.getId()).build();
  }

  record Pair(Actor a, Actor b, Message first) {}

  static Pair pair() {
    UUID tenant = UUID.randomUUID();
    Actor a = bind(endpoint(actor(tenant)), "headquarters-original", "");
    Actor b = endpoint(actor(tenant));
    Message m = open(a, b, "first", "open-key");
    b = bind(b, "member-real-session", m.getThreadId());
    return new Pair(a, b, m);
  }

  static Message open(Actor a, Actor b, String text, String key) {
    return call(
        a,
        () ->
            service.openThread(
                OpenThreadRequest.newBuilder()
                    .setActor(a)
                    .setTargetEndpointId(b.getEndpointId())
                    .setText(text)
                    .setIdempotencyKey(key)
                    .build()));
  }

  static Message reply(Actor a, String thread, String text, String key) {
    return call(
        a,
        () ->
            service.replyThread(
                ReplyThreadRequest.newBuilder()
                    .setActor(a)
                    .setThreadId(thread)
                    .setText(text)
                    .setIdempotencyKey(key)
                    .build()));
  }

  static DeliveryPage pull(Actor a) {
    return call(
        a,
        () ->
            service.pullDeliveries(
                PullDeliveriesRequest.newBuilder().setActor(a).setLimit(100).build()));
  }

  static Delivery ack(Actor a, Delivery d) {
    return call(
        a,
        () ->
            service.ackDelivery(
                AckDeliveryRequest.newBuilder()
                    .setActor(a)
                    .setDeliveryId(d.getId())
                    .setLeaseToken(d.getLeaseToken())
                    .setHostRequestId("host-" + d.getId())
                    .build()));
  }

  static void error(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  static <T> T sql(Actor a, Supplier<T> work) {
    return call(a, () -> new TransactionTemplate(manager).execute(s -> work.get()));
  }

  @Test
  void realPersistentRoundTripAndRuntimeRole() {
    Pair p = pair();
    Delivery d = pull(p.b()).getItems(0);
    assertEquals(p.first(), d.getMessage());
    assertEquals("leased", d.getStatus());
    assertEquals("host_accepted", ack(p.b(), d).getStatus());
    assertEquals("host_accepted", ack(p.b(), d).getStatus());
    Message answer = reply(p.b(), p.first().getThreadId(), "answer", "reply-key");
    assertEquals(2, answer.getSequence());
    CollabService restarted = new CollabService(jdbc, manager, (a, op) -> {});
    var history =
        call(
            p.a(),
            () ->
                restarted.readThread(
                    ReadThreadRequest.newBuilder()
                        .setActor(p.a())
                        .setThreadId(answer.getThreadId())
                        .setLimit(100)
                        .build()));
    assertEquals(
        List.of("first", "answer"), history.getItemsList().stream().map(Message::getText).toList());
    assertEquals("collab_app", jdbc.queryForObject("SELECT current_user", String.class));
    assertFalse(
        jdbc.queryForObject(
            "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user",
            Boolean.class));
    assertEquals(0, jdbc.queryForObject("SELECT count(*) FROM collab.message", Integer.class));
  }

  @Test
  void idempotencyAndConcurrentSequence() throws Exception {
    Pair p = pair();
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var tasks = new ArrayList<Callable<Message>>();
      for (int i = 0; i < 12; i++) tasks.add(() -> open(p.a(), p.b(), "first", "open-key"));
      for (var f : pool.invokeAll(tasks)) assertEquals(p.first().getId(), f.get().getId());
      error("common.conflict", () -> open(p.a(), p.b(), "different", "open-key"));
      tasks.clear();
      for (int i = 0; i < 12; i++) {
        final int n = i;
        tasks.add(() -> reply(p.a(), p.first().getThreadId(), "message-" + n, "key-" + n));
      }
      Set<Long> numbers = new TreeSet<>();
      for (var f : pool.invokeAll(tasks)) numbers.add(f.get().getSequence());
      assertEquals(12, numbers.size());
      assertEquals(2L, numbers.iterator().next());
      assertTrue(numbers.contains(13L));
    }
    assertEquals(
        13,
        sql(
            p.a(),
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.message WHERE tenant_id=?",
                    Integer.class,
                    UUID.fromString(p.a().getTenantId()))));
  }

  @Test
  void participantsSessionAndTenantFences() {
    Pair p = pair();
    Actor other = bind(endpoint(actor(UUID.fromString(p.a().getTenantId()))), "unrelated", "");
    error("common.not_found", () -> reply(other, p.first().getThreadId(), "intrusion", "x"));
    Actor spoof = p.a().toBuilder().setHostSessionId("forged-meta").build();
    error("common.forbidden", () -> reply(spoof, p.first().getThreadId(), "x", "x"));
    Actor second = bind(p.a(), "another-session", "");
    error("common.forbidden", () -> reply(second, p.first().getThreadId(), "x", "x"));
    Actor foreign = bind(endpoint(actor(UUID.randomUUID())), "foreign", "");
    error("common.not_found", () -> reply(foreign, p.first().getThreadId(), "x", "x"));
    assertThrows(
        org.springframework.dao.DataAccessException.class,
        () ->
            sql(
                p.a(),
                () ->
                    jdbc.update(
                        "INSERT INTO collab.thread(id,tenant_id) VALUES (?,?)",
                        UUID.randomUUID(),
                        UUID.randomUUID())));
    assertEquals(
        0,
        sql(
            foreign,
            () -> jdbc.queryForObject("SELECT count(*) FROM collab.message", Integer.class)));
    Delivery d = pull(p.b()).getItems(0);
    error("common.not_found", () -> ack(p.a(), d));
  }

  @Test
  void leaseOrderingReclaimAndRevocation() {
    Pair p = pair();
    reply(p.a(), p.first().getThreadId(), "second", "second");
    Delivery old = pull(p.b()).getItems(0);
    assertEquals(0, pull(p.b()).getItemsCount());
    sql(
        p.b(),
        () ->
            jdbc.update(
                "UPDATE collab.delivery SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=? AND id=?",
                UUID.fromString(p.b().getTenantId()),
                UUID.fromString(old.getId())));
    Delivery current = pull(p.b()).getItems(0);
    assertEquals(old.getId(), current.getId());
    assertNotEquals(old.getLeaseToken(), current.getLeaseToken());
    error("common.precondition_failed", () -> ack(p.b(), old));
    ack(p.b(), current);
    assertEquals(2, pull(p.b()).getItems(0).getMessage().getSequence());
    call(
        p.b(),
        () -> service.revokeEndpoint(RevokeEndpointRequest.newBuilder().setActor(p.b()).build()));
    error("common.forbidden", () -> pull(p.b()));
    error(
        "common.forbidden", () -> reply(p.a(), p.first().getThreadId(), "after revoke", "revoked"));
  }

  @Test
  void defaultAdmissionFailsClosedAndNoCallerCannotBypass() {
    Pair p = pair();
    CollabAdmission deny =
        (a, operation) -> {
          throw new AurigaException(CommonErrors.UNAVAILABLE);
        };
    CollabService closed = new CollabService(jdbc, manager, deny);
    error(
        "common.unavailable",
        () ->
            call(
                p.a(),
                () ->
                    closed.listEndpoints(
                        ListEndpointsRequest.newBuilder().setActor(p.a()).setLimit(10).build())));
    error(
        "common.unauthenticated",
        () ->
            service.pullDeliveries(
                PullDeliveriesRequest.newBuilder().setActor(p.a()).setLimit(10).build()));
    error(
        "common.forbidden",
        () ->
            RequestContext.start(null, null)
                .withPrincipal(GATEWAY)
                .call(
                    () ->
                        service.pullDeliveries(
                            PullDeliveriesRequest.newBuilder()
                                .setActor(p.a())
                                .setLimit(10)
                                .build())));
  }

  @Test
  void failedTransactionLeavesNoThreadOrDedup() {
    UUID tenant = UUID.randomUUID();
    Actor a = bind(endpoint(actor(tenant)), "source", "");
    Actor b = endpoint(actor(tenant));
    sql(
        a,
        () ->
            jdbc.update(
                "UPDATE collab.endpoint SET receive_enabled=false WHERE tenant_id=? AND id=?",
                tenant,
                UUID.fromString(b.getEndpointId())));
    error("common.forbidden", () -> open(a, b, "text", "key"));
    assertEquals(
        0, sql(a, () -> jdbc.queryForObject("SELECT count(*) FROM collab.thread", Integer.class)));
    assertEquals(
        0,
        sql(
            a,
            () -> jdbc.queryForObject("SELECT count(*) FROM collab.request_dedup", Integer.class)));
  }

  @Test
  void databaseFailureAfterThreadCreationRollsBackEverything() {
    UUID tenant = UUID.randomUUID();
    Actor a = bind(endpoint(actor(tenant)), "source", "");
    Actor b = endpoint(actor(tenant));
    JdbcTemplate owner = new JdbcTemplate(roles.owner().dataSource());
    owner.execute(
        "ALTER TABLE collab.message ADD CONSTRAINT injected_failure CHECK(text <> 'transaction-fault')");
    try {
      assertThrows(
          org.springframework.dao.DataAccessException.class,
          () -> open(a, b, "transaction-fault", "fault-key"));
      for (String table :
          List.of("thread", "thread_participant", "message", "delivery", "request_dedup")) {
        assertEquals(
            0,
            sql(
                a,
                () -> jdbc.queryForObject("SELECT count(*) FROM collab." + table, Integer.class)),
            table);
      }
    } finally {
      owner.execute("ALTER TABLE collab.message DROP CONSTRAINT injected_failure");
    }
    assertEquals(1, open(a, b, "recovered", "fault-key").getSequence());
  }

  @Test
  void endpointRevocationSerializesWithInFlightSend() throws Exception {
    Pair p = pair();
    CountDownLatch locked = new CountDownLatch(1), release = new CountDownLatch(1);
    CollabService controlled =
        new CollabService(
            jdbc,
            manager,
            (a, operation) -> {
              if (operation == CollabAdmission.Operation.RECEIVE) {
                locked.countDown();
                try {
                  assertTrue(release.await(10, TimeUnit.SECONDS));
                } catch (InterruptedException e) {
                  Thread.currentThread().interrupt();
                  throw new IllegalStateException(e);
                }
              }
            });
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      Future<Message> send =
          pool.submit(
              () ->
                  call(
                      p.a(),
                      () ->
                          controlled.replyThread(
                              ReplyThreadRequest.newBuilder()
                                  .setActor(p.a())
                                  .setThreadId(p.first().getThreadId())
                                  .setText("in-flight")
                                  .setIdempotencyKey("race")
                                  .build())));
      assertTrue(locked.await(10, TimeUnit.SECONDS));
      Future<Endpoint> revoked =
          pool.submit(
              () ->
                  call(
                      p.b(),
                      () ->
                          service.revokeEndpoint(
                              RevokeEndpointRequest.newBuilder().setActor(p.b()).build())));
      release.countDown();
      assertEquals(2, send.get(10, TimeUnit.SECONDS).getSequence());
      assertTrue(revoked.get(10, TimeUnit.SECONDS).getRevoked());
      error(
          "common.forbidden", () -> reply(p.a(), p.first().getThreadId(), "after-commit", "after"));
      error("common.forbidden", () -> pull(p.b()));
    } finally {
      release.countDown();
    }
  }

  @Test
  void compositeForeignKeysRejectCrossTenantAndWrongMessageThread() {
    Pair first = pair(), other = pair();
    assertThrows(
        org.springframework.dao.DataAccessException.class,
        () ->
            sql(
                first.a(),
                () ->
                    jdbc.update(
                        "INSERT INTO collab.thread_participant(id,tenant_id,thread_id,endpoint_id) VALUES (?,?,?,?)",
                        UUID.randomUUID(),
                        UUID.fromString(first.a().getTenantId()),
                        UUID.fromString(first.first().getThreadId()),
                        UUID.fromString(other.b().getEndpointId()))));
    Actor extra = endpoint(actor(UUID.fromString(first.a().getTenantId())));
    Message another = open(first.a(), extra, "another-thread", "another-thread-key");
    assertThrows(
        org.springframework.dao.DataAccessException.class,
        () ->
            sql(
                first.a(),
                () ->
                    jdbc.update(
                        "INSERT INTO collab.delivery(id,tenant_id,message_id,thread_id,target_endpoint_id) VALUES (?,?,?,?,?)",
                        UUID.randomUUID(),
                        UUID.fromString(first.a().getTenantId()),
                        UUID.fromString(first.first().getId()),
                        UUID.fromString(another.getThreadId()),
                        UUID.fromString(extra.getEndpointId()))));
  }

  @Test
  void directoryAndSendingRecheckReceiverAuthority() {
    Pair p = pair();
    CollabService restricted =
        new CollabService(
            jdbc,
            manager,
            (actor, operation) -> {
              if (operation == CollabAdmission.Operation.RECEIVE
                  && actor.getEndpointId().equals(p.b().getEndpointId()))
                throw new AurigaException(CommonErrors.FORBIDDEN);
            });
    var page =
        call(
            p.a(),
            () ->
                restricted.listEndpoints(
                    ListEndpointsRequest.newBuilder().setActor(p.a()).setLimit(100).build()));
    assertTrue(
        page.getItemsList().stream().noneMatch(e -> e.getId().equals(p.b().getEndpointId())));
    error(
        "common.forbidden",
        () ->
            call(
                p.a(),
                () ->
                    restricted.replyThread(
                        ReplyThreadRequest.newBuilder()
                            .setActor(p.a())
                            .setThreadId(p.first().getThreadId())
                            .setText("denied-recipient")
                            .setIdempotencyKey("denied-recipient")
                            .build())));
    assertEquals(
        1,
        sql(
            p.a(),
            () -> jdbc.queryForObject("SELECT count(*) FROM collab.message", Integer.class)));
  }
}
