package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.audit.OutboxAuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.resource.app.DesktopAuditWriter;
import auriga.resource.persistence.ResourceStore;
import auriga.testsupport.PostgresExtension;
import java.time.Clock;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.json.JsonMapper;

@ExtendWith(PostgresExtension.class)
class DesktopAuditWriterTest {
  @Test
  void receiptAndUnifiedAuditCommitTogetherAndSurviveRetry() {
    var roles =
        PostgresExtension.serviceRoles(
            getClass(), "desktop_resource_owner", "desktop_resource_app", "resource");
    CommonMigrations.migrate(roles.owner().dataSource(), "resource");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("resource")
                .defaultSchema("resource")
                .locations("classpath:db/resource")
                .load())
        .migrate();
    var jdbc = new JdbcTemplate(roles.runtime().dataSource());
    var transactions =
        new ResourceStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    var json = JsonMapper.builder().build();
    var publisher =
        new OutboxEventPublisher(
            jdbc, "resource", "resource", new EventSchemas(), json, Clock.systemUTC(), () -> {});
    var tenant = UUID.randomUUID();
    var writer =
        new DesktopAuditWriter(transactions, jdbc, new OutboxAuditLogger(publisher), tenant);
    var event =
        json.createObjectNode()
            .put("id", UuidV7.next().toString())
            .put("actor", UUID.randomUUID().toString())
            .put("action", "publish")
            .put("sequence", 3)
            .put("at", "2026-11-12T00:00:00Z");
    writer.record(event);
    writer.record(event);
    TenantContext.run(
        tenant,
        () ->
            transactions.read(
                () -> {
                  assertEquals(
                      1,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.desktop_audit_receipt", Integer.class));
                  assertEquals(
                      1,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.outbox_event WHERE event_type='audit.recorded'",
                          Integer.class));
                  var envelope =
                      json.readTree(
                          jdbc.queryForObject(
                              "SELECT envelope::text FROM resource.outbox_event", String.class));
                  assertEquals(
                      "resource.desktop.publish", envelope.path("data").path("action").asString());
                  assertEquals(
                      event.path("actor").asString(),
                      envelope.path("data").path("actor").path("id").asString());
                  assertEquals(
                      "auriga.audit.resource",
                      jdbc.queryForObject(
                          "SELECT nats_subject FROM resource.outbox_event", String.class));
                  return true;
                }));
    var failed =
        new DesktopAuditWriter(
            transactions,
            jdbc,
            entry -> {
              throw new IllegalStateException("unavailable");
            },
            tenant);
    var second = event.deepCopy().put("id", UuidV7.next().toString());
    assertThrows(IllegalStateException.class, () -> failed.record(second));
    writer.record(second);
    TenantContext.run(
        tenant,
        () ->
            transactions.read(
                () -> {
                  assertEquals(
                      2,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.outbox_event", Integer.class));
                  return true;
                }));
  }
}
