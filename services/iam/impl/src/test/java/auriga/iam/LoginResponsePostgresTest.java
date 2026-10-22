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
    var events = mock(EventPublisher.class);
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
    assertEquals(0, source.getHikariPoolMXBean().getActiveConnections());
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
    assertEquals(0, source.getHikariPoolMXBean().getActiveConnections());
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
    assertEquals(0, source.getHikariPoolMXBean().getActiveConnections());
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
