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
                    .setHostRequestId(d.getId())
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

  @Test
  void nonParticipantReplyCannotLockPeersOrDeadlockLegitimateReply() throws Exception {
    UUID tenant = UUID.randomUUID();
    var endpoints = new ArrayList<Actor>();
    for (int i = 0; i < 3; i++) endpoints.add(endpoint(actor(tenant)));
    endpoints.sort(Comparator.comparing(e -> UUID.fromString(e.getEndpointId())));
    Actor a = bind(endpoints.get(0), "a", "");
    Actor c = bind(endpoints.get(1), "c", "");
    Actor d = endpoints.get(2);
    Message cd = open(c, d, "cd", "cd");
    Message ac = open(a, c, "ac", "ac");
    final Actor legitimate = bind(c, "c", ac.getThreadId());
    CountDownLatch legalHasA = new CountDownLatch(1), illegalAdvanced = new CountDownLatch(1);
    var illegalPeerLocks = new java.util.concurrent.atomic.AtomicInteger();
    ThreadLocal<String> source = new ThreadLocal<>();
    CollabService controlled =
        new CollabService(
            jdbc,
            manager,
            (actor, operation) -> {
              if (operation == CollabAdmission.Operation.REPLY) source.set(actor.getEndpointId());
              if (operation != CollabAdmission.Operation.RECEIVE) return;
              if (legitimate.getEndpointId().equals(source.get())
                  && actor.getEndpointId().equals(a.getEndpointId())) {
                legalHasA.countDown();
                await(illegalAdvanced);
              } else if (a.getEndpointId().equals(source.get())) {
                if (illegalPeerLocks.incrementAndGet() == 2) illegalAdvanced.countDown();
              }
            });
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var legal = pool.submit(() -> replyAttempt(controlled, legitimate, ac.getThreadId()));
      assertTrue(legalHasA.await(10, TimeUnit.SECONDS));
      var illegal =
          pool.submit(
              () -> {
                try {
                  return replyAttempt(controlled, a, cd.getThreadId());
                } finally {
                  illegalAdvanced.countDown();
                }
              });
      Attempt good = legal.get(15, TimeUnit.SECONDS), denied = illegal.get(15, TimeUnit.SECONDS);
      assertAll(
          () -> assertNull(good.failure(), "legitimate reply: " + good.failure()),
          () ->
              assertEquals(
                  "common.not_found",
                  denied.failure(),
                  "nonparticipant must be rejected before peer locks"),
          () ->
              assertEquals(
                  0,
                  illegalPeerLocks.get(),
                  "nonparticipant reached RECEIVE while holding peer rows"));
      assertEquals(2, good.message().getSequence());
    } finally {
      illegalAdvanced.countDown();
    }
  }

  @Test
  void reusedHostRequestIdCannotFalselyAcceptAnotherDelivery() {
    Pair p = pair();
    reply(p.a(), p.first().getThreadId(), "second", "second");
    reply(p.a(), p.first().getThreadId(), "third", "third");
    Delivery first = pull(p.b()).getItems(0);
    // rc.2 hasPromptRequest returns accepted for an already seen session rpcId;
    // this models that documented dedup boundary, not a real DSH runtime test.
    Map<String, String> admitted = new HashMap<>();
    admitted.putIfAbsent(first.getId(), first.getMessage().getText());
    assertEquals("host_accepted", ackWithRequest(p.b(), first, first.getId()).getStatus());
    assertEquals("host_accepted", ackWithRequest(p.b(), first, first.getId()).getStatus());
    Delivery second = pull(p.b()).getItems(0);
    admitted.putIfAbsent(first.getId(), second.getMessage().getText());
    assertEquals(1, admitted.size());
    error("common.conflict", () -> ackWithRequest(p.b(), second, first.getId()));
    assertEquals(
        "leased",
        sql(
            p.b(),
            () ->
                jdbc.queryForObject(
                    "SELECT status FROM collab.delivery WHERE tenant_id=? AND id=?",
                    String.class,
                    UUID.fromString(p.b().getTenantId()),
                    UUID.fromString(second.getId()))));
    assertEquals(
        0,
        pull(p.b()).getItemsCount(),
        "third must remain blocked behind the unaccepted second message");
    admitted.putIfAbsent(second.getId(), second.getMessage().getText());
    assertEquals("host_accepted", ackWithRequest(p.b(), second, second.getId()).getStatus());
    assertEquals(2, admitted.size());
    assertEquals(3, pull(p.b()).getItems(0).getMessage().getSequence());
  }

  static Delivery ackWithRequest(Actor actor, Delivery delivery, String requestId) {
    return call(
        actor,
        () ->
            service.ackDelivery(
                AckDeliveryRequest.newBuilder()
                    .setActor(actor)
                    .setDeliveryId(delivery.getId())
                    .setLeaseToken(delivery.getLeaseToken())
                    .setHostRequestId(requestId)
                    .build()));
  }

  @Test
  void invalidOpenSourceAndReplyBindingNeverLockRecipientRows() {
    Pair p = pair();
    var receives = new java.util.concurrent.atomic.AtomicInteger();
    CollabService controlled =
        new CollabService(
            jdbc,
            manager,
            (actor, operation) -> {
              if (operation == CollabAdmission.Operation.RECEIVE) receives.incrementAndGet();
            });
    for (Actor invalid :
        List.of(
            p.a().toBuilder().setCredentialSubject(UUID.randomUUID().toString()).build(),
            p.a().toBuilder().setHostSessionId("forged-session").build())) {
      error(
          "common.forbidden",
          () ->
              call(
                  invalid,
                  () ->
                      controlled.openThread(
                          OpenThreadRequest.newBuilder()
                              .setActor(invalid)
                              .setTargetEndpointId(p.b().getEndpointId())
                              .setText("rejected")
                              .setIdempotencyKey("rejected")
                              .build())));
      error(
          "common.forbidden",
          () ->
              call(
                  invalid,
                  () ->
                      controlled.replyThread(
                          ReplyThreadRequest.newBuilder()
                              .setActor(invalid)
                              .setThreadId(p.first().getThreadId())
                              .setText("rejected")
                              .setIdempotencyKey("rejected")
                              .build())));
    }
    assertEquals(
        0,
        receives.get(),
        "credential/binding rejection must precede peer row locks and RECEIVE authority");
    assertEquals(
        1,
        sql(
            p.a(),
            () -> jdbc.queryForObject("SELECT count(*) FROM collab.message", Integer.class)));
  }

  @Test
  void canonicalDeliveryRequestIdentityAlsoFencesSameSessionAcrossThreads() {
    Pair p = pair();
    Delivery first = pull(p.b()).getItems(0);
    assertEquals("host_accepted", ackWithRequest(p.b(), first, first.getId()).getStatus());
    Message another = open(p.a(), p.b(), "another thread", "another thread");
    Actor sameSession = bind(p.b(), p.b().getHostSessionId(), another.getThreadId());
    assertEquals(p.b().getBindingId(), sameSession.getBindingId());
    Delivery second = pull(sameSession).getItems(0);
    error("common.conflict", () -> ackWithRequest(sameSession, second, first.getId()));
    error("common.conflict", () -> ackWithRequest(sameSession, second, "random-host-request"));
    sql(
        sameSession,
        () ->
            jdbc.update(
                "UPDATE collab.delivery SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=? AND id=?",
                UUID.fromString(sameSession.getTenantId()),
                UUID.fromString(second.getId())));
    error("common.precondition_failed", () -> ackWithRequest(sameSession, second, second.getId()));
    Delivery renewed = pull(sameSession).getItems(0);
    assertEquals(second.getId(), renewed.getId());
    error("common.precondition_failed", () -> ackWithRequest(sameSession, second, second.getId()));
    assertEquals(
        "host_accepted", ackWithRequest(sameSession, renewed, renewed.getId()).getStatus());
    assertEquals(
        "host_accepted", ackWithRequest(sameSession, renewed, renewed.getId()).getStatus());
    error("common.conflict", () -> ackWithRequest(sameSession, renewed, first.getId()));
    assertEquals(0, pull(sameSession).getItemsCount());
  }

  record Attempt(Message message, String failure) {}

  static Attempt replyAttempt(CollabService core, Actor actor, String thread) {
    try {
      return new Attempt(
          call(
              actor,
              () ->
                  core.replyThread(
                      ReplyThreadRequest.newBuilder()
                          .setActor(actor)
                          .setThreadId(thread)
                          .setText("reply")
                          .setIdempotencyKey("race")
                          .build())),
          null);
    } catch (RuntimeException error) {
      for (Throwable cause = error; cause != null; cause = cause.getCause()) {
        if (cause instanceof AurigaException business)
          return new Attempt(null, business.errorCode().code());
        if (cause instanceof java.sql.SQLException sql)
          return new Attempt(null, "SQLSTATE=" + sql.getSQLState());
      }
      throw error;
    }
  }

  static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(10, TimeUnit.SECONDS));
    } catch (InterruptedException error) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException(error);
    }
  }
}
