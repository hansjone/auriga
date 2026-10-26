package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;

import auriga.common.audit.*;
import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jose.jwk.source.ImmutableJWKSet;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpServletResponse;
import org.springframework.security.authentication.ProviderManager;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.oauth2.server.resource.authentication.JwtAuthenticationProvider;
import org.springframework.security.oauth2.server.resource.web.authentication.BearerTokenAuthenticationFilter;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;
import org.springframework.transaction.TransactionSystemException;
import org.springframework.transaction.support.DefaultTransactionStatus;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import tools.jackson.databind.node.ObjectNode;

/** Actual controller/bearer filters, OAuth devices and PG; permissions are a local IAM fixture. */
@ExtendWith(PostgresExtension.class)
class NetworkRegistrationOutcomePostgresTest {
  static javax.sql.DataSource runtime;
  static JdbcTemplate jdbc;
  final UUID tenant = UuidV7.next(), account = UuidV7.next();
  final Clock clock = Clock.systemUTC();
  final AtomicReference<Runnable> commitHook = new AtomicReference<>();
  IamStore store;
  TokenService tokens;
  SessionManager sessions;
  SessionManager.Issued session;
  OAuthProvider oauth;
  AuditLogger audit;
  IamNetworkAuthority authority;
  MockMvc mvc;
  ObjectNode credential;
  String access, device;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkRegistrationOutcomePostgresTest.class, "outcome_owner", "outcome_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    runtime = roles.runtime().dataSource();
    jdbc = new JdbcTemplate(runtime);
  }

  @BeforeEach
  void setup() throws Exception {
    var manager =
        new TenantTransactionManager(runtime) {
          @Override
          protected void doCommit(DefaultTransactionStatus status) {
            var hook = commitHook.getAndSet(null);
            if (hook != null) hook.run();
            super.doCommit(status);
          }
        };
    manager.setRollbackOnCommitFailure(true);
    store = spy(new IamStore(jdbc, manager));
    var key = new ECKeyGenerator(Curve.P_256).keyID(UuidV7.next().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks()).thenReturn(Json.object("keys", List.of(key.toPublicJWK().toJSONObject())));
    var schemas = new ContractSchemas();
    tokens =
        new TokenService(
            keys,
            schemas,
            (t, s, c) -> Set.of("auriga.connect", "auriga.connect.collab.use"),
            clock,
            "https://iam.example.com",
            Map.of());
    var events =
        new OutboxEventPublisher(jdbc, "iam", "iam", new EventSchemas(), Json.MAPPER, clock, null);
    audit = spy(new OutboxAuditLogger(events));
    sessions =
        new SessionManager(
            store, new PostgresSessionKv(store, schemas, clock), tokens, clock, events, audit);
    session =
        tx(
            () -> {
              UUID provider = UuidV7.next();
              store.insert("account", account.toString(), Json.object("status", "active"), null);
              store.insert("idp_provider", provider.toString(), Json.object(), null);
              return sessions.create(
                  account, provider, "portal", true, Duration.ofHours(2), Duration.ofHours(1));
            });
    oauth =
        new OAuthProvider(
            store,
            sessions,
            tokens,
            clock,
            List.of(
                new OAuthClient(
                    "device-example",
                    tenant,
                    null,
                    Set.of(),
                    Set.of("auriga.connect"),
                    Set.of(OAuthProvider.DEVICE, "refresh_token"),
                    true)));
    credential = login();
    access = credential.path("access_token").asString();
    device = tokens.verify(access).path("did").asString();
    authority =
        new IamNetworkAuthority(
            store,
            new DeviceAccessAuthority(store, sessions, tokens),
            audit,
            clock,
            Set.of("server-a"));
    var settings =
        new AccessTokenSettings(
            tokens.issuer(),
            java.net.URI.create(tokens.issuer() + "/oauth2/jwks"),
            null,
            null,
            null,
            null,
            clock,
            false);
    var provider =
        new JwtAuthenticationProvider(
            AccessTokenDecoders.create(
                settings, new ImmutableJWKSet<>(new JWKSet(key.toPublicJWK()))));
    provider.setJwtAuthenticationConverter(AurigaAuthentication.converter());
    var bearer = new BearerTokenAuthenticationFilter(new ProviderManager(provider));
    var problems = new Problems(new ErrorMessages(List.of(), Locale.ENGLISH));
    bearer.setAuthenticationEntryPoint(new ProblemSecurityHandlers(problems));
    mvc =
        MockMvcBuilders.standaloneSetup(
                new IamNetworkClientController(
                    new IamNetworkClient(authority, store, clock, "server-a", Set.of("server-a")),
                    schemas))
            .setControllerAdvice(new ProblemExceptionHandler(problems))
            .addFilters(bearer, new PrincipalBindingFilter())
            .build();
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  <T> T tx(Supplier<T> work) {
    return store.tenant(tenant, work);
  }

  ObjectNode login() {
    return TenantContext.call(
        tenant,
        () -> {
          var grant =
              oauth.deviceAuthorization(
                  Map.of("client_id", "device-example", "scope", "auriga.connect"));
          oauth.decideDevice(
              grant.path("user_code").asString(),
              session.session().path("sessionId").asString(),
              true);
          return oauth.token(
              Map.of(
                  "grant_type",
                  OAuthProvider.DEVICE,
                  "client_id",
                  "device-example",
                  "device_code",
                  grant.path("device_code").asString()));
        });
  }

  MockHttpServletResponse postNetwork(String path, String token, String body) throws Exception {
    try {
      var request = post("/api/iam/v1/network/" + path).secure(true);
      if (token != null) request.header("Authorization", "Bearer " + token);
      if (body != null) request.contentType("application/json").content(body);
      return mvc.perform(request).andReturn().getResponse();
    } finally {
      SecurityContextHolder.clearContext();
    }
  }

  ObjectNode registered(String token, String body) throws Exception {
    var response = postNetwork("register", token, body);
    assertEquals(200, response.getStatus(), response.getContentAsString());
    assertEquals("no-store", response.getHeader("Cache-Control"));
    assertEquals("no-cache", response.getHeader("Pragma"));
    assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
    var json = Json.read(response.getContentAsString());
    new ContractSchemas().validate("network/register-result.schema.json", json);
    assertEquals(6, json.size());
    assertTrue(json.path("created").isBoolean());
    return json;
  }

  int count(String table) {
    assertTrue(
        Set.of("network_node", "network_ticket", "network_policy_revision", "outbox_event")
            .contains(table));
    return tx(
        () ->
            jdbc.queryForObject(
                "SELECT count(*) FROM iam." + table + " WHERE tenant_id=?", Integer.class, tenant));
  }

  String revision() {
    return tx(
        () ->
            jdbc.queryForObject(
                "SELECT revision::text FROM iam.network_policy_revision WHERE tenant_id=?",
                String.class,
                tenant));
  }

  ObjectNode identity(ObjectNode result) {
    var identity = result.deepCopy();
    identity.remove("created");
    return identity;
  }

  @Test
  void firstRepeatRefreshAndLostFirstResponseKeepIdentityAndOnlyFirstAdvancesRevision()
      throws Exception {
    var first = registered(access, null);
    assertTrue(first.path("created").asBoolean());
    assertEquals(device, first.path("device").asString());
    assertEquals("2", revision());
    var repeated = registered(access, "{}");
    assertFalse(repeated.path("created").asBoolean());
    assertEquals(identity(first), identity(repeated));
    credential =
        TenantContext.call(
            tenant,
            () ->
                oauth.token(
                    Map.of(
                        "grant_type",
                        "refresh_token",
                        "client_id",
                        "device-example",
                        "refresh_token",
                        credential.path("refresh_token").asString())));
    String refreshed = credential.path("access_token").asString();
    assertEquals(device, tokens.verify(refreshed).path("did").asString());
    var afterRefresh = registered(refreshed, "{}");
    assertFalse(afterRefresh.path("created").asBoolean());
    assertEquals(identity(first), identity(afterRefresh));
    assertEquals("2", revision());
    var ticketResponse =
        postNetwork(
            "tickets", refreshed, Json.object("node", first.path("node").asString()).toString());
    assertEquals(200, ticketResponse.getStatus(), ticketResponse.getContentAsString());
    var ticketIdentity = Json.read(ticketResponse.getContentAsString()).path("identity");
    assertEquals(identity(first), ticketIdentity);
    assertEquals(5, ticketIdentity.size());
    assertFalse(ticketIdentity.has("created"));
    assertEquals("2", revision());
    var old = TenantContext.call(tenant, () -> authority.register(refreshed));
    assertEquals(first.path("node").asString(), old.node());
    assertEquals(old, TenantContext.call(tenant, () -> authority.register(refreshed)));
    assertEquals("2", revision());
    assertEquals(1, count("network_node"));

    // Simulate a committed first response that the caller never retains.
    String freshAccess = login().path("access_token").asString();
    assertEquals(200, postNetwork("register", freshAccess, "{}").getStatus());
    var lostRetry = registered(freshAccess, "{}");
    assertFalse(lostRetry.path("created").asBoolean());
    var persisted = TenantContext.call(tenant, () -> authority.register(freshAccess));
    assertEquals(persisted.node(), lostRetry.path("node").asString());
    assertNotEquals(old.node(), persisted.node());
    assertEquals("3", revision());
    assertEquals(2, count("network_node"));
  }

  @Test
  void concurrentHttpRegistrationReturnsExactlyOneTrueAndOneStableIdentity() throws Exception {
    var start = new CountDownLatch(1);
    var reserved = new CountDownLatch(6);
    doAnswer(
            call -> {
              Object result = call.callRealMethod();
              if (result instanceof String) {
                reserved.countDown();
                await(reserved);
              }
              return result;
            })
        .when(store)
        .isolated(any(), any());
    try (var pool = Executors.newFixedThreadPool(6)) {
      var pending = new ArrayList<Future<ObjectNode>>();
      for (int i = 0; i < 6; i++)
        pending.add(
            pool.submit(
                () -> {
                  await(start);
                  return registered(access, "{}");
                }));
      start.countDown();
      var results = new ArrayList<ObjectNode>();
      for (var response : pending) results.add(response.get(20, TimeUnit.SECONDS));
      assertEquals(1, results.stream().filter(r -> r.path("created").asBoolean()).count());
      for (var result : results) assertEquals(identity(results.getFirst()), identity(result));
    }
    assertEquals(1, count("network_node"));
    assertEquals("2", revision());
    assertEquals(
        "7",
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT next_node::text FROM iam.network_realm WHERE tenant_id=?",
                    String.class,
                    tenant)));
  }

  @ParameterizedTest
  @ValueSource(strings = {"audit", "commit", "overflow"})
  void finalTransactionFailuresNeverReturnSuccessOrCommitNodeRevisionOrAudit(String fault)
      throws Exception {
    if (fault.equals("overflow"))
      tx(
          () -> {
            store.lock("network-authority");
            new IamNetworkPolicyRevision(store).current();
            jdbc.update(
                "UPDATE iam.network_policy_revision SET revision=18446744073709551615 WHERE tenant_id=?",
                tenant);
            return null;
          });
    int events = count("outbox_event");
    if (fault.equals("audit"))
      doThrow(new IllegalStateException("audit failure")).when(audit).record(any());
    if (fault.equals("commit"))
      doAnswer(
              call -> {
                Object result = call.callRealMethod();
                commitHook.set(
                    () -> {
                      throw new TransactionSystemException("commit failure");
                    });
                return result;
              })
          .when(audit)
          .record(any());
    var response = postNetwork("register", access, "{}");
    assertEquals(
        fault.equals("overflow") ? 503 : 500, response.getStatus(), response.getContentAsString());
    assertEquals(
        fault.equals("overflow") ? "common.unavailable" : "common.internal",
        Json.read(response.getContentAsString()).path("code").asString());
    assertFalse(Json.read(response.getContentAsString()).has("created"));
    assertEquals(0, count("network_node"));
    assertEquals(events, count("outbox_event"));
    assertEquals(fault.equals("overflow") ? 1 : 0, count("network_policy_revision"));
    if (fault.equals("overflow")) assertEquals("18446744073709551615", revision());
    assertEquals(
        "2",
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT next_node::text FROM iam.network_realm WHERE tenant_id=?",
                    String.class,
                    tenant)));
    doCallRealMethod().when(audit).record(any());
    if (fault.equals("overflow"))
      tx(
          () ->
              jdbc.update(
                  "UPDATE iam.network_policy_revision SET revision=1 WHERE tenant_id=?", tenant));
    var retry = registered(access, "{}");
    assertTrue(retry.path("created").asBoolean());
    assertEquals("2", retry.path("node").asString());
    assertEquals("2", revision());
    assertFalse(registered(access, "{}").path("created").asBoolean());
  }

  @Test
  void httpSuccessWaitsForActualFinalCommit() throws Exception {
    var reached = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    doAnswer(
            call -> {
              Object result = call.callRealMethod();
              commitHook.set(
                  () -> {
                    reached.countDown();
                    await(release);
                  });
              return result;
            })
        .when(audit)
        .record(any());
    try (var pool = Executors.newSingleThreadExecutor()) {
      var response = pool.submit(() -> registered(access, "{}"));
      try {
        await(reached);
        assertFalse(response.isDone());
        assertEquals(0, count("network_node"));
        assertEquals(0, count("network_policy_revision"));
      } finally {
        release.countDown();
      }
      assertTrue(response.get(10, TimeUnit.SECONDS).path("created").asBoolean());
    }
    assertEquals(1, count("network_node"));
    assertEquals("2", revision());
  }

  @Test
  void injectedFieldsAndInvalidCredentialsHaveNoRegistrationSideEffects() throws Exception {
    int events = count("outbox_event");
    for (String field :
        List.of("created", "node", "serverAudience", "audience", "tenant", "device", "account")) {
      var response = postNetwork("register", access, Json.object(field, "attacker").toString());
      assertEquals(400, response.getStatus(), field);
      assertEquals(
          "common.validation_failed",
          Json.read(response.getContentAsString()).path("code").asString());
    }
    for (String bad :
        List.of("ait_invalid", "invalid", credential.path("refresh_token").asString()))
      assertEquals(401, postNetwork("register", bad, "{}").getStatus());
    assertEquals(401, postNetwork("register", null, "{}").getStatus());
    for (String context : List.of("portal", "admin", "service")) {
      String bad =
          tokens.access(
              tenant,
              account.toString(),
              "auriga",
              context.equals("service") ? "service_account" : "user",
              context,
              !context.equals("service")
                  ? UUID.fromString(session.session().path("sessionId").asString())
                  : null,
              null,
              clock.instant(),
              clock.instant(),
              "auriga.connect");
      var response = postNetwork("register", bad, "{}");
      assertEquals(403, response.getStatus(), response.getContentAsString());
    }
    String ai =
        tokens.access(
            tenant,
            account.toString(),
            "auriga.ai-gateway",
            "device",
            "device",
            null,
            UUID.fromString(device),
            clock.instant(),
            clock.instant(),
            "auriga.connect");
    assertEquals(401, postNetwork("register", ai, "{}").getStatus());
    for (UUID tokenTenant : List.of(tenant, UuidV7.next())) {
      String foreign =
          tokens.access(
              tokenTenant,
              account.toString(),
              "auriga",
              "device",
              "device",
              null,
              tokenTenant.equals(tenant) ? UuidV7.next() : UUID.fromString(device),
              clock.instant(),
              clock.instant(),
              "auriga.connect");
      var response = postNetwork("register", foreign, "{}");
      assertEquals(403, response.getStatus(), response.getContentAsString());
      assertEquals(
          "common.forbidden", Json.read(response.getContentAsString()).path("code").asString());
      assertEquals(
          0,
          store.tenant(
              tokenTenant,
              () ->
                  jdbc.queryForObject(
                      "SELECT count(*) FROM iam.network_node WHERE tenant_id=?",
                      Integer.class,
                      tokenTenant)));
    }
    try {
      var cookieOnly =
          mvc.perform(
                  post("/api/iam/v1/network/register")
                      .cookie(
                          new jakarta.servlet.http.Cookie(
                              "__Host-auriga_session", session.cookie()))
                      .contentType("application/json")
                      .content("{}"))
              .andReturn()
              .getResponse();
      assertEquals(401, cookieOnly.getStatus());
    } finally {
      SecurityContextHolder.clearContext();
    }
    assertEquals(0, count("network_node"));
    assertEquals(0, count("network_ticket"));
    assertEquals(0, count("network_policy_revision"));
    assertEquals(events, count("outbox_event"));
  }

  @ParameterizedTest
  @ValueSource(strings = {"device", "session", "account", "organization", "expired-session"})
  void liveDenialsRejectHttpWithoutNodeRevisionOrAudit(String kind) throws Exception {
    tx(
        () -> {
          String table = kind.equals("expired-session") ? "session" : kind;
          if (table.equals("organization"))
            store.insert("network_config", "organization", Json.object("enabled", false), null);
          else {
            String id =
                table.equals("device")
                    ? device
                    : table.equals("account")
                        ? account.toString()
                        : session.session().path("sessionId").asString();
            var row = store.get(table, id, true);
            if (table.equals("account")) row.put("status", "disabled");
            else if (kind.equals("expired-session"))
              row.put("idleExpiresAt", clock.instant().minusSeconds(1).toString());
            else row.put("revoked", true);
            store.save(table, row);
          }
          return null;
        });
    int events = count("outbox_event");
    var response = postNetwork("register", access, "{}");
    assertEquals(403, response.getStatus(), response.getContentAsString());
    assertEquals(
        "common.forbidden", Json.read(response.getContentAsString()).path("code").asString());
    assertEquals(0, count("network_node"));
    assertEquals(0, count("network_policy_revision"));
    assertEquals(events, count("outbox_event"));
  }

  @Test
  void credentialRevokedAfterReservationCannotCommitNodeOrCreationFact() throws Exception {
    doAnswer(
            call -> {
              Object result = call.callRealMethod();
              if (result instanceof String)
                tx(
                    () -> {
                      var row = store.get("device", device, true);
                      row.put("revoked", true);
                      store.save("device", row);
                      return null;
                    });
              return result;
            })
        .when(store)
        .isolated(any(), any());
    int events = count("outbox_event");
    var response = postNetwork("register", access, "{}");
    assertEquals(403, response.getStatus(), response.getContentAsString());
    assertEquals(
        "common.forbidden", Json.read(response.getContentAsString()).path("code").asString());
    assertEquals(0, count("network_node"));
    assertEquals(0, count("network_policy_revision"));
    assertEquals(events, count("outbox_event"));
    assertEquals(
        "2",
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT next_node::text FROM iam.network_realm WHERE tenant_id=?",
                    String.class,
                    tenant)));
  }

  @Test
  void revokedNodeCannotBeRecreatedButNewOAuthDeviceGetsNewNode() throws Exception {
    var first = registered(access, "{}");
    tx(() -> jdbc.update("UPDATE iam.network_node SET revoked=true WHERE tenant_id=?", tenant));
    var response = postNetwork("register", access, "{}");
    assertEquals(403, response.getStatus());
    assertEquals(
        "common.forbidden", Json.read(response.getContentAsString()).path("code").asString());
    assertEquals(1, count("network_node"));
    assertEquals("2", revision());
    var fresh = registered(login().path("access_token").asString(), "{}");
    assertTrue(fresh.path("created").asBoolean());
    assertNotEquals(first.path("device"), fresh.path("device"));
    assertNotEquals(first.path("node"), fresh.path("node"));
    assertEquals(2, count("network_node"));
    assertEquals("3", revision());
  }

  static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(10, TimeUnit.SECONDS), "barrier timeout");
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException(e);
    }
  }
}
