package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.IdentityPolicyHooks;
import auriga.iam.web.model.CreateLoginTransaction;
import auriga.testsupport.NatsExtension;
import auriga.testsupport.PostgresExtension;
import com.zaxxer.hikari.HikariDataSource;
import jakarta.servlet.http.Cookie;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.*;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith({PostgresExtension.class, NatsExtension.class})
class LoginResponsePostgresTest {
  static final java.util.concurrent.atomic.AtomicInteger clientIds =
      new java.util.concurrent.atomic.AtomicInteger(20);
  final String clientIp = "192.0.2." + clientIds.incrementAndGet();
  static PostgresExtension.ServiceRoles roles;
  HikariDataSource source;
  IamStore store;
  io.nats.client.Connection connection;
  AnnotationConfigApplicationContext context;
  CompositeSessionKv projection;
  NatsSessionKv consumer;
  SessionManager sessions;
  IamLoginController controller;
  LoginManager login;
  Secrets secrets;
  SigningKeys keys;
  TokenService tokens;
  TeamAccounts team;
  EventPublisher events;
  ConnectorRegistry registry;
  RequestIdentity identity;
  IamMeController me;
  final ContractSchemas schemas = new ContractSchemas();
  final AdminCsrfPostgresTest.Time clock = new AdminCsrfPostgresTest.Time();
  final auriga.contracts.deploymentconfig.DeploymentAccess access =
      HttpBoundaryTest.fixture("domain-proxy.json");
  final UUID tenant = UUID.fromString(access.tenantId());
  final UUID provider = UUID.randomUUID();

