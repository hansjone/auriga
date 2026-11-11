package auriga.org;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import auriga.common.context.TenantContext;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.PostgresExtension;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Instant;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class OrgEgressUpgradeTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static final UUID LEGACY = UUID.fromString("00000000-0000-0000-0000-000000000002");
  private static final String OLD =
      "{\"categories\":[\"old_category\"],\"allowDomains\":[\"API.Example.com\",\"10.0.0.1\",\"api.example.com\"],\"denyDomains\":[\"BLOCKED.Example.com\",\"http://invalid.example.com\"],\"oldField\":true}"; // gitleaks:allow
  private static OrgStore store;
  private static OrgService service;
  @TempDir Path directory;

  @BeforeAll
  static void upgrade() {
    var roles =
        PostgresExtension.serviceRoles(OrgEgressUpgradeTest.class, "org_owner", "org_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .target("2")
                .load())
        .migrate();
    store =
        new OrgStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
    store.tenant(
        LEGACY,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO policy(id,tenant_id,key,version,value) VALUES (?,?,'egress',7,?::jsonb)",
                  UUID.randomUUID(),
                  LEGACY,
                  OLD);
          return null;
        });
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    service = new OrgService(store, mock(EventPublisher.class), Clock.systemUTC());
  }

  @BeforeEach
  void resetDefault() {
    store.tenant(
        TENANT,
        () -> {
          store.jdbc().update("DELETE FROM egress_policy_report WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM egress_policy_delivery WHERE tenant_id=?", TENANT);
          store
              .jdbc()
              .update(
                  "UPDATE policy SET version=1,value='{\"categories\":[],\"allowDomains\":[],\"denyDomains\":[]}'::jsonb,updated_by=NULL WHERE tenant_id=? AND key='egress'",
                  TENANT);
          return null;
        });
  }

  private OrgEgressDistribution channel() throws Exception {
    return new OrgEgressDistribution(
        service,
        Clock.systemUTC(),
        TENANT,
        "egress-primary",
        Files.createDirectories(directory.resolve("policies")),
        Files.createDirectories(directory.resolve("reports")));
  }

  private ObjectNode status(OrgEgressDistribution channel) {
    return TenantContext.call(TENANT, channel::status);
  }

  private void saveRaw(long version, String raw) {
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update(
                  "UPDATE policy SET version=?,value=?::jsonb WHERE tenant_id=? AND key='egress'",
                  version,
                  raw,
                  TENANT);
          return null;
        });
  }

  private void report(String digest, String result, long applied) throws Exception {
    Files.writeString(
        directory.resolve("reports/report.json"),
        OrgJson.object(
                "tenantId",
                TENANT.toString(),
                "version",
                4,
                "nodeId",
                "egress-primary",
                "digest",
                digest,
                "result",
                result,
                "appliedVersion",
                applied,
                "reportedAt",
                Instant.now().toString())
            .toString());
  }

  private String digest() throws Exception {
    return OrgService.hashCode(Files.readString(directory.resolve("policies/policy.json")));
  }

  @Test
  void seedAndMissingInstallationPreserveEnvironmentDefault() throws Exception {
    var channel = channel();
    channel.reconcile();
    assertFalse(Files.exists(directory.resolve("policies/policy.json")));
    assertEquals("unconfigured", status(channel).path("phase").asString());
    assertEquals(
        "open",
        TenantContext.call(TENANT, () -> service.getPolicy("egress"))
            .path("value")
            .path("mode")
            .asString());
    new OrgEgressDistribution(
            service,
            Clock.systemUTC(),
            null,
            "egress-primary",
            directory.resolve("policies"),
            directory.resolve("reports"))
        .reconcile();
    assertFalse(Files.exists(directory.resolve("policies/policy.json")));
  }

  @Test
  void migratesSavedLegacyVersionAndDistributesValidParts() throws Exception {
    var old = TenantContext.call(LEGACY, () -> service.getPolicy("egress"));
    assertEquals(8, old.path("version").asLong());
    assertEquals("api.example.com", old.path("value").path("allowDomains").get(0).asString());
    assertEquals(1, old.path("value").path("allowDomains").size());
    saveRaw(8, OLD);
    var channel = channel();
    channel.reconcile();
    assertTrue(status(channel).path("legacyAdjusted").asBoolean());
    var value =
        OrgJson.MAPPER
            .readTree(Files.readAllBytes(directory.resolve("policies/policy.json")))
            .path("value");
    assertEquals("allowlist", value.path("mode").asString());
    assertEquals("blocked.example.com", value.path("denyDomains").get(0).asString());
    assertEquals(0, value.path("categories").size());
  }

  @Test
  void inconsistentReportCannotRollBackPublishedDelivery() throws Exception {
    saveRaw(4, "{\"mode\":\"open\"}");
    var channel = channel();
    channel.reconcile();
    report("0".repeat(64), "applied", 4);
    assertDoesNotThrow(channel::reconcile);
    assertEquals("4", status(channel).path("distributedVersion").asString());
    assertEquals("failed", status(channel).path("phase").asString());
    assertEquals("invalid_report", status(channel).path("reportError").asString());
    report(digest(), "applied", 4);
    channel.reconcile();
    assertEquals("effective", status(channel).path("phase").asString());
    report(digest(), "failed", 9);
    channel.reconcile();
    assertEquals("failed", status(channel).path("phase").asString());
    assertEquals("9", status(channel).path("appliedVersion").asString());
    assertEquals("4", status(channel).path("distributedVersion").asString());
    assertEquals("failed", status(channel()).path("phase").asString());
  }
}
