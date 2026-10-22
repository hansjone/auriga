package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
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

  @Test
  void wrongQrCodeCommitsFailureAndCannotBeRetried() {
    UUID tenant = UUID.randomUUID(), account = UUID.randomUUID();
    var clock = java.time.Clock.systemUTC();
    var qr = new QrChallenges(store, clock);
    var sessions = org.mockito.Mockito.mock(SessionManager.class);
    org.mockito.Mockito.when(sessions.activeById("qr-session"))
        .thenReturn(Json.object("accountId", account.toString(), "mfa", true));
    var controller = new IamLoginController(null, null, store, sessions, qr, null);
    var principal =
        new auriga.common.security.AurigaPrincipal(
            account.toString(),
            tenant,
            auriga.common.security.PrincipalType.USER,
            auriga.common.security.TokenContext.PORTAL,
            "qr-session",
            null,
            "member",
            List.of(),
            Set.of(),
            clock.instant(),
            clock.instant(),
            "qr-test");
    try {
      TenantContext.run(
          tenant,
          () ->
              auriga.common.context.RequestContext.start("qr-test", null)
                  .withPrincipal(principal)
                  .run(
                      () -> {
                        var challenge =
                            store.tx(
                                () ->
                                    qr.create(
                                        "tx",
                                        UUID.randomUUID().toString(),
                                        "login",
                                        "192.0.2.10",
                                        "test",
                                        "https://portal.example.com"));
                        String id = challenge.path("challengeId").asString();
                        store.tx(
                            () -> {
                              qr.markScanned(id, account.toString());
                              return null;
                            });
                        String code = challenge.path("matchCode").asString(),
                            wrong = code.equals("000000") ? "000001" : "000000";
                        assertEquals(
                            "iam.qr_match_mismatch",
                            assertThrows(
                                    auriga.common.error.AurigaException.class,
                                    () -> controller.confirmQr(id, Json.object("matchCode", wrong)))
                                .errorCode()
                                .code());
                        assertEquals(
                            "failed", store.tx(() -> qr.row(id, false)).path("status").asString());
                        assertEquals(
                            "iam.external_denied",
                            assertThrows(
                                    auriga.common.error.AurigaException.class,
                                    () -> controller.confirmQr(id, Json.object("matchCode", code)))
                                .errorCode()
                                .code());
                      }));
    } finally {
      controller.destroy();
    }
  }

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
  void instanceAiAccountLookupUsesTenantScopedRuntimeRls() {
    UUID tenant = UUID.randomUUID(), other = UUID.randomUUID(), owner = UUID.randomUUID();
    store.tenant(
        tenant,
        () ->
            store.insert(
                "account",
                owner.toString(),
                Json.object(
                    "accountId",
                    owner.toString(),
                    "status",
                    "active",
                    "createdAt",
                    Instant.now().toString()),
                null));
    var accounts = new IamAccounts(store);
    var request =
        auriga.contracts.iam.v1.GetAccountRequest.newBuilder()
            .setTenantId(tenant.toString())
            .setAccountId(owner.toString())
            .build();
    assertEquals(
        auriga.contracts.iam.v1.AccountStatus.ACCOUNT_STATUS_ACTIVE,
        TenantContext.call(tenant, () -> accounts.get(request)).getAccount().getStatus());
    assertEquals(
        "common.not_found",
        assertThrows(
                auriga.common.error.AurigaException.class,
                () ->
                    TenantContext.call(
                        other,
                        () ->
                            accounts.get(
                                request.toBuilder().setTenantId(other.toString()).build())))
            .errorCode()
            .code());
    store.tenant(
        tenant,
        () -> {
          var row = store.get("account", owner.toString(), true);
          row.put("status", "disabled");
          store.save("account", row);
          return null;
        });
    assertEquals(
        auriga.contracts.iam.v1.AccountStatus.ACCOUNT_STATUS_DISABLED,
        TenantContext.call(tenant, () -> accounts.get(request)).getAccount().getStatus());
  }

  @Test
  void allBusinessTablesForceRlsAndRuntimeCannotDisableIt() {
    var tables =
        jdbc.queryForList(
            "SELECT relname,relrowsecurity,relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='iam' AND relname IN ('account','idp_provider','identity','login_tx','session','session_kv','device','signing_key','emergency_account','oauth_grant','account_link_log','rate_limit','team_account','team_totp','team_passkey','team_ceremony','qr_challenge')");
    assertEquals(17, tables.size());
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
