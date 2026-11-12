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
  }
}
