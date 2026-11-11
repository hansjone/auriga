package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class OpsDeliveryTest {
  static OpsStore ops;
  static Sender sender;
  static OpsDelivery delivery;

  static class Sender extends OpsWebhook {
    int calls;
    boolean fail;

    @Override
    void send(String url, String bearer, String payload) {
      calls++;
      assertFalse(
          org.springframework.transaction.support.TransactionSynchronizationManager
              .isActualTransactionActive());
      assertTrue(payload.contains("deliveryId"));
      if (fail) throw OpsError.error("delivery_failed", 502);
    }
  }

  @BeforeAll
  static void setup() {
    var roles =
        PostgresExtension.serviceRoles(
            OpsDeliveryTest.class, "delivery_owner", "delivery_app", "ops");
    CommonMigrations.migrate(roles.owner().dataSource(), "ops");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("ops")
                .locations("classpath:db/ops")
                .load())
        .migrate();
    sender = new Sender();
    ops =
        new OpsStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()),
            new OpsSecrets(Base64.getEncoder().encodeToString(new byte[32])),
            sender);
    delivery = new OpsDelivery(ops);
  }

  UUID channel(UUID tenant, String level) {
    var id = UuidV7.next();
    TenantContext.run(
        tenant, () -> ops.save(id, "primary", "https://example.com/hook", "", true, level));
    return id;
  }

  @Test
  void routesNewAndEscalationOnlyAndSendsAfterCommit() {
    UUID t = UUID.randomUUID();
    channel(t, "critical");
    int before = sender.calls;
    ops.observe(t, "node_offline", "node", "warning", true);
    assertFalse(delivery.processOne(t));
    ops.observe(t, "node_offline", "node", "critical", true);
    ops.observe(t, "node_offline", "node", "critical", true);
    assertTrue(delivery.processOne(t));
    assertFalse(delivery.processOne(t));
    assertEquals(before + 1, sender.calls);
    TenantContext.run(
        t,
        () -> {
          var id = UUID.fromString(ops.alerts().path("items").get(0).path("id").asText());
          assertEquals("sent", ops.deliveries(id).path("items").get(0).path("status").asText());
        });
  }

  @Test
  void retriesWithBackoffAndStopsAtBound() {
    UUID t = UUID.randomUUID();
    channel(t, "warning");
    sender.fail = true;
    ops.observe(t, "disk_low", "disk", "warning", true);
    for (int i = 1; i <= 5; i++) {
      assertTrue(delivery.processOne(t));
      assertFalse(delivery.processOne(t));
      TenantContext.run(
          t,
          () ->
              ops.transaction(
                  () -> {
                    ops.jdbc.update(
                        "UPDATE ops.notification_delivery SET next_attempt_at=now()-interval '1 second' WHERE tenant_id=?",
                        t);
                    ops.jdbc.update(
                        "UPDATE ops.notification_channel SET last_delivery_at=NULL WHERE tenant_id=?",
                        t);
                    return null;
                  }));
    }
    assertFalse(delivery.processOne(t));
    sender.fail = false;
    TenantContext.run(
        t,
        () ->
            assertEquals(
                "failed",
                ops.transaction(
                    () ->
                        ops.jdbc.queryForObject(
                            "SELECT status FROM ops.notification_delivery WHERE tenant_id=?",
                            String.class,
                            t))));
  }

  @Test
  void skipsAcknowledgedSilencedAndCrossTenant() {
    UUID t = UUID.randomUUID();
    channel(t, "warning");
    ops.observe(t, "disk_low", "disk", "warning", true);
    TenantContext.run(
        t,
        () -> {
          UUID id = UUID.fromString(ops.alerts().path("items").get(0).path("id").asText());
          ops.action(id, "silence", 60);
        });
    assertFalse(delivery.processOne(t));
    assertFalse(delivery.processOne(UUID.randomUUID()));
    assertTrue(ops.tenants().contains(t));
  }

  @Test
  void rollbackDoesNotPublishAndUnavailableKeyPreservesAttempts() {
    UUID t = UUID.randomUUID();
    channel(t, "warning");
    TenantContext.run(
        t,
        () ->
            assertThrows(
                IllegalStateException.class,
                () ->
                    ops.transaction(
                        () -> {
                          ops.observe(t, "disk_low", "rollback", "warning", true);
                          throw new IllegalStateException("rollback");
                        })));
    assertFalse(delivery.processOne(t));
    ops.observe(t, "disk_low", "committed", "warning", true);
    var unavailable =
        new OpsStore(
            ops.jdbc,
            new TenantTransactionManager(ops.jdbc.getDataSource()),
            new OpsSecrets(""),
            sender);
    assertFalse(new OpsDelivery(unavailable).processOne(t));
    TenantContext.run(
        t,
        () ->
            ops.transaction(
                () -> {
                  assertEquals(
                      0,
                      ops.jdbc.queryForObject(
                          "SELECT attempts FROM ops.notification_delivery WHERE tenant_id=?",
                          Integer.class,
                          t));
                  return null;
                }));
  }

  @Test
  void cancelsAcknowledgedResolvedDisabledAndRaisedThreshold() {
    for (String reason : List.of("acknowledge", "resolve", "disable", "threshold")) {
      UUID t = UUID.randomUUID();
      UUID c = channel(t, "warning");
      ops.observe(t, "disk_low", "disk", "warning", true);
      TenantContext.run(
          t,
          () -> {
            UUID id = UUID.fromString(ops.alerts().path("items").get(0).path("id").asText());
            switch (reason) {
              case "acknowledge" -> ops.action(id, "acknowledge", 60);
              case "resolve" -> ops.observe(t, "disk_low", "disk", "warning", false);
              case "disable" ->
                  ops.save(c, "primary", "https://example.com/hook", "", false, "warning");
              case "threshold" ->
                  ops.save(c, "primary", "https://example.com/hook", "", true, "critical");
            }
            assertFalse(delivery.processOne(t), reason);
            assertEquals(
                "cancelled", ops.deliveries(id).path("items").get(0).path("status").asText());
          });
    }
  }

  @Test
  void recoversExpiredLeaseAndExcludesLiveLease() {
    UUID t = UUID.randomUUID();
    channel(t, "warning");
    ops.observe(t, "disk_low", "disk", "warning", true);
    TenantContext.run(
        t,
        () ->
            ops.transaction(
                () -> {
                  ops.jdbc.update(
                      "UPDATE ops.notification_delivery SET status='sending',attempts=1,lease_id=?,lease_until=now()+interval '90 seconds' WHERE tenant_id=?",
                      UUID.randomUUID(),
                      t);
                  return null;
                }));
    assertFalse(delivery.processOne(t));
    TenantContext.run(
        t,
        () ->
            ops.transaction(
                () -> {
                  ops.jdbc.update(
                      "UPDATE ops.notification_delivery SET lease_until=now()-interval '1 second' WHERE tenant_id=?",
                      t);
                  return null;
                }));
    assertTrue(delivery.processOne(t));
    TenantContext.run(
        t,
        () ->
            ops.transaction(
                () -> {
                  assertEquals(
                      2,
                      ops.jdbc.queryForObject(
                          "SELECT attempts FROM ops.notification_delivery WHERE tenant_id=?",
                          Integer.class,
                          t));
                  return null;
                }));
  }
}
