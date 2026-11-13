package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class UacProviderPostgresTest {
  static IamStore store;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            UacProviderPostgresTest.class, "uac_owner", "uac_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    store =
        new IamStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @Test
  void oneFactSourceEtagInitialOffConfigValidationAndCrossTenantIsolation() {
    UUID tenant = UUID.randomUUID();
    TenantContext.run(
        tenant,
        () -> {
          var adapter =
              new UacConfiguredAdapter(
                  UacConfiguredAdapterTest.settings("https://idp.example.com"), false);
          var audit = mock(AuditLogger.class);
          var sessions = mock(SessionManager.class);
          var registry = new ConnectorRegistry(new ContractSchemas(), List.of(adapter));
          var providers =
              new ProviderManager(
                  store,
                  registry,
                  new Secrets(key()),
                  audit,
                  Clock.systemUTC(),
                  "https://portal.example.com");
          var uac =
              new UacProviderService(store, providers, adapter, sessions, audit, Clock.systemUTC());
          String id =
              providers
                  .create(
                      Json.object(
                          "connectorType",
                          "uac",
                          "display",
                          Json.object("name", Json.object("en", "Company")),
                          "config",
                          Json.object()))
                  .path("id")
                  .asString();
          assertFalse(providers.get(id).path("enabled").asBoolean(true));
          assertEquals(
              "common.not_found",
              assertThrows(AurigaException.class, () -> uac.get(id)).errorCode().code());
          String first = store.tx(() -> UacProviderService.etag(uac.row(id, false)));
          var config =
              Json.object(
                  "enabled",
                  true,
                  "baseUrl",
                  "https://idp.example.com",
                  "systemCode",
                  "test-system");
          for (String invalid : List.of("", "\"stale\""))
            assertEquals(
                "common.precondition_failed",
                assertThrows(AurigaException.class, () -> uac.put(id, config, invalid))
                    .errorCode()
                    .code());
          assertEquals("test-system", uac.put(id, config, first).path("systemCode").asString());
          assertTrue(providers.get(id).path("enabled").asBoolean());
          String second = store.tx(() -> UacProviderService.etag(uac.row(id, false)));
          assertNotEquals(first, second);
          assertEquals(
              "common.precondition_failed",
              assertThrows(AurigaException.class, () -> uac.put(id, config, first))
                  .errorCode()
                  .code());
          var unsafe = config.deepCopy();
          unsafe.put("baseUrl", "https://idp.example.com?redirect=1");
          assertEquals(
              "common.validation_failed",
              assertThrows(AurigaException.class, () -> uac.put(id, unsafe, second))
                  .errorCode()
                  .code());
          TenantContext.run(
              UUID.randomUUID(),
              () -> {
                assertEquals(
                    "common.not_found",
                    assertThrows(AurigaException.class, () -> uac.get(id)).errorCode().code());
                assertEquals(
                    "common.not_found",
                    assertThrows(AurigaException.class, () -> uac.put(id, config, second))
                        .errorCode()
                        .code());
                store.tx(
                    () -> {
                      assertNull(store.byId("idp_provider", id, false));
                      return null;
                    });
              });
          config.put("enabled", false);
          uac.put(id, config, second);
          assertFalse(uac.get(id).path("enabled").asBoolean());
        });
  }

  @Test
  void disableCancelsSharedChallengesAndPendingLoginsAndRevokesOnlySourceSessions() {
    TenantContext.run(
        UUID.randomUUID(),
        () -> {
          var adapter =
              new UacConfiguredAdapter(
                  UacConfiguredAdapterTest.settings("https://idp.example.com"), false);
          var registry = new ConnectorRegistry(new ContractSchemas(), List.of(adapter));
          var sessions = mock(SessionManager.class);
          var providers =
              new ProviderManager(
                  store,
                  registry,
                  new Secrets(key()),
                  mock(AuditLogger.class),
                  Clock.systemUTC(),
                  "https://portal.example.com");
          var uac =
              new UacProviderService(
                  store, providers, adapter, sessions, mock(AuditLogger.class), Clock.systemUTC());
          String id =
              providers
                  .create(
                      Json.object(
                          "connectorType",
                          "uac",
                          "display",
                          Json.object(),
                          "config",
                          Json.object()))
                  .path("id")
                  .asString();
          String challenge = UUID.randomUUID().toString(), tx = UUID.randomUUID().toString();
          store.tx(
              () -> {
                store.insert(
                    "uac_challenge",
                    challenge,
                    Json.object(
                        "providerId",
                        id,
                        "state",
                        "verified",
                        "credential",
                        "encrypted-placeholder"),
                    Instant.now().plusSeconds(120));
                store.insert(
                    "login_tx",
                    tx,
                    Json.object(
                        "providerId",
                        id,
                        "status",
                        "pending",
                        "uacCredential",
                        "encrypted-placeholder"),
                    Instant.now().plusSeconds(120));
                return null;
              });
          providers.delete(id);
          store.tx(
              () -> {
                assertEquals(
                    "cancelled",
                    store.get("uac_challenge", challenge, false).path("state").asString());
                assertFalse(store.get("uac_challenge", challenge, false).has("credential"));
                assertEquals(
                    "cancelled", store.get("login_tx", tx, false).path("status").asString());
                return null;
              });
          TenantContext.run(
              UUID.randomUUID(),
              () ->
                  store.tx(
                      () -> {
                        assertTrue(store.all("uac_challenge").isEmpty());
                        assertTrue(store.all("uac_credential").isEmpty());
                        assertTrue(store.all("uac_navigation").isEmpty());
                        assertTrue(store.all("uac_job").isEmpty());
                        return null;
                      }));
        });
  }

  static String key() {
    byte[] bytes = new byte[32];
    new java.security.SecureRandom().nextBytes(bytes);
    return Base64.getEncoder().encodeToString(bytes);
  }
}
