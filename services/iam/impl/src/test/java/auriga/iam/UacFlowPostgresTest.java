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
    final LoginManager login;
    final IdentityPolicyHooks hooks = mock(IdentityPolicyHooks.class);
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
    final String envelopeKey = UacProviderPostgresTest.key();

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
              Map.of("initial", envelopeKey));
      uac = new UacSessions(store, cipher, adapter, providers, sessions, time, policy);
      when(hooks.uacMember(any()))
          .thenReturn(Optional.of(new IdentityPolicyHooks.UacMember("Test member", "member")));
      login =
          new LoginManager(
              store,
              secrets,
              new ContractSchemas(),
              registry,
              mock(EmergencyAccounts.class),
              mock(TeamAccounts.class),
              sessions,
              hooks,
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
                          id + ":001AB",
                          Json.object(
                              "providerId",
                              id,
                              "accountId",
                              account.toString(),
                              "subject",
                              "001AB"),
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
  void onlineEnvelopeRotationIsBoundedAndHistoricalKeyRemovalFailsUntilAllPathsMigrate()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      String sid = f.login().session().session().path("sessionId").asString();
      var nav = navigation(f, "true");
      f.call(() -> nav.issue(navigationInput(f, "portal"), false));
      String nextKey = UacProviderPostgresTest.key();
      var ring =
          Json.object(
              "activeKeyId", "next", "decryptKeyIds", List.of("initial", "next"), "revision", 1);
      var cipher =
          new UacCredentialCipher(
              new ContractSchemas(), ring, Map.of("initial", f.envelopeKey, "next", nextKey));
      var nextOnly =
          new UacCredentialCipher(
              new ContractSchemas(),
              Json.object("activeKeyId", "next", "decryptKeyIds", List.of("next"), "revision", 2),
              Map.of("next", nextKey));
      var guard = new UacKeyRotation(store, nextOnly);
      var rotation = new UacKeyRotation(store, cipher);
      assertThrows(
          IllegalStateException.class,
          () ->
              f.call(
                  () -> {
                    guard.verifyTenantKeys();
                    return null;
                  }));
      f.call(
          () -> {
            rotation.verifyTenantKeys();
            return null;
          });
      int changed = 0;
      for (int i = 0; i < 20; i++) {
        int batch = f.call(() -> rotation.rotateBatch(1));
        assertTrue(batch <= 1);
        changed += batch;
        if (batch == 0) break;
      }
      assertTrue(changed >= 3);
      f.call(
          () -> {
            guard.verifyTenantKeys();
            return null;
          });
      f.call(
          () ->
              store.tx(
                  () -> {
                    var session = store.byId("session", sid, false);
                    var credential =
                        store.byId(
                            "uac_credential", session.path("uacCredentialId").asString(), false);
                    assertEquals("next", credential.path("envelope").path("keyId").asString());
                    assertEquals(
                        f.token,
                        Json.read(
                                nextOnly.decrypt(
                                    credential.path("envelope"),
                                    f.tenant,
                                    UUID.fromString(f.provider)))
                            .path("token")
                            .asString());
                    assertEquals(1, credential.path("version").asLong());
                    return null;
                  }));
    }
  }

  @Test
  void diagnosticsRevisionIsIndependentCrossReplicaAndExpiryAndStorageFaultsFailClosed()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var providers =
          new UacProviderService(
              store, f.uac.providers, f.uac.adapter, f.sessions, f.audit, f.time);
      var first = new UacDiagnosticSettings(store, providers, f.audit, f.time);
      var second = new UacDiagnosticSettings(store, providers, f.audit, f.time);
      String before =
          f.call(() -> store.tx(() -> UacProviderService.etag(providers.row(f.provider, false))));
      var off = f.call(() -> first.get(f.provider));
      assertFalse(off.path("enabled").asBoolean());
      assertEquals(-1L, f.call(() -> first.capture(f.provider)));
      assertEquals(
          "common.precondition_failed",
          assertThrows(
                  AurigaException.class,
                  () -> f.call(() -> first.put(f.provider, Json.object("enabled", true), null)))
              .errorCode()
              .code());
      var on =
          f.call(
              () ->
                  first.put(
                      f.provider,
                      Json.object("enabled", true, "ttlSeconds", 3),
                      UacDiagnosticSettings.etag(off)));
      assertEquals(on, f.call(() -> second.get(f.provider)));
      long revision = on.path("revision").asLong();
      assertTrue(f.call(() -> second.permits(f.provider, revision)));
      assertEquals(
          before,
          f.call(() -> store.tx(() -> UacProviderService.etag(providers.row(f.provider, false)))));
      f.time.advance(3);
      assertFalse(f.call(() -> second.get(f.provider)).path("enabled").asBoolean());
      assertFalse(f.call(() -> first.permits(f.provider, revision)));
      var restarted = new UacDiagnosticSettings(store, providers, f.audit, f.time);
      var newOn =
          f.call(
              () ->
                  restarted.put(
                      f.provider, Json.object("enabled", true), UacDiagnosticSettings.etag(on)));
      assertEquals(
          900,
          Duration.between(f.time.instant(), Instant.parse(newOn.path("expiresAt").asString()))
              .toSeconds());
      assertFalse(f.call(() -> first.permits(f.provider, revision)));
      f.call(
          () ->
              second.put(
                  f.provider, Json.object("enabled", false), UacDiagnosticSettings.etag(newOn)));
      assertFalse(f.call(() -> restarted.get(f.provider)).path("enabled").asBoolean());
      TenantContext.run(
          UUID.randomUUID(),
          () -> assertThrows(AurigaException.class, () -> first.get(f.provider)));
      var broken = mock(IamStore.class);
      when(broken.tx(any())).thenThrow(new IllegalStateException("storage unavailable"));
      assertEquals(
          -1, new UacDiagnosticSettings(broken, providers, f.audit, f.time).capture(f.provider));
      verify(f.audit, atLeast(3)).record(argThat(e -> e.action().equals("iam.provider.update")));
    }
  }

  @Test
  void invitationOwnerCommitCanSurviveIamRollbackWithoutSecondRedemptionOrNewAccount()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      f.call(
          () ->
              store.tx(
                  () -> {
                    store.delete("identity", f.provider + ":001AB");
                    return null;
                  }));
      AtomicReference<UUID> canonical = new AtomicReference<>();
      when(f.hooks.resolveUacEmployee(any(), any()))
          .thenAnswer(i -> Optional.ofNullable(canonical.get()));
      when(f.hooks.consumeUacInvitation(
              eq("trusted-invite"), eq(UUID.fromString(f.provider)), eq("001AB"), any()))
          .thenAnswer(
              i -> {
                assertTrue(canonical.compareAndSet(null, i.getArgument(3)));
                return true;
              });
      AtomicBoolean fail = new AtomicBoolean(true);
      when(f.hooks.uacMember(any()))
          .thenAnswer(
              i -> {
                if (fail.getAndSet(false))
                  throw new IllegalStateException("simulated IAM commit boundary failure");
                return Optional.of(new IdentityPolicyHooks.UacMember("Owner member", "external"));
              });
      var c =
          f.call(
              () ->
                  f.flow.create(
                      Json.object("providerId", f.provider, "invitationCode", "trusted-invite"),
                      "portal",
                      "https://portal.example.com",
                      "127.0.0.1",
                      null,
                      null));
      f.run();
      assertThrows(IllegalStateException.class, () -> f.complete(c));
      assertNotNull(canonical.get());
      f.call(
          () ->
              store.tx(
                  () -> {
                    assertNull(store.get("account", canonical.get().toString(), false));
                    assertEquals(
                        "verified",
                        store
                            .get("uac_challenge", c.challenge().path("id").asString(), false)
                            .path("state")
                            .asString());
                    return null;
                  }));
      var result = f.complete(c);
      assertEquals(
          canonical.get().toString(), result.session().session().path("accountId").asString());
      verify(f.hooks, times(1)).consumeUacInvitation(any(), any(), any(), any());
      f.call(
          () ->
              store.tx(
                  () -> {
                    assertEquals(
                        "external",
                        store
                            .get("account", canonical.get().toString(), false)
                            .path("memberType")
                            .asString());
                    return null;
                  }));
    }
  }

  @Test
  void unknownEmployeeCannotJoinWithoutVerifiedInvitationAndInactiveOwnerCannotLogin()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var c = f.begin();
      f.run();
      when(f.hooks.uacMember(any())).thenReturn(Optional.empty());
      assertEquals(
          "iam.account_disabled",
          assertThrows(AurigaException.class, () -> f.complete(c)).errorCode().code());
      f.call(
          () ->
              store.tx(
                  () -> {
                    store.delete("identity", f.provider + ":001AB");
                    return null;
                  }));
      assertEquals(
          "iam.admission_denied",
          assertThrows(AurigaException.class, () -> f.complete(c)).errorCode().code());
      f.call(
          () ->
              store.tx(
                  () -> {
                    assertTrue(store.all("session").isEmpty());
                    return null;
                  }));
    }
  }

  @Test
  void linkCompletionRechecksSourceGenerationAndAuthenticationFreshness() throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var source = f.login().session().session();
      var c =
          f.call(
              () ->
                  f.flow.create(
                      Json.object("providerId", f.provider, "purpose", "link"),
                      "portal",
                      "https://portal.example.com",
                      "127.0.0.1",
                      null,
                      source));
      f.run();
      f.call(
          () ->
              store.tx(
                  () -> {
                    var row = store.byId("session", source.path("id").asString(), true);
                    row.put("authGeneration", row.path("authGeneration").asLong() + 1);
                    row.withObject("/uacSessionBinding")
                        .put("authGeneration", row.path("authGeneration").asLong());
                    store.save("session", row);
                    return null;
                  }));
      assertEquals(
          "iam.unauthenticated",
          assertThrows(AurigaException.class, () -> f.complete(c)).errorCode().code());
      var stale = source.deepCopy().put("authTime", f.time.instant().minusSeconds(601).toString());
      assertEquals(
          "iam.step_up_required",
          assertThrows(
                  AurigaException.class,
                  () ->
                      f.call(
                          () ->
                              f.flow.create(
                                  Json.object("providerId", f.provider, "purpose", "link"),
                                  "portal",
                                  "https://portal.example.com",
                                  "127.0.0.1",
                                  null,
                                  stale)))
              .errorCode()
              .code());
      assertEquals(
          "iam.step_up_required",
          assertThrows(
                  AurigaException.class,
                  () ->
                      f.call(
                          () ->
                              f.flow.create(
                                  Json.object("providerId", f.provider, "purpose", "link"),
                                  "portal",
                                  "https://portal.example.com",
                                  "127.0.0.1",
                                  null,
                                  source
                                      .deepCopy()
                                      .put(
                                          "authTime",
                                          f.time.instant().plusSeconds(31).toString()))))
              .errorCode()
              .code());
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

  static UacNavigation navigation(Fixture f, String enabled) {
    return new UacNavigation(
        store,
        f.flow,
        f.uac,
        new UacProviderService(store, f.uac.providers, f.uac.adapter, f.sessions, f.audit, f.time),
        f.login,
        f.time,
        enabled);
  }

  static ObjectNode navigationInput(Fixture f, String continuation) {
    return Json.object(
        "requestId",
        UUID.randomUUID().toString(),
        "providerId",
        f.provider,
        "origin",
        "https://portal.example.com",
        "continuation",
        continuation,
        "fetchMetadata",
        Json.object("dest", "document", "mode", "navigate", "site", "none", "user", "?1"));
  }

  static ObjectNode ssoInput(Fixture f, ObjectNode issued, String mode) {
    return Json.object(
        "providerId",
        f.provider,
        "navigationProof",
        issued.path("context").path("navigationProof"),
        "mode",
        mode);
  }

  static UacNavigation.Prepared prepare(
      Fixture f,
      UacNavigation nav,
      ObjectNode issued,
      String mode,
      String suppression,
      String session) {
    return f.call(
        () ->
            nav.prepare(
                ssoInput(f, issued, mode),
                issued.path("transactionCookie").asString(),
                issued.path("csrfCookie").asString(),
                issued.path("csrfCookie").asString(),
                suppression,
                session,
                "https://portal.example.com"));
  }

  static ObjectNode ssoCookies(Fixture f) {
    return Json.object(
        "test_portal_token", List.of(f.token), "test_portal_account", List.of("001Ab"));
  }

  @Test
  void portalSsoSavesVerifiedNewTokenAndSharesTheInformationJobWithoutQr() throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var nav = navigation(f, "true");
      ObjectNode input = navigationInput(f, "portal");
      var issued = f.call(() -> nav.issue(input, false));
      assertEquals(issued, f.call(() -> nav.issue(input, false)));
      assertTrue(issued.path("context").path("autoEligible").asBoolean());
      assertFalse(issued.path("context").path("autoSuppressed").asBoolean());
      assertTrue(SessionManager.canonicalCsrf(issued.path("csrfCookie").asString()));
      var prepared = prepare(f, nav, issued, "auto", null, null);
      JsonNode verified = f.call(() -> nav.verify(prepared, ssoCookies(f)));
      var completed =
          f.call(() -> nav.finish(prepared, verified, issued.path("transactionCookie").asString()));
      assertEquals("authenticated", completed.state());
      assertEquals(0, f.scans.get());
      assertEquals(1, f.verifications.get());
      String sid = completed.result().session().session().path("sessionId").asString();
      assertEquals(f.replacement, f.read(sid).path("token").asString());
      assertEquals(0, f.departments.get());
      f.run();
      f.run();
      assertEquals(1, f.departments.get());
      assertEquals(
          issued.path("context").path("transactionId").asString(),
          completed.result().transaction().path("id").asString());
      assertEquals(
          completed.correlation(),
          f.call(
              () ->
                  nav.correlation(
                      ssoInput(f, issued, "auto"),
                      issued.path("transactionCookie").asString(),
                      "https://portal.example.com")));
      assertEquals(
          "common.conflict",
          assertThrows(AurigaException.class, () -> prepare(f, nav, issued, "auto", null, null))
              .errorCode()
              .code());
    }
  }

  @Test
  void replacementFencesLateSsoAndCompletedQrAndOnlyOneSuccessorWins() throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var nav = navigation(f, "true");
      var issued = f.call(() -> nav.issue(navigationInput(f, "portal"), false));
      var prepared = prepare(f, nav, issued, "auto", null, null);
      var verified = f.call(() -> nav.verify(prepared, ssoCookies(f)));
      var qr =
          f.call(
              () ->
                  f.flow.create(
                      Json.object("providerId", f.provider),
                      "portal",
                      "https://portal.example.com",
                      "127.0.0.1",
                      issued.path("transactionCookie").asString(),
                      null));
      assertEquals(
          "common.conflict",
          assertThrows(
                  AurigaException.class,
                  () ->
                      f.call(
                          () ->
                              nav.finish(
                                  prepared, verified, issued.path("transactionCookie").asString())))
              .errorCode()
              .code());
      f.run();
      var completed = f.complete(qr);
      String sid = completed.session().session().path("sessionId").asString();
      var next = navigationInput(f, "portal");
      next.put("transactionCookie", qr.binding());
      f.call(() -> nav.issue(next, false));
      assertTrue(f.session(sid).path("revoked").asBoolean());
      assertThrows(AurigaException.class, () -> f.complete(qr));
      assertThrows(
          AurigaException.class,
          () ->
              f.call(
                  () ->
                      nav.issue(
                          next.deepCopy().put("requestId", UUID.randomUUID().toString()), false)));
      assertEquals(1, f.verifications.get());
    }
  }

  @Test
  void navigationKeepsAcknowledgedSsoSessionButRevokesUnacknowledgedResponse() throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var nav = navigation(f, "true");
      var issued = f.call(() -> nav.issue(navigationInput(f, "portal"), false));
      var prepared = prepare(f, nav, issued, "auto", null, null);
      var verified = f.call(() -> nav.verify(prepared, ssoCookies(f)));
      var completed =
          f.call(() -> nav.finish(prepared, verified, issued.path("transactionCookie").asString()));
      String sid = completed.result().session().session().path("sessionId").asString();
      var next = navigationInput(f, "portal");
      next.put("transactionCookie", issued.path("transactionCookie").asString());
      next.put("csrfCookie", completed.result().session().csrf());
      var acknowledged = f.call(() -> nav.issue(next, false));
      assertFalse(f.session(sid).path("revoked").asBoolean());
      assertEquals(completed.result().session().csrf(), acknowledged.path("csrfCookie").asString());
      var other = f.call(() -> nav.issue(navigationInput(f, "portal"), false));
      var p = prepare(f, nav, other, "auto", null, null);
      var result =
          f.call(
              () ->
                  nav.finish(
                      p, nav.verify(p, ssoCookies(f)), other.path("transactionCookie").asString()));
      var retry = navigationInput(f, "portal");
      retry.put("transactionCookie", other.path("transactionCookie").asString());
      f.call(() -> nav.issue(retry, false));
      assertTrue(
          f.session(result.result().session().session().path("sessionId").asString())
              .path("revoked")
              .asBoolean());
    }
  }

  @Test
  void rejectedPortalCookieDoesNotTryAnotherAccountOrRestoreOldToken() throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      f.verifyBody.set(
          Json.object("code", Json.object("code", "0000"), "bo", Json.object("code", "denied")));
      var nav = navigation(f, "true");
      var issued = f.call(() -> nav.issue(navigationInput(f, "portal"), false));
      var p = prepare(f, nav, issued, "auto", null, null);
      var cookies = ssoCookies(f);
      cookies.set("test_uds_token", Json.MAPPER.valueToTree(List.of(Secrets.random())));
      cookies.set("test_uds_account", Json.MAPPER.valueToTree(List.of("AnotherUser")));
      var verified = f.call(() -> nav.verify(p, cookies));
      assertFalse(verified.path("success").asBoolean());
      assertEquals(
          "scan_required",
          f.call(() -> nav.finish(p, verified, issued.path("transactionCookie").asString()))
              .state());
      assertEquals(1, f.verifications.get());
      assertEquals(0, f.departments.get());
      f.call(
          () ->
              store.tx(
                  () -> {
                    assertTrue(store.all("session").isEmpty());
                    return null;
                  }));
    }
  }

  @Test
  void navigationCsrfOriginBindingAndReplacementFailClosed() throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var nav = navigation(f, "true");
      var input = navigationInput(f, "portal");
      var issued = f.call(() -> nav.issue(input, false));
      var body = ssoInput(f, issued, "auto");
      String binding = issued.path("transactionCookie").asString(),
          csrf = issued.path("csrfCookie").asString();
      assertEquals(
          "common.forbidden",
          assertThrows(
                  AurigaException.class,
                  () ->
                      f.call(
                          () ->
                              nav.prepare(
                                  body,
                                  binding,
                                  csrf,
                                  Secrets.random(),
                                  null,
                                  null,
                                  "https://portal.example.com")))
              .errorCode()
              .code());
      assertEquals(
          "common.forbidden",
          assertThrows(
                  AurigaException.class,
                  () ->
                      f.call(
                          () ->
                              nav.prepare(
                                  body,
                                  binding,
                                  csrf,
                                  csrf,
                                  null,
                                  null,
                                  "https://other.example.com")))
              .errorCode()
              .code());
      assertEquals(
          "common.forbidden",
          assertThrows(
                  AurigaException.class,
                  () ->
                      f.call(
                          () ->
                              nav.prepare(
                                  body,
                                  Secrets.random(),
                                  csrf,
                                  csrf,
                                  null,
                                  null,
                                  "https://portal.example.com")))
              .errorCode()
              .code());
      var next = navigationInput(f, "portal");
      next.put("transactionCookie", binding);
      next.put("csrfCookie", csrf);
      var replacement = f.call(() -> nav.issue(next, false));
      assertEquals(csrf, replacement.path("csrfCookie").asString());
      assertNotEquals(binding, replacement.path("transactionCookie").asString());
      assertThrows(AurigaException.class, () -> prepare(f, nav, issued, "auto", null, null));
      assertEquals(0, f.verifications.get());
      TenantContext.run(
          UUID.randomUUID(),
          () ->
              assertThrows(
                  AurigaException.class,
                  () ->
                      nav.prepare(
                          body, binding, csrf, csrf, null, null, "https://portal.example.com")));
    }
  }

  @Test
  void logoutSuppressionPersistsAcrossExplicitLoginAndExpiredOldCookieNeverRestoresAuto()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var nav = navigation(f, "true");
      String suppression = f.call(() -> store.tx(() -> nav.suppress("https://portal.example.com")));
      var input = navigationInput(f, "portal");
      input.put("suppressionCookie", suppression);
      var issued = f.call(() -> nav.issue(input, false));
      assertTrue(issued.path("context").path("autoSuppressed").asBoolean());
      var auto = prepare(f, nav, issued, "auto", suppression, null);
      assertFalse(auto.attempt());
      var skipped =
          f.call(
              () ->
                  nav.finish(
                      auto,
                      nav.verify(auto, ssoCookies(f)),
                      issued.path("transactionCookie").asString()));
      assertEquals("scan_required", skipped.state());
      assertEquals(0, f.verifications.get());
      var second = navigationInput(f, "portal");
      second.put("suppressionCookie", suppression);
      var explicit = f.call(() -> nav.issue(second, false));
      var prepared = prepare(f, nav, explicit, "continue", suppression, null);
      var verified = f.call(() -> nav.verify(prepared, ssoCookies(f)));
      assertEquals(
          "authenticated",
          f.call(
                  () ->
                      nav.finish(prepared, verified, explicit.path("transactionCookie").asString()))
              .state());
      assertTrue(
          f.call(() -> store.tx(() -> nav.suppressed(suppression, "https://portal.example.com"))));
      f.time.advance(86401);
      assertTrue(
          f.call(() -> store.tx(() -> nav.suppressed(suppression, "https://portal.example.com"))));
    }
  }

  @Test
  void deviceNavigationResolvesOnlyPendingOwnUserCodeAndNeverAutomaticallyVerifiesOrApproves()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var nav = navigation(f, "true");
      String id = UUID.randomUUID().toString();
      f.call(
          () ->
              store.tx(
                  () -> {
                    store.insert(
                        "oauth_grant",
                        id,
                        Json.object(
                            "kind",
                            "device",
                            "clientId",
                            "test-client",
                            "userCode",
                            "BCDF-GHJK",
                            "status",
                            "pending",
                            "used",
                            false,
                            "expiresAt",
                            f.time.instant().plusSeconds(600).toString()),
                        f.time.instant().plusSeconds(600));
                    return null;
                  }));
      var input = navigationInput(f, "device");
      input.put("userCode", "BCDF-GHJK");
      var issued = f.call(() -> nav.issue(input, true));
      assertFalse(issued.toString().contains("deviceGrantId"));
      assertFalse(issued.path("context").path("autoEligible").asBoolean());
      var auto = prepare(f, nav, issued, "auto", null, null);
      assertFalse(auto.attempt());
      assertFalse(f.call(() -> nav.verify(auto, ssoCookies(f))).path("success").asBoolean());
      assertEquals(0, f.verifications.get());
      var next = navigationInput(f, "device");
      next.put("userCode", "BCDF-GHJK");
      var explicit = f.call(() -> nav.issue(next, true));
      var prepared = prepare(f, nav, explicit, "continue", null, null);
      JsonNode verified = f.call(() -> nav.verify(prepared, ssoCookies(f)));
      assertEquals(
          "authenticated",
          f.call(
                  () ->
                      nav.finish(prepared, verified, explicit.path("transactionCookie").asString()))
              .state());
      f.call(
          () ->
              store.tx(
                  () -> {
                    assertEquals(
                        "pending", store.get("oauth_grant", id, false).path("status").asString());
                    return null;
                  }));
      var forged = navigationInput(f, "device");
      forged.put("userCode", "BCDF-GHJK");
      forged.put("deviceGrantId", id);
      assertThrows(AurigaException.class, () -> f.call(() -> nav.issue(forged, true)));
      TenantContext.run(
          UUID.randomUUID(),
          () ->
              assertEquals(
                  "common.not_found",
                  assertThrows(AurigaException.class, () -> nav.issue(input, true))
                      .errorCode()
                      .code()));
    }
  }

  @Test
  void missingCookiesDisabledSsoAndExistingSessionDoNotCallUpstreamOrSwitchIdentity()
      throws Exception {
    try (var f = new Fixture("token-verify", 3)) {
      var off = navigation(f, "false");
      var issued = f.call(() -> off.issue(navigationInput(f, "portal"), false));
      var skipped = prepare(f, off, issued, "continue", null, null);
      assertFalse(skipped.attempt());
      assertFalse(f.call(() -> off.verify(skipped, ssoCookies(f))).path("success").asBoolean());
      var nav = navigation(f, "true");
      var empty = f.call(() -> nav.issue(navigationInput(f, "portal"), false));
      var noCookies = prepare(f, nav, empty, "auto", null, null);
      assertFalse(f.call(() -> nav.verify(noCookies, Json.object())).path("success").asBoolean());
      assertEquals(0, f.verifications.get());
      var existing = f.login();
      var next = f.call(() -> nav.issue(navigationInput(f, "portal"), false));
      var keep = prepare(f, nav, next, "auto", null, existing.session().cookie());
      assertFalse(keep.attempt());
      var kept =
          f.call(
              () ->
                  nav.finish(
                      keep,
                      nav.verify(keep, ssoCookies(f)),
                      next.path("transactionCookie").asString()));
      assertEquals("authenticated", kept.state());
      assertNull(kept.result().session());
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
