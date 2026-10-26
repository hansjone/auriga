package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.audit.OutboxAuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.events.EventSchemas;
import auriga.common.events.OutboxEventPublisher;
import auriga.common.events.ReceivedEvent;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.iam.api.NetworkAuthority.*;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.math.BigInteger;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;
import tools.jackson.databind.node.ObjectNode;

/** Real IAM identity/session/device and FORCE RLS. Local permission fixtures are not org proof. */
@ExtendWith(PostgresExtension.class)
class NetworkPolicyRevisionPostgresTest {
  static final String MAX = "18446744073709551615";
  static JdbcTemplate jdbc, ownerJdbc, observer;
  static javax.sql.DataSource runtime;
  IamStore store;
  final UUID tenant = UuidV7.next(), account = UuidV7.next();
  final Clock clock = Clock.systemUTC();
  AuditLogger audit;
  EventPublisher published;
  final Set<String> permissions = ConcurrentHashMap.newKeySet();
  SessionManager sessions;
  TokenService tokens;
  PostgresSessionKv kv;
  DeviceAccessAuthority devices;
  IamNetworkAuthority network;
  IamAccountEvents handler;
  SessionManager.Issued issued;
  String device, access;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkPolicyRevisionPostgresTest.class, "policy_owner", "policy_app", "iam");
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
    ownerJdbc = new JdbcTemplate(roles.owner().dataSource());
    observer =
        new JdbcTemplate(
            PostgresExtension.database(NetworkPolicyRevisionPostgresTest.class).dataSource());
  }

  @BeforeEach
  void identity() throws Exception {
    store = spy(new IamStore(jdbc, new TenantTransactionManager(runtime)));
    var key = new ECKeyGenerator(Curve.P_256).keyID(UuidV7.next().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks()).thenReturn(Json.object("keys", List.of(key.toPublicJWK().toJSONObject())));
    permissions.addAll(Set.of("auriga.connect", "auriga.connect.collab.use"));
    var schemas = new ContractSchemas();
    published =
        new OutboxEventPublisher(jdbc, "iam", "iam", new EventSchemas(), Json.MAPPER, clock, null);
    audit = spy(new OutboxAuditLogger(published));
    tokens =
        new TokenService(
            keys,
            schemas,
            (t, s, c) -> Set.copyOf(permissions),
            clock,
            "https://iam.example.com",
            Map.of());
    kv = new PostgresSessionKv(store, schemas, clock);
    sessions = new SessionManager(store, kv, tokens, clock, published, audit);
    devices = new DeviceAccessAuthority(store, sessions, tokens);
    network = new IamNetworkAuthority(store, devices, audit, clock, Set.of("server-a"));
    handler = new IamAccountEvents(store, sessions);
    issued =
        tx(
            () -> {
              UUID provider = UuidV7.next();
              store.insert("account", account.toString(), Json.object("status", "active"), null);
              store.insert("idp_provider", provider.toString(), Json.object(), null);
              return sessions.create(
                  account, provider, "portal", true, Duration.ofHours(2), Duration.ofHours(1));
            });
    var oauth =
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
    TenantContext.run(
        tenant,
        () -> {
          var grant =
              oauth.deviceAuthorization(
                  Map.of("client_id", "device-example", "scope", "auriga.connect"));
          oauth.decideDevice(grant.path("user_code").asString(), sessionId(), true);
          var credential =
              oauth.token(
                  Map.of(
                      "grant_type",
                      OAuthProvider.DEVICE,
                      "client_id",
                      "device-example",
                      "device_code",
                      grant.path("device_code").asString()));
          access = credential.path("access_token").asString();
          device = tokens.verify(access).path("did").asString();
        });
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  String sessionId() {
    return issued.session().path("sessionId").asString();
  }

  <T> T tx(Supplier<T> work) {
    return store.tenant(tenant, work);
  }

  String revision() {
    return tx(
        () ->
            jdbc.queryForObject(
                "SELECT revision::text FROM iam.network_policy_revision WHERE tenant_id=?",
                String.class,
                tenant));
  }

  String initialize() {
    return tx(
        () -> {
          store.lock("network-authority");
          return new IamNetworkPolicyRevision(store).current().toString();
        });
  }

  String bump(UUID target) {
    return store.tenant(
        target,
        () -> {
          store.lock("network-authority");
          return new IamNetworkPolicyRevision(store).bump().toString();
        });
  }

  void principal(String subject, TokenContext context, UUID principalTenant) {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
                new AurigaPrincipal(
                    subject,
                    principalTenant,
                    context == TokenContext.SERVICE
                        ? PrincipalType.INTERNAL_SERVICE
                        : PrincipalType.USER,
                    context,
                    null,
                    null,
                    "member",
                    List.of(),
                    Set.of("iam.network.manage"),
                    null,
                    null,
                    null)));
  }

  void admin() {
    principal(account.toString(), TokenContext.ADMIN, tenant);
  }

  void collab() {
    principal("svc:collab", TokenContext.SERVICE, TenantContext.PLATFORM_TENANT);
  }

  ReceivedEvent event(String type, UUID user) {
    return new ReceivedEvent(
        UuidV7.next(),
        "user." + type,
        "org",
        user.toString(),
        clock.instant(),
        tenant,
        "1",
        null,
        Json.object("userId", user));
  }

  void handle(String type) {
    TenantContext.run(tenant, () -> handler.handle(event(type, account)));
  }

  Node register() {
    return TenantContext.call(tenant, () -> network.register(access));
  }

  void unavailable(Runnable work) {
    assertEquals(
        "common.unavailable", assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  int outboxSize() {
    return tx(
        () ->
            jdbc.queryForObject(
                "SELECT count(*) FROM iam.outbox_event WHERE tenant_id=?", Integer.class, tenant));
  }

  @Test
  void migrationForceRlsRuntimePrivilegesAndTenantIsolation() {
    assertEquals("1", initialize());
    assertTrue(
        ownerJdbc.queryForObject(
            "SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='iam.network_policy_revision'::regclass",
            Boolean.class));
    assertTrue(
        jdbc.queryForObject(
            "SELECT NOT rolsuper AND NOT rolbypassrls FROM pg_roles WHERE rolname=current_user",
            Boolean.class));
    assertEquals(
        0, jdbc.queryForObject("SELECT count(*) FROM iam.network_policy_revision", Integer.class));
    assertEquals(
        0,
        ownerJdbc.queryForObject(
            "SELECT count(*) FROM iam.network_policy_revision", Integer.class));
    TenantContext.run(
        tenant,
        () ->
            new org.springframework.transaction.support.TransactionTemplate(
                    new TenantTransactionManager(ownerJdbc.getDataSource()))
                .execute(
                    s -> {
                      assertEquals(
                          1,
                          ownerJdbc.queryForObject(
                              "SELECT count(*) FROM iam.network_policy_revision WHERE tenant_id=?",
                              Integer.class,
                              tenant));
                      return null;
                    }));
    UUID foreign = UuidV7.next();
    store.tenant(
        foreign,
        () -> {
          assertEquals(
              0,
              jdbc.queryForObject(
                  "SELECT count(*) FROM iam.network_policy_revision WHERE tenant_id=?",
                  Integer.class,
                  tenant));
          assertEquals(
              0,
              jdbc.update(
                  "UPDATE iam.network_policy_revision SET revision=2 WHERE tenant_id=?", tenant));
          return null;
        });
    assertThrows(
        DataAccessException.class,
        () ->
            store.tenant(
                foreign,
                () ->
                    jdbc.update(
                        "INSERT INTO iam.network_policy_revision(tenant_id,revision) VALUES (?,1)",
                        tenant)));
    assertThrows(
        DataAccessException.class,
        () ->
            tx(
                () ->
                    jdbc.update(
                        "UPDATE iam.network_policy_revision SET tenant_id=? WHERE tenant_id=?",
                        foreign,
                        tenant)));
    assertThrows(
        DataAccessException.class,
        () -> jdbc.execute("ALTER TABLE iam.network_policy_revision DISABLE ROW LEVEL SECURITY"));
    assertEquals("1", revision());
  }

  @ParameterizedTest
  @ValueSource(strings = {"0", "-1", "18446744073709551616"})
  void rejectsRevisionOutsidePositiveUint64(String invalid) {
    initialize();
    assertThrows(
        DataAccessException.class,
        () ->
            tx(
                () ->
                    jdbc.update(
                        "UPDATE iam.network_policy_revision SET revision=?::numeric WHERE tenant_id=?",
                        invalid,
                        tenant)));
    assertEquals("1", revision());
  }

  void maximum() {
    initialize();
    tx(
        () ->
            jdbc.update(
                "UPDATE iam.network_policy_revision SET revision=?::numeric WHERE tenant_id=?",
                MAX,
                tenant));
    assertEquals(MAX, revision());
  }

  @Test
  void lastUint64IncrementIsExactAndNextIncrementFailsClosed() {
    initialize();
    tx(
        () ->
            jdbc.update(
                "UPDATE iam.network_policy_revision SET revision=?::numeric WHERE tenant_id=?",
                new BigInteger(MAX).subtract(BigInteger.ONE).toString(),
                tenant));
    assertEquals(MAX, bump(tenant));
    unavailable(() -> bump(tenant));
    assertEquals(MAX, revision());
  }

  @ParameterizedTest
  @ValueSource(strings = {"permissions_changed", "disabled", "offboarded", "enabled"})
  void missingAccountEventDoesNotInitializeRevisionOrChangeIdentity(String type) {
    var before = tx(() -> store.byId("session", sessionId(), false));
    TenantContext.run(tenant, () -> handler.handle(event(type, UuidV7.next())));
    assertEquals(
        0,
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.network_policy_revision WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    assertEquals(before, tx(() -> store.byId("session", sessionId(), false)));
  }

  @Test
  void helperRequiresExistingTransactionAndRollsBackLazyInitialization() {
    TenantContext.run(
        tenant, () -> unavailable(() -> new IamNetworkPolicyRevision(store).current()));
    assertThrows(
        IllegalStateException.class,
        () ->
            tx(
                () -> {
                  store.lock("network-authority");
                  assertEquals(BigInteger.ONE, new IamNetworkPolicyRevision(store).current());
                  throw new IllegalStateException("rollback");
                }));
    assertEquals(
        0,
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.network_policy_revision WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
  }

  @Test
  void sameTenantConcurrentBumpsDoNotLoseUpdatesAndForeignTenantDoesNotWait() throws Exception {
    initialize();
    UUID foreign = UuidV7.next();
    var held = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    try (var pool = Executors.newFixedThreadPool(5)) {
      var first =
          pool.submit(
              () ->
                  tx(
                      () -> {
                        store.lock("network-authority");
                        new IamNetworkPolicyRevision(store).current();
                        held.countDown();
                        await(release);
                        return new IamNetworkPolicyRevision(store).bump();
                      }));
      try {
        await(held);
        assertEquals("2", pool.submit(() -> bump(foreign)).get(5, TimeUnit.SECONDS));
        var pending = new ArrayList<Future<String>>();
        for (int i = 0; i < 3; i++) pending.add(pool.submit(() -> bump(tenant)));
        release.countDown();
        assertEquals(BigInteger.TWO, first.get(10, TimeUnit.SECONDS));
        var values = new HashSet<String>();
        for (var result : pending) values.add(result.get(10, TimeUnit.SECONDS));
        assertEquals(Set.of("3", "4", "5"), values);
        assertEquals("5", revision());
      } finally {
        release.countDown();
      }
    }
  }

  @Test
  void fiveMutationsAndFirstVersusRepeatedRegisterAdvanceExactlyWithinTheirTransactions() {
    assertEquals("1", initialize());
    Node node = register();
    assertEquals("2", revision());
    assertEquals(node, register());
    assertEquals("2", revision());
    admin();
    TenantContext.run(
        tenant,
        () -> {
          network.configure(new Configuration(true, true, false, Map.of()));
          assertEquals("3", revision());
          network.nodePolicy(node.node(), node.node(), true);
          assertEquals("4", revision());
          network.grant(
              node.node(),
              Sender.AGENT,
              "1",
              Operation.FIRST_CONTACT,
              clock.instant().plusSeconds(600));
          assertEquals("5", revision());
          network.withdraw(node.node(), Sender.AGENT, "1", Operation.FIRST_CONTACT);
          assertEquals("6", revision());
          network.revoke(node.node());
          assertEquals("7", revision());
          assertEquals(
              "common.forbidden",
              assertThrows(
                      AurigaException.class, () -> network.nodePolicy("999", node.node(), true))
                  .errorCode()
                  .code());
          assertEquals(
              "common.forbidden",
              assertThrows(
                      AurigaException.class,
                      () ->
                          network.grant(
                              node.node(),
                              Sender.AGENT,
                              "1",
                              Operation.FIRST_CONTACT,
                              clock.instant().plusSeconds(600)))
                  .errorCode()
                  .code());
          assertEquals("7", revision());
          unavailable(
              () ->
                  store.tx(
                      () -> {
                        network.configure(new Configuration(false, false, false, Map.of()));
                        return null;
                      }));
          assertEquals("7", revision());
        });
  }

  @Test
  void failureAfterRevisionBumpRollsBackPolicyAndLazyRevisionTogether() {
    admin();
    int events = outboxSize();
    doThrow(new IllegalStateException("audit failure")).when(audit).record(any());
    TenantContext.run(
        tenant,
        () ->
            assertThrows(
                IllegalStateException.class,
                () -> network.configure(new Configuration(false, false, false, Map.of()))));
    assertEquals(
        0,
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.network_policy_revision WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    assertNull(tx(() -> store.get("network_config", "organization", false)));
    assertEquals(events, outboxSize());
  }

  @ParameterizedTest
  @ValueSource(strings = {"configure", "nodePolicy", "grant", "withdraw", "revoke", "register"})
  void overflowRollsBackEachMutationAndOnlyRegisterReservationSurvives(String mutation) {
    Node node = mutation.equals("register") ? null : register();
    if (mutation.equals("withdraw")) {
      admin();
      TenantContext.run(
          tenant,
          () ->
              network.grant(
                  node.node(),
                  Sender.AGENT,
                  "1",
                  Operation.FIRST_CONTACT,
                  clock.instant().plusSeconds(600)));
    }
    maximum();
    int events = outboxSize();
    admin();
    TenantContext.run(
        tenant,
        () ->
            unavailable(
                () -> {
                  switch (mutation) {
                    case "configure" ->
                        network.configure(new Configuration(false, false, true, Map.of()));
                    case "nodePolicy" -> network.nodePolicy(node.node(), node.node(), false);
                    case "grant" ->
                        network.grant(
                            node.node(),
                            Sender.AGENT,
                            "1",
                            Operation.FIRST_CONTACT,
                            clock.instant().plusSeconds(600));
                    case "withdraw" ->
                        network.withdraw(node.node(), Sender.AGENT, "1", Operation.FIRST_CONTACT);
                    case "revoke" -> network.revoke(node.node());
                    case "register" -> network.register(access);
                    default -> throw new AssertionError(mutation);
                  }
                }));
    assertEquals(MAX, revision());
    assertEquals(events, outboxSize());
    tx(
        () -> {
          switch (mutation) {
            case "configure" -> assertNull(store.get("network_config", "organization", false));
            case "nodePolicy" ->
                assertNull(store.get("network_policy", node.node() + ":" + node.node(), false));
            case "grant" -> assertTrue(store.all("network_grant").isEmpty());
            case "withdraw" -> assertEquals(1, store.all("network_grant").size());
            case "revoke" ->
                assertFalse(
                    jdbc.queryForObject(
                        "SELECT revoked FROM iam.network_node WHERE tenant_id=? AND node=?::numeric",
                        Boolean.class,
                        tenant,
                        node.node()));
            case "register" -> {
              assertEquals(
                  0,
                  jdbc.queryForObject(
                      "SELECT count(*) FROM iam.network_node WHERE tenant_id=?",
                      Integer.class,
                      tenant));
              assertEquals(
                  "2",
                  jdbc.queryForObject(
                      "SELECT next_node::text FROM iam.network_realm WHERE tenant_id=?",
                      String.class,
                      tenant));
            }
            default -> throw new AssertionError(mutation);
          }
          return null;
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"permissions_changed", "disabled", "offboarded", "enabled"})
  void eachAccountEventKeepsRealEffectsAndRevisionAtomic(String type) {
    initialize();
    if (type.equals("enabled"))
      tx(
          () -> {
            var a = store.get("account", account.toString(), true);
            a.put("status", "disabled");
            store.save("account", a);
            return null;
          });
    ObjectNode before = tx(() -> store.byId("session", sessionId(), false));
    int events = outboxSize();
    assertThrows(
        IllegalStateException.class,
        () ->
            tx(
                () -> {
                  handler.handle(event(type, account));
                  assertEquals("2", revision());
                  throw new IllegalStateException("inbox rollback");
                }));
    assertEquals("1", revision());
    assertEquals(events, outboxSize());
    assertEquals(before, tx(() -> store.byId("session", sessionId(), false)));
    assertFalse(tx(() -> store.get("device", device, false)).path("revoked").asBoolean());
    if (type.equals("permissions_changed")) permissions.clear();
    handle(type);
    assertEquals("2", revision());
    tx(
        () -> {
          var row = store.byId("session", sessionId(), false);
          if (type.equals("permissions_changed")) {
            assertEquals(Instant.EPOCH.toString(), row.path("accessTokenExpiresAt").asString());
            assertEquals(
                Instant.EPOCH.toString(),
                kv.get(Secrets.hash(issued.cookie())).path("accessTokenExpiresAt").asString());
            var refreshed =
                sessions.introspect(
                    auriga.contracts.iam.v1.IntrospectSessionRequest.newBuilder()
                        .setSessionToken(issued.cookie())
                        .setAudience(
                            auriga.contracts.iam.v1.SessionAudience.SESSION_AUDIENCE_PORTAL)
                        .build());
            assertTrue(refreshed.getActive());
            assertNotEquals(before.path("accessToken").asString(), refreshed.getAccessToken());
            assertEquals(0, tokens.verify(refreshed.getAccessToken()).path("perms").size());
          } else if (!type.equals("enabled")) {
            assertTrue(row.path("revoked").asBoolean());
            assertTrue(store.get("device", device, false).path("revoked").asBoolean());
            assertNull(sessions.activeById(sessionId()));
            assertNull(devices.credential(tenant.toString(), account.toString(), device));
            assertNull(kv.get(Secrets.hash(issued.cookie())));
            assertEquals(
                type.equals("offboarded") ? "user_offboarded" : "user_disabled",
                jdbc.queryForObject(
                    "SELECT envelope::jsonb->'data'->>'reason' FROM iam.outbox_event WHERE tenant_id=? AND event_type='identity.session_revoked'",
                    String.class,
                    tenant));
            assertEquals(
                1,
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.outbox_event WHERE tenant_id=? AND event_type='audit.recorded' AND envelope::jsonb->'data'->>'action'='iam.session.revoke'",
                    Integer.class,
                    tenant));
          }
          assertEquals(
              type.equals("disabled") || type.equals("offboarded") ? "disabled" : "active",
              store.get("account", account.toString(), false).path("status").asString());
          return null;
        });
    TenantContext.run(tenant, () -> handler.handle(event(type, UuidV7.next())));
    assertEquals("2", revision());
  }

  @ParameterizedTest
  @ValueSource(strings = {"permissions_changed", "disabled", "offboarded", "enabled"})
  void eventOverflowRollsBackIdentityAndProjection(String type) {
    maximum();
    int events = outboxSize();
    var before = tx(() -> store.byId("session", sessionId(), false));
    unavailable(() -> handle(type));
    assertEquals(MAX, revision());
    assertEquals(events, outboxSize());
    assertEquals(before, tx(() -> store.byId("session", sessionId(), false)));
    assertFalse(tx(() -> store.get("device", device, false)).path("revoked").asBoolean());
    assertEquals(
        "active",
        tx(() -> store.get("account", account.toString(), false)).path("status").asString());
    assertNotNull(tx(() -> kv.get(Secrets.hash(issued.cookie()))));
  }

  @ParameterizedTest
  @ValueSource(strings = {"permissions_changed", "disabled", "offboarded", "enabled"})
  void eventFirstBusinessLockAndIdentityBeforeAccountAreObservable(String type) {
    var locks = new ArrayList<String>();
    doAnswer(
            call -> {
              locks.add("advisory");
              return call.callRealMethod();
            })
        .when(store)
        .lock("network-authority");
    doAnswer(
            call -> {
              locks.add("account");
              return call.callRealMethod();
            })
        .when(store)
        .get("account", account.toString(), true);
    doAnswer(
            call -> {
              locks.add("session");
              return call.callRealMethod();
            })
        .when(store)
        .byId("session", sessionId(), true);
    handle(type);
    assertEquals(
        "advisory", locks.getFirst(), "event must take network advisory before any identity row");
    if (!type.equals("enabled"))
      assertTrue(locks.indexOf("session") < locks.indexOf("account"), "session before account");
    else assertEquals(List.of("advisory", "account"), locks);
  }

  IamNetworkAuthority.TargetBinding target() {
    Node node = register();
    return TenantContext.call(
        tenant,
        () -> {
          var ticket = network.prepare(access, node.node(), "server-a");
          var b = NetworkAuthorityTest.binding(ticket.ticket());
          principal("svc:ai-gateway", TokenContext.SERVICE, TenantContext.PLATFORM_TENANT);
          network.redeem(ticket.ticket(), node.node(), device, "server-a", b);
          collab();
          return new IamNetworkAuthority.TargetBinding(
              node.realm(), node.node(), b.ingress(), b.serverId(), b.clientId(), b.account());
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"permissions_changed", "disabled", "offboarded", "enabled"})
  void targetFinalSessionLockAndAccountEventUseSameFirstAdvisory(String type) throws Exception {
    var binding = target();
    String before = revision();
    var targetHoldsSession = new CountDownLatch(1);
    var releaseTarget = new CountDownLatch(1);
    var eventStarted = new CountDownLatch(1);
    var targetThread = new AtomicReference<Thread>();
    var eventThread = new AtomicReference<Thread>();
    var finalPhase = new AtomicBoolean();
    var eventPid = new AtomicInteger();
    doAnswer(
            call -> {
              if (Thread.currentThread() == targetThread.get()) finalPhase.set(true);
              if (Thread.currentThread() == eventThread.get()) {
                eventPid.set(jdbc.queryForObject("SELECT pg_backend_pid()", Integer.class));
                eventStarted.countDown();
              }
              return call.callRealMethod();
            })
        .when(store)
        .lock("network-authority");
    doAnswer(
            call -> {
              Object result = call.callRealMethod();
              if (Thread.currentThread() == targetThread.get() && finalPhase.get()) {
                targetHoldsSession.countDown();
                await(releaseTarget);
              }
              return result;
            })
        .when(store)
        .byId("session", sessionId(), true);
    // Assert the production event cannot take the historical account lock before advisory.
    doAnswer(
            call -> {
              if (Thread.currentThread() == eventThread.get())
                assertTrue(eventPid.get() > 0, "event acquired account before network advisory");
              return call.callRealMethod();
            })
        .when(store)
        .get("account", account.toString(), true);
    try (var pool = Executors.newFixedThreadPool(2)) {
      var target =
          pool.submit(
              () -> {
                targetThread.set(Thread.currentThread());
                collab();
                try {
                  return TenantContext.call(tenant, () -> network.resolveTarget(binding));
                } finally {
                  SecurityContextHolder.clearContext();
                }
              });
      try {
        await(targetHoldsSession);
        var event =
            pool.submit(
                () -> {
                  eventThread.set(Thread.currentThread());
                  handle(type);
                });
        await(eventStarted);
        awaitAdvisoryWait(eventPid.get());
        assertEquals(before, revision());
        releaseTarget.countDown();
        assertNotNull(target.get(10, TimeUnit.SECONDS));
        event.get(10, TimeUnit.SECONDS);
        assertEquals(new BigInteger(before).add(BigInteger.ONE).toString(), revision());
        collab();
        if (type.equals("disabled") || type.equals("offboarded"))
          assertNull(TenantContext.call(tenant, () -> network.resolveTarget(binding)));
      } finally {
        releaseTarget.countDown();
      }
    }
  }

  @ParameterizedTest
  @ValueSource(strings = {"permissions_changed", "disabled", "offboarded", "enabled"})
  void eventCommitBetweenCredentialAndFinalTargetCheckRejectsOldSnapshot(String type)
      throws Exception {
    var binding = target();
    String before = revision();
    var once = new AtomicBoolean();
    try (var pool = Executors.newSingleThreadExecutor()) {
      doAnswer(
              call -> {
                if (once.compareAndSet(false, true))
                  pool.submit(() -> handle(type)).get(10, TimeUnit.SECONDS);
                return call.callRealMethod();
              })
          .when(store)
          .lock("network-authority");
      collab();
      Instant result = TenantContext.call(tenant, () -> network.resolveTarget(binding));
      // Already-active enabled is conservatively versioned but leaves identity snapshots equal.
      if (!type.equals("enabled")) assertNull(result);
      else assertNotNull(result);
      assertEquals(new BigInteger(before).add(BigInteger.ONE).toString(), revision());
    }
  }

  void awaitAdvisoryWait(int pid) {
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
    while (System.nanoTime() < deadline) {
      Boolean waiting =
          observer.queryForObject(
              "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=? AND locktype='advisory' AND NOT granted)",
              Boolean.class,
              pid);
      if (Boolean.TRUE.equals(waiting)) return;
      Thread.onSpinWait();
    }
    fail("event did not wait for the tenant advisory before account/session locks");
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
