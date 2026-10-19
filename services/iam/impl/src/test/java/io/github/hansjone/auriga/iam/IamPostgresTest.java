package io.github.hansjone.auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.persistence.*;
import io.github.hansjone.auriga.testsupport.PostgresExtension;
import java.time.Instant;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real migrations/runtime role/FORCE RLS. Never replace this with H2 or a superuser test. */
@ExtendWith(PostgresExtension.class)
class IamPostgresTest {
  private static IamStore store;
  private static JdbcTemplate jdbc;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(IamPostgresTest.class, "iam_owner", "iam_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    store = new IamStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @Test
  void allBusinessTablesForceRlsAndRuntimeCannotDisableIt() {
    var tables =
        jdbc.queryForList(
            "SELECT relname,relrowsecurity,relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='iam' AND relname IN ('account','idp_provider','identity','login_tx','session','session_kv','device','signing_key','emergency_account','oauth_grant','account_link_log','rate_limit')");
    assertEquals(12, tables.size());
    tables.forEach(
        t -> {
          assertEquals(true, t.get("relrowsecurity"));
          assertEquals(true, t.get("relforcerowsecurity"));
        });
    assertThrows(
        Exception.class, () -> jdbc.execute("ALTER TABLE iam.account DISABLE ROW LEVEL SECURITY"));
  }

  @Test
  void identicalLookupKeysCannotCrossTenantsOrPoolReuse() {
    UUID a = UUID.randomUUID(), b = UUID.randomUUID();
    store.tenant(
        a, () -> store.insert("account", "same-key", Json.object("status", "active"), null));
    store.tenant(
        b,
        () -> {
          assertNull(store.get("account", "same-key", false));
          store.insert("account", "same-key", Json.object("status", "disabled"), null);
          return null;
        });
    assertEquals(
        "active",
        store.tenant(a, () -> store.get("account", "same-key", false)).path("status").asString());
    assertEquals(0, jdbc.queryForObject("SELECT count(*) FROM iam.account", Integer.class));
    TenantContext.run(
        a,
        () ->
            store.tx(
                () -> {
                  assertThrows(
                      Exception.class,
                      () ->
                          jdbc.update(
                              "INSERT INTO iam.account(id,tenant_id,lookup_key,data) VALUES (?,?,?,?::jsonb)",
                              UuidV7.next(),
                              b,
                              "cross",
                              "{}"));
                  return null;
                }));
  }

  @Test
  void nestedFailureRollsBackBusinessWritesButPreservesFailureRecord() {
    UUID tenant = UUID.randomUUID();
    store.tenant(
        tenant,
        () -> {
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.atomic(
                      () -> {
                        store.insert("account", "rollback", Json.object(), null);
                        throw new IllegalStateException("rollback");
                      }));
          store.insert("account_link_log", "failure", Json.object("result", "failed"), null);
          return null;
        });
    store.tenant(
        tenant,
        () -> {
          assertNull(store.get("account", "rollback", false));
          assertNotNull(store.get("account_link_log", "failure", false));
          return null;
        });
  }

  @Test
  void rateLimitPersistsAcrossRequestsAndExpiresIntoNewWindow() {
    UUID tenant = UUID.randomUUID();
    Instant now = Instant.parse("2026-01-01T00:00:00Z");
    store.tenant(
        tenant,
        () -> {
          store.limit("example", now, 1);
          return null;
        });
    assertThrows(
        RuntimeException.class,
        () ->
            store.tenant(
                tenant,
                () -> {
                  store.limit("example", now, 1);
                  return null;
                }));
    assertDoesNotThrow(
        () ->
            store.tenant(
                tenant,
                () -> {
                  store.limit("example", now.plusSeconds(300), 1);
                  return null;
                }));
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                tenant,
                () -> {
                  store.limit("failed-operation", now, 1);
                  throw new IllegalStateException("business operation failed");
                }));
    assertThrows(
        RuntimeException.class,
        () ->
            store.tenant(
                tenant,
                () -> {
                  store.limit("failed-operation", now, 1);
                  return null;
                }));
  }
}
