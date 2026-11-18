package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.resource.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real migration/runtime-role/RLS fixture; root alone runs it in the unique PostgreSQL slot. */
@ExtendWith(PostgresExtension.class)
class CatalogSharedInstallationsPostgresTest {
  static ResourceStore tx;
  static ResourceWorkflowStore db;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            CatalogSharedInstallationsPostgresTest.class,
            "shared_install_owner",
            "shared_install_runtime",
            "resource");
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
    tx = new ResourceStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    db = new ResourceWorkflowStore(jdbc);
  }

  static Map<UUID, Long> apply(
      UUID tenant, UUID placement, Map<UUID, CatalogAssignments.Effective> selections) {
    return TenantContext.call(
        tenant,
        () ->
            tx.write(
                () -> {
                  db.lock("shared-view/" + placement);
                  return new CatalogSharedInstallations(db).reconcile(placement, selections);
                }));
  }

  static CatalogAssignments.Effective selection(
      UUID tenant, UUID pack, UUID version, String mode, long sequence) {
    return new CatalogAssignments.Effective(
        JSON.createObjectNode()
            .put("tenantId", tenant.toString())
            .put("packageId", pack.toString())
            .put("versionId", version.toString())
            .put("kind", "skill")
            .put("version", "1.0.0")
            .put("manifestDigest", "sha256:" + "a".repeat(64)),
        mode,
        sequence);
  }

  @Test
  void removalAndRestartRetainRevisionAndReleaseHighWater() {
    UUID tenant = UUID.randomUUID(),
        placement = UUID.randomUUID(),
        pack = UUID.randomUUID(),
        version = UUID.randomUUID();
    var one = Map.of(pack, selection(tenant, pack, version, "required", 1));
    assertEquals(1L, apply(tenant, placement, one).get(pack));
    assertEquals(1L, apply(tenant, placement, one).get(pack));
    var mode = Map.of(pack, selection(tenant, pack, version, "default", 1));
    assertEquals(2L, apply(tenant, placement, mode).get(pack));
    assertTrue(apply(tenant, placement, Map.of()).isEmpty());
    assertTrue(apply(tenant, placement, Map.of()).isEmpty());
    assertEquals(4L, apply(tenant, placement, mode).get(pack));
    var next = Map.of(pack, selection(tenant, pack, UUID.randomUUID(), "default", 2));
    assertEquals(5L, apply(tenant, placement, next).get(pack));
    apply(tenant, placement, Map.of());
    assertThrows(AurigaException.class, () -> apply(tenant, placement, one));
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  var row =
                      db.row(
                          "SELECT * FROM resource.catalog_shared_installation WHERE tenant_id=:tenant AND placement_id=:placement AND package_id=:package",
                          db.args("placement", placement, "package", pack));
                  assertEquals(6L, ResourceWorkflowStore.number(row, "revision"));
                  assertEquals(false, row.get("active"));
                  return null;
                }));
  }

  @Test
  void tenantAndPlacementHistoriesRemainIndependentUnderForcedRls() {
    UUID a = UUID.randomUUID(),
        b = UUID.randomUUID(),
        placement = UUID.randomUUID(),
        pack = UUID.randomUUID(),
        version = UUID.randomUUID();
    assertEquals(
        1L,
        apply(a, placement, Map.of(pack, selection(a, pack, version, "required", 1))).get(pack));
    assertEquals(
        1L,
        apply(b, placement, Map.of(pack, selection(b, pack, version, "required", 1))).get(pack));
    assertEquals(
        1L,
        apply(a, UUID.randomUUID(), Map.of(pack, selection(a, pack, version, "required", 1)))
            .get(pack));
    TenantContext.run(
        b,
        () ->
            tx.write(
                () -> {
                  assertEquals(
                      1,
                      db.rows("SELECT * FROM resource.catalog_shared_installation", db.args())
                          .size());
                  return null;
                }));
  }

  @Test
  void concurrentInitialReplaysSerializeWithoutInventingAdditionalRevisions() throws Exception {
    UUID tenant = UUID.randomUUID(), placement = UUID.randomUUID(), pack = UUID.randomUUID();
    var choices = Map.of(pack, selection(tenant, pack, UUID.randomUUID(), "required", 1));
    try (var workers = Executors.newFixedThreadPool(2)) {
      var first = workers.submit(() -> apply(tenant, placement, choices));
      var second = workers.submit(() -> apply(tenant, placement, choices));
      assertEquals(1L, first.get(10, TimeUnit.SECONDS).get(pack));
      assertEquals(1L, second.get(10, TimeUnit.SECONDS).get(pack));
    }
  }
}
