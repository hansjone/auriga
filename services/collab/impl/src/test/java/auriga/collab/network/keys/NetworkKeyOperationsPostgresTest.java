package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.PostgresExtension;
import java.time.Clock;
import java.time.ZoneOffset;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class NetworkKeyOperationsPostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  final KeyTestMaterial m = new KeyTestMaterial();
  final Clock clock = Clock.fixed(KeyTestMaterial.NOW, ZoneOffset.UTC);
  final UUID tenant = UUID.randomUUID();

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkKeyOperationsPostgresTest.class, "keys_owner", "keys_app", "collab");
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
  }

  NetworkKeyOperations service() {
    return new NetworkKeyOperations(jdbc, manager, tenant, "2", m.verifier(clock), "1", clock);
  }

  @Test
  void restartRejectsRollbackSameVersionMutationAndStaleLocalSigner() {
    var first = m.release(m.payload(4, KeyTestMaterial.NOW));
    service().publish(first);
    service().publish(first);
    var p = m.payload(5, KeyTestMaterial.NOW);
    ((ObjectNode) p.path("keys").get(0)).put("state", "revoked");
    var revoked = m.release(p);
    service().publish(revoked);
    assertEquals(revoked, service().current());
    assertThrows(RuntimeException.class, () -> service().publish(first));
    assertThrows(
        RuntimeException.class,
        () -> service().publish(m.release(m.payload(5, KeyTestMaterial.NOW))));
    assertThrows(
        RuntimeException.class,
        () -> service().assertApproved(NetworkJson.canonical(m.payload(4, KeyTestMaterial.NOW))));
    service().assertApproved(NetworkJson.canonical(p));
  }

  @Test
  void revokedKeysCannotDisappearOrResurrectOrChangePurpose() {
    var p = m.payload(1, KeyTestMaterial.NOW);
    ((ObjectNode) p.path("keys").get(0)).put("state", "revoked");
    service().publish(m.release(p));
    assertThrows(
        RuntimeException.class,
        () -> service().publish(m.release(m.payload(2, KeyTestMaterial.NOW))));
    var omitted = p.deepCopy();
    omitted.put("keyset_version", "2");
    ((tools.jackson.databind.node.ArrayNode) omitted.path("keys")).remove(0);
    assertThrows(RuntimeException.class, () -> service().publish(m.release(omitted)));
    var changed = p.deepCopy();
    changed.put("keyset_version", "2");
    ((ObjectNode) changed.path("keys").get(0)).put("purpose", "management_state");
    assertThrows(RuntimeException.class, () -> service().publish(m.release(changed)));
  }

  @Test
  void durablePushRetriesAndOldAcknowledgementCannotHideANewerRevocation() {
    service().publish(m.release(m.payload(1, KeyTestMaterial.NOW)));
    assertThrows(
        IllegalStateException.class,
        () ->
            service()
                .pushPending(
                    (realm, signed, announcement) -> {
                      throw new IllegalStateException("transport unavailable");
                    }));
    int[] pushes = {0};
    service()
        .pushPending(
            (realm, signed, announcement) -> {
              pushes[0]++;
              assertEquals("2", realm);
              m.verifier(clock).verify(signed, announcement);
              var next = m.payload(2, KeyTestMaterial.NOW);
              ((ObjectNode) next.path("keys").get(0)).put("state", "revoked");
              service().publish(m.release(next));
            });
    service()
        .pushPending(
            (realm, signed, announcement) -> {
              pushes[0]++;
              assertEquals(
                  "2", NetworkJson.parse(signed).path("keyset").path("keyset_version").asString());
            });
    service().pushPending((realm, signed, announcement) -> fail("already acknowledged"));
    assertEquals(2, pushes[0]);
  }

  @Test
  void rlsAndTrustedTenantBindingPreventCrossTenantReads() {
    service().publish(m.release(m.payload(1, KeyTestMaterial.NOW)));
    Integer visible =
        TenantContext.call(
            UUID.randomUUID(),
            () ->
                new TransactionTemplate(manager)
                    .execute(
                        s ->
                            jdbc.queryForObject(
                                "SELECT count(*) FROM collab.network_key_publication",
                                Integer.class)));
    assertEquals(0, visible);
    assertThrows(
        RuntimeException.class,
        () ->
            TenantContext.call(
                UUID.randomUUID(),
                () -> new TransactionTemplate(manager).execute(s -> service().current())));
  }

  @Test
  void nextKeyMustBePublishedForADayAndKeepOldValidationOverlap() {
    var next = m.payload(1, KeyTestMaterial.NOW);
    var added =
        m.key("business-next", "business_delivery", KeyTestMaterial.pair()).put("state", "next");
    ((tools.jackson.databind.node.ArrayNode) next.path("keys")).add(added);
    service().publish(m.release(next));
    var early = next.deepCopy().put("keyset_version", "2");
    ((ObjectNode) early.path("keys").get(0)).put("state", "retired");
    ((ObjectNode) early.path("keys").get(3)).put("state", "active");
    assertThrows(RuntimeException.class, () -> service().publish(m.release(early)));
    Clock later = Clock.offset(clock, java.time.Duration.ofDays(1));
    early
        .put("issued_at", later.instant().toString())
        .put("valid_until", later.instant().plusSeconds(86400).toString());
    var rotated =
        new NetworkKeyOperations(jdbc, manager, tenant, "2", m.verifier(later), "1", later);
    rotated.publish(m.release(early));
    assertEquals(
        "2",
        NetworkJson.parse(rotated.current().signedKeyset())
            .path("keyset")
            .path("keyset_version")
            .asString());
  }

  @Test
  void signaturesAndRevocationSerializeOnTheDurablePublication() throws Exception {
    var p = m.payload(1, KeyTestMaterial.NOW);
    var first = m.release(p);
    service().publish(first);
    var entered = new java.util.concurrent.CountDownLatch(1);
    var finish = new java.util.concurrent.CountDownLatch(1);
    var signerPid = new java.util.concurrent.atomic.AtomicInteger();
    var publisherPid = new java.util.concurrent.atomic.AtomicInteger();
    var publisherReady = new java.util.concurrent.CountDownLatch(1);
    var next = m.payload(2, KeyTestMaterial.NOW);
    ((ObjectNode) next.path("keys").get(0)).put("state", "revoked");
    var revocation = m.release(next);
    try (var workers = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      // This finally must run before ExecutorService.close(), including assertion failures.
      try {
        var signed =
            workers.submit(
                () ->
                    service()
                        .signApproved(
                            NetworkJson.canonical(p),
                            () -> {
                              signerPid.set(
                                  jdbc.queryForObject("SELECT pg_backend_pid()", Integer.class));
                              entered.countDown();
                              try {
                                assertTrue(finish.await(10, java.util.concurrent.TimeUnit.SECONDS));
                                return "signed-before-revocation";
                              } catch (InterruptedException e) {
                                Thread.currentThread().interrupt();
                                throw new IllegalStateException(e);
                              }
                            }));
        assertTrue(entered.await(10, java.util.concurrent.TimeUnit.SECONDS));
        var failedPublication =
            workers.submit(
                () ->
                    TenantContext.call(
                        tenant,
                        () ->
                            new TransactionTemplate(manager)
                                .execute(
                                    status -> {
                                      jdbc.execute("SET LOCAL lock_timeout = '250ms'");
                                      var conflict =
                                          assertThrows(
                                              org.springframework.dao.DataAccessException.class,
                                              () -> service().publish(revocation));
                                      var sql =
                                          assertInstanceOf(
                                              java.sql.SQLException.class,
                                              conflict.getMostSpecificCause());
                                      assertEquals("55P03", sql.getSQLState(), conflict::toString);
                                      if (conflict
                                          instanceof
                                          org.springframework.jdbc.UncategorizedSQLException
                                              uncategorized) {
                                        assertTrue(
                                            uncategorized
                                                .getSql()
                                                .contains(
                                                    "INSERT INTO collab.network_key_publication"));
                                      }
                                      var aborted =
                                          assertThrows(
                                              org.springframework.dao.DataAccessException.class,
                                              () -> jdbc.queryForObject("SELECT 1", Integer.class));
                                      assertEquals(
                                          "25P02",
                                          assertInstanceOf(
                                                  java.sql.SQLException.class,
                                                  aborted.getMostSpecificCause())
                                              .getSQLState(),
                                          aborted::toString);
                                      // The nested publish marked this transaction rollback-only.
                                      assertTrue(status.isRollbackOnly());
                                      status.setRollbackOnly();
                                      return null;
                                    })));
        failedPublication.get(10, java.util.concurrent.TimeUnit.SECONDS);
        assertEquals(
            first, service().current(), "Failed revocation must not publish any new state");

        // A fresh transaction retries the real publication. Observe its actual blocker rather
        // than guessing from elapsed time or racing the short error-path lock timeout.
        var recoveredPublication =
            workers.submit(
                () ->
                    TenantContext.call(
                        tenant,
                        () ->
                            new TransactionTemplate(manager)
                                .execute(
                                    status -> {
                                      publisherPid.set(
                                          jdbc.queryForObject(
                                              "SELECT pg_backend_pid()", Integer.class));
                                      publisherReady.countDown();
                                      service().publish(revocation);
                                      return null;
                                    })));
        assertTrue(publisherReady.await(10, java.util.concurrent.TimeUnit.SECONDS));
        boolean blockedBySigner = false;
        long deadline = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(5);
        while (!blockedBySigner && !recoveredPublication.isDone() && System.nanoTime() < deadline) {
          blockedBySigner =
              Boolean.TRUE.equals(
                  jdbc.queryForObject(
                      "SELECT ? = ANY(pg_blocking_pids(?))",
                      Boolean.class,
                      signerPid.get(),
                      publisherPid.get()));
        }
        assertTrue(blockedBySigner, "Publication must wait for the exact signing backend");
        assertFalse(
            recoveredPublication.isDone(), "Revocation cannot commit while signing is held");
        assertEquals(first, service().current());
        finish.countDown();
        assertEquals(
            "signed-before-revocation", signed.get(10, java.util.concurrent.TimeUnit.SECONDS));
        recoveredPublication.get(10, java.util.concurrent.TimeUnit.SECONDS);
        assertEquals(revocation, service().current());
        assertEquals(
            "revoked",
            NetworkJson.parse(service().current().signedKeyset())
                .path("keyset")
                .path("keys")
                .get(0)
                .path("state")
                .asString());
        var denied =
            assertThrows(
                auriga.common.error.AurigaException.class,
                () ->
                    service()
                        .signApproved(
                            NetworkJson.canonical(p), () -> fail("revoked state must not sign")));
        assertEquals("common.unavailable", denied.errorCode().code());
        assertEquals(
            "signed-with-current-protocol-key",
            service()
                .signApproved(
                    NetworkJson.canonical(next), () -> "signed-with-current-protocol-key"));
      } finally {
        finish.countDown();
      }
    }
  }

  @Test
  void hardDeadlineFloorAndReusedKeyMaterialFailClosed() {
    var p = m.payload(3, KeyTestMaterial.NOW);
    service().publish(m.release(p));
    Clock expired = Clock.offset(clock, java.time.Duration.ofDays(1));
    var afterDeadline =
        new NetworkKeyOperations(jdbc, manager, tenant, "2", m.verifier(expired), "1", expired);
    assertThrows(RuntimeException.class, afterDeadline::current);
    var floor = new NetworkKeyOperations(jdbc, manager, tenant, "2", m.verifier(clock), "4", clock);
    assertThrows(RuntimeException.class, () -> floor.publish(m.release(p)));
    assertThrows(RuntimeException.class, floor::current);
    var changed = m.payload(4, KeyTestMaterial.NOW);
    ((ObjectNode) changed.path("keys").get(0))
        .put(
            "public_key_spki",
            m.key("new", "business_delivery", KeyTestMaterial.pair())
                .path("public_key_spki")
                .asString());
    assertThrows(RuntimeException.class, () -> service().publish(m.release(changed)));
    service().publish(m.release(p)); // Re-downloading never renews the deadline.
    assertThrows(RuntimeException.class, afterDeadline::current);
  }
}
