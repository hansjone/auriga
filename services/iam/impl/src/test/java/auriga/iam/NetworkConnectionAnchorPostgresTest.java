package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.persistence.UuidV7;
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
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;

/** Real PG + IAM OAuth/redeem/Resolve/Read gRPC; org permission lookup is an explicit fixture. */
@ExtendWith(PostgresExtension.class)
class NetworkConnectionAnchorPostgresTest {
  static IamStore store;
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID();
  final NetworkAuthorityTest.Time clock = new NetworkAuthorityTest.Time();
  final IamGrpcDevicesSpringTest callers = new IamGrpcDevicesSpringTest();
  SessionManager sessions;
  TokenService tokens;
  OAuthProvider oauth;
  IamNetworkAuthority authority;
  IamGrpcNetworkAuthority rpc;
  String device, sessionId, access;
  Node node;
  Ticket ticket;
  IngressBinding binding;
  NetworkAuthorityConnection connection;
  ECKey key;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkConnectionAnchorPostgresTest.class, "iamrpc_owner", "iamrpc_app", "iam");
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
    rpc = new IamGrpcNetworkAuthority(authority, clock, mock(IamNetworkSourceAuthority.class));
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

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  ResolveNetworkTargetResponse resolve(NetworkAuthorityConnection c) {
    return callers.as(
        "svc:collab",
        tenant,
        () -> {
          var result = new IamGrpcDevicesSpringTest.Capture<ResolveNetworkTargetResponse>();
          rpc.resolveNetworkTarget(
              ResolveNetworkTargetRequest.newBuilder().setConnection(c).setLocal("0").build(),
              result);
          return result.values.getFirst();
        });
  }

  void mutate(String table, String id, String field, Object value) {
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  var row = store.byId(table, id, true);
                  assertNotNull(row);
                  row.set(field, Json.MAPPER.valueToTree(value));
                  store.save(table, row);
                  return null;
                }));
    // Read in a new transaction only after the mutation has committed, including FK checks.
    assertEquals(
        Json.MAPPER.valueToTree(value),
        TenantContext.call(tenant, () -> store.tx(() -> store.byId(table, id, false))).path(field));
  }

  void withRpc(
      java.util.function.Consumer<NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub>
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
    var server =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(rpc),
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
              // Historical tests advance the shared clock beyond the internal JWT lifetime.
              new AurigaClientInterceptor(
                  () -> internal.issue("collab", secret), Duration.ofSeconds(5)));
      // Keep this shared stub without an absolute deadline; resolveRpc bounds each new call.
      work.accept(NetworkAuthorityServiceGrpc.newBlockingStub(channel));
    } finally {
      if (channel != null) channel.shutdownNow();
      server.stop();
    }
  }

  ResolveNetworkTargetResponse resolveRpc(
      NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub stub) {
    return TenantContext.call(
        tenant,
        () ->
            stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                .resolveNetworkTarget(
                    ResolveNetworkTargetRequest.newBuilder()
                        .setConnection(connection)
                        .setLocal("0")
                        .build()));
  }

  <T> T tx(Supplier<T> work) {
    return store.tenant(tenant, work);
  }

  int count(String table) {
    return tx(
        () ->
            store.networkJdbc().queryForObject("SELECT count(*) FROM iam." + table, Integer.class));
  }

  NetworkConnectionAnchor reference(
      NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub stub,
      NetworkAuthorityConnection c) {
    var result =
        TenantContext.call(
            tenant,
            () ->
                stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                    .resolveNetworkTarget(
                        ResolveNetworkTargetRequest.newBuilder()
                            .setConnection(c)
                            .setLocal("0")
                            .build()));
    assertTrue(result.getAllowed(), "new C2 must actually resolve before testing history");
    assertEquals(c, result.getConnection());
    assertTrue(result.hasConnectionAnchor());
    assertEquals(c, result.getConnectionAnchor().getConnection());
    return result.getConnectionAnchor();
  }

  ReadNetworkConnectionAnchorResponse read(
      NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub stub,
      NetworkConnectionAnchor original,
      NetworkConnectionAnchor current) {
    return TenantContext.call(
        tenant,
        () ->
            stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                .readNetworkConnectionAnchor(
                    ReadNetworkConnectionAnchorRequest.newBuilder()
                        .setOriginal(original)
                        .setCurrent(current)
                        .build()));
  }

  void denied(
      NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub stub,
      NetworkConnectionAnchor original,
      NetworkConnectionAnchor current) {
    IamGrpcNetworkAuthoritySpringTest.grpc(
        io.grpc.Status.Code.PERMISSION_DENIED, () -> read(stub, original, current));
  }

  NetworkAuthorityConnection redeem(
      String owner, String did, String nodeId, String server, String cid, String brokerAccount) {
    return TenantContext.call(
        tenant,
        () -> {
          String token =
              tokens.access(
                  tenant,
                  owner,
                  "auriga",
                  "device",
                  "device",
                  null,
                  UUID.fromString(did),
                  clock.instant(),
                  clock.instant(),
                  "auriga.connect");
          var next = authority.prepare(token, nodeId, "server-a");
          var b =
              new IngressBinding(
                  server,
                  cid,
                  "U" + "A".repeat(55),
                  IamNetworkAuthority.ingressForTicket(next.ticket()),
                  brokerAccount);
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> authority.redeem(next.ticket(), nodeId, did, "server-a", b));
          return connection.toBuilder()
              .setNode(nodeId)
              .setIngress(b.ingress())
              .setServerId(server)
              .setClientId(cid)
              .setClientAccount(brokerAccount)
              .build();
        });
  }

  NetworkAuthorityConnection reconnect() {
    return redeem(
        account.toString(), device, node.node(), "N" + "B".repeat(55), "43", "OTHER-BROKER");
  }

  String session(UUID owner) {
    return tx(
        () -> {
          UUID provider = UuidV7.next();
          store.insert("idp_provider", provider.toString(), Json.object(), null);
          return sessions
              .create(owner, provider, "portal", true, Duration.ofHours(2), Duration.ofHours(2))
              .session()
              .path("sessionId")
              .asString();
        });
  }

  String device(UUID owner, String sid) {
    String did = UuidV7.next().toString();
    tx(
        () ->
            store.insert(
                "device",
                did,
                Json.object(
                    "accountId",
                    owner,
                    "sessionId",
                    sid,
                    "scope",
                    "auriga.connect",
                    "revoked",
                    false),
                null));
    return did;
  }

  Map<String, List<String>> facts() {
    return tx(
        () -> {
          var result = new TreeMap<String, List<String>>();
          for (String table :
              List.of(
                  "network_connection_anchor",
                  "network_source_anchor",
                  "network_source",
                  "network_source_operation",
                  "network_source_chain",
                  "account",
                  "session",
                  "device",
                  "network_node",
                  "network_ingress",
                  "network_ticket",
                  "network_policy_revision"))
            result.put(
                table,
                store
                    .networkJdbc()
                    .queryForList(
                        "SELECT to_jsonb(t)::text FROM iam."
                            + table
                            + " t ORDER BY to_jsonb(t)::text",
                        String.class));
          return result;
        });
  }

  @Test
  void committedRedeemHasOneImmutableRlsAnchorAndRetryDoesNotDuplicate() {
    withRpc(
        stub -> {
          var a = reference(stub, connection);
          assertEquals(7, UUID.fromString(a.getAnchorId()).version());
          assertEquals(1, count("network_connection_anchor"));
          tx(
              () -> {
                var jdbc = store.networkJdbc();
                assertTrue(
                    jdbc.queryForObject(
                        "SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE oid='iam.network_connection_anchor'::regclass",
                        Boolean.class));
                var row =
                    jdbc.queryForMap(
                        "SELECT a.*,i.id AS real_ingress FROM iam.network_connection_anchor a JOIN iam.network_ingress i ON i.tenant_id=a.tenant_id AND i.lookup_key=a.ingress_key");
                assertEquals(row.get("real_ingress"), row.get("ingress_id"));
                assertEquals(account, row.get("owner_id"));
                assertEquals(UUID.fromString(device), row.get("device_id"));
                assertEquals(UUID.fromString(sessionId), row.get("session_id"));
                assertArrayEquals(connection.toByteArray(), (byte[]) row.get("connection"));
                assertNotNull(row.get("redeemed_at"));
                return null;
              });
          assertThrows(
              DataAccessException.class,
              () ->
                  tx(
                      () ->
                          store
                              .networkJdbc()
                              .update(
                                  "UPDATE iam.network_connection_anchor SET redeemed_at=redeemed_at")));
          assertThrows(
              DataAccessException.class,
              () ->
                  tx(
                      () ->
                          store.networkJdbc().update("DELETE FROM iam.network_connection_anchor")));
          assertEquals(
              0,
              store
                  .networkJdbc()
                  .queryForObject(
                      "SELECT count(*) FROM iam.network_connection_anchor", Integer.class));
          assertEquals(
              0,
              TenantContext.call(
                  UUID.randomUUID(),
                  () ->
                      store.tx(
                          () ->
                              store
                                  .networkJdbc()
                                  .queryForObject(
                                      "SELECT count(*) FROM iam.network_connection_anchor",
                                      Integer.class))));
          assertThrows(
              DataAccessException.class,
              () ->
                  tx(
                      () ->
                          store
                              .networkJdbc()
                              .update(
                                  "INSERT INTO iam.network_connection_anchor SELECT * FROM iam.network_connection_anchor")));
          assertThrows(
              DataAccessException.class,
              () ->
                  tx(
                      () ->
                          store
                              .networkJdbc()
                              .update(
                                  """
          INSERT INTO iam.network_connection_anchor
          (id,tenant_id,ingress_id,ingress_key,connection,realm,node,owner_id,device_id,session_id,redeemed_at)
          SELECT ?,?,ingress_id,ingress_key,connection,realm,node,owner_id,device_id,session_id,redeemed_at
          FROM iam.network_connection_anchor
          """,
                                  UuidV7.next(),
                                  UUID.randomUUID())));
          var before = facts();
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> {
                assertThrows(
                    AurigaException.class,
                    () ->
                        authority.redeem(
                            ticket.ticket(), node.node(), device, "server-a", binding));
                return null;
              });
          assertEquals(a, read(stub, a, a).getOriginal());
          assertEquals(before, facts());
        });
  }

  @Test
  void insertFailureRollsBackIngressAndAnchorAndInvalidRedeemLeavesNoNewFact() {
    int before = count("network_connection_anchor");
    var failingJdbc =
        new JdbcTemplate(store.networkJdbc().getDataSource()) {
          @Override
          public int update(String sql, Object... args) {
            int changed = super.update(sql, args);
            if (sql.contains("INSERT INTO iam.network_connection_anchor"))
              throw new IllegalStateException("after actual insert");
            return changed;
          }
        };
    var failingStore =
        new IamStore(failingJdbc, new TenantTransactionManager(failingJdbc.getDataSource()));
    var failing =
        new IamNetworkAuthority(
            failingStore,
            new DeviceAccessAuthority(failingStore, sessions, tokens),
            mock(AuditLogger.class),
            clock,
            Set.of("server-a"));
    TenantContext.run(
        tenant,
        () -> {
          var next = authority.prepare(access, node.node(), "server-a");
          var b =
              new IngressBinding(
                  binding.serverId(),
                  "99",
                  binding.userNkey(),
                  IamNetworkAuthority.ingressForTicket(next.ticket()),
                  binding.account());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> {
                assertThrows(
                    IllegalStateException.class,
                    () -> failing.redeem(next.ticket(), node.node(), device, "server-a", b));
                return null;
              });
          assertNull(tx(() -> store.get("network_ingress", b.ingress(), false)));
          assertNull(tx(() -> store.get("network_ticket", Secrets.hash(next.ticket()), false)));
          assertEquals(before, count("network_connection_anchor"));
          var invalid = authority.prepare(access, node.node(), "server-a");
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> {
                assertThrows(
                    AurigaException.class,
                    () ->
                        authority.redeem(
                            invalid.ticket(),
                            node.node(),
                            device,
                            "wrong",
                            NetworkAuthorityTest.binding(invalid.ticket())));
                return null;
              });
          assertEquals(before, count("network_connection_anchor"));
        });
  }

  @Test
  void sameBackingSessionReconnectSurvivesRealIngressSweepWithoutAnyReadWrites() {
    withRpc(
        stub -> {
          var original = reference(stub, connection);
          mutate(
              "session", sessionId, "idleExpiresAt", clock.instant().plusSeconds(7200).toString());
          clock.advance(3500);
          var c2 = reconnect();
          var current = reference(stub, c2);
          assertNotEquals(original.getAnchorId(), current.getAnchorId());
          clock.advance(101);
          assertTrue(new NetworkIngressCleanup(store, clock).sweepOnce() >= 1);
          assertNull(tx(() -> store.get("network_ingress", connection.getIngress(), false)));
          assertEquals(current, reference(stub, c2));
          var before = facts();
          var result = read(stub, original, current);
          assertEquals(original, result.getOriginal());
          assertEquals(current, result.getCurrent());
          assertEquals(before, facts());
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"node", "owner", "device", "session", "deviceOwner", "sessionOwner"})
  void legalNewC2CannotReadHistoryAfterOriginalAssociationChanges(String change) {
    withRpc(
        stub -> {
          var original = reference(stub, connection);
          UUID other = UuidV7.next();
          tx(
              () ->
                  store.insert("account", other.toString(), Json.object("status", "active"), null));
          String owner = account.toString(), did = device, nodeId = node.node();
          if (change.equals("owner")) {
            mutate("session", sessionId, "accountId", other.toString());
            mutate("device", device, "accountId", other.toString());
            tx(
                () ->
                    store
                        .networkJdbc()
                        .update(
                            "UPDATE iam.network_node SET account_id=? WHERE tenant_id=? AND node=?",
                            other,
                            tenant,
                            new java.math.BigDecimal(node.node())));
            owner = other.toString();
          } else if (change.equals("session") || change.equals("sessionOwner")) {
            if (change.equals("sessionOwner"))
              mutate("session", sessionId, "accountId", other.toString());
            mutate("device", device, "sessionId", session(account));
          } else {
            if (change.equals("deviceOwner"))
              mutate("device", device, "accountId", other.toString());
            did = device(account, sessionId);
            if (change.equals("node")) {
              String token =
                  tokens.access(
                      tenant,
                      owner,
                      "auriga",
                      "device",
                      "device",
                      null,
                      UUID.fromString(did),
                      clock.instant(),
                      clock.instant(),
                      "auriga.connect");
              nodeId = TenantContext.call(tenant, () -> authority.register(token)).node();
            } else {
              String nextDevice = did;
              tx(
                  () ->
                      store
                          .networkJdbc()
                          .update(
                              "UPDATE iam.network_node SET device_id=? WHERE tenant_id=? AND node=?",
                              UUID.fromString(nextDevice),
                              tenant,
                              new java.math.BigDecimal(node.node())));
            }
          }
          var c2 = redeem(owner, did, nodeId, "N" + "C".repeat(55), "44", "C2-BROKER");
          var current = reference(stub, c2);
          var before = facts();
          denied(stub, original, current);
          assertEquals(before, facts());
        });
  }

  @Test
  void everyNonemptyOriginalTupleSubstitutionAndWrongIdFailsAgainstRealRedeems() {
    withRpc(
        stub -> {
          var original = reference(stub, connection);
          var c2 = reconnect();
          var current = reference(stub, c2);
          var before = facts();
          for (var c :
              List.of(
                  connection.toBuilder().setIngress(c2.getIngress()),
                  connection.toBuilder().setServerId(c2.getServerId()),
                  connection.toBuilder().setClientId(c2.getClientId()),
                  connection.toBuilder().setClientAccount(c2.getClientAccount()),
                  connection.toBuilder().setRealm("99999"),
                  connection.toBuilder().setNode("99999"),
                  connection.toBuilder().setTenantId(UUID.randomUUID().toString()))) {
            denied(stub, original.toBuilder().setConnection(c).build(), current);
            denied(stub, original, current.toBuilder().setConnection(c).build());
          }
          denied(stub, original.toBuilder().setAnchorId(current.getAnchorId()).build(), current);
          denied(stub, original.toBuilder().setAnchorId(UuidV7.next().toString()).build(), current);
          IamGrpcNetworkAuthoritySpringTest.grpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () ->
                  TenantContext.call(
                      UUID.randomUUID(),
                      () ->
                          stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                              .readNetworkConnectionAnchor(
                                  ReadNetworkConnectionAnchorRequest.newBuilder()
                                      .setOriginal(original)
                                      .setCurrent(current)
                                      .build())));
          assertEquals(before, facts());
        });
  }

  @Test
  void logoutExpiryAndRevocationAloneDoNotChangeHistoricalAssociationsOrGrantCurrentLiveness() {
    withRpc(
        stub -> {
          var original = reference(stub, connection);
          var current = reference(stub, reconnect());
          TenantContext.run(
              tenant,
              () ->
                  store.tx(
                      () -> {
                        sessions.revokeOne(store.byId("session", sessionId, true), "logout");
                        return null;
                      }));
          mutate("device", device, "revoked", true);
          var before = facts();
          assertEquals(current, read(stub, original, current).getCurrent());
          assertEquals(before, facts());
          assertFalse(resolve(connection).getAllowed());
          clock.advance(7201);
          before = facts();
          assertEquals(original, read(stub, original, current).getOriginal());
          assertEquals(before, facts());
          // Read returns historical association facts even when BOTH transports are dead.
          assertFalse(resolve(current.getConnection()).getAllowed());
        });
  }

  @Test
  void preUpgradeIngressKeepsOldResolveFieldsButNeverBackfillsOrAcceptsMissingHistory() {
    var legacy = connection.toBuilder().setIngress("z".repeat(26)).setClientId("101").build();
    tx(
        () -> {
          var data = store.get("network_ingress", connection.getIngress(), false).deepCopy();
          data.put("clientId", legacy.getClientId());
          return store.insert(
              "network_ingress", legacy.getIngress(), data, ticket.connectionExpiresAt());
        });
    withRpc(
        stub -> {
          var before = facts();
          var result =
              TenantContext.call(
                  tenant,
                  () ->
                      stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                          .resolveNetworkTarget(
                              ResolveNetworkTargetRequest.newBuilder()
                                  .setConnection(legacy)
                                  .setLocal("0")
                                  .build()));
          assertTrue(result.getAllowed());
          assertEquals(legacy, result.getConnection());
          assertEquals("system", result.getTargetType());
          assertEquals("0", result.getLocal());
          assertEquals(ticket.connectionExpiresAt().toString(), result.getValidUntil());
          assertFalse(result.hasConnectionAnchor());
          denied(
              stub,
              NetworkConnectionAnchor.newBuilder()
                  .setAnchorId(UuidV7.next().toString())
                  .setConnection(legacy)
                  .build(),
              reference(stub, connection));
          IamGrpcNetworkAuthoritySpringTest.grpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () ->
                  TenantContext.call(
                      tenant,
                      () ->
                          stub.readNetworkConnectionAnchor(
                              ReadNetworkConnectionAnchorRequest.getDefaultInstance())));
          assertEquals(before, facts());
          // A malformed retained tuple is never silently downgraded to the pre-upgrade absence
          // case.
          UUID badId = UuidV7.next();
          tx(
              () ->
                  store
                      .networkJdbc()
                      .update(
                          """
          INSERT INTO iam.network_connection_anchor
          (id,tenant_id,ingress_id,ingress_key,connection,realm,node,owner_id,device_id,session_id,redeemed_at)
          SELECT ?,tenant_id,id,lookup_key,?,?,?, ?,?,?,? FROM iam.network_ingress WHERE lookup_key=?
          """,
                          badId,
                          legacy.toBuilder().setClientAccount("CORRUPTED").build().toByteArray(),
                          new java.math.BigDecimal(legacy.getRealm()),
                          new java.math.BigDecimal(legacy.getNode()),
                          account,
                          UUID.fromString(device),
                          UUID.fromString(sessionId),
                          java.sql.Timestamp.from(clock.instant()),
                          legacy.getIngress()));
          before = facts();
          IamGrpcNetworkAuthoritySpringTest.grpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () ->
                  TenantContext.call(
                      tenant,
                      () ->
                          stub.resolveNetworkTarget(
                              ResolveNetworkTargetRequest.newBuilder()
                                  .setConnection(legacy)
                                  .setLocal("0")
                                  .build())));
          denied(
              stub,
              NetworkConnectionAnchor.newBuilder()
                  .setAnchorId(badId.toString())
                  .setConnection(legacy)
                  .build(),
              reference(stub, connection));
          assertEquals(before, facts());
        });
  }
}
