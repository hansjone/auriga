package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority.*;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
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
    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
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
    rpc = new IamGrpcNetworkAuthority(authority, clock);
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
                  row.set(field, Json.MAPPER.valueToTree(value));
                  store.save(table, row);
                  return null;
                }));
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
    var expiry = clock.instant().plusSeconds(10);
    mutate("session", sessionId, "idleExpiresAt", expiry.toString());
    assertEquals(expiry.toString(), resolve(connection).getValidUntil());
    mutate("device", device, "revoked", true);
    assertFalse(resolve(connection).getAllowed());
    mutate("device", device, "revoked", false);
    mutate("session", sessionId, "accountId", UUID.randomUUID().toString());
    assertFalse(resolve(connection).getAllowed());
    mutate("session", sessionId, "accountId", account.toString());
    mutate("account", account.toString(), "status", "disabled");
    assertFalse(resolve(connection).getAllowed());
    mutate("account", account.toString(), "status", "active");
    assertTrue(resolve(connection).getAllowed());
    oauthRevoke();
    assertFalse(resolve(connection).getAllowed());
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
