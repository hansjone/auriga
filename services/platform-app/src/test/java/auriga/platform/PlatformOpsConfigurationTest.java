package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.M3PermissionCatalog;
import auriga.ops.OpsModuleConfiguration;
import auriga.ops.api.OpsSignals;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

@ExtendWith(PostgresExtension.class)
class PlatformOpsConfigurationTest {
  @Test
  void separateRolesMigrationsAndFailClosedKeyInCombinedAssembly() {
    var roles =
        PostgresExtension.serviceRoles(
            PlatformOpsConfigurationTest.class, "platform_ops_owner", "platform_ops_app", "ops");
    new ApplicationContextRunner()
        .withUserConfiguration(PlatformOpsConfiguration.class, OpsModuleConfiguration.class)
        .withPropertyValues(
            "auriga.platform.combined=true",
            "auriga.services.ops.enabled=true",
            "auriga.platform.ops.jdbc-url="
                + PostgresExtension.database(PlatformOpsConfigurationTest.class)
                    .jdbcUrl()
                    .split("\\?")[0],
            "auriga.platform.ops.username=" + roles.runtime().role(),
            "auriga.platform.ops.password=",
            "auriga.platform.ops.migration-username=" + roles.owner().role(),
            "auriga.platform.ops.migration-password=")
        .run(
            ctx -> {
              assertNull(ctx.getStartupFailure());
              var ds = ctx.getBean("opsDataSource", DataSource.class);
              var jdbc = new JdbcTemplate(ds);
              assertEquals(
                  "platform_ops_app", jdbc.queryForObject("SELECT current_user", String.class));
              assertEquals(0, jdbc.queryForObject("SELECT count(*) FROM ops.alert", Integer.class));
              var t = UUID.randomUUID();
              ctx.getBean(OpsSignals.class)
                  .observe(t, "instance_failed", "instance-a", "critical", true);
              var tx =
                  new TransactionTemplate(
                      ctx.getBean("opsTransactionManager", TenantTransactionManager.class));
              TenantContext.run(
                  t,
                  () ->
                      tx.execute(
                          s -> {
                            assertEquals(
                                1,
                                jdbc.queryForObject(
                                    "SELECT count(*) FROM ops.alert", Integer.class));
                            return null;
                          }));
              TenantContext.run(
                  UUID.randomUUID(),
                  () ->
                      tx.execute(
                          s -> {
                            assertEquals(
                                0,
                                jdbc.queryForObject(
                                    "SELECT count(*) FROM ops.alert", Integer.class));
                            return null;
                          }));
              var handler =
                  ctx.getBean("opsInstanceEvents", auriga.common.events.EventHandler.class);
              var inbox =
                  new auriga.common.events.InboxProcessor(
                      ctx.getBean("opsTransactionManager", TenantTransactionManager.class),
                      jdbc,
                      "ops",
                      new auriga.common.observability.EventMetrics(
                          new io.micrometer.core.instrument.simple.SimpleMeterRegistry(), "ops"),
                      java.time.Clock.systemUTC(),
                      5,
                      auriga.common.events.EventProducers.of(Map.of()));
              UUID instance = UUID.randomUUID();
              var event =
                  tools.jackson.databind.json.JsonMapper.builder()
                      .build()
                      .createObjectNode()
                      .put("id", UUID.randomUUID().toString())
                      .put("tenantid", t.toString())
                      .put("source", "auriga/workspace")
                      .put("type", "instance.failed")
                      .put("time", java.time.Instant.now().toString());
              event.putObject("data").put("instanceId", instance.toString());
              var bytes = event.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8);
              assertEquals(
                  auriga.common.events.InboxProcessor.Outcome.PROCESSED,
                  inbox.process(
                      "ops-instance-alerts", handler, "auriga.evt.instance.failed", bytes, 1));
              assertEquals(
                  auriga.common.events.InboxProcessor.Outcome.DUPLICATE,
                  inbox.process(
                      "ops-instance-alerts", handler, "auriga.evt.instance.failed", bytes, 2));
              event.put("id", UUID.randomUUID().toString()).put("type", "instance.started");
              assertEquals(
                  auriga.common.events.InboxProcessor.Outcome.PROCESSED,
                  inbox.process(
                      "ops-instance-alerts",
                      handler,
                      "auriga.evt.instance.started",
                      event.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8),
                      1));
              TenantContext.run(
                  t,
                  () ->
                      tx.execute(
                          status -> {
                            assertEquals(
                                1,
                                jdbc.queryForObject(
                                    "SELECT count(*) FROM ops.alert WHERE subject=? AND resolved_at IS NOT NULL",
                                    Integer.class,
                                    instance.toString()));
                            return null;
                          }));
              assertNotNull(ctx.getBean("opsNodeSignals"));
              assertNotNull(ctx.getBean("opsDeliveryWorker"));
            });
  }

  @Test
  void grantsOnlyAdministrativeBuiltinRoles() {
    var expected =
        Set.of(
            "ops.alerts.read",
            "ops.alerts.write",
            "ops.notifications.read",
            "ops.notifications.write");
    assertTrue(M3PermissionCatalog.forRole("owner").containsAll(expected));
    assertTrue(M3PermissionCatalog.forRole("admin").containsAll(expected));
    assertTrue(Collections.disjoint(M3PermissionCatalog.forRole("member"), expected));
    expected.forEach(p -> assertTrue(M3PermissionCatalog.adminOnly(p)));
  }

  @Test
  void enabledWithoutOwnDatabaseFailsStartup() {
    new ApplicationContextRunner()
        .withUserConfiguration(PlatformOpsConfiguration.class, OpsModuleConfiguration.class)
        .withPropertyValues("auriga.platform.combined=true", "auriga.services.ops.enabled=true")
        .run(ctx -> assertNotNull(ctx.getStartupFailure()));
  }
}
