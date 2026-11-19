package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.contracts.node.v1.*;
import auriga.resource.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

/** Compiled by the author; root alone runs the real PostgreSQL fixture. */
@ExtendWith(PostgresExtension.class)
class CatalogPlacementSelectionsPostgresTest {
  static ResourceStore tx;
  static ResourceWorkflowStore db;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            CatalogPlacementSelectionsPostgresTest.class,
            "placement_choice_owner",
            "placement_choice_runtime",
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

  SharedDshBinding binding(UUID tenant) {
    return SharedDshBinding.newBuilder()
        .setTenantId(tenant.toString())
        .setMemberId(UUID.randomUUID().toString())
        .setPlacementId(UUID.randomUUID().toString())
        .setPhysicalInstanceId(UUID.randomUUID().toString())
        .setMode(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
        .setEpoch(1)
        .setRevision(1)
        .build();
  }

  ObjectNode selection(UUID tenant, UUID pack, UUID version, String action) {
    return JSON.createObjectNode()
        .put("action", action)
        .set(
            "identity",
            JSON.createObjectNode()
                .put("tenantId", tenant.toString())
                .put("packageId", pack.toString())
                .put("versionId", version.toString())
                .put("kind", "plugin")
                .put("version", "1.0.0")
                .put("manifestDigest", "sha256:" + "a".repeat(64)));
  }

  Map<String, Object> write(SharedDshBinding b, ObjectNode selection, long seq, String etag) {
    return TenantContext.call(
        UUID.fromString(b.getTenantId()),
        () ->
            tx.write(
                () -> {
                  db.lock("shared-view/" + b.getPlacementId());
                  return new CatalogPlacementSelections(db).write(b, selection, seq, null, etag);
                }));
  }

  @Test
  void revisionsSurviveRemovalAndRestartAndReleaseRollbackIsRejected() {
    UUID tenant = UUID.randomUUID(), pack = UUID.randomUUID(), version = UUID.randomUUID();
    var b = binding(tenant);
    assertEquals(
        1L, write(b, selection(tenant, pack, version, "install"), 2, "\"0\"").get("revision"));
    assertEquals(
        2L, write(b, selection(tenant, pack, version, "disable"), 2, "\"1\"").get("revision"));
    assertEquals(
        3L, write(b, selection(tenant, pack, version, "remove"), 2, "\"2\"").get("revision"));
    assertThrows(
        AurigaException.class,
        () -> write(b, selection(tenant, pack, UUID.randomUUID(), "install"), 1, "\"3\""));
    assertEquals(
        4L, write(b, selection(tenant, pack, version, "enable"), 2, "\"3\"").get("revision"));
  }

  @Test
  void newEpochDoesNotRestoreSelectionsAndTenantRlsPreventsCrossReads() {
    UUID tenant = UUID.randomUUID(), pack = UUID.randomUUID(), version = UUID.randomUUID();
    var b = binding(tenant);
    write(b, selection(tenant, pack, version, "install"), 1, "\"0\"");
    var next = b.toBuilder().setEpoch(2).build();
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  assertTrue(
                      CatalogPlacementSelections.material(
                              next, Map.of(), new CatalogPlacementSelections(db).rows(next))
                          .isEmpty());
                  return null;
                }));
    assertEquals(
        2L, write(next, selection(tenant, pack, version, "install"), 1, "\"0\"").get("revision"));
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            tx.write(
                () -> {
                  assertTrue(
                      db.rows("SELECT * FROM resource.catalog_placement_selection", db.args())
                          .isEmpty());
                  return null;
                }));
    var other = next.toBuilder().setMemberId(UUID.randomUUID().toString()).build();
    assertThrows(
        AurigaException.class,
        () -> write(other, selection(tenant, pack, version, "install"), 1, "\"2\""));
  }

  @Test
  void concurrentFirstSelectionsUseRealCasAndOnePersistentRow() throws Exception {
    UUID tenant = UUID.randomUUID(), pack = UUID.randomUUID(), version = UUID.randomUUID();
    var b = binding(tenant);
    try (var workers = Executors.newFixedThreadPool(2)) {
      var one =
          workers.submit(() -> write(b, selection(tenant, pack, version, "install"), 1, "\"0\""));
      var two =
          workers.submit(() -> write(b, selection(tenant, pack, version, "install"), 1, "\"0\""));
      int success = 0, conflict = 0;
      for (var result : List.of(one, two))
        try {
          assertEquals(1L, result.get(10, TimeUnit.SECONDS).get("revision"));
          success++;
        } catch (ExecutionException error) {
          assertInstanceOf(AurigaException.class, error.getCause());
          conflict++;
        }
      assertEquals(1, success);
      assertEquals(1, conflict);
    }
  }
}
