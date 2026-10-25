package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.NetworkSourceAuthority.Source;
import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.NetworkAuthority.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

@ExtendWith(PostgresExtension.class)
class NetworkContactBudgetPostgresTest {
  static JdbcTemplate jdbc;
  static TransactionTemplate tx;
  static final Instant START = Instant.parse("2026-10-01T00:00:00Z");
  UUID tenant = UUID.randomUUID();
  Instant now = START;
  Decision limits = new Decision(true, "allowed", Map.of());

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkContactBudgetPostgresTest.class, "budget_owner", "budget_app", "collab");
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
    tx = new TransactionTemplate(new TenantTransactionManager(roles.runtime().dataSource()));
  }

  <T> T run(Supplier<T> work) {
    return TenantContext.call(tenant, () -> tx.execute(s -> work.get()));
  }

  Source source(String node, String local, String user) {
    return new Source(
        "2",
        node,
        local,
        "session",
        "agent",
        user,
        "test",
        "1",
        now,
        "binding",
        "1",
        "1",
        now.plusSeconds(600));
  }

  NetworkContactBudget.Reservation reserve(
      String node, String local, String user, String target, String key) {
    return run(
        () ->
            new NetworkContactBudget(jdbc)
                .reserve(
                    tenant,
                    source(node, local, user),
                    target,
                    "1",
                    key,
                    "digest-" + key,
                    now,
                    now.plusSeconds(60),
                    limits));
  }

  @Test
  void retryAndDifferentBodyDoNotConsumeAnotherReservation() {
    var first = reserve("10", "1", "alice", "20", "same");
    assertNull(first.error());
    var retry = reserve("10", "1", "alice", "20", "same");
    assertEquals(first.id(), retry.id());
    assertTrue(retry.retry());
    var conflict =
        run(
            () ->
                new NetworkContactBudget(jdbc)
                    .reserve(
                        tenant,
                        source("10", "1", "alice"),
                        "20",
                        "1",
                        "same",
                        "different",
                        now,
                        now.plusSeconds(60),
                        limits));
    assertEquals("network.conflict", conflict.error().code());
    assertEquals(
        1,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_contact_submission", Integer.class)));
  }

  @Test
  void localDailyLimitRetainsTargetsAfterVisiblePendingLifetime() {
    for (int i = 0; i < 40; i++) {
      now = START.plusSeconds(i * 180);
      assertNull(reserve("10", "1", "alice", "" + (100 + i), "k" + i).error());
    }
    now = START.plusSeconds(8000);
    assertEquals(
        "network.quota_exceeded", reserve("10", "1", "alice", "900", "over").error().code());
    now = START.plusSeconds(86401);
    assertNull(reserve("10", "1", "alice", "900", "after").error());
  }

  @Test
  void nodeFreezesAt80AndRequiresBothMinimumHourAndVolumeDrop() {
    for (int i = 0; i < 80; i++) {
      now = START.plusSeconds(i * 180);
      assertNull(reserve("10", "" + (i % 4 + 1), "alice", "" + (100 + i), "k" + i).error());
    }
    now = now.plusSeconds(3601);
    assertEquals(
        "network.quota_exceeded", reserve("10", "5", "alice", "900", "frozen").error().code());
    now = START.plusSeconds(86401);
    assertNull(reserve("10", "5", "alice", "900", "thawed").error());
  }

  @Test
  void userBudgetAggregatesNodesAndFreezesAt160() {
    for (int i = 0; i < 160; i++) {
      now = START.plusSeconds(i * 120);
      assertNull(
          reserve("" + (10 + i % 4), "" + (1 + i % 8), "alice", "" + (100 + i), "k" + i).error());
    }
    now = now.plusSeconds(3601);
    assertEquals(
        "network.quota_exceeded", reserve("99", "1", "alice", "900", "over").error().code());
    assertNull(reserve("99", "1", "bob", "900", "other-user").error());
  }

  @Test
  void organizationMayTightenEachDailyDimension() {
    for (var limit :
        List.of(
            Limit.FIRST_LOCAL_TARGETS_DAY,
            Limit.FIRST_NODE_TARGETS_DAY,
            Limit.FIRST_USER_TARGETS_DAY)) {
      tenant = UUID.randomUUID();
      limits = new Decision(true, "allowed", Map.of(limit, 1L));
      assertNull(reserve("10", "1", "alice", "20", "one").error());
      now = now.plusSeconds(180);
      assertEquals(
          "network.quota_exceeded",
          reserve("10", limit == Limit.FIRST_LOCAL_TARGETS_DAY ? "1" : "2", "alice", "21", "two")
              .error()
              .code());
    }
  }

  @Test
  void atomicPendingCeilingAcrossConcurrentLocalsAndRls() throws Exception {
    limits = new Decision(true, "allowed", Map.of(Limit.FIRST_USER_PENDING, 1L));
    assertEquals(
        1, parallel(i -> reserve("" + (10 + i), "" + (1 + i), "alice", "" + (20 + i), "p" + i)));
    tenant = UUID.randomUUID();
    assertEquals(
        0,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_contact_submission", Integer.class)));
  }

  long parallel(java.util.function.IntFunction<NetworkContactBudget.Reservation> work)
      throws Exception {
    var barrier = new CyclicBarrier(8);
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var jobs = new ArrayList<Future<NetworkContactBudget.Reservation>>();
      for (int i = 0; i < 8; i++) {
        int index = i;
        jobs.add(
            pool.submit(
                () -> {
                  barrier.await(30, TimeUnit.SECONDS);
                  return work.apply(index);
                }));
      }
      long accepted = 0;
      for (var job : jobs) if (job.get(30, TimeUnit.SECONDS).error() == null) accepted++;
      return accepted;
    }
  }

  @Test
  void concurrentLocalAndNodeDailyLimitsHaveOnlyOneRemainingSlot() throws Exception {
    for (boolean local : List.of(true, false)) {
      tenant = UUID.randomUUID();
      limits =
          new Decision(
              true, "allowed", local ? Map.of() : Map.of(Limit.FIRST_NODE_TARGETS_DAY, 40L));
      for (int i = 0; i < 39; i++) {
        now = START.plusSeconds(i * 180);
        assertNull(
            reserve("10", local ? "1" : "" + (i % 4 + 1), "alice", "" + (100 + i), "seed" + i)
                .error());
      }
      now = START.plusSeconds(39 * 180);
      assertEquals(
          1,
          parallel(
              i -> reserve("10", local ? "1" : "" + (i + 5), "alice", "" + (900 + i), "race" + i)));
      assertEquals(
          40,
          run(
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM collab.network_contact_submission", Integer.class)));
    }
  }

  @Test
  void concurrent80thTargetFreezesOtherThreadsAtomically() throws Exception {
    for (int i = 0; i < 79; i++) {
      now = START.plusSeconds(i * 180);
      assertNull(reserve("10", "" + (i % 4 + 1), "alice", "" + (100 + i), "seed" + i).error());
    }
    now = START.plusSeconds(79 * 180);
    assertEquals(
        1, parallel(i -> reserve("10", "" + (i + 5), "alice", "" + (900 + i), "race" + i)));
    assertEquals(
        80,
        run(
            () ->
                jdbc.queryForObject(
                    "SELECT count(DISTINCT target_node) FROM collab.network_contact_submission",
                    Integer.class)));
    assertEquals(
        now.plusSeconds(3600),
        run(() ->
                jdbc.queryForObject(
                    "SELECT frozen_until FROM collab.network_contact_budget WHERE scope='node' AND subject='10'",
                    java.sql.Timestamp.class))
            .toInstant());
  }

  @Test
  void unavailableResultsRemainRetryableAndOtherErrorCodesRoundTripExactly() {
    var reservation = reserve("10", "1", "alice", "20", "key");
    run(
        () -> {
          new NetworkContactBudget(jdbc)
              .complete(
                  tenant, reservation.id(), null, auriga.common.error.CommonErrors.UNAVAILABLE);
          return null;
        });
    var retry = reserve("10", "1", "alice", "20", "key");
    assertNull(retry.error());
    assertNull(retry.resultJson());
    var error =
        auriga.common.error.ErrorCode.of(
            "network.contact_expired", 410, io.grpc.Status.Code.FAILED_PRECONDITION);
    run(
        () -> {
          new NetworkContactBudget(jdbc).complete(tenant, reservation.id(), null, error);
          return null;
        });
    assertEquals(error, reserve("10", "1", "alice", "20", "key").error());
  }

  @Test
  void freezeCannotBeRolledBackByTheRejectionOrClearedBeforeOneHour() {
    for (int i = 0; i < 80; i++) {
      now = START.plusSeconds(i * 180);
      assertNull(reserve("10", "" + (i % 4 + 1), "alice", "" + (100 + i), "k" + i).error());
    }
    run(
        () ->
            jdbc.update(
                "UPDATE collab.network_contact_submission SET created_at=created_at-interval '2 days',visible_until=visible_until-interval '2 days' WHERE tenant_id=?",
                tenant));
    assertEquals(
        "network.quota_exceeded", reserve("10", "5", "alice", "900", "frozen").error().code());
    now = now.plusSeconds(3601);
    assertNull(reserve("10", "5", "alice", "900", "thawed").error());
  }

  @Test
  void sourcePendingAndRateQuotasAreSharedAndExpireUniformly() {
    limits = new Decision(true, "allowed", Map.of(Limit.FIRST_NODE_PENDING, 1L));
    assertNull(reserve("10", "1", "alice", "20", "a").error());
    assertEquals("network.quota_exceeded", reserve("10", "2", "alice", "21", "b").error().code());
    now = now.plusSeconds(601);
    assertNull(reserve("10", "2", "alice", "21", "c").error());
    tenant = UUID.randomUUID();
    limits = new Decision(true, "allowed", Map.of());
    for (int i = 0; i < 3; i++) assertNull(reserve("10", "1", "alice", "20", "k" + i).error());
    assertEquals(
        "network.quota_exceeded", reserve("10", "1", "alice", "20", "burst").error().code());
    now = now.plusSeconds(601);
    assertNull(reserve("10", "1", "alice", "20", "refilled").error());
  }
}
