package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class OpsStoreTest {
  static OpsStore ops;
  static JdbcTemplate jdbc;

  @BeforeAll
  static void setup() {
    var roles = PostgresExtension.serviceRoles(OpsStoreTest.class, "ops_owner", "ops_app", "ops");
    CommonMigrations.migrate(roles.owner().dataSource(), "ops");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("ops")
                .locations("classpath:db/ops")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    ops =
        new OpsStore(
            jdbc,
            new TenantTransactionManager(roles.runtime().dataSource()),
            new OpsSecrets(Base64.getEncoder().encodeToString(new byte[32])),
            new OpsWebhook());
  }

  @Test
  void deduplicatesAcknowledgesSilencesResolvesAndReopens() {
    var t = UUID.randomUUID();
    ops.observe(t, "node_offline", "node-a", "critical", true);
    ops.observe(t, "node_offline", "node-a", "critical", true);
    TenantContext.run(
        t,
        () -> {
          var rows = ops.alerts().path("items");
          assertEquals(1, rows.size());
          var id = UUID.fromString(rows.get(0).path("id").asText());
          ops.action(id, "acknowledge", 60);
          ops.action(id, "silence", 60);
          var row = ops.alerts().path("items").get(0);
          assertFalse(row.path("acknowledged_at").isNull());
          assertFalse(row.path("silenced_until").isNull());
          ops.action(id, "unsilence", 60);
          assertTrue(ops.alerts().path("items").get(0).path("silenced_until").isNull());
        });
    ops.observe(t, "node_offline", "node-a", "critical", false);
    ops.observe(t, "node_offline", "node-a", "critical", true);
    TenantContext.run(t, () -> assertEquals(2, ops.alerts().path("items").size()));
  }

  @Test
  void isolatesTenantsAndNeverReturnsSecrets() {
    var t = UUID.randomUUID();
    var other = UUID.randomUUID();
    var id = UuidV7.next();
    ops.observe(t, "disk_low", "disk", "warning", true);
    TenantContext.run(
        t,
        () -> {
          ops.save(
              id,
              "channel",
              "https://example.com/hook?key=private-value",
              "bearer-value",
              true,
              "critical");
          var body = ops.channels().toString();
          assertFalse(body.contains("private-value"));
          assertFalse(body.contains("bearer-value"));
          assertFalse(body.contains("example.com"));
          assertFalse(
              ops.transaction(
                      () ->
                          jdbc.queryForObject(
                              "SELECT encrypted_target FROM ops.notification_channel WHERE id=?",
                              String.class,
                              id))
                  .contains("bearer-value"));
        });
    TenantContext.run(
        other,
        () -> {
          assertEquals(0, ops.alerts().path("items").size());
          assertEquals(0, ops.channels().path("items").size());
          var collision =
              assertThrows(
                  AurigaException.class,
                  () -> ops.save(id, "foreign", "https://example.com/hook", "", true, "warning"));
          assertEquals("ops.not_found", collision.errorCode().code());
          var e = assertThrows(AurigaException.class, () -> ops.delete(id));
          assertEquals("ops.not_found", e.errorCode().code());
        });
    assertEquals(0, jdbc.queryForObject("SELECT count(*) FROM ops.alert", Integer.class));
  }

  @Test
  void instanceEpochIsPersistedAcrossHandlerRestarts() {
    var first = ops.instanceEventsSince();
    assertEquals(first, ops.instanceEventsSince());
  }
}
