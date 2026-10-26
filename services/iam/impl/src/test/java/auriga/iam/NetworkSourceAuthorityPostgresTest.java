package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority.*;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.ECKey;
import com.nimbusds.jose.jwk.JWKSet;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;

/** Real OAuth/redeem/session/PG; org adapter proves the boundary, not live org connectivity. */
@ExtendWith(PostgresExtension.class)
class NetworkSourceAuthorityPostgresTest {
  static IamStore store;
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID();
  final NetworkAuthorityTest.Time clock = new NetworkAuthorityTest.Time();
  final IamGrpcDevicesSpringTest callers = new IamGrpcDevicesSpringTest();
  SessionManager sessions;
  TokenService tokens;
  OAuthProvider oauth;
  IamNetworkAuthority authority;
  String device, sessionId, access;
  Node node;
  Ticket ticket;
  IngressBinding binding;
  NetworkAuthorityConnection connection;
  ECKey key;
  IamNetworkSourceAuthority sources;
  auriga.iam.api.InstanceAiAuthorization organization =
      request -> {
        store.requireTransactionReleased();
        assertEquals(tenant.toString(), request.getTenantId());
        assertEquals(account.toString(), request.getAccountId());
        return allowed();
      };

  static auriga.contracts.org.v1.GetEffectivePermissionsResponse allowed() {
    return auriga.contracts.org.v1.GetEffectivePermissionsResponse.newBuilder()
        .setMemberTypeValue(1)
        .setStatusValue(1)
        .addPermissions("auriga.connect.collab.use")
        .build();
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  <T> T call(java.util.function.Supplier<T> work) {
    return callers.as("svc:collab", tenant, work);
  }

  <T> T tx(java.util.function.Supplier<T> work) {
    return store.tenant(tenant, work);
  }

  RegisterAgentSourceRequest request(String op) {
    return RegisterAgentSourceRequest.newBuilder()
        .setConnection(connection)
        .setScopeValue(1)
        .setOperationId(op)
        .setRegistration(
            NetworkAgentSourceRegistration.newBuilder()
                .setRegistrationId("registration")
                .setAllocationOpId("allocation")
                .setAllocationFingerprint("sha256:" + "a".repeat(64))
                .setRegistrationRevision(1)
                .setRealm(connection.getRealm())
                .setNode(connection.getNode())
                .setLocal("1")
                .setAddressType("session"))
        .build();
  }

  GetAgentSourceRequest getRequest(NetworkAgentSource s) {
    return GetAgentSourceRequest.newBuilder()
        .setSourceBinding(s.getSourceBinding())
        .setConnection(connection)
        .setRegistration(s.getRegistration())
        .build();
  }

  RevokeAgentSourceRequest stop(NetworkAgentSource s, String op) {
    return RevokeAgentSourceRequest.newBuilder()
        .setSourceBinding(s.getSourceBinding())
        .setConnection(s.getOriginalConnection())
        .setExpectedRevision(s.getRevision())
        .setOperationId(op)
        .build();
  }

  void mutate(String table, String id, String field, Object value) {
    tx(
        () -> {
          var row = store.byId(table, id, true);
          assertNotNull(row);
          row.set(field, Json.MAPPER.valueToTree(value));
          store.save(table, row);
          return null;
        });
  }

  void error(String code, Runnable work) {
    IamGrpcDevicesSpringTest.error(code, work);
  }

  @Test
  void realAuthorityReconnectAndImmutableReceiptWholeSecondDeadlines() {
    call(
        () -> {
          var original = request("first");
          var s = sources.register(original).getSource();
          assertEquals(account.toString(), s.getOwnerUserId());
          assertEquals(0, Instant.parse(s.getExpiresAt()).getNano());
          assertEquals(
              clock.instant().plusSeconds(30).truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(s.getExpiresAt()));
          var view = sources.get(getRequest(s));
          assertEquals(
              clock.instant().plusSeconds(5).truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(view.getValidUntil()));
          assertTrue(view.getPolicyRevision() > 0);
          assertEquals(s, sources.register(original).getSource());
          var next = authority.prepare(access, node.node(), "server-a");
          var b = NetworkAuthorityTest.binding(next.ticket());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> authority.redeem(next.ticket(), node.node(), device, "server-a", b));
          connection =
              connection.toBuilder()
                  .setIngress(b.ingress())
                  .setServerId(b.serverId())
                  .setClientId(b.clientId())
                  .setClientAccount(b.account())
                  .build();
          var reconnect = sources.get(getRequest(s));
          assertEquals(connection, reconnect.getConnection());
          assertEquals(s.getOriginalConnection(), reconnect.getSource().getOriginalConnection());
          mutate(
              "session",
              sessionId,
              "idleExpiresAt",
              clock.instant().plusSeconds(2).plusMillis(900).toString());
          assertEquals(
              clock.instant().plusSeconds(2).truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(sources.get(getRequest(s)).getValidUntil()));
          error(
              "common.conflict",
              () -> sources.register(original.toBuilder().setConnection(connection).build()));
          return null;
        });
  }

