package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventSchemas;
import auriga.common.events.OutboxEventPublisher;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.PostgresExtension;
import java.time.Clock;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class OrgEgressDistributionTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static OrgStore store;
  private static OrgService svc;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            OrgEgressDistributionTest.class, "org_owner", "org_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    var manager = new TenantTransactionManager(roles.runtime().dataSource());
    store = new OrgStore(new JdbcTemplate(roles.runtime().dataSource()), manager);
    svc =
        new OrgService(
            store,
            new OutboxEventPublisher(
                store.jdbc(),
                "org",
                "org",
                new EventSchemas(),
                OrgJson.MAPPER,
                Clock.systemUTC(),
                null),
            Clock.systemUTC());
  }

  @org.junit.jupiter.api.io.TempDir java.nio.file.Path directory;

  @Test
  void persistsVersionBoundReportAndRejectsForgery() throws Exception {
    var policies = java.nio.file.Files.createDirectory(directory.resolve("policies"));
    var reports = java.nio.file.Files.createDirectory(directory.resolve("reports"));
    var channel =
        new OrgEgressDistribution(
            svc, Clock.systemUTC(), TENANT, "egress-primary", policies, reports);
    assertEquals("saved", TenantContext.call(TENANT, channel::status).path("phase").asString());
    channel.reconcile();
    assertEquals(
        "distributed", TenantContext.call(TENANT, channel::status).path("phase").asString());
    var snapshot =
        OrgJson.MAPPER.readTree(java.nio.file.Files.readAllBytes(policies.resolve("policy.json")));
    assertEquals(1, snapshot.path("version").asLong());
    var bytes = java.nio.file.Files.readAllBytes(policies.resolve("policy.json"));
    String digest =
        java.util.HexFormat.of()
            .formatHex(java.security.MessageDigest.getInstance("SHA-256").digest(bytes));
    var report =
        OrgJson.object(
            "tenantId",
            TENANT.toString(),
            "version",
            1,
            "nodeId",
            "egress-primary",
            "digest",
            digest,
            "result",
            "applied",
            "appliedVersion",
            1,
            "reportedAt",
            java.time.Instant.now().toString());
    java.nio.file.Files.writeString(reports.resolve("report.json"), report.toString());
    channel.reconcile();
    assertEquals("effective", TenantContext.call(TENANT, channel::status).path("phase").asString());
    var restarted =
        new OrgEgressDistribution(
            svc, Clock.systemUTC(), TENANT, "egress-primary", policies, reports);
    assertEquals(
        "effective", TenantContext.call(TENANT, restarted::status).path("phase").asString());
    report.put("digest", "0".repeat(64));
    report.put("result", "failed");
    report.put("reportedAt", java.time.Instant.now().toString());
    java.nio.file.Files.writeString(reports.resolve("report.json"), report.toString());
    assertThrows(RuntimeException.class, restarted::reconcile);
    assertEquals(
        "effective", TenantContext.call(TENANT, restarted::status).path("phase").asString());
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update("UPDATE policy SET version=2 WHERE tenant_id=? AND key='egress'", TENANT);
          return null;
        });
    restarted.reconcile();
    assertEquals(
        "distributed", TenantContext.call(TENANT, restarted::status).path("phase").asString());
    assertEquals(
        "2", TenantContext.call(TENANT, restarted::status).path("distributedVersion").asString());
    assertEquals(
        "1", TenantContext.call(TENANT, restarted::status).path("appliedVersion").asString());
    var nextBytes = java.nio.file.Files.readAllBytes(policies.resolve("policy.json"));
    var nextDigest =
        java.util.HexFormat.of()
            .formatHex(java.security.MessageDigest.getInstance("SHA-256").digest(nextBytes));
    report.put("version", 2);
    report.put("digest", nextDigest);
    report.put("appliedVersion", 1);
    report.put("result", "failed");
    report.put("reportedAt", java.time.Instant.now().toString());
    java.nio.file.Files.writeString(reports.resolve("report.json"), report.toString());
    restarted.reconcile();
    assertEquals("failed", TenantContext.call(TENANT, restarted::status).path("phase").asString());
    report.put("appliedVersion", 2);
    report.put("result", "applied");
    report.put("reportedAt", java.time.Instant.now().toString());
    java.nio.file.Files.writeString(reports.resolve("report.json"), report.toString());
    restarted.reconcile();
    assertEquals(
        "effective", TenantContext.call(TENANT, restarted::status).path("phase").asString());
    var later =
        new OrgEgressDistribution(
            svc,
            Clock.offset(Clock.systemUTC(), java.time.Duration.ofSeconds(40)),
            TENANT,
            "egress-primary",
            policies,
            reports);
    assertEquals("stale", TenantContext.call(TENANT, later::status).path("phase").asString());
    assertEquals(
        0,
        store.tenant(
            UUID.randomUUID(),
            () ->
                store
                    .jdbc()
                    .queryForObject("SELECT count(*) FROM egress_policy_report", Integer.class)));
    for (String field : java.util.List.of("tenantId", "nodeId", "reportedAt")) {
      var forged = report.deepCopy();
      forged.put(
          field,
          field.equals("tenantId")
              ? UUID.randomUUID().toString()
              : field.equals("nodeId")
                  ? "another-node"
                  : java.time.Instant.now().plusSeconds(60).toString());
      java.nio.file.Files.writeString(reports.resolve("report.json"), forged.toString());
      assertThrows(RuntimeException.class, restarted::reconcile);
    }
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update("UPDATE policy SET version=1 WHERE tenant_id=? AND key='egress'", TENANT);
          return null;
        });
    assertThrows(IllegalStateException.class, restarted::reconcile);
    assertThrows(
        AurigaException.class, () -> TenantContext.call(UUID.randomUUID(), restarted::status));
  }
}
