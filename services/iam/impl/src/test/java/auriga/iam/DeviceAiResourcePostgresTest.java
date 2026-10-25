package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.crypto.ECDSAVerifier;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.SignedJWT;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class DeviceAiResourcePostgresTest {
  static IamStore store;
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
  final AdminCsrfPostgresTest.Time clock = new AdminCsrfPostgresTest.Time();
  final InstanceAiAuthorization authorization = mock(InstanceAiAuthorization.class);
  final Set<String> effective =
      new HashSet<>(
          Set.of(
              "auriga.connect.models.invoke",
              "auriga.connect.collab.use",
              "auriga.connect",
              "ai.models.invoke",
              "iam.accounts.admin"));
  TokenService tokens;
  SessionManager sessions;
  OAuthProvider oauth;
  ECKey key;
  String refresh, sid;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            DeviceAiResourcePostgresTest.class, "device_ai_owner", "device_ai_app", "iam");
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
  void setup() throws Exception {
    clock.now = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    tokens =
        spy(
            new TokenService(
                keys,
                new ContractSchemas(),
                (t, s, c) -> effective,
                clock,
                "https://portal.example.com",
                Map.of()));
    sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
    when(authorization.getEffectivePermissions(any()))
        .thenAnswer(
            i -> {
              GetEffectivePermissionsRequest r = i.getArgument(0);
              assertEquals(tenant.toString(), r.getTenantId());
              assertEquals(account.toString(), r.getAccountId());
              return GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                  .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                  .addAllPermissions(effective)
                  .build();
            });
    oauth =
        new OAuthProvider(
            store, sessions, tokens, clock, List.of(client("native-example", true)), authorization);
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  store.insert(
                      "account",
                      account.toString(),
                      Json.object("accountId", account, "status", "active"),
                      null);
                  UUID provider = UUID.randomUUID();
                  store.insert(
                      "idp_provider",
                      provider.toString(),
                      Json.object(
                          "providerId", provider, "connectorType", "team-account", "enabled", true),
                      null);
                  var issued =
                      sessions.create(
                          account,
                          provider,
                          "portal",
                          true,
                          Duration.ofSeconds(600),
                          Duration.ofSeconds(120));
                  sid = issued.session().path("sessionId").asString();
                  store.insert(
                      "device",
                      device.toString(),
                      Json.object(
                          "deviceId",
                          device,
                          "accountId",
                          account,
                          "sessionId",
                          sid,
                          "clientId",
                          "native-example",
                          "scope",
                          "auriga.connect",
                          "authorizationMode",
                          "native_tx",
                          "revoked",
                          false),
                      null);
                  refresh = Secrets.random();
                  store.insert(
                      "oauth_grant",
                      Secrets.hash(refresh),
                      Json.object(
                          "kind",
                          "refresh",
                          "clientId",
                          "native-example",
                          "sessionId",
                          sid,
                          "deviceId",
                          device,
                          "scope",
                          "auriga.connect",
                          "authorizationMode",
                          "native_tx",
                          "used",
                          false,
                          "expiresAt",
                          clock.instant().plusSeconds(600).toString()),
                      clock.instant().plusSeconds(600));
                  return null;
                }));
  }

  OAuthClient client(String id, boolean nativeConnect) {
    return new OAuthClient(
        id,
        tenant,
        null,
        Set.of(),
        Set.of("auriga.connect"),
        Set.of(OAuthProvider.DEVICE, "refresh_token"),
        true,
        nativeConnect);
  }

  Map<String, String> input(String raw, boolean ai) {
    var input =
        new HashMap<>(
            Map.of(
                "grant_type",
                "refresh_token",
                "client_id",
                "native-example",
                "refresh_token",
                raw));
    if (ai) input.put("resource", "urn:auriga:ai-gateway");
    return input;
  }

  ObjectNode exchange(String raw, boolean ai) {
    return TenantContext.call(tenant, () -> oauth.token(input(raw, ai)));
  }

  ObjectNode grant() {
    return TenantContext.call(
        tenant, () -> store.tx(() -> store.get("oauth_grant", Secrets.hash(refresh), false)));
  }

  ObjectNode session() {
    return TenantContext.call(tenant, () -> store.tx(() -> store.byId("session", sid, false)));
  }

  @Test
  void signedAiClaimsAreNarrowBoundedAndRotationWorksInBothDirections() throws Exception {
    ObjectNode before = session();
    clock.now = clock.now.plusSeconds(50);
    ObjectNode ai = exchange(refresh, true);
    assertTrue(ai.has("access_token"), ai.toString());
    SignedJWT jwt = SignedJWT.parse(ai.path("access_token").asString());
    assertTrue(jwt.verify(new ECDSAVerifier(key.toPublicJWK())));
    assertEquals("at+jwt", jwt.getHeader().getType().toString());
    ObjectNode claims = tokens.verify(ai.path("access_token").asString());
    assertEquals("auriga.ai-gateway", claims.path("aud").asString());
    assertEquals("device", claims.path("ptyp").asString());
    assertEquals("device", claims.path("ctx").asString());
    assertEquals(device.toString(), claims.path("did").asString());
    assertEquals(account.toString(), claims.path("sub").asString());
    assertEquals(tenant.toString(), claims.path("tid").asString());
    assertFalse(claims.has("sid"));
    assertFalse(claims.has("roles"));
    assertFalse(claims.has("instanceId"));
    assertEquals(
        Json.MAPPER.valueToTree(List.of("auriga.connect.models.invoke")), claims.path("perms"));
    assertEquals("auriga.connect", claims.path("scope").asString());
    assertEquals(70, ai.path("expires_in").asInt());
    assertEquals(
        Instant.parse(before.path("idleExpiresAt").asString()).getEpochSecond(),
        claims.path("exp").asLong());
    assertEquals(before.path("idleExpiresAt"), session().path("idleExpiresAt"));
    assertEquals(before.path("expiresAt"), session().path("expiresAt"));
    ObjectNode platform = exchange(ai.path("refresh_token").asString(), false);
    assertEquals(
        "auriga", tokens.verify(platform.path("access_token").asString()).path("aud").asString());
    ObjectNode next = exchange(platform.path("refresh_token").asString(), true);
    assertTrue(next.has("access_token"), next.toString());
    assertEquals(
        device.toString(),
        tokens.verify(next.path("access_token").asString()).path("did").asString());
    assertNull(
        TenantContext.call(
            tenant,
            () ->
                new DeviceAccessAuthority(store, sessions, tokens)
                    .access(next.path("access_token").asString())));
  }

  @Test
  void invalidTargetsDoNotConsumeRefreshOrRevokeItsSession() {
    for (String resource :
        List.of("", "auriga.ai-gateway", "urn:other", "urn:auriga:ai-gateway urn:other")) {
      var input = input(refresh, true);
      input.put("resource", resource);
      assertEquals(
          "invalid_target",
          TenantContext.call(tenant, () -> oauth.token(input)).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    var audience = input(refresh, false);
    audience.put("audience", "auriga.ai-gateway");
    assertEquals(
        "invalid_target",
        TenantContext.call(tenant, () -> oauth.token(audience)).path("error").asString());
    assertTrue(exchange(refresh, false).has("access_token"));
    assertEquals(
        "invalid_target",
        TenantContext.call(tenant, () -> oauth.token(audience)).path("error").asString());
    assertFalse(session().path("revoked").asBoolean());
  }

  @Test
  void noModelPermissionIsRejectedWithoutConsumingRefresh() {
    effective.remove("auriga.connect.models.invoke");
    assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
    assertFalse(grant().path("used").asBoolean());
    assertTrue(exchange(refresh, false).has("access_token"));
  }

  @Test
  void inactiveUnknownOrUnavailableMembershipCannotIssueOrConsume() {
    for (MemberStatus status :
        List.of(
            MemberStatus.MEMBER_STATUS_DISABLED,
            MemberStatus.MEMBER_STATUS_OFFBOARDED,
            MemberStatus.MEMBER_STATUS_UNSPECIFIED)) {
      when(authorization.getEffectivePermissions(any()))
          .thenReturn(
              GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(status)
                  .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                  .addAllPermissions(effective)
                  .build());
      assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    when(authorization.getEffectivePermissions(any()))
        .thenThrow(new IllegalStateException("offline"));
    assertThrows(auriga.common.error.AurigaException.class, () -> exchange(refresh, true));
    assertFalse(grant().path("used").asBoolean());
  }

  @Test
  void deviceBindingAndAccountRevocationFailClosed() {
    TenantContext.run(
        tenant,
        () -> {
          ObjectNode original = store.tx(() -> store.get("device", device.toString(), false));
          for (String field :
              List.of(
                  "clientId", "sessionId", "accountId", "scope", "authorizationMode", "revoked")) {
            store.tx(
                () -> {
                  ObjectNode row = original.deepCopy();
                  if (field.equals("revoked")) row.put(field, true);
                  else row.put(field, "wrong");
                  store.save("device", row);
                  return null;
                });
            assertEquals("invalid_grant", exchange(refresh, true).path("error").asString(), field);
            assertFalse(grant().path("used").asBoolean());
          }
          store.tx(
              () -> {
                store.save("device", original);
                ObjectNode row = store.get("account", account.toString(), true);
                row.put("status", "disabled");
                store.save("account", row);
                return null;
              });
          assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
          assertFalse(grant().path("used").asBoolean());
        });
  }

  @Test
  void forgedCrossTenantClientAndNonNativeRefreshAreDenied() {
    assertEquals("invalid_grant", exchange("forged", true).path("error").asString());
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            assertThrows(
                auriga.common.error.AurigaException.class,
                () -> oauth.token(input(refresh, true))));
    var other =
        new OAuthProvider(
            store,
            sessions,
            tokens,
            clock,
            List.of(client("native-example", true), client("other-native", true)));
    var wrong = input(refresh, true);
    wrong.put("client_id", "other-native");
    assertEquals(
        "invalid_grant",
        TenantContext.call(tenant, () -> other.token(wrong)).path("error").asString());
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  ObjectNode row = store.get("oauth_grant", Secrets.hash(refresh), true);
                  row.put("authorizationMode", "manual");
                  store.save("oauth_grant", row);
                  return null;
                }));
    assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
    assertFalse(grant().path("used").asBoolean());
  }

  @Test
  void idleOrAbsoluteExpiryCannotBeExtendedByAi() {
    clock.now = clock.now.plusSeconds(121);
    assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
    assertFalse(grant().path("used").asBoolean());
  }

  @Test
  void absoluteDeadlineCapsAiEvenWhenIdleDeadlineIsLater() {
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  ObjectNode row = store.byId("session", sid, true);
                  row.put("expiresAt", clock.instant().plusSeconds(31).toString());
                  store.save("session", row);
                  return null;
                }));
    ObjectNode result = exchange(refresh, true);
    assertTrue(result.has("access_token"), result.toString());
    assertEquals(31, result.path("expires_in").asInt());
    assertEquals(
        clock.instant().plusSeconds(31).getEpochSecond(),
        tokens.verify(result.path("access_token").asString()).path("exp").asLong());
  }

  @Test
  void invalidScopeAndIdentityParametersLeaveRefreshAvailable() {
    for (String scope : List.of("", "openid", "auriga.connect other")) {
      var input = input(refresh, true);
      input.put("scope", scope);
      assertEquals(
          "invalid_scope",
          TenantContext.call(tenant, () -> oauth.token(input)).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    for (String field : List.of("did", "sid", "tenant_id", "permissions")) {
      var input = input(refresh, true);
      input.put(field, "forged");
      assertEquals(
          "invalid_request",
          TenantContext.call(tenant, () -> oauth.token(input)).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    assertTrue(exchange(refresh, true).has("access_token"));
  }

  @Test
  void responseTransactionRollbackRestoresConsumedRefreshAndSuccessor() {
    TenantContext.run(
        tenant,
        () -> {
          int before = store.tx(() -> store.all("oauth_grant").size());
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.tx(
                      () -> {
                        assertTrue(oauth.token(input(refresh, true)).has("access_token"));
                        throw new IllegalStateException("response transaction failed");
                      }));
          assertFalse(grant().path("used").asBoolean());
          assertEquals(before, store.tx(() -> store.all("oauth_grant").size()));
          assertTrue(exchange(refresh, true).has("access_token"));
        });
  }

  @Test
  void signingFailureLeavesTheRefreshUsable() {
    doThrow(new IllegalStateException("signer unavailable"))
        .when(tokens)
        .deviceAi(any(), any(), anyString(), anySet());
    assertThrows(IllegalStateException.class, () -> exchange(refresh, true));
    assertFalse(grant().path("used").asBoolean());
    doCallRealMethod().when(tokens).deviceAi(any(), any(), anyString(), anySet());
    assertTrue(exchange(refresh, true).has("access_token"));
  }

  @Test
  void parallelAiAndPlatformRotationHasOneWinnerAndReplayRevokesFamily() throws Exception {
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var go = new CountDownLatch(1);
      var a =
          pool.submit(
              () -> {
                go.await();
                return exchange(refresh, true);
              });
      var b =
          pool.submit(
              () -> {
                go.await();
                return exchange(refresh, false);
              });
      go.countDown();
      var results = List.of(a.get(10, TimeUnit.SECONDS), b.get(10, TimeUnit.SECONDS));
      assertEquals(1, results.stream().filter(r -> r.has("access_token")).count());
      assertTrue(session().path("revoked").asBoolean());
      String next =
          results.stream()
              .filter(r -> r.has("access_token"))
              .findFirst()
              .orElseThrow()
              .path("refresh_token")
              .asString();
      assertEquals("invalid_grant", exchange(next, true).path("error").asString());
    }
  }
}