  @BeforeAll
  static void migrate() {
    roles =
        PostgresExtension.serviceRoles(
            LoginResponsePostgresTest.class, "login_response_owner", "login_response_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
  }

  @BeforeEach
  void open() throws Exception {
    clock.now = Instant.now();
    source = new HikariDataSource();
    source.setJdbcUrl(roles.runtime().jdbcUrl());
    source.setMaximumPoolSize(5);
    assertEquals(30000, source.getConnectionTimeout());
    store = new IamStore(new JdbcTemplate(source), new TenantTransactionManager(source));
    connection = io.nats.client.Nats.connect(NatsExtension.url());
    consumer = new NatsSessionKv(connection, schemas);
    context = new AnnotationConfigApplicationContext();
    context.registerBean(IamStore.class, () -> store);
    context.registerBean(Clock.class, () -> clock);
    context.registerBean(io.nats.client.Connection.class, () -> connection);
    context.register(SessionProjectionPostgresTest.LifecycleAssembly.class);
    context.refresh();
    projection = assertInstanceOf(CompositeSessionKv.class, context.getBean(SessionKv.class));
    assertTrue(projection.isRunning());
    byte[] secret = new byte[32];
    new java.security.SecureRandom().nextBytes(secret);
    secrets = new Secrets(Base64.getEncoder().encodeToString(secret));
    keys = new SigningKeys(store, secrets, clock, Duration.ofSeconds(30));
    String kid = keys.publishNext();
    clock.now = clock.now.plusSeconds(31);
    keys.activate(kid);
    tokens =
        new TokenService(
            keys,
            schemas,
            (t, s, c) -> Set.of("workspace.read"),
            clock,
            access.portalUrl(),
            Map.of());
    events = mock(EventPublisher.class);
    var audit = mock(AuditLogger.class);
    sessions = new SessionManager(store, projection, tokens, clock, events, audit);
    team = new TeamAccounts(store, secrets, clock, events);
    var passkeys = new TeamPasskeys(store, secrets, clock, events);
    var qr = new QrChallenges(store, clock);
    registry =
        new ConnectorRegistry(
            schemas, List.of(new TeamAccountConnector(team, passkeys, qr, tokens.issuer())));
    login =
        new LoginManager(
            store,
            secrets,
            schemas,
            registry,
            new EmergencyAccounts(store, secrets, clock, false),
            team,
            sessions,
            mock(IdentityPolicyHooks.class),
            events,
            audit,
            clock,
            tokens.issuer());
    identity =
        new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    controller = new IamLoginController(login, identity, store, sessions, qr, secrets);
    me = new IamMeController(store, sessions, team, passkeys, null, clock, tokens);
    store.tenant(
        tenant,
        () -> {
          store.insert(
              "idp_provider",
              provider.toString(),
              Json.object(
                  "providerId",
                  provider,
                  "connectorType",
                  "team-account",
                  "enabled",
                  true,
                  "display",
                  Json.object("name", Json.object("en", "Team")),
                  "config",
                  secrets.encrypt(
                      Json.object(
                              "rpId",
                              "portal.example.com",
                              "origins",
                              List.of(access.portalUrl(), access.adminUrl()),
                              "primaryStep",
                              "form")
                          .toString(),
                      "provider:" + tenant + ":" + provider),
                  "admission",
                  Json.object("mode", "invite_only", "mfa", "never")),
              null);
          team.register(provider.toString(), "alice", "correct-horse", "Alice");
          UUID account = UUID.randomUUID();
          store.insert(
              "account",
              account.toString(),
              Json.object(
                  "accountId",
                  account,
                  "status",
                  "active",
                  "displayName",
                  "Alice",
                  "memberType",
                  "member",
                  "createdAt",
                  clock.instant().toString()),
              null);
          store.insert(
              "identity",
              provider + ":alice",
              Json.object(
                  "providerId",
                  provider,
                  "accountId",
                  account,
                  "subject",
                  "alice",
                  "profile",
                  Json.object("displayName", "Alice")),
              null);
          return null;
        });
  }

  @AfterEach
  void close() throws Exception {
    if (controller != null) controller.destroy();
    if (registry != null) registry.close();
    if (context != null) context.close();
    if (connection != null) connection.close();
    if (source != null) source.close();
  }

  MockHttpServletRequest request() {
    var request = HttpBoundaryTest.request(access, "admin");
    request.removeHeader("X-Auriga-Client-IP");
    request.addHeader("X-Auriga-Client-IP", clientIp);
    request.addHeader("Origin", access.adminUrl());
    return request;
  }

  record Pending(String id, MockHttpServletRequest request) {}

  Pending begin() {
    var request = request();
    var response = new MockHttpServletResponse();
    var input =
        new CreateLoginTransaction()
            .providerId(provider)
            .purpose(CreateLoginTransaction.PurposeEnum.LOGIN);
    var result = controller.begin(input, request, response);
    assertEquals("pending", result.path("status").asString());
    request.setCookies(
        new Cookie(
            "__Host-auriga_admin_login_tx",
            response.getCookie("__Host-auriga_admin_login_tx").getValue()));
    return new Pending(result.path("id").asString(), request);
  }

  ObjectNode complete(Pending pending, MockHttpServletResponse response) {
    return controller.advance(
        pending.id(),
        Json.object(
            "type",
            "form",
            "formId",
            "password",
            "values",
            Json.object("username", "alice", "password", "correct-horse")),
        pending.request(),
        response);
  }

  @Test
  void completedProductionLoginIsImmediatelyReadableByOriginalNatsConsumer() {
    var pending = begin();
    var response = new MockHttpServletResponse();
    var result = complete(pending, response);
    assertEquals("completed", result.path("status").asString());
    Cookie cookie = response.getCookie("__Host-auriga_admin_session");
    assertNotNull(cookie);
    var projected = consumer.get(Secrets.hash(cookie.getValue()));
    assertNotNull(
        projected,
        "A successful login response must already be consumable, without polling or replay");
    assertEquals(tenant.toString(), projected.path("tenantId").asString());
    assertEquals("admin", projected.path("audience").asString());
    assertNotNull(
        store.tenant(tenant, () -> sessions.activeById(projected.path("sessionId").asString())));
    assertImmediateMe(projected);
    assertRequestReleasedAndPoolBecomesIdle();
  }

  void assertImmediateMe(ObjectNode projected) {
    // Consume the exact access token from NATS, verify its real signature, and call the original
    // me.
    var claims = tokens.verify(projected.path("accessToken").asString());
    var principal =
        new auriga.common.security.AurigaPrincipal(
            claims.path("sub").asString(),
            UUID.fromString(claims.path("tid").asString()),
            auriga.common.security.PrincipalType.USER,
            auriga.common.security.TokenContext.ADMIN,
            claims.path("sid").asString(),
            null,
            claims.path("mtyp").asString(),
            List.of(),
            Set.of(),
            Instant.ofEpochSecond(claims.path("auth_time").asLong()),
            null,
            claims.path("jti").asString());
    ObjectNode result =
        TenantContext.call(
            tenant,
            () ->
                auriga.common.context.RequestContext.start("immediate-me", null)
                    .withPrincipal(principal)
                    .call(me::me));
    assertEquals(
        projected.path("sessionId").asString(), result.path("session").path("id").asString());
  }

  @Test
  void productionHttpRevokeResponseImmediatelyRemovesOriginalNatsProjection() {
    var response = new MockHttpServletResponse();
    complete(begin(), response);
    String hash = Secrets.hash(response.getCookie("__Host-auriga_admin_session").getValue());
    ObjectNode projected = assertNotNullValue(consumer.get(hash));
    asUser(projected, () -> controller.revoke(projected.path("sessionId").asString()));
    assertNull(
        consumer.get(hash),
        "A successful production revoke response must already reject the old cookie");
    asUser(projected, () -> controller.revoke(projected.path("sessionId").asString()));
    assertNull(consumer.get(hash));
  }

  void asUser(ObjectNode projected, Runnable work) {
    asUser(projected, false, work);
  }

  void asUser(ObjectNode projected, boolean steppedUp, Runnable work) {
    var claims = tokens.verify(projected.path("accessToken").asString());
    var principal =
        new auriga.common.security.AurigaPrincipal(
            claims.path("sub").asString(),
            tenant,
            auriga.common.security.PrincipalType.USER,
            auriga.common.security.TokenContext.ADMIN,
            claims.path("sid").asString(),
            null,
            "member",
            List.of(),
            Set.of(),
            clock.instant(),
            steppedUp ? clock.instant() : null,
            claims.path("jti").asString());
    TenantContext.run(
        tenant,
        () ->
            auriga.common.context.RequestContext.start("revoke-response", null)
                .withPrincipal(principal)
                .run(work));
  }

  ObjectNode assertNotNullValue(ObjectNode value) {
    assertNotNull(value);
    return value;
  }

  record Live(String hash, ObjectNode value, MockHttpServletRequest request) {}

  Live live() {
    var pending = begin();
    var response = new MockHttpServletResponse();
    complete(pending, response);
    var cookie = response.getCookie("__Host-auriga_admin_session");
    String hash = Secrets.hash(cookie.getValue());
    var value = assertNotNullValue(consumer.get(hash));
    var csrf = response.getCookie("__Host-auriga_csrf");
    assertNotNull(csrf);
    pending.request().setCookies(cookie, csrf);
    pending.request().addHeader("X-Auriga-CSRF", csrf.getValue());
    return new Live(hash, value, pending.request());
  }

  @Test
  void productionGrpcRevokeConfirmsBeforeObserverAndRetriesIdempotently() {
    var live = live();
    var principal =
        new auriga.common.security.AurigaPrincipal(
            "svc:org",
            tenant,
            auriga.common.security.PrincipalType.INTERNAL_SERVICE,
            auriga.common.security.TokenContext.SERVICE,
            null,
            null,
            null,
            List.of(),
            Set.of(),
            null,
            null,
            "grpc-revoke");
    var request =
        auriga.contracts.iam.v1.RevokeSessionsRequest.newBuilder()
            .setTenantId(tenant.toString())
            .setAccountId(live.value().path("accountId").asString())
            .addSessionIds(live.value().path("sessionId").asString())
            .setReason(auriga.contracts.iam.v1.RevokeReason.REVOKE_REASON_USER_DISABLED)
            .build();
    var replies = new ArrayList<Long>();
    var grpc = new IamGrpcSessions(sessions);
    var observer =
        new io.grpc.stub.StreamObserver<auriga.contracts.iam.v1.RevokeSessionsResponse>() {
          public void onNext(auriga.contracts.iam.v1.RevokeSessionsResponse response) {
            assertNull(consumer.get(live.hash()));
            replies.add(response.getRevoked());
          }

          public void onError(Throwable error) {
            fail(error);
          }

          public void onCompleted() {}
        };
    TenantContext.run(
        tenant,
        () ->
            auriga.common.context.RequestContext.start("grpc-revoke", null)
                .withPrincipal(principal)
                .run(
                    () -> {
                      grpc.revokeSessions(request, observer);
                      grpc.revokeSessions(request, observer);
                    }));
    assertEquals(List.of(1L, 0L), replies);
  }

  @Test
  void fiveProductionRevokeResponsesReleasePoolAndImmediatelyDeleteAllKeys() throws Exception {
    var lives = new ArrayList<Live>();
    for (int i = 0; i < 5; i++) lives.add(live());
    var gate = new CountDownLatch(1);
    long began = System.nanoTime();
    try (var threads = Executors.newVirtualThreadPerTaskExecutor()) {
      var calls = new ArrayList<Future<?>>();
      for (var live : lives)
        calls.add(
            threads.submit(
                () -> {
                  assertTrue(gate.await(5, TimeUnit.SECONDS));
                  asUser(
                      live.value(),
                      () -> controller.revoke(live.value().path("sessionId").asString()));
                  assertNull(consumer.get(live.hash()));
                  store.requireTransactionReleased();
                  return null;
                }));
      gate.countDown();
      for (var call : calls) call.get(10, TimeUnit.SECONDS);
    }
    long elapsed = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - began);
    assertTrue(elapsed < 7000);
    System.out.printf("PRODUCTION_REVOKE_POOL elapsed_ms=%d removed=5/5 pool=5%n", elapsed);
  }

