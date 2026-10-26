package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.testsupport.PostgresExtension;
import java.sql.SQLException;
import java.util.List;
import java.util.UUID;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

/** The production owner migrates; the non-owner runtime writes through FORCE RLS. */
@ExtendWith(PostgresExtension.class)
class NetworkSourceMigrationPostgresTest {
  static JdbcTemplate jdbc;
  static TransactionTemplate tx;
  static boolean ownerSetBefore, runtimeSetBefore;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkSourceMigrationPostgresTest.class,
            "source_migration_owner",
            "source_migration_app",
            "collab");
    var admin =
        new JdbcTemplate(
            PostgresExtension.database(NetworkSourceMigrationPostgresTest.class).dataSource());
    ownerSetBefore = canSet(admin, roles.owner().role());
    runtimeSetBefore = canSet(admin, roles.runtime().role());
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    assertEquals(ownerSetBefore, canSet(admin, roles.owner().role()));
    assertEquals(runtimeSetBefore, canSet(admin, roles.runtime().role()));
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    tx = new TransactionTemplate(new TenantTransactionManager(roles.runtime().dataSource()));
  }

  private static boolean canSet(JdbcTemplate admin, String role) {
    return Boolean.TRUE.equals(
        admin.queryForObject(
            "SELECT has_parameter_privilege(?, 'auriga.tenant_id', 'SET')", Boolean.class, role));
  }

  @Test
  void migrationNeverInstallsAnOwnerTenantOverrideOrWeakensRls() {
    var function =
        jdbc.queryForMap(
            """
        SELECT p.prosecdef, p.proconfig::text AS settings, r.rolsuper, r.rolbypassrls
          FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
          JOIN pg_roles r ON r.oid=p.proowner
         WHERE n.nspname='collab' AND p.proname='network_source_wake_worker'
        """);
    assertEquals(false, function.get("prosecdef"));
    assertEquals(false, function.get("rolsuper"));
    assertEquals(false, function.get("rolbypassrls"));
    assertFalse(function.get("settings").toString().contains("auriga.tenant_id"));
    assertFalse(
        Boolean.TRUE.equals(
            jdbc.queryForObject(
                "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user",
                Boolean.class)));
    for (String table :
        List.of(
            "network_source_lineage",
            "network_source_operation",
            "network_source_outbox",
            "network_source_binding",
            "network_source_witness",
            "network_source_slot",
            "network_source_worker_tenant")) {
      var flags =
          jdbc.queryForMap(
              "SELECT relrowsecurity,relforcerowsecurity FROM pg_class WHERE oid=?::regclass",
              "collab." + table);
      assertEquals(true, flags.get("relrowsecurity"), table);
      assertEquals(true, flags.get("relforcerowsecurity"), table);
    }
  }

  @Test
  void outboxTriggerPreservesBusinessContextAndDiscoveryOnlyExposesRoutingMetadata() {
    UUID first = UUID.randomUUID(), second = UUID.randomUUID();
    in(
        first,
        () -> {
          assertEquals(first.toString(), currentTenant());
          enqueue(first);
          enqueue(first); // Existing wakeup must not require a different tenant context.
          assertEquals(first.toString(), currentTenant());
          assertEquals(1, count("network_source_worker_tenant", first));
          assertEquals(2, count("network_source_outbox", first));
          return null;
        });
    in(
        second,
        () -> {
          assertEquals(0, count("network_source_worker_tenant", first));
          assertEquals(0, count("network_source_outbox", first));
          enqueue(second);
          return null;
        });
    in(
        TenantContext.PLATFORM_TENANT,
        () -> {
          var routes =
              jdbc.queryForList(
                  "SELECT business_tenant FROM collab.network_source_worker_tenant WHERE business_tenant IN (?,?)",
                  UUID.class,
                  first,
                  second);
          assertEquals(2, routes.size());
          assertTrue(routes.containsAll(List.of(first, second)));
          assertEquals(0, count("network_source_operation", first));
          assertEquals(0, count("network_source_outbox", second));
          return null;
        });
    assertEquals(
        0,
        jdbc.queryForObject(
            "SELECT count(*) FROM collab.network_source_worker_tenant", Integer.class));
  }

  @Test
  void businessAndPlatformCannotWriteAnotherTenantsWakeup() {
    UUID actor = UUID.randomUUID(), victim = UUID.randomUUID();
    rejected(
        () ->
            in(
                actor,
                () ->
                    jdbc.update(
                        "INSERT INTO collab.network_source_worker_tenant(id,tenant_id,business_tenant) VALUES (?,?,?)",
                        UuidV7.next(),
                        victim,
                        victim)));
    rejected(
        () ->
            in(
                TenantContext.PLATFORM_TENANT,
                () ->
                    jdbc.update(
                        "INSERT INTO collab.network_source_worker_tenant(id,tenant_id,business_tenant) VALUES (?,?,?)",
                        UuidV7.next(),
                        victim,
                        victim)));
    in(
        actor,
        () -> {
          assertEquals(actor.toString(), currentTenant());
          enqueue(actor);
          return null;
        });
  }

  @Test
  void outboxAndWakeupRollbackTogether() {
    UUID tenant = UUID.randomUUID();
    TenantContext.run(
        tenant,
        () ->
            tx.executeWithoutResult(
                status -> {
                  enqueue(tenant);
                  assertEquals(1, count("network_source_worker_tenant", tenant));
                  status.setRollbackOnly();
                }));
    in(
        tenant,
        () -> {
          assertEquals(0, count("network_source_operation", tenant));
          assertEquals(0, count("network_source_outbox", tenant));
          assertEquals(0, count("network_source_worker_tenant", tenant));
          return null;
        });
  }

  private static void enqueue(UUID tenant) {
    UUID operation = UuidV7.next();
    jdbc.update(
        """
        INSERT INTO collab.network_source_operation(id,tenant_id,method,operation_id,realm,node_id,
          business_json,request_digest,original_frame,original_connection,epoch,boot_id,status)
        VALUES (?,?,'Revoke',?,'2','20','{}',?,?,?,1,'boot','pending')
        """,
        operation,
        tenant,
        "wake-" + operation,
        "sha256:" + "a".repeat(64),
        new byte[] {1},
        new byte[] {2});
    jdbc.update(
        "INSERT INTO collab.network_source_outbox(id,tenant_id,operation_id) VALUES (?,?,?)",
        UuidV7.next(),
        tenant,
        operation);
  }

  private static int count(String table, UUID tenant) {
    return jdbc.queryForObject(
        "SELECT count(*) FROM collab." + table + " WHERE tenant_id=?", Integer.class, tenant);
  }

  private static String currentTenant() {
    return jdbc.queryForObject("SELECT current_setting('auriga.tenant_id')", String.class);
  }

  private static <T> T in(UUID tenant, Supplier<T> work) {
    return TenantContext.call(tenant, () -> tx.execute(status -> work.get()));
  }

  private static void rejected(Runnable work) {
    var failure = assertThrows(DataAccessException.class, work::run);
    var sql = assertInstanceOf(SQLException.class, failure.getMostSpecificCause());
    assertEquals("42501", sql.getSQLState());
  }
}