  @Test
  void everyRealTupleFieldAndOwnerDeviceBackingSessionIsChecked() {
    call(
        () -> {
          var source = sources.register(request("first")).getSource();
          for (var field : connection.getDescriptorForType().getFields()) {
            String bad =
                switch (field.getName()) {
                  case "tenant_id" -> UUID.randomUUID().toString();
                  case "realm", "node", "client_id" -> "18446744073709551615";
                  case "ingress" -> "b".repeat(26);
                  case "server_id" -> "N" + "B".repeat(55);
                  default -> "OTHER";
                };
            var c = connection.toBuilder().setField(field, bad).build();
            error(
                "common.forbidden",
                () -> sources.get(getRequest(source).toBuilder().setConnection(c).build()));
          }
          for (var change :
              List.of(
                  new String[] {"device", device, "revoked", "true"},
                  new String[] {"account", account.toString(), "status", "disabled"},
                  new String[] {"session", sessionId, "expiresAt", clock.instant().toString()},
                  new String[] {"session", sessionId, "idleExpiresAt", clock.instant().toString()},
                  new String[] {"device", device, "accountId", UUID.randomUUID().toString()},
                  new String[] {"device", device, "sessionId", UUID.randomUUID().toString()})) {
            var saved = tx(() -> store.byId(change[0], change[1], false));
            mutate(change[0], change[1], change[2], change[3].equals("true") ? true : change[3]);
            error("common.forbidden", () -> sources.get(getRequest(source)));
            tx(
                () -> {
                  store.save(change[0], saved);
                  return null;
                });
          }
          return null;
        });
  }

  @Test
  void currentOrgNegativesUnknownAndNoConfiguredPermissionFallback() {
    call(
        () -> {
          for (var response :
              List.of(
                  allowed().toBuilder().clearPermissions().build(),
                  allowed().toBuilder().setStatusValue(2).build(),
                  allowed().toBuilder().setStatusValue(3).build())) {
            organization = r -> response;
            error("common.forbidden", () -> sources.register(request("new")));
          }
          for (var response :
              List.of(
                  allowed().toBuilder().setStatusValue(0).build(),
                  allowed().toBuilder().setStatusValue(999).build(),
                  allowed().toBuilder().setMemberTypeValue(999).build())) {
            organization = r -> response;
            error("common.unavailable", () -> sources.register(request("new")));
          }
          organization = r -> null;
          error("common.unavailable", () -> sources.register(request("new")));
          organization =
              r -> {
                throw io.grpc.Status.DEADLINE_EXCEEDED.asRuntimeException();
              };
          error("common.unavailable", () -> sources.register(request("new")));
          organization = r -> allowed().toBuilder().setMemberTypeValue(2).build();
          assertEquals(1, sources.register(request("new")).getSource().getStateValue());
          return null;
        });
  }

  @Test
  void committedReceiptStopAndTerminalSurviveLogoutCleanupAndDisabledSwitch() {
    call(
        () -> {
          var r = request("first");
          var receipt = sources.register(r);
          clock.advance(3601);
          assertTrue(new NetworkIngressCleanup(store, clock).sweepOnce() > 0);
          mutate("session", sessionId, "revoked", true);
          mutate("device", device, "revoked", true);
          mutate("account", account.toString(), "status", "disabled");
          sources = authority(new IamNetworkSourceAuthority.Policy(0, 0));
          organization =
              req -> {
                fail("receipt/terminal/stop must not call org");
                return null;
              };
          assertEquals(receipt, sources.register(r));
          var terminal = sources.get(getRequest(receipt.getSource()));
          assertEquals(3, terminal.getSource().getStateValue());
          assertEquals("", terminal.getValidUntil());
          var revoke = stop(terminal.getSource(), "stop");
          var stopped = sources.revoke(revoke);
          assertEquals(stopped, sources.revoke(revoke));
          assertEquals(receipt, sources.register(r));
          error(
              "common.conflict",
              () ->
                  sources.register(
                      r.toBuilder().setExpectedRevision(2).setPredecessorBinding("other").build()));
          error("common.unavailable", () -> sources.register(request("unknown")));
          sources = authority(new IamNetworkSourceAuthority.Policy(30, 5));
          error("common.forbidden", () -> sources.register(request("unknown")));
          mutate("device", device, "accountId", UUID.randomUUID().toString());
          error("common.forbidden", () -> sources.register(r));
          error("common.forbidden", () -> sources.get(getRequest(receipt.getSource())));
          error("common.forbidden", () -> sources.revoke(revoke));
          return null;
        });
  }

