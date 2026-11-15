package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import jakarta.servlet.http.HttpServletRequest;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpServletResponse;
import tools.jackson.databind.node.ObjectNode;

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

  @Test
  void genericGetKeepsPlaceholderBodyAndEtagFromOneRowWhenFirstConfigurationCommits() {
    assertGetSnapshot(false);
  }

  @Test
  void uacGetKeepsConfiguredBodyAndEtagFromOneRowWhenAnotherConfigurationCommits() {
    assertGetSnapshot(true);
  }

  private void assertGetSnapshot(boolean specialized) {
    UUID tenant = UUID.randomUUID();
    TenantContext.run(
        tenant,
        () -> {
          var adapter =
              new UacConfiguredAdapter(
                  UacConfiguredAdapterTest.settings("https://idp.example.com"), false);
          var registry = new ConnectorRegistry(new ContractSchemas(), List.of(adapter));
          var audit = mock(AuditLogger.class);
          var sessions = mock(SessionManager.class);
          var secrets = new Secrets(key());
          var readStore = spy(store);
          var providers =
              new ProviderManager(
                  readStore,
                  registry,
                  secrets,
                  audit,
                  Clock.systemUTC(),
                  "https://portal.example.com");
          var uac =
              new UacProviderService(
                  readStore, providers, adapter, sessions, audit, Clock.systemUTC());
          var writerProviders =
              new ProviderManager(
                  store, registry, secrets, audit, Clock.systemUTC(), "https://portal.example.com");
          var writer =
              new UacProviderService(
                  store, writerProviders, adapter, sessions, audit, Clock.systemUTC());
          String id =
              providers
                  .create(
                      Json.object(
                          "connectorType",
                          "uac",
                          "display",
                          Json.object("name", Json.object("en", "Example")),
                          "config",
                          Json.object()))
                  .path("id")
                  .asString();
          var request = mock(HttpServletRequest.class);
          var identity = mock(RequestIdentity.class);
          when(identity.call(eq(request), any()))
              .thenAnswer(i -> ((Supplier<?>) i.getArgument(1)).get());
          var genericController = new IamAdminController(providers, registry, readStore);
          var uacController = new UacAdminController(uac, readStore, identity, sessions);
          var initialRow = store.tx(() -> store.get("idp_provider", id, false));
          if (specialized) {
            writer.put(
                id,
                Json.object(
                    "enabled",
                    false,
                    "baseUrl",
                    "https://idp.example.com",
                    "systemCode",
                    "example-old"),
                UacProviderService.etag(initialRow));
          } else {
            var notFoundResponse = new MockHttpServletResponse();
            assertEquals(
                "common.not_found",
                assertThrows(
                        AurigaException.class,
                        () -> uacController.get(id, request, notFoundResponse))
                    .errorCode()
                    .code());
            assertNull(notFoundResponse.getHeader("ETag"));
          }
          var snapshot = store.tx(() -> store.get("idp_provider", id, false));
          String oldTag = UacProviderService.etag(snapshot);
          var replacement =
              Json.object(
                  "enabled",
                  true,
                  "baseUrl",
                  "https://idp.example.com",
                  "systemCode",
                  "example-new");
          var reads = new AtomicInteger();
          clearInvocations(readStore);
          try (var executor = Executors.newSingleThreadExecutor()) {
            doAnswer(
                    i -> {
                      ObjectNode row = (ObjectNode) i.callRealMethod();
                      if (reads.getAndIncrement() == 0) {
                        // Commit on a separate connection after this SELECT returns its old row.
                        executor
                            .submit(
                                () ->
                                    TenantContext.run(
                                        tenant, () -> writer.put(id, replacement, oldTag)))
                            .get(10, TimeUnit.SECONDS);
                      }
                      return row;
                    })
                .when(readStore)
                .get("idp_provider", id, false);
            var response = new MockHttpServletResponse();
            var body =
                specialized
                    ? uacController.get(id, request, response)
                    : genericController.get(id, response);
            assertFalse(body.path("enabled").asBoolean(true));
            if (specialized) {
              assertEquals("example-old", body.path("systemCode").asString());
              assertEquals("no-store", response.getHeader("Cache-Control"));
            } else {
              assertTrue(body.path("config").isEmpty());
              assertNull(response.getHeader("Cache-Control"));
            }
            assertEquals(oldTag, response.getHeader("ETag"));
            verify(readStore, times(1)).get("idp_provider", id, false);
            verify(readStore, never()).get("idp_provider", id, true);
            assertEquals(
                "common.precondition_failed",
                assertThrows(
                        AurigaException.class,
                        () ->
                            writer.put(
                                id,
                                replacement.deepCopy().put("systemCode", "example-stale"),
                                response.getHeader("ETag")))
                    .errorCode()
                    .code());
            assertEquals("example-new", writer.get(id).path("systemCode").asString());
            assertTrue(writer.get(id).path("enabled").asBoolean());
            String currentTag =
                store.tx(() -> UacProviderService.etag(store.get("idp_provider", id, false)));
            assertNotEquals(oldTag, currentTag);
            verify(identity).browserSession(request, sessions, false);
          }
        });
  }
}
