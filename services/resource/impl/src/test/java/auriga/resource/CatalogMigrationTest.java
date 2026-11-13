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
    UUID instance = UuidV7.next(),
        node = UuidV7.next(),
        operation = UuidV7.next(),
        legacyInstance = UuidV7.next();
    String resource =
        "{\"identity\":{\"packageId\":\""
            + pkg
            + "\",\"versionId\":\""
            + version
            + "\"},\"installationRevision\":5,\"desiredState\":\"ENABLED\"}";
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  jdbc.update(
                      "INSERT INTO resource.catalog_instance_installation(id,tenant_id,instance_id,owner_id,package_id,revision,resource,selection_revision) VALUES(?,?,?,?,?,3,CAST(? AS jsonb),2)",
                      UuidV7.next(),
                      tenant,
                      instance,
                      owner,
                      pkg,
                      resource);
                  for (int i = 2; i <= 3; i++)
                    jdbc.update(
                        "INSERT INTO resource.catalog_distribution(id,tenant_id,actor_id,instance_id,owner_id,node_id,selection_revision,fingerprint,document,resolution) VALUES(?,?,?,?,?,?,?,'fixture','{}',CAST(? AS jsonb))",
                        UuidV7.next(),
                        tenant,
                        owner,
                        instance,
                        owner,
                        node,
                        i,
                        "{\"resources\":[" + resource + "]}");
                  jdbc.update(
                      "INSERT INTO resource.catalog_distribution(id,tenant_id,actor_id,instance_id,owner_id,node_id,selection_revision,fingerprint,document,resolution) VALUES(?,?,?,?,?,?,2,'legacy','{}',CAST(? AS jsonb))",
                      UuidV7.next(),
                      tenant,
                      owner,
                      legacyInstance,
                      owner,
                      node,
                      "{\"resources\":["
                          + resource.replace(
                              "\"installationRevision\":5", "\"installationRevision\":0")
                          + "]}");
                  jdbc.update(
                      "INSERT INTO resource.resource_operation(id,tenant_id,actor_id,operation,request_key,fingerprint,response,expires_at) VALUES(?,?,?,'catalog/ticket/client','fixture','fixture','{\"ticket\":\"secret\"}',now()+interval '1 day')",
                      operation,
                      tenant,
                      owner);
                  return true;
                }));
    var v10 =
        new String(
            new ClassPathResource("db/resource/V10__catalog_delivery_lifecycle.sql")
                .getInputStream()
                .readAllBytes(),
            java.nio.charset.StandardCharsets.UTF_8);
    try (var connection = roles.owner().dataSource().getConnection()) {
      connection.setAutoCommit(false);
      try (var statement = connection.createStatement()) {
        statement.execute("SET LOCAL search_path=resource");
        statement.execute(v10);
      }
      connection.rollback();
    }
    assertNull(
        ownerJdbc.queryForObject(
            "SELECT to_regclass('resource.catalog_instance_counter')::text", String.class));
    TenantContext.run(
        tenant,
        () ->
            tx.read(
                () -> {
                  assertTrue(
                      jdbc.queryForObject(
                          "SELECT jsonb_exists(response,'ticket') FROM resource.resource_operation WHERE id=?",
                          Boolean.class,
                          operation));
                  return true;
                }));
    CommonMigrations.serviceFlyway(config.target("10").load()).migrate();
    TenantContext.run(
        tenant,
        () ->
            tx.read(
                () -> {
                  assertEquals(
                      5L,
                      jdbc.queryForObject(
                          "SELECT revision FROM resource.catalog_instance_counter WHERE instance_id=?",
                          Long.class,
                          instance));
                  assertEquals(
                      1L,
                      jdbc.queryForObject(
                          "SELECT revision FROM resource.catalog_instance_counter WHERE instance_id=?",
                          Long.class,
                          legacyInstance));
                  assertEquals(
                      List.of("superseded", "pending"),
                      jdbc.queryForList(
                          "SELECT lifecycle FROM resource.catalog_distribution WHERE instance_id=? ORDER BY selection_revision",
                          String.class,
                          instance));
                  assertFalse(
                      jdbc.queryForObject(
                          "SELECT jsonb_exists(response,'ticket') FROM resource.resource_operation WHERE id=?",
                          Boolean.class,
                          operation));
                  assertEquals(
                      3L,
                      jdbc.queryForObject(
                          "SELECT revision FROM resource.catalog_instance_installation WHERE instance_id=?",
                          Long.class,
                          instance));
                  return true;
                }));
    for (String table :
        List.of(
            "catalog_distribution",
            "catalog_instance_counter",
            "catalog_instance_installation",
            "resource_operation"))
      assertTrue(
          ownerJdbc.queryForObject(
              "SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid=?::regclass",
              Boolean.class,
              "resource." + table));
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            tx.read(
                () -> {
                  assertEquals(
                      0,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM resource.catalog_instance_counter", Integer.class));
                  return true;
                }));
    UUID legacyPackage =
        TenantContext.call(
            tenant,
            () ->
                tx.read(
                    () ->
                        jdbc.queryForObject(
                            "SELECT id FROM resource.resource_package ORDER BY id LIMIT 1",
                            UUID.class)));
    UUID legacyVersion = UuidV7.next(), proof = UuidV7.next();
    TenantContext.run(
        tenant,
        () ->
            tx.write(
                () -> {
                  jdbc.update(
                      "INSERT INTO resource.resource_version(id,tenant_id,package_id,author_id,version,digest,size_bytes,state,revision,created_at) VALUES(?,?,?,?,'1.0.0',?,1,'draft',1,now())",
                      legacyVersion,
                      tenant,
                      legacyPackage,
                      owner,
                      "sha256:" + "0".repeat(64));
                  jdbc.update(
                      "INSERT INTO resource.catalog_migration_proof(id,tenant_id,package_id,version_id,release_sequence,archive_digest,manifest_digest,proof,activated_at) VALUES(?,?,?,?,1,?,?,'{}',now())",
                      proof,
                      tenant,
                      legacyPackage,
                      legacyVersion,
                      "sha256:" + "0".repeat(64),
                      "sha256:" + "1".repeat(64));
                  return true;
                }));
    TenantContext.run(
        tenant,
        () -> {
          assertThrows(
              org.springframework.dao.DataIntegrityViolationException.class,
              () ->
                  tx.write(
                      () ->
                          jdbc.update(
                              "UPDATE resource.catalog_migration_proof SET proof='{}' WHERE id=?",
                              proof)));
          assertThrows(
              org.springframework.dao.DataIntegrityViolationException.class,
              () ->
                  tx.write(
                      () ->
                          jdbc.update(
                              "DELETE FROM resource.catalog_migration_proof WHERE id=?", proof)));
        });
  }
}