  @Test
  void unavailableRevokeKeepsDurableDeletionAcrossReconstructionAndRetry() throws Exception {
    var live = live();
    connection.close();
    long began = System.nanoTime();
    var failure =
        assertThrows(
            auriga.common.error.AurigaException.class,
            () ->
                asUser(
                    live.value(),
                    () -> controller.revoke(live.value().path("sessionId").asString())));
    assertEquals("common.unavailable", failure.errorCode().code());
    assertTrue(TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - began) < 7000);
    assertTrue(store.tenant(tenant, () -> store.sessionProjectionPending(live.hash())));
    assertTrue(
        store
            .tenant(
                tenant,
                () -> store.byId("session", live.value().path("sessionId").asString(), false))
            .path("revoked")
            .asBoolean());
    reconstructProjection();
    // Retrying through the original controller requires the same fresh store as the new worker.
    var rebuiltSessions =
        new SessionManager(
            store, projection, tokens, clock, mock(EventPublisher.class), mock(AuditLogger.class));
    var rebuiltController =
        new IamLoginController(
            login, identity, store, rebuiltSessions, new QrChallenges(store, clock), secrets);
    try {
      asUser(
          live.value(), () -> rebuiltController.revoke(live.value().path("sessionId").asString()));
      assertNull(consumer.get(live.hash()));
      assertFalse(store.tenant(tenant, () -> store.sessionProjectionPending(live.hash())));
    } finally {
      rebuiltController.destroy();
    }
    System.out.println(
        "PRODUCTION_REVOKE_RECOVERY unavailable=true durable=true reconstructed=true idempotent=true");
  }

  @Test
  void externallyOwnedTransactionRejectsRevokeBeforeChangingAuthoritativeState() {
    var live = live();
    store.tenant(
        tenant,
        () -> {
          var failure =
              assertThrows(
                  auriga.common.error.AurigaException.class,
                  () ->
                      asUser(
                          live.value(),
                          () -> controller.revoke(live.value().path("sessionId").asString())));
          assertEquals("common.unavailable", failure.errorCode().code());
          assertFalse(
              store
                  .byId("session", live.value().path("sessionId").asString(), false)
                  .path("revoked")
                  .asBoolean());
          return null;
        });
    assertNotNull(consumer.get(live.hash()));
    var rejected = new java.util.concurrent.atomic.AtomicInteger();
    TenantContext.run(
        tenant,
        () ->
            sessions.response(
                () -> {
                  org.springframework.transaction.support.TransactionSynchronizationManager
                      .registerSynchronization(
                          new org.springframework.transaction.support.TransactionSynchronization() {
                            void reject() {
                              var failure =
                                  assertThrows(
                                      auriga.common.error.AurigaException.class,
                                      () ->
                                          asUser(
                                              live.value(),
                                              () ->
                                                  controller.revoke(
                                                      live.value().path("sessionId").asString())));
                              assertEquals("common.unavailable", failure.errorCode().code());
                              rejected.incrementAndGet();
                            }

                            public void afterCommit() {
                              reject();
                            }

                            public void afterCompletion(int status) {
                              reject();
                            }
                          });
                  return null;
                }));
    assertEquals(2, rejected.get());
    assertNotNull(consumer.get(live.hash()));
  }

  @Test
  void passwordRetryMustConfirmEarlierCommittedSecurityDeletion() throws Exception {
    var old = live();
    var current = live();
    UUID account = UUID.fromString(current.value().path("accountId").asString());
    var otherAccount = scopedSession(tenant, UUID.randomUUID());
    UUID otherTenant = UUID.randomUUID();
    var otherTenantSession = scopedSession(otherTenant, UUID.randomUUID());
    try (var readerConnection = io.nats.client.Nats.connect(NatsExtension.url())) {
      var reader = new NatsSessionKv(readerConnection, schemas);
      assertNotNull(reader.get(old.hash()));
      connection.close();
      assertCredentialUnavailable(
          current.value(),
          () ->
              me.changePassword(
                  Json.object(
                      "currentPassword", "correct-horse", "newPassword", "retry-password-one")));
      assertTrue(store.tenant(tenant, () -> store.sessionProjectionPending(old.hash())));
      assertNotNull(
          reader.get(old.hash()), "The original consumer has a separate healthy connection");
      assertCredentialUnavailable(
          current.value(),
          () ->
              me.changePassword(
                  Json.object(
                      "currentPassword",
                      "retry-password-one",
                      "newPassword",
                      "retry-password-two")));
      assertTrue(store.tenant(tenant, () -> store.sessionProjectionPending(old.hash())));
      assertNotNull(reader.get(old.hash()));
      reconstructCredentialController();
      asUser(
          current.value(),
          () ->
              me.changePassword(
                  Json.object(
                      "currentPassword",
                      "retry-password-two",
                      "newPassword",
                      "retry-password-three")));
      assertNull(
          reader.get(old.hash()),
          "Successful credential response must immediately reject the old cookie");
      assertFalse(store.tenant(tenant, () -> store.sessionProjectionPending(old.hash())));
      assertNotNull(reader.get(current.hash()));
      assertScopePreserved(reader, otherAccount, tenant);
      assertScopePreserved(reader, otherTenantSession, otherTenant);
      verify(events, times(1))
          .publish(eq("identity.session_revoked"), eq(account.toString()), any());
      asUser(current.value(), () -> assertEquals(0, me.revokeOthers().path("revoked").asInt()));
      verify(events, times(1))
          .publish(eq("identity.session_revoked"), eq(account.toString()), any());
      assertRequestReleasedAndPoolBecomesIdle();
      System.out.println(
          "CREDENTIAL_PASSWORD_RETRY failed=503,503 recovered=success old_cookie=absent current=preserved");
    }
  }

  void assertCredentialUnavailable(ObjectNode current, Runnable change) {
    var error =
        assertThrows(auriga.common.error.AurigaException.class, () -> asUser(current, change));
    assertEquals("common.unavailable", error.errorCode().code());
    assertEquals(503, error.errorCode().httpStatus());
    store.requireTransactionReleased();
  }

  void reconstructCredentialController() throws Exception {
    reconstructProjection();
    sessions =
        new SessionManager(store, projection, tokens, clock, events, mock(AuditLogger.class));
    team = new TeamAccounts(store, secrets, clock, events);
    me =
        new IamMeController(
            store,
            sessions,
            team,
            new TeamPasskeys(store, secrets, clock, events),
            null,
            clock,
            tokens);
  }

  SessionManager.Issued scopedSession(UUID scope, UUID account) {
    return TenantContext.call(
        scope,
        () ->
            sessions.withSigningKey(
                () ->
                    sessions.response(
                        () -> {
                          store.insert(
                              "account",
                              account.toString(),
                              Json.object(
                                  "accountId", account, "status", "active", "memberType", "member"),
                              null);
                          UUID scopedProvider = provider;
                          if (!scope.equals(tenant)) {
                            scopedProvider = UUID.randomUUID();
                            store.insert(
                                "idp_provider",
                                scopedProvider.toString(),
                                Json.object("providerId", scopedProvider, "enabled", true),
                                null);
                          }
                          return sessions.create(
                              account,
                              scopedProvider,
                              "admin",
                              false,
                              Duration.ofHours(1),
                              Duration.ofMinutes(30));
                        })));
  }

  void assertScopePreserved(NatsSessionKv reader, SessionManager.Issued issued, UUID scope) {
    String hash = Secrets.hash(issued.cookie());
    assertNotNull(reader.get(hash));
    var row = store.tenant(scope, () -> store.get("session", hash, false));
    assertNotNull(row);
    assertFalse(row.path("revoked").asBoolean());
    assertFalse(store.tenant(scope, () -> store.sessionProjectionPending(hash)));
    assertNotNull(tokens.verify(reader.get(hash).path("accessToken").asString()));
  }

  @Test
  void totpConfirmationMustIncludeEarlierRevokedSessionsWhileProjectionRemainsUnavailable()
      throws Exception {
    credentialTotpRetry(false);
  }

  @Test
  void recoveredTotpConfirmationImmediatelyDeletesEarlierRevokedSessions() throws Exception {
    credentialTotpRetry(true);
  }

  void credentialTotpRetry(boolean recoverBeforeConfirmation) throws Exception {
    var old = live();
    var current = live();
    String account = current.value().path("accountId").asString();
    try (var readerConnection = io.nats.client.Nats.connect(NatsExtension.url())) {
      var reader = new NatsSessionKv(readerConnection, schemas);
      connection.close();
      assertCredentialUnavailable(
          current.value(),
          () ->
              me.changePassword(
                  Json.object(
                      "currentPassword", "correct-horse", "newPassword", "totp-retry-password")));
      assertNotNull(reader.get(old.hash()));
      assertTrue(store.tenant(tenant, () -> store.sessionProjectionPending(old.hash())));
      if (recoverBeforeConfirmation) reconstructCredentialController();
      var enrollment = new java.util.concurrent.atomic.AtomicReference<ObjectNode>();
      asUser(current.value(), true, () -> enrollment.set(me.beginTotp()));
      String enrollmentId = enrollment.get().path("enrollmentId").asString();
      String code =
          Totp.code(
              enrollment.get().path("secret").asString(), clock.instant().getEpochSecond() / 30);
      Runnable confirmation =
          () ->
              asUser(
                  current.value(),
                  true,
                  () -> me.confirmTotp(enrollmentId, Json.object("code", code)));
      if (recoverBeforeConfirmation) {
        confirmation.run();
        assertNull(reader.get(old.hash()));
        assertFalse(store.tenant(tenant, () -> store.sessionProjectionPending(old.hash())));
      } else {
        var error = assertThrows(auriga.common.error.AurigaException.class, confirmation::run);
        assertEquals("common.unavailable", error.errorCode().code());
        assertEquals(503, error.errorCode().httpStatus());
        assertNotNull(reader.get(old.hash()));
        assertTrue(store.tenant(tenant, () -> store.sessionProjectionPending(old.hash())));
      }
      assertTrue(store.tenant(tenant, () -> team.hasTotp(UUID.fromString(account))));
      assertNull(store.tenant(tenant, () -> store.get("team_ceremony", enrollmentId, false)));
      verify(events, times(1)).publish(eq("identity.session_revoked"), eq(account), any());
      verify(events, times(1)).publish(eq("identity.mfa_changed"), eq(account), any());
      assertNotNull(reader.get(current.hash()));
      store.requireTransactionReleased();
      System.out.printf(
          "CREDENTIAL_TOTP_RETRY restored=%s status=%s events_once=true current=preserved%n",
          recoverBeforeConfirmation, recoverBeforeConfirmation ? "success" : "503");
    }
  }

  @Test
  void productionRevokeOthersPasswordAndLogoutConfirmBeforeReturning() {
    var first = live();
    var current = live();
    asUser(current.value(), () -> assertEquals(1, me.revokeOthers().path("revoked").asInt()));
    assertNull(consumer.get(first.hash()));
    assertNotNull(consumer.get(current.hash()));
    asUser(current.value(), () -> assertEquals(0, me.revokeOthers().path("revoked").asInt()));
    var other = live();
    asUser(
        current.value(),
        () ->
            me.changePassword(
                Json.object(
                    "currentPassword", "correct-horse", "newPassword", "new-correct-horse")));
    assertNull(consumer.get(other.hash()));
    assertNotNull(consumer.get(current.hash()));
    var response = new MockHttpServletResponse();
    asUser(current.value(), () -> controller.logout(current.request(), response));
    assertNull(consumer.get(current.hash()));
    assertTrue(
        response.getHeaders("Set-Cookie").stream()
            .anyMatch(c -> c.contains("auriga_admin_session=") && c.contains("Max-Age=0")));
  }

  @Test
  void accountEventKeepsInboxOwnedCommitAndRollbackWithoutNestedConfirmation() {
    var live = live();
    var handler = new IamAccountEvents(store, sessions);
    var event =
        new auriga.common.events.ReceivedEvent(
            UUID.randomUUID(),
            "user.disabled",
            "org",
            live.value().path("accountId").asString(),
            clock.instant(),
            tenant,
            "1",
            null,
            Json.object("userId", live.value().path("accountId").asString()));
    projection.stop();
    try {
      assertThrows(
          IllegalStateException.class,
          () ->
              store.tenant(
                  tenant,
                  () -> {
                    handler.handle(event);
                    assertTrue(store.sessionProjectionPending(live.hash()));
                    assertNotNull(consumer.get(live.hash()));
                    throw new IllegalStateException("inbox rollback");
                  }));
      assertFalse(store.tenant(tenant, () -> store.sessionProjectionPending(live.hash())));
      assertNotNull(
          store.tenant(
              tenant, () -> sessions.activeById(live.value().path("sessionId").asString())));
      store.tenant(
          tenant,
          () -> {
            handler.handle(event);
            return null;
          });
      assertTrue(store.tenant(tenant, () -> store.sessionProjectionPending(live.hash())));
      assertNotNull(consumer.get(live.hash()));
    } finally {
      projection.start();
    }
    TenantContext.run(tenant, () -> projection.awaitProjected(List.of(live.hash())));
    assertNull(consumer.get(live.hash()));
  }

  @Test
  void fiveProductionLoginsReleaseFiveConnectionsBeforeWaitingForVisibility() throws Exception {
    var pending = new ArrayList<Pending>();
    for (int i = 0; i < 5; i++) pending.add(begin());
    var start = new CountDownLatch(1);
    long began = System.nanoTime();
    try (var threads = Executors.newVirtualThreadPerTaskExecutor()) {
      var results = new ArrayList<Future<?>>();
      for (var flow : pending)
        results.add(
            threads.submit(
                () -> {
                  assertTrue(start.await(5, TimeUnit.SECONDS));
                  var response = new MockHttpServletResponse();
                  assertEquals("completed", complete(flow, response).path("status").asString());
                  var cookie = response.getCookie("__Host-auriga_admin_session");
                  assertNotNull(cookie);
                  assertNotNull(consumer.get(Secrets.hash(cookie.getValue())));
                  return null;
                }));
      start.countDown();
      for (var result : results) result.get(40, TimeUnit.SECONDS);
    }
    long elapsed = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - began);
    System.out.printf(
        "PRODUCTION_LOGIN_POOL elapsed_ms=%d visible=5/5 pool=%d timeout_ms=%d%n",
        elapsed, source.getMaximumPoolSize(), source.getConnectionTimeout());
    assertTrue(elapsed < 10000);
    assertRequestReleasedAndPoolBecomesIdle();
  }

  @Test
  void externallyOwnedTransactionRejectsLoginResponseWithoutIssuingCookieOrFinishingFlow() {
    var pending = begin();
    var response = new MockHttpServletResponse();
    var outer =
        new org.springframework.transaction.support.TransactionTemplate(
            new TenantTransactionManager(source));
    var callbacksRejected = new java.util.concurrent.atomic.AtomicInteger();
    TenantContext.run(
        tenant,
        () ->
            outer.execute(
                status -> {
                  var failure =
                      assertThrows(
                          auriga.common.error.AurigaException.class,
                          () -> complete(pending, response));
                  assertEquals("common.unavailable", failure.errorCode().code());
                  assertNull(response.getHeader("Set-Cookie"));
                  assertEquals(
                      "pending",
                      store.get("login_tx", pending.id(), false).path("status").asString());
                  org.springframework.transaction.support.TransactionSynchronizationManager
                      .registerSynchronization(
                          new org.springframework.transaction.support.TransactionSynchronization() {
                            private void rejectBeforeCleanup() {
                              var callbackResponse = new MockHttpServletResponse();
                              var callbackFailure =
                                  assertThrows(
                                      auriga.common.error.AurigaException.class,
                                      () -> complete(pending, callbackResponse));
                              assertEquals(
                                  "common.unavailable", callbackFailure.errorCode().code());
                              assertNull(callbackResponse.getHeader("Set-Cookie"));
                              callbacksRejected.incrementAndGet();
                            }

                            public void afterCommit() {
                              rejectBeforeCleanup();
                            }

                            public void afterCompletion(int status) {
                              rejectBeforeCleanup();
                            }
                          });
                  return null;
                }));
    assertEquals(2, callbacksRejected.get());
    assertEquals(
        "pending",
        store
            .tenant(tenant, () -> store.get("login_tx", pending.id(), false))
            .path("status")
            .asString());
  }

  @Test
  void realBrokerFailureIsBoundedAndDoesNotIssueSessionCookie() throws Exception {
    var pending = begin();
    connection.close();
    var response = new MockHttpServletResponse();
    long began = System.nanoTime();
    var failure =
        assertThrows(auriga.common.error.AurigaException.class, () -> complete(pending, response));
    long elapsed = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - began);
    assertEquals("common.unavailable", failure.errorCode().code());
    assertNull(response.getHeader("Set-Cookie"));
    assertTrue(elapsed < 7000);
    store.tenant(
        tenant,
        () -> {
          assertEquals(
              "completed", store.get("login_tx", pending.id(), false).path("status").asString());
          assertTrue(
              new JdbcTemplate(source)
                      .queryForObject("SELECT count(*) FROM iam.session_projection", Integer.class)
                  > 0);
          return null;
        });
    assertRequestReleasedAndPoolBecomesIdle();
    System.out.printf(
        "PRODUCTION_LOGIN_UNAVAILABLE elapsed_ms=%d cookie=false durable=true%n", elapsed);
    String hash =
        store.tenant(
            tenant,
            () ->
                store.all("session").stream()
                    .filter(s -> s.path("providerId").asString().equals(provider.toString()))
                    .findFirst()
                    .orElseThrow()
                    .path("cookieHash")
                    .asString());
    reconstructProjection();
    org.awaitility.Awaitility.await()
        .atMost(Duration.ofSeconds(8))
        .untilAsserted(() -> assertNotNull(consumer.get(hash)));
    connection.close();
    store.tenant(
        tenant,
        () -> {
          projection.delete(hash);
          return null;
        });
    org.awaitility.Awaitility.await()
        .atMost(Duration.ofSeconds(5))
        .untilAsserted(
            () ->
                assertTrue(
                    store.tenant(
                            tenant,
                            () ->
                                new JdbcTemplate(source)
                                    .queryForObject(
                                        "SELECT attempts FROM iam.session_projection WHERE cookie_hash=?",
                                        Integer.class,
                                        hash))
                        > 0));
    reconstructProjection();
    org.awaitility.Awaitility.await()
        .atMost(Duration.ofSeconds(8))
        .untilAsserted(
            () -> {
              assertNull(consumer.get(hash));
              assertFalse(store.tenant(tenant, () -> store.sessionProjectionPending(hash)));
            });
    System.out.println(
        "PRODUCTION_LOGIN_RECOVERY rebuilt=2 published=true revoked=true durable_pending=0");
  }

  void reconstructProjection() throws Exception {
    context.close();
    source.close();
    connection.close();
    source = new HikariDataSource();
    source.setJdbcUrl(roles.runtime().jdbcUrl());
    source.setMaximumPoolSize(5);
    assertEquals(30000, source.getConnectionTimeout());
    store = new IamStore(new JdbcTemplate(source), new TenantTransactionManager(source));
    clock.now = clock.now.plusSeconds(2);
    connection = io.nats.client.Nats.connect(NatsExtension.url());
    consumer = new NatsSessionKv(connection, schemas);
    context = new AnnotationConfigApplicationContext();
    context.registerBean(IamStore.class, () -> store);
    context.registerBean(Clock.class, () -> clock);
    context.registerBean(io.nats.client.Connection.class, () -> connection);
    context.register(SessionProjectionPostgresTest.LifecycleAssembly.class);
    context.refresh();
    projection = assertInstanceOf(CompositeSessionKv.class, context.getBean(SessionKv.class));
    keys = new SigningKeys(store, secrets, clock, Duration.ofSeconds(30));
    tokens =
        new TokenService(
            keys,
            schemas,
            (t, s, c) -> Set.of("workspace.read"),
            clock,
            access.portalUrl(),
            Map.of());
  }

  void assertRequestReleasedAndPoolBecomesIdle() {
    store.requireTransactionReleased();
    // The one production projection worker may legitimately be in a transaction right now.
    // The separate latch probe proves active=1 belongs to that worker while requests are free.
    assertTrue(source.getHikariPoolMXBean().getActiveConnections() <= 1);
    org.awaitility.Awaitility.await()
        .atMost(Duration.ofSeconds(2))
        .untilAsserted(() -> assertEquals(0, source.getHikariPoolMXBean().getActiveConnections()));
  }

  @Test
  void failedPasswordAttemptsCommitTheirCountersAndSigningScopeFollowsRotation() throws Exception {
    var pending = begin();
    var response = new MockHttpServletResponse();
    var result =
        controller.advance(
            pending.id(),
            Json.object(
                "type",
                "form",
                "formId",
                "password",
                "values",
                Json.object("username", "alice", "password", "wrong-password")),
            pending.request(),
            response);
    assertEquals("failed", result.path("status").asString());
    assertNull(response.getCookie("__Host-auriga_admin_session"));
    String bucket =
        Secrets.hash("login-provider:" + provider + ":user:alice")
            + ":"
            + clock.instant().getEpochSecond() / 300;
    assertEquals(
        1,
        store.tenant(tenant, () -> store.get("rate_limit", bucket, false)).path("count").asInt());
    var before = new MockHttpServletResponse();
    assertEquals("completed", complete(begin(), before).path("status").asString());
    String oldAccess =
        consumer
            .get(Secrets.hash(before.getCookie("__Host-auriga_admin_session").getValue()))
            .path("accessToken")
            .asString();
    String next = keys.publishNext();
    clock.now = clock.now.plusSeconds(31);
    keys.activate(next);
    var after = new MockHttpServletResponse();
    assertEquals("completed", complete(begin(), after).path("status").asString());
    String newAccess =
        consumer
            .get(Secrets.hash(after.getCookie("__Host-auriga_admin_session").getValue()))
            .path("accessToken")
            .asString();
    assertEquals(next, com.nimbusds.jwt.SignedJWT.parse(newAccess).getHeader().getKeyID());
    assertNotEquals(next, com.nimbusds.jwt.SignedJWT.parse(oldAccess).getHeader().getKeyID());
    assertNotNull(tokens.verify(oldAccess));
    assertNotNull(tokens.verify(newAccess));
  }

  void fixtureConnector(boolean immediate) {
    // A deterministic local IdP plugin, while the core login, persistence and token paths are real.
    registry.register(
        new auriga.iam.api.IdentityConnector() {
          public tools.jackson.databind.JsonNode descriptor() {
            var value = (ObjectNode) new EmergencyConnector().descriptor();
            value.put("type", "response-fixture");
            value.set("steps", Json.MAPPER.valueToTree(List.of("redirect")));
            return value;
          }

          public void validateConfig(tools.jackson.databind.JsonNode config) {}

          private tools.jackson.databind.JsonNode done() {
            return Json.object(
                "step",
                Json.object(
                    "type",
                    "done",
                    "profile",
                    Json.object(
                        "subject",
                        "alice",
                        "displayName",
                        "Alice",
                        "authTime",
                        clock.instant().toString())));
          }

          public tools.jackson.databind.JsonNode begin(
              tools.jackson.databind.JsonNode ctx, tools.jackson.databind.JsonNode cfg) {
            return immediate
                ? done()
                : Json.object(
                    "state",
                    Json.object(),
                    "step",
                    Json.object(
                        "type",
                        "redirect",
                        "url",
                        "https://idp.example.com/auth?state=" + Secrets.random(),
                        "method",
                        "GET"));
          }

          public tools.jackson.databind.JsonNode advance(
              tools.jackson.databind.JsonNode ctx,
              tools.jackson.databind.JsonNode cfg,
              tools.jackson.databind.JsonNode input) {
            return done();
          }
        });
    store.tenant(
        tenant,
        () -> {
          var row = store.get("idp_provider", provider.toString(), true);
          row.put("connectorType", "response-fixture");
          store.save("idp_provider", row);
          return null;
        });
  }

  @Test
  void immediateBeginCompletionConfirmsBeforeIssuingCookie() {
    fixtureConnector(true);
    var response = new MockHttpServletResponse();
    assertEquals(
        "completed",
        controller
            .begin(new CreateLoginTransaction().providerId(provider), request(), response)
            .path("status")
            .asString());
    var projected =
        consumer.get(Secrets.hash(response.getCookie("__Host-auriga_admin_session").getValue()));
    assertNotNull(projected);
    assertImmediateMe(projected);
  }

  @Test
  void callbackConfirmsBeforeRedirectAndUnavailableCallbackNeverIssuesCookie() throws Exception {
    fixtureConnector(false);
    var callback = new LoginCallbackController(login, identity, store);
    var pending = begin();
    var response = new MockHttpServletResponse();
    var result = callback.get(provider, callbackParams(pending), pending.request(), response);
    assertEquals(302, result.getStatusCode().value());
    var projected =
        consumer.get(Secrets.hash(response.getCookie("__Host-auriga_admin_session").getValue()));
    assertNotNull(projected);
    assertImmediateMe(projected);
    var unavailable = begin();
    var failureParams = callbackParams(unavailable);
    connection.close();
    var failureResponse = new MockHttpServletResponse();
    var failure =
        assertThrows(
            auriga.common.error.AurigaException.class,
            () -> callback.get(provider, failureParams, unavailable.request(), failureResponse));
    assertEquals("common.unavailable", failure.errorCode().code());
    assertNull(failureResponse.getHeader("Set-Cookie"));
  }

  Map<String, String> callbackParams(Pending pending) {
    return Map.of(
        "state",
        store
            .tenant(tenant, () -> store.get("login_tx", pending.id(), false))
            .path("callbackState")
            .asString());
  }
}
