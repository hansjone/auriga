package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
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

/** Real OAuth device grant, signed device token, register/prepare/redeem and FORCE RLS. */
@ExtendWith(PostgresExtension.class)
class NetworkAuthorityRpcPostgresTest {
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
            NetworkAuthorityRpcPostgresTest.class, "iamrpc_owner", "iamrpc_app", "iam");
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
    String serviceToken = internal.issue("collab", secret);
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
              new AurigaClientInterceptor(() -> serviceToken, Duration.ofSeconds(5)));
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

  @Test
  void fourRpcsUseRealDeviceAndPersistedBindingWithoutRefreshingSessionOrTicket() {
    var before =
        TenantContext.call(tenant, () -> store.tx(() -> store.byId("session", sessionId, false)));
    var result = resolve(connection);
    assertTrue(result.getAllowed());
    assertEquals(connection, result.getConnection());
    assertEquals(ticket.connectionExpiresAt().toString(), result.getValidUntil());
    callers.as(
        "svc:collab",
        tenant,
        () -> {
          var active = new IamGrpcDevicesSpringTest.Capture<NetworkAuthorityActiveResponse>();
          rpc.networkAuthorityActive(
              NetworkAuthorityActiveRequest.newBuilder()
                  .setRealm(node.realm())
                  .setNode(node.node())
                  .build(),
              active);
          assertTrue(active.values.getFirst().getActive());
          var check = new IamGrpcDevicesSpringTest.Capture<CheckNetworkResponse>();
          rpc.checkNetwork(
              CheckNetworkRequest.newBuilder()
                  .setRealm(node.realm())
                  .setSourceNode(node.node())
                  .setTargetNode(node.node())
                  .setLocal("0")
                  .setSender(NetworkAuthoritySender.NETWORK_AUTHORITY_SENDER_HUMAN)
                  .setOperation(NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_RECEIVE)
                  .setTargetNodes(1)
                  .setTargetLocals(1)
                  .build(),
              check);
          assertTrue(check.values.getFirst().getAllowed());
          var limits = new IamGrpcDevicesSpringTest.Capture<NetworkLimitsResponse>();
          rpc.networkLimits(NetworkLimitsRequest.getDefaultInstance(), limits);
          assertEquals(Limit.values().length, limits.values.getFirst().getLimitsCount());
          return null;
        });
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  assertEquals(before, store.byId("session", sessionId, false));
                  assertNull(store.get("network_ticket", Secrets.hash(ticket.ticket()), false));
                  return null;
                }));
    callers.as(
        "svc:collab",
        UUID.randomUUID(),
        () -> {
          var capture = new IamGrpcDevicesSpringTest.Capture<ResolveNetworkTargetResponse>();
          rpc.resolveNetworkTarget(
              ResolveNetworkTargetRequest.newBuilder()
                  .setConnection(
                      connection.toBuilder().setTenantId(TenantContext.require().toString()))
                  .setLocal("0")
                  .build(),
              capture);
          assertFalse(capture.values.getFirst().getAllowed());
          return null;
        });
  }

  @Test
  void eachTupleMismatchFailsClosedAndExpiredOrDeletedRedemptionIsNeverReconstructed() {
    for (var other :
        List.of(
            connection.toBuilder().setRealm("1"),
            connection.toBuilder().setNode("9999"),
            connection.toBuilder().setIngress("b".repeat(26)),
            connection.toBuilder().setServerId("N" + "B".repeat(55)),
            connection.toBuilder().setClientId("43"),
            connection.toBuilder().setClientAccount("OTHER")))
      assertEquals(ResolveNetworkTargetResponse.getDefaultInstance(), resolve(other.build()));
    clock.advance(3600);
    assertFalse(resolve(connection).getAllowed());
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  store.delete("network_ingress", binding.ingress());
                  return null;
                }));
    assertFalse(resolve(connection).getAllowed());
  }

  @Test
  void currentBackingExpiryRevocationAndOwnerChangesAreObservedOnEveryCall() {
    UUID otherAccount = UUID.randomUUID();
    // Both session and device accountId have tenant-scoped FKs. Commit a real target first.
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () ->
                    store.insert(
                        "account",
                        otherAccount.toString(),
                        Json.object("accountId", otherAccount, "status", "active"),
                        null)));
    assertNotNull(
        TenantContext.call(
            tenant, () -> store.tx(() -> store.byId("account", otherAccount.toString(), false))));
    withRpc(
        stub -> {
          var expiry = clock.instant().plusSeconds(10);
          mutate("session", sessionId, "idleExpiresAt", expiry.toString());
          assertEquals(expiry.toString(), resolveRpc(stub).getValidUntil());
          mutate("device", device, "revoked", true);
          assertEquals(ResolveNetworkTargetResponse.getDefaultInstance(), resolveRpc(stub));
          mutate("device", device, "revoked", false);
          assertTrue(resolveRpc(stub).getAllowed());
          mutate("session", sessionId, "accountId", otherAccount.toString());
          assertEquals(ResolveNetworkTargetResponse.getDefaultInstance(), resolveRpc(stub));
          mutate("session", sessionId, "accountId", account.toString());
          assertTrue(resolveRpc(stub).getAllowed());
          mutate("account", account.toString(), "status", "disabled");
          assertEquals(ResolveNetworkTargetResponse.getDefaultInstance(), resolveRpc(stub));
          mutate("account", account.toString(), "status", "active");
          assertTrue(resolveRpc(stub).getAllowed());
          oauthRevoke();
          assertEquals(ResolveNetworkTargetResponse.getDefaultInstance(), resolveRpc(stub));
        });
  }

  void oauthRevoke() {
    // The confidential/manual device flow has no native public revocation endpoint; revoke the
    // real backing session through the same SessionManager authority used by logout.
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  sessions.revokeOne(store.byId("session", sessionId, true), "logout");
                  return null;
                }));
  }
}