  @Test
  void remoteCheckReleasesAllResourcesAndChangedLocalFactsCannotCommit() throws Exception {
    for (String table :
        List.of(
            "account",
            "session",
            "device",
            "network_ingress",
            "revision",
            "indexed-session-expiry",
            "indexed-device-expiry")) {
      var entered = new java.util.concurrent.CountDownLatch(1);
      var release = new java.util.concurrent.CountDownLatch(1);
      organization =
          req -> {
            store.requireTransactionReleased();
            assertFalse(
                org.springframework.transaction.support.TransactionSynchronizationManager
                    .isSynchronizationActive());
            assertFalse(
                org.springframework.transaction.support.TransactionSynchronizationManager
                    .hasResource(store.networkJdbc().getDataSource()));
            entered.countDown();
            try {
              assertTrue(release.await(10, TimeUnit.SECONDS));
            } catch (InterruptedException e) {
              throw new AssertionError(e);
            }
            return allowed();
          };
      try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
        var future = executor.submit(() -> call(() -> sources.register(request("race-" + table))));
        assertTrue(entered.await(10, TimeUnit.SECONDS));
        try {
          if (table.startsWith("indexed-"))
            tx(
                () -> {
                  String target = table.equals("indexed-session-expiry") ? "session" : "device";
                  String id = target.equals("session") ? sessionId : device;
                  store
                      .networkJdbc()
                      .update(
                          "UPDATE iam." + target + " SET expires_at=? WHERE tenant_id=? AND id=?",
                          java.sql.Timestamp.from(clock.instant().plusSeconds(1800)),
                          tenant,
                          UUID.fromString(id));
                  return null;
                });
          else if (table.equals("revision"))
            tx(
                () -> {
                  store.lock("network-authority");
                  new IamNetworkPolicyRevision(store).bump();
                  return null;
                });
          else {
            String id =
                switch (table) {
                  case "account" -> account.toString();
                  case "session" -> sessionId;
                  case "device" -> device;
                  default ->
                      tx(() -> store.get("network_ingress", connection.getIngress(), false))
                          .path("id")
                          .asString();
                };
            mutate(table, id, "versionWitness", table);
          }
        } finally {
          release.countDown();
        }
        var error =
            assertThrows(
                java.util.concurrent.ExecutionException.class,
                () -> future.get(15, TimeUnit.SECONDS));
        assertEquals("common.forbidden", ((AurigaException) error.getCause()).errorCode().code());
        assertEquals(
            0,
            tx(
                () ->
                    store
                        .networkJdbc()
                        .queryForObject(
                            "SELECT count(*) FROM iam.network_source WHERE tenant_id=?",
                            Integer.class,
                            tenant)));
      }
    }
  }

  @Test
  void successorCasStopAndClockRollbackNeverReviveOriginal() throws Exception {
    var first = call(() -> sources.register(request("first"))).getSource();
    clock.advance(31);
    var expired = call(() -> sources.get(getRequest(first))).getSource();
    assertEquals(3, expired.getStateValue());
    var next =
        request("next").toBuilder()
            .setPredecessorBinding(first.getSourceBinding())
            .setExpectedRevision(expired.getRevision())
            .build();
    try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var gate = new java.util.concurrent.CountDownLatch(1);
      var jobs = new ArrayList<java.util.concurrent.Future<Boolean>>();
      for (String op : List.of("a", "b"))
        jobs.add(
            executor.submit(
                () -> {
                  assertTrue(gate.await(5, TimeUnit.SECONDS));
                  try {
                    call(() -> sources.register(next.toBuilder().setOperationId(op).build()));
                    return true;
                  } catch (AurigaException e) {
                    assertEquals("common.conflict", e.errorCode().code());
                    return false;
                  }
                }));
      gate.countDown();
      int success = 0;
      for (var job : jobs) if (job.get(15, TimeUnit.SECONDS)) success++;
      assertEquals(1, success);
    }
    call(() -> sources.revoke(stop(expired, "stop-old")));
    var head =
        tx(
            () ->
                store
                    .networkJdbc()
                    .queryForObject(
                        "SELECT head::text FROM iam.network_source_chain WHERE tenant_id=?",
                        String.class,
                        tenant));
    var headRequest = getRequest(first).toBuilder().setSourceBinding(head).build();
    assertEquals("", call(() -> sources.get(headRequest)).getValidUntil());
    clock.advance(-100);
    assertEquals(3, call(() -> sources.get(getRequest(first))).getSource().getStateValue());
    assertEquals(2, call(() -> sources.get(headRequest)).getSource().getStateValue());
    assertEquals(first, call(() -> sources.register(request("first"))).getSource());
  }

  IamNetworkSourceAuthority authority(IamNetworkSourceAuthority.Policy policy) {
    return new IamNetworkSourceAuthority(
        store,
        authority,
        new DeviceAccessAuthority(store, sessions, tokens),
        r -> organization.getEffectivePermissions(r),
        clock,
        policy);
  }

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkSourceAuthorityPostgresTest.class, "sourceauth_owner", "sourceauth_app", "iam");
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
  void issuedDeviceAndRealRedemption() throws Exception {
    key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks()).thenReturn(Json.object("keys", List.of(key.toPublicJWK().toJSONObject())));
    tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of("auriga.connect", "auriga.connect.collab.use"),
            clock,
            "https://portal.example.com",
            Map.of());
    sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
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
    authority =
        new IamNetworkAuthority(
            store,
            new DeviceAccessAuthority(store, sessions, tokens),
            mock(AuditLogger.class),
            clock,
            Set.of("server-a"));
    sources = authority(new IamNetworkSourceAuthority.Policy(30, 5));
    TenantContext.run(
        tenant,
        () -> {
          var issued =
              store.tx(
                  () -> {
                    var provider = UUID.randomUUID();
                    store.insert(
                        "account", account.toString(), Json.object("status", "active"), null);
                    store.insert("idp_provider", provider.toString(), Json.object(), null);
                    return sessions.create(
                        account,
                        provider,
                        "portal",
                        true,
                        Duration.ofHours(2),
                        Duration.ofHours(1));
                  });
          sessionId = issued.session().path("sessionId").asString();
          var grant =
              oauth.deviceAuthorization(
                  Map.of("client_id", "device-example", "scope", "auriga.connect"));
          oauth.decideDevice(grant.path("user_code").asString(), sessionId, true);
          var credential =
              oauth.token(
                  Map.of(
                      "grant_type",
                      OAuthProvider.DEVICE,
                      "client_id",
                      "device-example",
                      "device_code",
                      grant.path("device_code").asString()));
          assertTrue(credential.has("access_token"), credential.toString());
          access = credential.path("access_token").asString();
          device = tokens.verify(access).path("did").asString();
          node = authority.register(access);
          ticket = authority.prepare(access, node.node(), "server-a");
          binding = NetworkAuthorityTest.binding(ticket.ticket());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> authority.redeem(ticket.ticket(), node.node(), device, "server-a", binding));
          connection =
              NetworkAuthorityConnection.newBuilder()
                  .setTenantId(tenant.toString())
                  .setRealm(node.realm())
                  .setNode(node.node())
                  .setIngress(binding.ingress())
                  .setServerId(binding.serverId())
                  .setClientId(binding.clientId())
                  .setClientAccount(binding.account())
                  .build();
        });
  }

  void withRpc(
      java.util.function.Consumer<
              NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceBlockingStub>
          work) {
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
    var jwks =
        new com.nimbusds.jose.jwk.source.ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext>(
            new JWKSet(key.toPublicJWK()));
    var verifier = new AccessTokenVerifier(AccessTokenDecoders.createInternal(settings, jwks));
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(key);
    String secret = Secrets.random();
    var internal =
        new TokenService(
            signing,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            tokens.issuer(),
            Map.of("collab", Secrets.hash(secret)));
    String serviceToken = internal.issue("collab", secret);
    var server =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new IamGrpcNetworkSources(sources)),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:collab")),
            null,
            false,
            Set.of());
    io.grpc.ManagedChannel channel = null;
    try {
      server.start();
      channel =
          GrpcChannels.open(
              "127.0.0.1:" + server.port(),
              new AurigaClientInterceptor(() -> serviceToken, Duration.ofSeconds(5)));
      // Keep this shared stub without an absolute deadline; resolveRpc bounds each new call.
      work.accept(NetworkSourceAuthorityServiceGrpc.newBlockingStub(channel));
    } finally {
      if (channel != null) channel.shutdownNow();
      server.stop();
    }
  }

  @Test
  void threeRpcsUseActualServiceAndPersistentAuthority() {
    withRpc(
        stub ->
            TenantContext.run(
                tenant,
                () -> {
                  var r = request("wire");
                  var source = stub.registerAgentSource(r).getSource();
                  assertEquals(account.toString(), source.getOwnerUserId());
                  assertEquals(connection, stub.getAgentSource(getRequest(source)).getConnection());
                  var bad =
                      getRequest(source).toBuilder()
                          .setConnection(connection.toBuilder().setClientAccount("OTHER"))
                          .build();
                  IamGrpcNetworkAuthoritySpringTest.grpc(
                      io.grpc.Status.Code.PERMISSION_DENIED, () -> stub.getAgentSource(bad));
                  var result = stub.revokeAgentSource(stop(source, "wire-stop"));
                  assertEquals(2, result.getSource().getStateValue());
                  assertEquals("", stub.getAgentSource(getRequest(source)).getValidUntil());
                  assertEquals(source, stub.registerAgentSource(r).getSource());
                }));
  }

  @Test
  void switchesLargeIntegerLimitsAndClockRollbackFailClosed() {
    call(
        () -> {
          var s = sources.register(request("first")).getSource();
          sources = authority(new IamNetworkSourceAuthority.Policy(0, 5));
          error("common.unavailable", () -> sources.get(getRequest(s)));
          error("common.unavailable", () -> sources.register(request("unknown")));
          assertEquals(s, sources.register(request("first")).getSource());
          sources = authority(new IamNetworkSourceAuthority.Policy(30, 5));
          organization =
              req -> {
                clock.advance(-1);
                return allowed();
              };
          error("common.unavailable", () -> sources.get(getRequest(s)));
          clock.advance(1);
          organization = req -> allowed();
          clock.advance(-10);
          error("common.unavailable", () -> sources.get(getRequest(s)));
          clock.advance(10);
          sources = authority(new IamNetworkSourceAuthority.Policy(Long.MAX_VALUE, 300));
          var huge =
              request("large").toBuilder()
                  .setRegistration(
                      request("large").getRegistration().toBuilder().setRegistrationId("large"))
                  .build();
          var fact = sources.register(huge).getSource();
          assertEquals(
              ticket.connectionExpiresAt().truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(fact.getExpiresAt()));
          return null;
        });
  }

  void admin(Runnable work) {
    var p =
        new AurigaPrincipal(
            account.toString(),
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            sessionId,
            null,
            "member",
            List.of("admin"),
            Set.of("iam.network.manage"),
            clock.instant(),
            clock.instant(),
            "policy-test");
    auriga.common.context.RequestContext.start("policy-test", null)
        .withPrincipal(p)
        .run(() -> TenantContext.run(tenant, work));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(
      strings = {"node", "config", "policy", "grant", "withdraw", "account-event"})
  void everyPolicyWriterFencesARealInFlightSourceDecision(String mutation) {
    organization =
        request -> {
          store.requireTransactionReleased();
          admin(
              () -> {
                switch (mutation) {
                  case "node" -> authority.revoke(node.node());
                  case "config" ->
                      authority.configure(new Configuration(true, true, true, Map.of()));
                  case "policy" -> authority.nodePolicy(node.node(), node.node(), false);
                  case "grant" ->
                      authority.grant(
                          node.node(),
                          Sender.AGENT,
                          "1",
                          Operation.FIRST_CONTACT,
                          clock.instant().plusSeconds(60));
                  case "withdraw" ->
                      authority.withdraw(node.node(), Sender.AGENT, "1", Operation.FIRST_CONTACT);
                  default ->
                      new IamAccountEvents(store, sessions)
                          .handle(
                              new auriga.common.events.ReceivedEvent(
                                  UUID.randomUUID(),
                                  "user.permissions_changed",
                                  "org",
                                  "account",
                                  clock.instant(),
                                  tenant,
                                  "1",
                                  null,
                                  Json.object("userId", account.toString())));
                }
              });
          return allowed();
        };
    call(
        () -> {
          error("common.forbidden", () -> sources.register(request("policy-race")));
          assertEquals(
              0,
              tx(
                  () ->
                      store
                          .networkJdbc()
                          .queryForObject(
                              "SELECT count(*) FROM iam.network_source WHERE tenant_id=?",
                              Integer.class,
                              tenant)));
          return null;
        });
  }

  @Test
  void realDifferentOwnerDeviceNodeAndNewBackingSessionNeverInherit() {
    call(
        () -> {
          var first = sources.register(request("first")).getSource();
          var savedConnection = connection;
          UUID otherAccount = UUID.randomUUID();
          var otherSession =
              tx(
                  () -> {
                    UUID provider = UUID.randomUUID();
                    store.insert(
                        "account", otherAccount.toString(), Json.object("status", "active"), null);
                    store.insert("idp_provider", provider.toString(), Json.object(), null);
                    return sessions.create(
                        otherAccount,
                        provider,
                        "portal",
                        true,
                        Duration.ofHours(2),
                        Duration.ofHours(1));
                  });
          var grant =
              oauth.deviceAuthorization(
                  Map.of("client_id", "device-example", "scope", "auriga.connect"));
          oauth.decideDevice(
              grant.path("user_code").asString(),
              otherSession.session().path("sessionId").asString(),
              true);
          var token =
              oauth
                  .token(
                      Map.of(
                          "grant_type",
                          OAuthProvider.DEVICE,
                          "client_id",
                          "device-example",
                          "device_code",
                          grant.path("device_code").asString()))
                  .path("access_token")
                  .asString();
          var otherDevice = tokens.verify(token).path("did").asString();
          var otherNode = authority.register(token);
          var ticket2 = authority.prepare(token, otherNode.node(), "server-a");
          var b = NetworkAuthorityTest.binding(ticket2.ticket());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () ->
                  authority.redeem(ticket2.ticket(), otherNode.node(), otherDevice, "server-a", b));
          organization = req -> allowed();
          connection =
              connection.toBuilder()
                  .setNode(otherNode.node())
                  .setIngress(b.ingress())
                  .setServerId(b.serverId())
                  .setClientId(b.clientId())
                  .setClientAccount(b.account())
                  .build();
          error("common.forbidden", () -> sources.get(getRequest(first)));
          connection = savedConnection;
          var replacement =
              tx(
                  () ->
                      sessions.create(
                          account,
                          UUID.fromString(otherSession.session().path("providerId").asString()),
                          "portal",
                          true,
                          Duration.ofHours(2),
                          Duration.ofHours(1)));
          mutate("device", device, "sessionId", replacement.session().path("sessionId").asString());
          error("common.forbidden", () -> sources.get(getRequest(first)));
          error("common.forbidden", () -> sources.register(request("first")));
          return null;
        });
  }

  @Test
  void absentRetainedAnchorFailsClosedForReceiptTerminalAndStop() throws Exception {
    var r = request("first");
    var first = call(() -> sources.register(r)).getSource();
    clock.advance(31);
    // Corrupt fixture through a separate superuser connection; runtime cannot erase its anchors.
    try (var c =
            PostgresExtension.database(NetworkSourceAuthorityPostgresTest.class)
                .dataSource()
                .getConnection();
        var statement = c.createStatement();
        var delete =
            c.prepareStatement("DELETE FROM iam.network_source_anchor WHERE tenant_id=?")) {
      statement.execute("SET session_replication_role=replica");
      delete.setObject(1, tenant);
      assertEquals(1, delete.executeUpdate());
    }
    call(
        () -> {
          error("common.forbidden", () -> sources.register(r));
          error("common.forbidden", () -> sources.get(getRequest(first)));
          error("common.forbidden", () -> sources.revoke(stop(first, "stop")));
          return null;
        });
  }

  @Test
  void getAndPermanentStopSerializeAndNeverReturnATerminalLease() throws Exception {
    var first = call(() -> sources.register(request("first"))).getSource();
    try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var gate = new java.util.concurrent.CountDownLatch(1);
      var get =
          executor.submit(
              () -> {
                assertTrue(gate.await(5, TimeUnit.SECONDS));
                return call(() -> sources.get(getRequest(first)));
              });
      var revoke =
          executor.submit(
              () -> {
                assertTrue(gate.await(5, TimeUnit.SECONDS));
                return call(() -> sources.revoke(stop(first, "stop")));
              });
      gate.countDown();
      assertEquals(2, revoke.get(15, TimeUnit.SECONDS).getSource().getStateValue());
      var observed = get.get(15, TimeUnit.SECONDS);
      if (observed.getSource().getStateValue() != 1) assertEquals("", observed.getValidUntil());
    }
    assertEquals("", call(() -> sources.get(getRequest(first))).getValidUntil());
  }
}
