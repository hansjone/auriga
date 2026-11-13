package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.*;
import auriga.resource.persistence.ResourceStore;
import auriga.testsupport.PostgresExtension;
import java.nio.file.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.core.io.ClassPathResource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.init.ScriptUtils;

/** A pre-commit DDL rollback followed by additive migration and unchanged legacy L0-L4 data. */
@ExtendWith(PostgresExtension.class)
class CatalogMigrationTest {
  @Test
  void migrationRollbackPreservesEveryLegacyLevelAndEvidence() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            CatalogMigrationTest.class,
            "catalog_migration_owner",
            "catalog_migration_app",
            "resource");
    CommonMigrations.migrate(roles.owner().dataSource(), "resource");
    var config =
        Flyway.configure()
            .dataSource(roles.owner().dataSource())
            .schemas("resource")
            .defaultSchema("resource")
            .locations("classpath:db/resource");
    CommonMigrations.serviceFlyway(config.target("7").load()).migrate();
    var jdbc = new JdbcTemplate(roles.runtime().dataSource());
    var tx = new ResourceStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    UUID tenant = UUID.randomUUID();
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  for (int i = 0; i < 5; i++)
                    jdbc.update(
                        "INSERT INTO resource.resource_package(id,tenant_id,owner_id,name,level,visibility,enabled,revision,release_high_water,created_at) VALUES(?,?,?,?,?,?,false,1,9,now())",
                        UuidV7.next(),
                        tenant,
                        UuidV7.next(),
                        "legacy-" + i,
                        "l" + i,
                        i == 4 ? "private" : "tenant");
                  return true;
                }));
    var snapshot =
        TenantContext.call(
            tenant,
            () ->
                tx.read(
                    () ->
                        jdbc.queryForList(
                            "SELECT row_to_json(p)::text AS data FROM resource.resource_package p ORDER BY id")));
    Files.writeString(
        Path.of("target/catalog-pre-V8-fixture-backup.json"),
        auriga.resource.web.ResourceBody.JSON.writeValueAsString(snapshot));
    // PostgreSQL transactional DDL rollback: no committed catalog data exists at this point.
    try (var connection = roles.owner().dataSource().getConnection()) {
      connection.setAutoCommit(false);
      try (var statement = connection.createStatement()) {
        statement.execute("SET LOCAL search_path=resource");
      }
      ScriptUtils.executeSqlScript(
          connection, new ClassPathResource("db/resource/V8__unified_catalog.sql"));
      connection.rollback();
    }
    assertNull(
        new JdbcTemplate(roles.owner().dataSource())
            .queryForObject("SELECT to_regclass('resource.catalog_package')::text", String.class));
    CommonMigrations.serviceFlyway(config.target("8").load()).migrate();
    assertEquals(
        snapshot,
        TenantContext.call(
            tenant,
            () ->
                tx.read(
                    () ->
                        jdbc.queryForList(
                            "SELECT row_to_json(p)::text AS data FROM resource.resource_package p ORDER BY id"))));
    TenantContext.run(
        tenant,
        () ->
            tx.read(
                () -> {
                  assertEquals(
                      5,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.catalog_plugin_projection",
                          Integer.class));
                  assertEquals(
                      0,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.catalog_migration_proof", Integer.class));
                  return true;
                }));
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            tx.read(
                () -> {
                  assertEquals(
                      0,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.catalog_plugin_projection",
                          Integer.class));
                  return true;
                }));

    UUID pkg = UuidV7.next(),
        version = UuidV7.next(),
        owner = UuidV7.next(),
        installation = UuidV7.next();
    String oldDocument =
        "{\"resource\":{\"enabled\":false},\"state\":\"removed\",\"installationRevision\":5,\"selectionRevision\":22,\"generation\":77}";
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  jdbc.update(
                      "INSERT INTO resource.catalog_package(id,tenant_id,owner_id,name,kind,visibility,created_at) VALUES(?,?,?,'migration-skill','skill','tenant',now())",
                      pkg,
                      tenant,
                      owner);
                  jdbc.update(
                      "INSERT INTO resource.catalog_version(id,tenant_id,package_id,author_id,version,digest,manifest_bytes,manifest,state,minimum_risk,risk,created_at) VALUES(?,?,?,?,'1.0.0',?,?,'{}','draft','r1','r1',now())",
                      version,
                      tenant,
                      pkg,
                      owner,
                      "sha256:" + "0".repeat(64),
                      new byte[] {123, 125});
                  jdbc.update(
                      "INSERT INTO resource.catalog_installation(id,tenant_id,owner_id,client_id,package_id,version_id,revision,document) VALUES(?,?,?,?,?,?,5,CAST(? AS jsonb))",
                      installation,
                      tenant,
                      owner,
                      UuidV7.next(),
                      pkg,
                      version,
                      oldDocument);
                  return true;
                }));
    String before =
        TenantContext.call(
            tenant,
            () ->
                tx.read(
                    () ->
                        jdbc.queryForObject(
                            "SELECT document::text FROM resource.catalog_installation WHERE id=?",
                            String.class,
                            installation)));
    Files.writeString(Path.of("target/catalog-pre-V9-fixture-backup.json"), before);
    try (var connection = roles.owner().dataSource().getConnection()) {
      connection.setAutoCommit(false);
      try (var statement = connection.createStatement()) {
        statement.execute("SET LOCAL search_path=resource");
      }
      ScriptUtils.executeSqlScript(
          connection,
          new ClassPathResource("db/resource/V9__catalog_installation_expectations.sql"));
      connection.rollback();
    }
    var ownerJdbc = new JdbcTemplate(roles.owner().dataSource());
    assertNull(
        ownerJdbc.queryForObject(
            "SELECT to_regclass('resource.catalog_instance_installation')::text", String.class));
    assertEquals(
        0,
        ownerJdbc.queryForObject(
            "SELECT count(*) FROM information_schema.columns WHERE table_schema='resource' AND table_name='catalog_installation' AND column_name='collected_generation'",
            Integer.class));
    assertEquals(
        before,
        TenantContext.call(
            tenant,
            () ->
                tx.read(
                    () ->
                        jdbc.queryForObject(
                            "SELECT document::text FROM resource.catalog_installation WHERE id=?",
                            String.class,
                            installation))));
    CommonMigrations.serviceFlyway(config.target("9").load()).migrate();
    TenantContext.run(
        tenant,
        () ->
            tx.read(
                () -> {
                  assertEquals(
                      5L,
                      jdbc.queryForObject(
                          "SELECT (document->'resource'->>'installationRevision')::bigint FROM resource.catalog_installation WHERE id=?",
                          Long.class,
                          installation));
                  assertEquals(
                      "DISABLED",
                      jdbc.queryForObject(
                          "SELECT document->'resource'->>'desiredState' FROM resource.catalog_installation WHERE id=?",
                          String.class,
                          installation));
                  assertEquals(
                      77L,
                      jdbc.queryForObject(
                          "SELECT (document->>'generation')::bigint FROM resource.catalog_installation WHERE id=?",
                          Long.class,
                          installation));
                  return true;
                }));
    assertTrue(
        ownerJdbc.queryForObject(
            "SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='resource.catalog_installation'::regclass",
            Boolean.class));
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            tx.read(
                () -> {
                  assertEquals(
                      0,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.catalog_installation", Integer.class));
                  return true;
                }));
  }
}
