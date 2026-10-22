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
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class AdminCsrfPostgresTest {
  static IamStore store;

  static final class Time extends Clock {
    volatile Instant now = Instant.parse("2026-01-01T00:00:00Z");

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return now;
    }
  }

  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), provider = UUID.randomUUID();
  final Time time = new Time();
  final ContractSchemas schemas = new ContractSchemas();
  final TokenService tokens = mock(TokenService.class);
  SessionManager sessions;
  SessionManager.Issued issued;
  SessionKv kv;
  AurigaPrincipal principal;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(AdminCsrfPostgresTest.class, "iam_owner", "iam_app", "iam");
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

  @BeforeEach
  void setup() {
    when(tokens.access(
            any(),
            anyString(),
            anyString(),
            anyString(),
            anyString(),
            any(),
            isNull(),
            any(),
            nullable(Instant.class),
            anyString(),
            anyString()))
        .thenReturn("signed-access-token");
    kv = new PostgresSessionKv(store, schemas, time);
    sessions =
        new SessionManager(
            store, kv, tokens, time, mock(EventPublisher.class), mock(AuditLogger.class));
    issued =
        store.tenant(
            tenant,
            () -> {
              store.insert(
                  "account",
                  account.toString(),
                  Json.object("accountId", account, "status", "active"),
                  null);
              store.insert(
                  "idp_provider", provider.toString(), Json.object("providerId", provider), null);
              return sessions.create(
                  account, provider, "admin", true, Duration.ofHours(2), Duration.ofMinutes(30));
            });
    principal =
        new AurigaPrincipal(
            account.toString(),
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            issued.session().path("sessionId").asString(),
            null,
            "member",
            List.of(),
            Set.of(),
            time.instant(),
            time.instant(),
            "jti");
  }

  Instant register(String token) {
    return TenantContext.call(
        tenant, () -> sessions.registerAdminCsrf(issued.cookie(), token, principal));
  }

  VerifyAdminSessionCsrfResponse verify(String cookie, String token) {
    return TenantContext.call(
        tenant,
        () ->
            sessions.verifyAdminCsrf(
                VerifyAdminSessionCsrfRequest.newBuilder()
                    .setSessionToken(cookie)
                    .setCsrfToken(token)
                    .build()));
  }

  ObjectNode row() {
    return store.tenant(tenant, () -> store.byId("session", principal.sessionId(), false));
  }

  static void error(String code, org.junit.jupiter.api.function.Executable work) {
    assertEquals(code, assertThrows(AurigaException.class, work).errorCode().code());
  }

  @Test
  void registrationHashesOnlyIsIdempotentAndTouchCannotExtendIt() {
    String token = Secrets.random();
    Instant expiry = register(token);
    assertEquals(time.instant().plusSeconds(900), expiry);
    time.now = time.now.plusSeconds(30);
    assertEquals(expiry, register(token));
    TenantContext.call(
        tenant,
        () ->
            sessions.touch(
                TouchSessionRequest.newBuilder()
                    .setSessionToken(issued.cookie())
                    .setAudience(SessionAudience.SESSION_AUDIENCE_ADMIN)
                    .build()));
    assertEquals(expiry, register(token));
    var row = row();
    assertEquals(1, row.path("adminCsrfTokens").size());
    assertFalse(row.toString().contains(token));
    assertEquals(Secrets.hash(token), row.path("adminCsrfTokens").get(0).path("hash").asString());
    assertTrue(verify(issued.cookie(), token).getActive());
    var negative = verify(issued.cookie(), Secrets.random());
    assertFalse(negative.getActive());
    assertFalse(negative.hasSession());
    time.now = expiry;
    assertFalse(verify(issued.cookie(), token).getActive());
    register(Secrets.random());
    assertEquals(1, row().path("adminCsrfTokens").size());
  }

  @Test
  void thirtyTwoConcurrentFirstRegistrationsHaveNoLostUpdatesAndFullDoesNotEvict()
      throws Exception {
    var start = new CountDownLatch(1);
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var futures = new ArrayList<Future<Instant>>();
      for (int i = 0; i < 32; i++)
        futures.add(
            pool.submit(
                () -> {
                  start.await();
                  return register(Secrets.random());
                }));
      start.countDown();
      for (var future : futures)
        assertEquals(time.instant().plusSeconds(900), future.get(20, TimeUnit.SECONDS));
    }
    assertEquals(32, row().path("adminCsrfTokens").size());
    var full = assertThrows(AurigaException.class, () -> register(Secrets.random()));
    assertEquals("common.rate_limited", full.errorCode().code());
    assertEquals(900L, full.params().get("retryAfter"));
    assertEquals(32, row().path("adminCsrfTokens").size());
  }

  @Test
  void concurrentDuplicateHashUsesOneSlotAndRollbackPublishesNothing() throws Exception {
    String token = Secrets.random();
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var calls = new ArrayList<Callable<Instant>>();
      for (int i = 0; i < 8; i++) calls.add(() -> register(token));
      for (var result : pool.invokeAll(calls))
        assertEquals(time.instant().plusSeconds(900), result.get());
    }
    assertEquals(1, row().path("adminCsrfTokens").size());
    String rolledBack = Secrets.random();
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                tenant,
                () -> {
                  register(rolledBack);
                  throw new IllegalStateException("rollback");
                }));
    assertEquals(1, row().path("adminCsrfTokens").size());
    assertFalse(verify(issued.cookie(), rolledBack).getActive());
    assertEquals(
        1,
        store
            .tenant(tenant, () -> kv.get(Secrets.hash(issued.cookie())))
            .path("adminCsrfTokens")
            .size());
  }

  @Test
  void sessionExpirySubjectTenantAndAudienceAreAuthoritative() {
    var wrong =
        new AurigaPrincipal(
            UUID.randomUUID().toString(),
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            principal.sessionId(),
            null,
            "member",
            List.of(),
            Set.of(),
            time.instant(),
            time.instant(),
            "jti");
    error(
        "common.unauthenticated",
        () ->
            TenantContext.call(
                tenant,
                () -> sessions.registerAdminCsrf(issued.cookie(), Secrets.random(), wrong)));
    error(
        "common.unauthenticated",
        () ->
            TenantContext.call(
                UUID.randomUUID(),
                () -> sessions.registerAdminCsrf(issued.cookie(), Secrets.random(), principal)));
    var portal =
        store.tenant(
            tenant,
            () ->
                sessions.create(
                    account, provider, "portal", true, Duration.ofHours(1), Duration.ofHours(1)));
    error("common.unauthenticated", () -> verify(portal.cookie(), Secrets.random()));
    store.tenant(
        tenant,
        () -> {
          var row = store.byId("session", principal.sessionId(), true);
          row.put("idleExpiresAt", time.instant().plusSeconds(20).toString());
          store.save("session", row);
          return null;
        });
    assertEquals(time.instant().plusSeconds(20), register(Secrets.random()));
    time.now = time.now.plusSeconds(20);
    error("common.unauthenticated", () -> register(Secrets.random()));
    error("common.unauthenticated", () -> verify(issued.cookie(), Secrets.random()));
  }

  @Test
  void committedRevocationRejectsNewChecksAndStaleStepupCannotResurrectIt() {
    String token = Secrets.random();
    register(token);
    ObjectNode stale = row();
    store.tenant(
        tenant,
        () -> {
          sessions.revokeOne(stale, "user");
          return null;
        });
    error("common.unauthenticated", () -> register(Secrets.random()));
    error("common.unauthenticated", () -> verify(issued.cookie(), token));
    error("common.unauthenticated", () -> TenantContext.run(tenant, () -> sessions.stepUp(stale)));
    assertFalse(
        TenantContext.call(
                tenant,
                () ->
                    sessions.touch(
                        TouchSessionRequest.newBuilder()
                            .setSessionToken(issued.cookie())
                            .setAudience(SessionAudience.SESSION_AUDIENCE_ADMIN)
                            .build()))
            .getActive());
    store.tenant(
        tenant,
        () -> {
          sessions.permissionsChanged(account.toString());
          sessions.revokeOne(stale, "user");
          return null;
        });
    assertTrue(row().path("revoked").asBoolean());
    assertFalse(row().has("adminCsrfTokens"));
    assertNull(store.tenant(tenant, () -> kv.get(Secrets.hash(issued.cookie()))));
  }

  @Test
  void malformedTokensNeverOccupySlotsAndUnavailableDatabaseFailsClosed() {
    for (String token :
        List.of("", Secrets.random() + "=", "A".repeat(42) + "B", "A".repeat(42), "A".repeat(44)))
      error("common.validation_failed", () -> register(token));
    assertFalse(row().has("adminCsrfTokens"));
    var broken = mock(IamStore.class);
    when(broken.tx(any()))
        .thenThrow(new org.springframework.dao.DataAccessResourceFailureException("offline"));
    var manager =
        new SessionManager(
            broken, kv, tokens, time, mock(EventPublisher.class), mock(AuditLogger.class));
    error(
        "common.unavailable",
        () ->
            TenantContext.call(
                tenant,
                () -> manager.verifyAdminCsrf(VerifyAdminSessionCsrfRequest.getDefaultInstance())));
  }

  @Test
  void secondaryRunsAfterCommitAndDelayedProjectionRereadsCommittedRevocation() {
    SessionKv secondary = mock(SessionKv.class);
    var composite = new CompositeSessionKv(store, kv, secondary, time);
    sessions =
        new SessionManager(
            store, composite, tokens, time, mock(EventPublisher.class), mock(AuditLogger.class));
    String token = Secrets.random();
    register(token);
    assertNotNull(store.tenant(tenant, () -> store.claimSessionProjection(time.instant())));
    verifyNoInteractions(secondary);
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                tenant,
                () -> {
                  register(Secrets.random());
                  throw new IllegalStateException("rollback");
                }));
    verifyNoInteractions(secondary);
    store.tenant(
        tenant,
        () -> {
          sessions.revokeOne(row(), "user");
          return null;
        });
    assertEquals(1, composite.forwardOnce());
    assertEquals(0, composite.forwardOnce());
    org.mockito.Mockito.verify(secondary, never()).put(anyString(), any());
    org.mockito.Mockito.verify(secondary).delete(Secrets.hash(issued.cookie()));
  }

  @Test
  void revocationAndNewVerificationSerializeOnTheSameRow() throws Exception {
    String token = Secrets.random();
    register(token);
    var locked = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      Future<?> revoke =
          pool.submit(
              () ->
                  store.tenant(
                      tenant,
                      () -> {
                        ObjectNode row = store.byId("session", principal.sessionId(), true);
                        locked.countDown();
                        try {
                          if (!release.await(10, TimeUnit.SECONDS))
                            throw new IllegalStateException("timeout");
                        } catch (InterruptedException e) {
                          throw new IllegalStateException(e);
                        }
                        sessions.revokeOne(row, "user");
                        return null;
                      }));
      assertTrue(locked.await(10, TimeUnit.SECONDS));
      Future<?> verification =
          pool.submit(() -> error("common.unauthenticated", () -> verify(issued.cookie(), token)));
      assertThrows(TimeoutException.class, () -> verification.get(100, TimeUnit.MILLISECONDS));
      release.countDown();
      revoke.get(10, TimeUnit.SECONDS);
      verification.get(10, TimeUnit.SECONDS);
    } finally {
      release.countDown();
    }
    assertTrue(row().path("revoked").asBoolean());
  }

  @Test
  void installationIssuerFileRefreshFeedsExistingGoEdgeMutualTlsRpc(
      @org.junit.jupiter.api.io.TempDir java.nio.file.Path dir) throws Exception {
    String csrf = Secrets.random();
    register(csrf);
    EdgeTokenEdgeIntegration.run(dir, time, sessions, tenant, account, issued, principal, csrf);
  }

  @Test
  void rpcUsesRealMutualTlsInternalJwtAndDelegatedTenant(
      @org.junit.jupiter.api.io.TempDir java.nio.file.Path dir) throws Exception {
    String csrf = Secrets.random();
    register(csrf);
    var jwk =
        new com.nimbusds.jose.jwk.gen.ECKeyGenerator(com.nimbusds.jose.jwk.Curve.P_256)
            .keyID("rpc-test")
            .generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(jwk);
    String secret = Secrets.random();
    var issuer =
        new TokenService(
            keys,
            schemas,
            (tid, sub, ctx) -> Set.of(),
            time,
            "https://portal.example.com",
            Map.of("edge", Secrets.hash(secret), "org", Secrets.hash(secret)));
    var settings =
        new AccessTokenSettings(
            "https://portal.example.com",
            java.net.URI.create("https://portal.example.com/jwks"),
            null,
            null,
            null,
            null,
            time,
            false);
    var source =
        new com.nimbusds.jose.jwk.source.ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext>(
            new com.nimbusds.jose.jwk.JWKSet(jwk.toPublicJWK()));
    var verifier = new AccessTokenVerifier(AccessTokenDecoders.createInternal(settings, source));
    var pki = new IamHopTlsTest();
    pki.dir = dir;
    var ca = IamHopTlsTest.certificate("test-ca", null, true);
    var serverCert = IamHopTlsTest.certificate("localhost", ca, false);
    var clientCert = IamHopTlsTest.certificate("edge.internal", ca, false);
    var caFile = pki.pem("ca.pem", "CERTIFICATE", ca.cert().getEncoded());
    var serverFile = pki.pem("server.pem", "CERTIFICATE", serverCert.cert().getEncoded());
    var serverKey =
        pki.pem("server-key.pem", "PRIVATE KEY", serverCert.key().getPrivate().getEncoded());
    var clientFile = pki.pem("client.pem", "CERTIFICATE", clientCert.cert().getEncoded());
    var clientKey =
        pki.pem("client-key.pem", "PRIVATE KEY", clientCert.key().getPrivate().getEncoded());
    var server =
        new auriga.common.grpc.GrpcServer(
            "127.0.0.1",
            0,
            List.of(new IamGrpcSessions(sessions)),
            new auriga.common.grpc.AurigaServerInterceptor(
                verifier, Set.of(), Set.of("svc:edge", "svc:org")),
            new auriga.common.grpc.GrpcServer.Tls(
                serverFile.toFile(), serverKey.toFile(), caFile.toFile()),
            false,
            Set.of());
    var channels = new ArrayList<io.grpc.ManagedChannel>();
    server.start();
    try {
      java.util.function.Function<String, SessionServiceGrpc.SessionServiceBlockingStub> stub =
          jwt -> {
            var channel =
                auriga.common.grpc.GrpcChannels.openTls(
                    "127.0.0.1:" + server.port(),
                    clientFile.toFile(),
                    clientKey.toFile(),
                    caFile.toFile(),
                    new auriga.common.grpc.AurigaClientInterceptor(
                        () -> jwt, Duration.ofSeconds(3)));
            channels.add(channel);
            return SessionServiceGrpc.newBlockingStub(channel)
                .withDeadlineAfter(3, TimeUnit.SECONDS);
          };
      var request =
          VerifyAdminSessionCsrfRequest.newBuilder()
              .setSessionToken(issued.cookie())
              .setCsrfToken(csrf)
              .build();
      var edge = stub.apply(issuer.issue("edge", secret));
      assertTrue(
          TenantContext.call(tenant, () -> edge.verifyAdminSessionCsrf(request)).getActive());
      var badCsrf =
          TenantContext.call(
              tenant,
              () ->
                  edge.verifyAdminSessionCsrf(
                      request.toBuilder().setCsrfToken(Secrets.random()).build()));
      assertFalse(badCsrf.getActive());
      assertFalse(badCsrf.hasSession());
      assertEquals(
          io.grpc.Status.Code.PERMISSION_DENIED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class, () -> edge.verifyAdminSessionCsrf(request))
              .getStatus()
              .getCode());
      assertEquals(
          io.grpc.Status.Code.UNAUTHENTICATED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () ->
                      TenantContext.call(
                          UUID.randomUUID(), () -> edge.verifyAdminSessionCsrf(request)))
              .getStatus()
              .getCode());
      var org = stub.apply(issuer.issue("org", secret));
      assertEquals(
          io.grpc.Status.Code.PERMISSION_DENIED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> TenantContext.call(tenant, () -> org.verifyAdminSessionCsrf(request)))
              .getStatus()
              .getCode());
      var invalid = stub.apply("invalid");
      assertEquals(
          io.grpc.Status.Code.UNAUTHENTICATED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> TenantContext.call(tenant, () -> invalid.verifyAdminSessionCsrf(request)))
              .getStatus()
              .getCode());
      var user =
          stub.apply(
              issuer.access(
                  tenant,
                  account.toString(),
                  "auriga",
                  "user",
                  "admin",
                  UUID.fromString(principal.sessionId()),
                  null,
                  time.instant(),
                  time.instant(),
                  "openid",
                  "member"));
      assertEquals(
          io.grpc.Status.Code.UNAUTHENTICATED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> TenantContext.call(tenant, () -> user.verifyAdminSessionCsrf(request)))
              .getStatus()
              .getCode());
      var ssl =
          io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts.forClient()
              .trustManager(caFile.toFile())
              .build();
      var noCertificate =
          io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder.forTarget(
                  "127.0.0.1:" + server.port())
              .sslContext(ssl)
              .build();
      channels.add(noCertificate);
      assertThrows(
          io.grpc.StatusRuntimeException.class,
          () ->
              SessionServiceGrpc.newBlockingStub(noCertificate)
                  .withDeadlineAfter(3, TimeUnit.SECONDS)
                  .verifyAdminSessionCsrf(request));
    } finally {
      for (var channel : channels) channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.stop();
    }
  }

  @Test
  void bootstrapUsesOwnAdminCookieAndCannotAcceptSessionIdsInJson() throws Exception {
    byte[] data =
        java.nio.file.Files.readString(
                java.nio.file.Path.of("../../../contracts/deployment/fixtures/ip-http.json"))
            .replace("019a0000-0000-7000-8000-000000000001", tenant.toString())
            .getBytes(java.nio.charset.StandardCharsets.UTF_8);
    var access = auriga.contracts.deploymentconfig.DeploymentAccess.load(data);
    var identity =
        new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    var controller = new AdminSessionCsrfController(identity, sessions);
    var req = HttpBoundaryTest.request(access, "admin");
    req.setMethod("POST");
    req.setContentType("application/json");
    req.addHeader("Origin", access.adminUrl());
    req.addHeader("X-Auriga-CSRF-Bootstrap", "admin-session-v1");
    req.setCookies(new jakarta.servlet.http.Cookie("auriga_admin_session", issued.cookie()));
    var res = new org.springframework.mock.web.MockHttpServletResponse();
    String token = Secrets.random();
    var result =
        auriga.common.context.RequestContext.start("csrf-http", null)
            .withPrincipal(principal)
            .call(() -> controller.register(Json.object("token", token), req, res));
    assertEquals(token, result.path("token").asString());
    assertEquals("no-store", res.getHeader("Cache-Control"));
    assertEquals("nosniff", res.getHeader("X-Content-Type-Options"));
    assertNull(res.getHeader("Set-Cookie"));
    error(
        "common.validation_failed",
        () ->
            auriga.common.context.RequestContext.start("csrf-http", null)
                .withPrincipal(principal)
                .run(
                    () ->
                        controller.register(
                            Json.object("token", Secrets.random(), "sid", principal.sessionId()),
                            req,
                            res)));
    req.setCookies(new jakarta.servlet.http.Cookie("auriga_session", issued.cookie()));
    error(
        "common.unauthenticated",
        () ->
            auriga.common.context.RequestContext.start("csrf-http", null)
                .withPrincipal(principal)
                .run(() -> controller.register(Json.object("token", Secrets.random()), req, res)));
    req.setCookies(new jakarta.servlet.http.Cookie("auriga_admin_session", issued.cookie()));
    req.removeHeader("Origin");
    req.addHeader("Origin", access.portalUrl());
    error(
        "common.forbidden",
        () ->
            auriga.common.context.RequestContext.start("csrf-http", null)
                .withPrincipal(principal)
                .run(() -> controller.register(Json.object("token", Secrets.random()), req, res)));
    req.removeHeader("Origin");
    req.addHeader("Origin", access.adminUrl());
    req.removeHeader("X-Auriga-CSRF-Bootstrap");
    error(
        "common.forbidden",
        () ->
            auriga.common.context.RequestContext.start("csrf-http", null)
                .withPrincipal(principal)
                .run(() -> controller.register(Json.object("token", Secrets.random()), req, res)));
  }
}
