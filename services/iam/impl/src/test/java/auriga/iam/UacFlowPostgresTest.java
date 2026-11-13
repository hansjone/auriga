package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.iam.api.IdentityPolicyHooks;
import auriga.testsupport.PostgresExtension;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.env.MockEnvironment;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class UacFlowPostgresTest {
  static IamStore store;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(UacFlowPostgresTest.class, "flow_owner", "flow_app", "iam");
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

  static final class Time extends Clock {
    final AtomicReference<Instant> now =
        new AtomicReference<>(Instant.parse("2026-01-01T00:00:00Z"));

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return now.get();
    }

    void advance(long seconds) {
      now.updateAndGet(t -> t.plusSeconds(seconds));
    }
  }

  static final class Fixture implements AutoCloseable {
    final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID();
    final Time time = new Time();
    final AuditLogger audit = mock(AuditLogger.class);
    final EventPublisher events = mock(EventPublisher.class);
    final HttpServer server;
    final String origin, provider;
    final SessionManager sessions;
    final UacSessions uac;
    final UacFlow flow;
    final UacWorker worker;
    final AtomicInteger scans = new AtomicInteger(),
        departments = new AtomicInteger(),
        verifications = new AtomicInteger();
    final AtomicInteger departmentStatus = new AtomicInteger(200),
        verifyStatus = new AtomicInteger(200);
    final AtomicReference<JsonNode> departmentBody =
        new AtomicReference<>(
            Json.object(
                "ok",
                true,
                "rows",
                List.of(Json.object("employee", "001Ab", "department", "Test department"))));
    final AtomicReference<JsonNode> verifyBody = new AtomicReference<>();
    final AtomicReference<CountDownLatch> verifyEntered = new AtomicReference<>(),
        verifyRelease = new AtomicReference<>();
    final String token = Secrets.random(), replacement = Secrets.random();

    Fixture(String strategy, int retries) throws Exception {
      server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
      origin = "http://127.0.0.1:" + server.getAddress().getPort();
      verifyBody.set(
          Json.object(
              "code",
              Json.object("code", "0000"),
              "bo",
              Json.object("code", "0000", "user", "001AB", "token", replacement)));
      server.createContext(
          "/",
          exchange -> {
            JsonNode request = Json.MAPPER.readTree(exchange.getRequestBody().readAllBytes());
            JsonNode response;
            int status = 200;
            switch (exchange.getRequestURI().getPath()) {
              case "/qr" -> {
                scans.incrementAndGet();
                assertEquals(32, request.path("first").asString().length());
                response = Json.object("state", "success", "employee", "001Ab", "token", token);
              }
              case "/department" -> {
                departments.incrementAndGet();
                status = departmentStatus.get();
                response = departmentBody.get();
              }
              case "/verify" -> {
                verifications.incrementAndGet();
                assertTrue(request.has("clientIP"));
                assertEquals("001Ab", request.path("account").asString());
                if (verifyEntered.get() != null) verifyEntered.get().countDown();
                if (verifyRelease.get() != null)
                  try {
                    assertTrue(verifyRelease.get().await(15, TimeUnit.SECONDS));
                  } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                  }
                status = verifyStatus.get();
                response = verifyBody.get();
              }
              default -> throw new AssertionError();
            }
            byte[] bytes = response.toString().getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(status, bytes.length);
            exchange.getResponseBody().write(bytes);
            exchange.close();
          });
      server.start();
      var adapter = spy(new UacConfiguredAdapter(UacConfiguredAdapterTest.settings(origin), true));
      doAnswer(
              i -> {
                store.requireTransactionReleased();
                return i.callRealMethod();
              })
          .when(adapter)
          .scan(any(), any());
      doAnswer(
              i -> {
                store.requireTransactionReleased();
                return i.callRealMethod();
              })
          .when(adapter)
          .verify(any(), any());
      doAnswer(
              i -> {
                store.requireTransactionReleased();
                return i.callRealMethod();
              })
          .when(adapter)
          .department(any(), any(), anyString());
      var secrets = new Secrets(UacProviderPostgresTest.key());
      var registry = new ConnectorRegistry(new ContractSchemas(), List.of(adapter));
      var providers =
          new ProviderManager(store, registry, secrets, audit, time, "https://portal.example.com");
      sessions =
          new SessionManager(
              store, mock(SessionKv.class), mock(TokenService.class), time, events, audit);
      var config = new UacProviderService(store, providers, adapter, sessions, audit, time);
      var policy =
          new UacRevalidationConfiguration(
              new MockEnvironment()
                  .withProperty("auriga.iam.uac.revalidation.strategy", strategy)
                  .withProperty("auriga.iam.uac.revalidation.max-retries", "" + retries)
                  .withProperty("auriga.iam.uac.revalidation.retry-interval-seconds", "5"));
      var cipher =
          new UacCredentialCipher(
              new ContractSchemas(),
              Json.object(
                  "activeKeyId", "initial", "decryptKeyIds", List.of("initial"), "revision", 0),
              Map.of("initial", UacProviderPostgresTest.key()));
      uac = new UacSessions(store, cipher, adapter, providers, sessions, time, policy);
      var login =
          new LoginManager(
              store,
              secrets,
              new ContractSchemas(),
              registry,
              mock(EmergencyAccounts.class),
              mock(TeamAccounts.class),
              sessions,
              mock(IdentityPolicyHooks.class),
              events,
              audit,
              time,
              "https://portal.example.com");
      login.uacEstablished(uac::established);
      provider =
          call(
              () -> {
                String id =
                    providers
                        .create(
                            Json.object(
                                "connectorType",
                                "uac",
                                "display",
                                Json.object("name", Json.object("en", "Company")),
                                "config",
                                Json.object(),
                                "admission",
                                Json.object("mode", "invite_only", "mfa", "never")))
                        .path("id")
                        .asString();
                String etag = store.tx(() -> UacProviderService.etag(config.row(id, false)));
                config.put(
                    id,
                    Json.object("enabled", true, "baseUrl", origin, "systemCode", "test-system"),
                    etag);
                store.tx(
                    () -> {
                      store.insert(
                          "account",
                          account.toString(),
                          Json.object(
                              "accountId",
                              account.toString(),
                              "status",
                              "active",
                              "displayName",
                              "Test member",
                              "memberType",
                              "member"),
                          null);
                      store.insert(
                          "identity",
                          id + ":001Ab",
                          Json.object(
                              "providerId",
                              id,
                              "accountId",
                              account.toString(),
                              "subject",
                              "001Ab"),
                          null);
                      return null;
                    });
                return id;
              });
      flow = new UacFlow(store, config, providers, adapter, uac, login, sessions, time);
      worker = new UacWorker(uac, audit);
    }

    <T> T call(Supplier<T> action) {
      return TenantContext.call(tenant, action::get);
    }

    UacFlow.Created begin() {
      return call(
          () ->
              flow.create(
                  Json.object("providerId", provider),
                  "portal",
                  "https://portal.example.com",
                  "127.0.0.1",
                  null,
                  null));
    }

    LoginManager.Result complete(UacFlow.Created c) {
      return call(
          () ->
              flow.complete(
                  c.challenge().path("id").asString(),
                  c.binding(),
                  "portal",
                  "https://portal.example.com"));
    }

    LoginManager.Result login() {
      var c = begin();
      call(() -> worker.runOnce());
      return complete(c);
    }

    ObjectNode session(String id) {
      return call(() -> store.tx(() -> store.byId("session", id, false)));
    }

    int run() {
      return call(() -> worker.runOnce());
    }

    AurigaPrincipal principal(String session) {
      return new AurigaPrincipal(
          account.toString(),
          tenant,
          PrincipalType.USER,
          TokenContext.PORTAL,
          session,
          null,
          "member",
          List.of(),
          Set.of(),
          time.instant(),
          null,
          UUID.randomUUID().toString());
    }

    ObjectNode read(String id) {
      return call(() -> store.tx(() -> uac.credentials(principal(id))));
    }

    public void close() {
      if (verifyRelease.get() != null) verifyRelease.get().countDown();
      worker.destroy();
      server.stop(0);
    }
  }

  @Test
  void concurrentCompleteCreatesOneSessionAndOneInformationJobAndResponseCanBeRecovered()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var challenge = f.begin();
      String id = challenge.challenge().path("id").asString();
      for (int i = 0; i < 5; i++)
        f.call(
            () -> f.flow.status(id, challenge.binding(), "portal", "https://portal.example.com"));
      assertEquals(0, f.scans.get());
      f.run();
      assertEquals(1, f.scans.get());
      try (var pool = Executors.newFixedThreadPool(2)) {
        var first = pool.submit(() -> f.complete(challenge));
        var second = pool.submit(() -> f.complete(challenge));
        var a = first.get(20, TimeUnit.SECONDS);
        var b = second.get(20, TimeUnit.SECONDS);
        assertEquals(a.session().cookie(), b.session().cookie());
        assertEquals(
            a.session().session().path("sessionId"), b.session().session().path("sessionId"));
        String sid = a.session().session().path("sessionId").asString();
        String next = f.session(sid).path("nextRevalidateAt").asString();
        assertEquals(0, f.departments.get());
        f.run();
        f.run();
        assertEquals(1, f.departments.get());
        assertEquals("available", f.session(sid).path("uacLoginInfo").path("status").asString());
        assertEquals(next, f.session(sid).path("nextRevalidateAt").asString());
        assertEquals(0, f.verifications.get());
        f.call(
            () ->
                store.tx(
                    () -> {
                      assertEquals(1, store.all("session").size());
                      return null;
                    }));
      }
    }
  }

  @Test
  void informationFailureNeverRevokesOrChangesDeadlineAndIsNotRetriedAfterRestart()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      f.departmentStatus.set(500);
      var login = f.login();
      String sid = login.session().session().path("sessionId").asString();
      String next = f.session(sid).path("nextRevalidateAt").asString();
      f.run();
      var restarted = new UacWorker(f.uac, f.audit);
      try {
        f.call(restarted::runOnce);
      } finally {
        restarted.destroy();
      }
      assertEquals(1, f.departments.get());
      assertEquals("unavailable", f.session(sid).path("uacLoginInfo").path("status").asString());
      assertEquals(next, f.session(sid).path("nextRevalidateAt").asString());
      assertFalse(f.session(sid).path("revoked").asBoolean());
    }
  }

  @Test
  void dueVerificationRotatesOnlyAtDeadlineAndPersistsNextPeriod() throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      String sid = f.login().session().session().path("sessionId").asString();
      f.run();
      f.time.advance(7199);
      f.run();
      assertEquals(0, f.verifications.get());
      f.time.advance(32);
      f.run();
      assertEquals(1, f.verifications.get());
      assertEquals(f.replacement, f.read(sid).path("token").asString());
      assertEquals(
          f.time.instant().plusSeconds(7200).toString(),
          f.session(sid).path("nextRevalidateAt").asString());
      assertEquals(1, f.departments.get());
      f.run();
      assertEquals(1, f.verifications.get());
    }
  }

  @Test
  void upstreamFaultUsesPersistedBudgetAndNeverAdvancesDeadline() throws Exception {
    try (var f = new Fixture("token-verify", 1)) {
      String sid = f.login().session().session().path("sessionId").asString();
      f.run();
      String next = f.session(sid).path("nextRevalidateAt").asString();
      f.verifyStatus.set(503);
      f.time.advance(7231);
      f.run();
      assertEquals(1, f.verifications.get());
      assertFalse(f.session(sid).path("revoked").asBoolean());
      assertEquals(next, f.session(sid).path("nextRevalidateAt").asString());
      f.run();
      assertEquals(1, f.verifications.get());
      f.time.advance(5);
      f.run();
      assertEquals(2, f.verifications.get());
      assertTrue(f.session(sid).path("revoked").asBoolean());
      assertEquals(
          "common.unauthenticated",
          assertThrows(AurigaException.class, () -> f.read(sid)).errorCode().code());
    }
  }

  @Test
  void futureSessionSharingCredentialIsNotRevalidatedOrRevokedBeforeItsOwnDeadline()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      String first = f.login().session().session().path("sessionId").asString();
      f.run();
      f.time.advance(3600);
      String second = f.login().session().session().path("sessionId").asString();
      f.run();
      assertEquals(
          f.session(first).path("uacCredentialId"), f.session(second).path("uacCredentialId"));
      f.verifyBody.set(
          Json.object("code", Json.object("code", "rejected"), "bo", Json.object("code", "0000")));
      f.time.advance(3631);
      f.run();
      assertTrue(f.session(first).path("revoked").asBoolean());
      assertFalse(f.session(second).path("revoked").asBoolean());
      assertEquals(f.token, f.read(second).path("token").asString());
      assertEquals(1, f.verifications.get());
      f.time.advance(3631);
      f.run();
      assertTrue(f.session(second).path("revoked").asBoolean());
      assertEquals(2, f.verifications.get());
    }
  }

  @Test
  void departmentStrategyFailsOnceWhileInformationLookupRemainsInformational() throws Exception {
    try (var f = new Fixture("department", 3)) {
      String sid = f.login().session().session().path("sessionId").asString();
      f.run();
      f.departmentStatus.set(500);
      f.time.advance(7231);
      f.run();
      assertEquals(2, f.departments.get());
      assertEquals(0, f.verifications.get());
      assertTrue(f.session(sid).path("revoked").asBoolean());
      f.run();
      assertEquals(2, f.departments.get());
    }
  }

  @Test
  void lateResponseCannotReviveLogoutAndOtherReplicaDoesNotSendConcurrentVerification()
      throws Exception {
    try (var f = new Fixture("token-verify", 3);
        var pool = Executors.newSingleThreadExecutor()) {
      String sid = f.login().session().session().path("sessionId").asString();
      f.run();
      f.time.advance(7231);
      var entered = new CountDownLatch(1);
      var release = new CountDownLatch(1);
      f.verifyEntered.set(entered);
      f.verifyRelease.set(release);
      var running = pool.submit(f::run);
      assertTrue(entered.await(10, TimeUnit.SECONDS));
      var replica = new UacWorker(f.uac, f.audit);
      try {
        f.call(replica::runOnce);
      } finally {
        replica.destroy();
      }
      assertEquals(1, f.verifications.get());
      f.call(
          () ->
              store.tx(
                  () -> {
                    f.sessions.revokeOne(store.byId("session", sid, true), "logout");
                    return null;
                  }));
      release.countDown();
      running.get(15, TimeUnit.SECONDS);
      assertTrue(f.session(sid).path("revoked").asBoolean());
      assertThrows(AurigaException.class, () -> f.read(sid));
      f.call(
          () ->
              store.tx(
                  () -> {
                    var credential =
                        store.byId(
                            "uac_credential",
                            f.session(sid).path("uacCredentialId").asString(),
                            false);
                    assertEquals(1, credential.path("version").asInt());
                    return null;
                  }));
    }
  }
}
