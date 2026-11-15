package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.PostgresExtension;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Upgrade existing forced-RLS databases without losing or guessing material authority. */
@ExtendWith(PostgresExtension.class)
class MsgnetSelectorMigrationTest {
  @Test
  void upgradePreservesAllTenantsNullAndAmbiguityAndRestoresForcedRls() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(getClass(), "selector_owner", "selector_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .target("29")
                .load())
        .migrate();
    var jdbc = new JdbcTemplate(roles.runtime().dataSource());
    var tx = new TransactionTemplate(new TenantTransactionManager(roles.runtime().dataSource()));
    JsonNode input;
    try (var resource =
        getClass()
            .getClassLoader()
            .getResourceAsStream("network-fixtures/auto-renewal-v2-vectors.json")) {
      input =
          KeyTestMaterial.JSON
              .readTree(resource)
              .path("boundary_cases")
              .valueStream()
              .filter(v -> "k3-confirmation-complete".equals(text(v, "id")))
              .findFirst()
              .orElseThrow()
              .path("input");
    }
    var bundle = (ObjectNode) input.path("bundle").deepCopy();
    String reader =
        text(input.path("evidence").path("payload").path("readers").get(0), "identity_id");
    ((ObjectNode) bundle.path("payload")).putArray("required_reader_ids").add(reader);
    var predecessor = MsgnetRenewal.object();
    for (String field : java.util.List.of("bundle", "authority", "control"))
      predecessor.set(field, input.path(field));
    predecessor.set("bundle", bundle);
    var snapshot =
        ((ObjectNode)
                input.path("evidence").path("payload").path("readers").get(0).path("selector"))
            .deepCopy();
    snapshot.remove("material_revision");
    snapshot.remove("manifest_digest");
    snapshot.putArray("files");
    snapshot.put("bundle_digest", digest(bundle));
    UUID assigned = UUID.randomUUID(),
        empty = UUID.randomUUID(),
        ambiguous = UUID.randomUUID(),
        refreshed = UUID.randomUUID();
    for (UUID tenant : java.util.List.of(assigned, empty, ambiguous, refreshed)) {
      TenantContext.run(
          tenant,
          () ->
              tx.execute(
                  status -> {
                    jdbc.update(
                        "INSERT INTO collab.msgnet_transport_history (id, tenant_id, realm, bundle_digest, generation, bundle_version, predecessor) VALUES (?, ?, '1', ?, 1, 1, ?)",
                        UUID.randomUUID(),
                        tenant,
                        digest(bundle),
                        MsgnetRenewal.canonical(predecessor));
                    if (!tenant.equals(empty)) insertApproved(jdbc, tenant, snapshot);
                    if (tenant.equals(ambiguous) || tenant.equals(refreshed)) {
                      var second =
                          snapshot
                              .deepCopy()
                              .put(
                                  "nonce",
                                  java.util.Base64.getUrlEncoder()
                                      .withoutPadding()
                                      .encodeToString(MsgnetKeyEncryptionTest.kek()));
                      insertApproved(jdbc, tenant, second, tenant.equals(refreshed) ? "2" : "1");
                    }
                    return null;
                  }));
    }
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    var accepted =
        TenantContext.call(
            assigned,
            () -> tx.execute(status -> MsgnetSelectorLedger.read(jdbc, assigned, "1", bundle)));
    assertTrue(accepted.selectors().path(reader).isTextual());
    var latest =
        TenantContext.call(
            refreshed,
            () -> tx.execute(status -> MsgnetSelectorLedger.read(jdbc, refreshed, "1", bundle)));
    assertEquals("2", text(latest.bindings().path(reader), "material_revision"));
    var migratedId =
        TenantContext.call(
            assigned,
            () ->
                tx.execute(
                    status ->
                        jdbc.queryForObject(
                            "SELECT id FROM collab.msgnet_transport_selector", UUID.class)));
    assertEquals(7, migratedId.version());
    var unassigned =
        TenantContext.call(
            empty, () -> tx.execute(status -> MsgnetSelectorLedger.read(jdbc, empty, "1", bundle)));
    assertTrue(unassigned.selectors().has(reader));
    assertTrue(unassigned.selectors().path(reader).isNull());
    assertThrows(
        auriga.common.error.AurigaException.class,
        () ->
            TenantContext.call(
                ambiguous,
                () ->
                    tx.execute(status -> MsgnetSelectorLedger.read(jdbc, ambiguous, "1", bundle))));
    int visible =
        TenantContext.call(
            assigned,
            () ->
                tx.execute(
                    status ->
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.msgnet_transport_selector",
                            Integer.class)));
    assertEquals(1, visible);
    for (String table :
        java.util.List.of(
            "msgnet_transport_selector", "msgnet_transport_history", "msgnet_projection_nonce")) {
      assertEquals(
          Boolean.TRUE,
          jdbc.queryForObject(
              "SELECT relforcerowsecurity FROM pg_class WHERE oid = ?::regclass",
              Boolean.class,
              "collab." + table));
    }
    assertThrows(
        org.springframework.dao.DataAccessException.class,
        () ->
            TenantContext.call(
                assigned,
                () ->
                    tx.execute(
                        status ->
                            jdbc.update(
                                "UPDATE collab.msgnet_transport_selector SET selector = '{}' WHERE tenant_id = ?",
                                assigned))));
    assertThrows(
        org.springframework.dao.DataAccessException.class,
        () ->
            TenantContext.call(
                empty,
                () ->
                    tx.execute(
                        status ->
                            jdbc.update(
                                "DELETE FROM collab.msgnet_transport_selector WHERE tenant_id = ?",
                                empty))));
  }

  private static void insertApproved(JdbcTemplate jdbc, UUID tenant, ObjectNode snapshot) {
    insertApproved(jdbc, tenant, snapshot, "1");
  }

  private static void insertApproved(
      JdbcTemplate jdbc, UUID tenant, ObjectNode snapshot, String revision) {
    var manifest = snapshot.deepCopy().put("material_revision", revision);
    var selector = manifest.deepCopy();
    selector.remove("files");
    selector.put("manifest_digest", digest(manifest));
    jdbc.update(
        "INSERT INTO collab.msgnet_projection_nonce (id, tenant_id, realm, installation_id, nonce, authorized_snapshot, local_selector) VALUES (?, ?, '1', ?, ?, ?, ?)",
        UUID.randomUUID(),
        tenant,
        text(snapshot, "installation_id"),
        text(snapshot, "nonce"),
        MsgnetRenewal.canonical(snapshot),
        MsgnetRenewal.canonical(selector));
  }
}
