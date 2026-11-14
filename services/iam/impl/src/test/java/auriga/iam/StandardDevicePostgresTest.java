package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import auriga.common.context.*;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import io.grpc.stub.StreamObserver;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.http.MediaType;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class StandardDevicePostgresTest {
  static IamStore store;

  /** Existing real OAuth/network fixtures now use the public standard-device registration. */
  static InstanceAiAuthorization networkAuthorization() {
    return request -> {
      assertFalse(
          org.springframework.transaction.support.TransactionSynchronizationManager
              .isActualTransactionActive());
      return GetEffectivePermissionsResponse.newBuilder()
          .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
          .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
          .addAllPermissions(Set.of("auriga.connect", "auriga.connect.collab.use"))
          .build();
    };
  }

  @BeforeAll
  static void migrate() {
    UacFlowPostgresTest.migrate();
    store = UacFlowPostgresTest.store;
  }

  static final class Harness implements AutoCloseable {
    final UacFlowPostgresTest.Fixture f;
    final TokenService tokens;
    final SigningKeys signing = mock(SigningKeys.class);
    final SessionManager.Issued browser;
    final OAuthProvider oauth;
    final InstanceAiAuthorization authorization = mock(InstanceAiAuthorization.class);
    final String sid;
    final Set<String> effective =
        new HashSet<>(
            Set.of(
                "auriga.connect",
                DeviceAiResource.INVOKE,
                "auriga.connect.collab.use",
                "iam.accounts.admin"));

    Harness() throws Exception {
      f = new UacFlowPostgresTest.Fixture("token-verify", 3);
      var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
      when(signing.current())
          .thenAnswer(
              i -> {
                assertFalse(
                    org.springframework.transaction.support.TransactionSynchronizationManager
                        .isActualTransactionActive(),
                    "Signing key must be loaded before borrowing the tenant transaction connection");
                return key;
              });
      when(signing.jwks())
          .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
      tokens =
          spy(
              new TokenService(
                  signing,
                  new ContractSchemas(),
                  (t, a, c) -> {
                    fail("Standard devices must obtain current owner permissions");
                    return Set.of();
                  },
                  f.time,
                  "https://portal.example.com",
                  Map.of()));
      when(authorization.getEffectivePermissions(any()))
          .thenAnswer(
              i -> {
                assertFalse(
                    org.springframework.transaction.support.TransactionSynchronizationManager
                        .isActualTransactionActive(),
                    "Org must run after IAM locks are released");
                return GetEffectivePermissionsResponse.newBuilder()
                    .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                    .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                    .addAllPermissions(effective)
                    .build();
              });
      var clients = new ArrayList<OAuthClient>();
      for (String name : List.of("desktop-example", "other-example"))
        clients.add(
            new OAuthClient(
                name,
                f.tenant,
                null,
                Set.of(),
                Set.of("auriga.connect", "openid", "offline_access"),
                Set.of(OAuthProvider.DEVICE, "refresh_token"),
                true,
                false));
      oauth = new OAuthProvider(store, f.sessions, tokens, f.time, clients, authorization);
      browser = f.login().session();
      sid = browser.session().path("sessionId").asString();
    }

    ObjectNode issue() {
      var challenge =
          f.call(
              () ->
                  oauth.deviceAuthorization(
                      Map.of(
                          "client_id",
                          "desktop-example",
                          "scope",
                          "auriga.connect openid offline_access")));
      f.call(
          () -> {
            oauth.decideDevice(challenge.path("user_code").asString(), sid, true);
            return null;
          });
      return challenge;
    }

    ObjectNode exchange(ObjectNode challenge) {
      return f.call(
          () ->
              oauth.token(
                  Map.of(
                      "client_id",
                      "desktop-example",
                      "grant_type",
                      OAuthProvider.DEVICE,
                      "device_code",
                      challenge.path("device_code").asString())));
    }

    ObjectNode refresh(String raw, String resource) {
      var input =
          new HashMap<>(
              Map.of(
                  "grant_type",
                  "refresh_token",
                  "client_id",
                  "desktop-example",
                  "refresh_token",
                  raw));
      if (resource != null) input.put("resource", resource);
      return f.call(() -> oauth.token(input));
    }

    ObjectNode claims(ObjectNode response) {
      return f.call(() -> tokens.verify(response.path("access_token").asString()));
    }

    ObjectNode identity(ObjectNode response) {
      return f.call(
          () -> oauth.standardDevices().identity(response.path("access_token").asString()));
    }

    boolean consumed(String raw) {
      return f.call(
          () ->
              store.tx(
                  () ->
                      store.get("oauth_grant", Secrets.hash(raw), false).path("used").asBoolean()));
    }

    public void close() {
      f.close();
    }
  }

  @Test
  void issuesBoundDeviceIdentityAndRotatesOneFamilyAcrossBothAudiencesWithoutTouchingSource()
      throws Exception {
    try (var h = new Harness()) {
      var source = h.f.session(h.sid);
      var response = h.exchange(h.issue());
      assertFalse(response.has("error"));
      var claims = h.claims(response);
      assertEquals("auriga", claims.path("aud").asString());
      assertEquals("device", claims.path("ptyp").asString());
      assertEquals("device", claims.path("ctx").asString());
      assertFalse(claims.has("sid"));
      assertEquals(source.path("uacSessionBinding"), claims.path("uacSessionBinding"));
      assertEquals(
          source.path("authTime").asString(),
          Instant.ofEpochSecond(claims.path("auth_time").asLong()).toString());
      var identity = h.identity(response);
      assertEquals(h.f.account.toString(), identity.path("memberId").asString());
      assertEquals(h.f.provider, identity.path("providerId").asString());
      assertEquals("standard_device", identity.path("authorizationMode").asString());
      assertFalse(identity.toString().contains(response.path("access_token").asString()));
      var ai = h.refresh(response.path("refresh_token").asString(), DeviceAiResource.RESOURCE);
      assertFalse(ai.has("error"));
      var aiClaims = h.claims(ai);
      assertEquals("auriga.ai-gateway", aiClaims.path("aud").asString());
      assertEquals(
          Json.MAPPER.valueToTree(StandardDevices.AI.stream().sorted().toList()),
          aiClaims.path("perms"));
      assertNull(
          h.f.call(() -> h.oauth.deviceAuthority().access(ai.path("access_token").asString())));
      var back = h.refresh(ai.path("refresh_token").asString(), null);
      assertEquals("auriga", h.claims(back).path("aud").asString());
      assertEquals(identity.path("deviceId"), h.identity(back).path("deviceId"));
      var after = h.f.session(h.sid);
      for (String field :
          List.of("idleExpiresAt", "expiresAt", "authGeneration", "nextRevalidateAt"))
        assertEquals(source.path(field), after.path(field));
      assertTrue(h.consumed(response.path("refresh_token").asString()));
      assertTrue(h.consumed(ai.path("refresh_token").asString()));
    }
  }

  @Test
  void invalidTargetScopeControlFailureAndSigningFailureCannotConsumeRefresh() throws Exception {
    try (var h = new Harness()) {
      String refresh = h.exchange(h.issue()).path("refresh_token").asString();
      assertEquals(
          "invalid_target",
          h.refresh(refresh, "https://other.example.com").path("error").asString());
      for (var extra :
          List.of(
              Map.of("scope", "auriga.connect admin"),
              Map.of("deviceId", UUID.randomUUID().toString()))) {
        var input =
            new HashMap<>(
                Map.of(
                    "grant_type",
                    "refresh_token",
                    "client_id",
                    "desktop-example",
                    "refresh_token",
                    refresh));
        input.putAll(extra);
        assertTrue(h.f.call(() -> h.oauth.token(input)).has("error"));
        assertFalse(h.consumed(refresh));
      }
      doThrow(new AurigaException(CommonErrors.UNAVAILABLE))
          .when(h.authorization)
          .getEffectivePermissions(any());
      assertEquals(
          "common.unavailable",
          assertThrows(AurigaException.class, () -> h.refresh(refresh, null)).errorCode().code());
      assertFalse(h.consumed(refresh));
      doAnswer(
              i ->
                  GetEffectivePermissionsResponse.newBuilder()
                      .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                      .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                      .addAllPermissions(h.effective)
                      .build())
          .when(h.authorization)
          .getEffectivePermissions(any());
      doThrow(new IllegalStateException("synthetic signer failure"))
          .when(h.tokens)
          .standardDevice(any(), any(), anyString(), anyString(), anySet(), anyString());
      assertThrows(IllegalStateException.class, () -> h.refresh(refresh, null));
      assertFalse(h.consumed(refresh));
      doCallRealMethod()
          .when(h.tokens)
          .standardDevice(any(), any(), anyString(), anyString(), anySet(), anyString());
      assertFalse(h.refresh(refresh, null).has("error"));
    }
  }

  @Test
  void replayAndSelfRevocationAffectOnlyTheSelectedDevice() throws Exception {
    try (var h = new Harness()) {
      var first = h.exchange(h.issue());
      var other = h.exchange(h.issue());
      var rotated = h.refresh(first.path("refresh_token").asString(), DeviceAiResource.RESOURCE);
      assertEquals(
          "invalid_grant",
          h.refresh(first.path("refresh_token").asString(), null).path("error").asString());
      assertThrows(AurigaException.class, () -> h.identity(first));
      assertEquals(
          "invalid_grant",
          h.refresh(rotated.path("refresh_token").asString(), null).path("error").asString());
      assertNotNull(h.identity(other));
      assertFalse(h.f.session(h.sid).path("revoked").asBoolean());
      assertTrue(
          h.f
              .call(
                  () ->
                      h.oauth.revoke(
                          Map.of(
                              "client_id",
                              "other-example",
                              "token",
                              other.path("access_token").asString())))
              .isEmpty());
      assertNotNull(h.identity(other));
      assertTrue(
          h.f
              .call(
                  () ->
                      h.oauth.revoke(
                          Map.of(
                              "client_id",
                              "desktop-example",
                              "token",
                              other.path("access_token").asString())))
              .isEmpty());
      assertThrows(AurigaException.class, () -> h.identity(other));
      assertEquals(
          "invalid_grant",
          h.refresh(other.path("refresh_token").asString(), null).path("error").asString());
      assertTrue(
          h.f
              .call(
                  () ->
                      h.oauth.revoke(
                          Map.of(
                              "client_id",
                              "desktop-example",
                              "token",
                              other.path("access_token").asString())))
              .isEmpty());
      assertFalse(h.f.session(h.sid).path("revoked").asBoolean());
    }
  }

  @Test
  void sourceRevocationAndBindingTamperingFailOnlineEvenWithoutNotifications() throws Exception {
    try (var h = new Harness()) {
      var response = h.exchange(h.issue());
      var ai = h.refresh(response.path("refresh_token").asString(), DeviceAiResource.RESOURCE);
      String did = h.claims(ai).path("did").asString();
      assertNotNull(
          h.f.call(
              () ->
                  h.oauth
                      .standardDevices()
                      .access(
                          ai.path("access_token").asString(),
                          "auriga.ai-gateway",
                          DeviceAiResource.INVOKE)));
      h.f.call(
          () ->
              store.tx(
                  () -> {
                    var device = store.get("device", did, true);
                    device.remove("uacSessionBinding");
                    store.save("device", device);
                    return null;
                  }));
      assertNull(
          h.f.call(
              () ->
                  h.oauth
                      .standardDevices()
                      .access(
                          ai.path("access_token").asString(),
                          "auriga.ai-gateway",
                          DeviceAiResource.INVOKE)));
      assertEquals(
          "invalid_grant",
          h.refresh(ai.path("refresh_token").asString(), null).path("error").asString());
      h.f.call(
          () ->
              store.tx(
                  () -> {
                    var device = store.get("device", did, true);
                    device.set("uacSessionBinding", h.f.session(h.sid).path("uacSessionBinding"));
                    store.save("device", device);
                    return null;
                  }));
      assertNotNull(
          h.f.call(
              () ->
                  h.oauth
                      .standardDevices()
                      .access(
                          ai.path("access_token").asString(),
                          "auriga.ai-gateway",
                          DeviceAiResource.INVOKE)));
      h.f.call(
          () -> {
            h.f.sessions.revokeOne(h.f.session(h.sid), "logout");
            return null;
          });
      assertNull(
          h.f.call(
              () ->
                  h.oauth
                      .standardDevices()
                      .access(
                          ai.path("access_token").asString(),
                          "auriga.ai-gateway",
                          DeviceAiResource.INVOKE)));
      assertEquals(
          "invalid_grant",
          h.refresh(ai.path("refresh_token").asString(), null).path("error").asString());
    }
  }

  @Test
  void sharedStorageConcurrentExchangeCreatesOneDevice() throws Exception {
    try (var h = new Harness()) {
      var challenge = h.issue();
      try (var executor = Executors.newFixedThreadPool(2)) {
        var a = executor.submit(() -> h.exchange(challenge));
        var b = executor.submit(() -> h.exchange(challenge));
        var results = List.of(a.get(15, TimeUnit.SECONDS), b.get(15, TimeUnit.SECONDS));
        assertEquals(1, results.stream().filter(r -> r.has("access_token")).count());
      }
      assertEquals(1, h.f.call(() -> store.tx(() -> store.all("device").size())));
    }
  }

  @Test
  void changedClientModeCannotRouteStandardLineageThroughTheLegacyGrantPath() throws Exception {
    try (var h = new Harness()) {
      var response = h.exchange(h.issue());
      var pending = h.issue();
      var legacyClient =
          new OAuthClient(
              "desktop-example",
              h.f.tenant,
              null,
              Set.of(),
              Set.of("auriga.connect"),
              Set.of(OAuthProvider.DEVICE, "refresh_token"),
              true,
              true);
      var legacy =
          new OAuthProvider(
              store, h.f.sessions, h.tokens, h.f.time, List.of(legacyClient), h.authorization);
      assertEquals(
          "invalid_grant",
          h.f
              .call(
                  () ->
                      legacy.token(
                          Map.of(
                              "grant_type",
                              "refresh_token",
                              "client_id",
                              "desktop-example",
                              "refresh_token",
                              response.path("refresh_token").asString())))
              .path("error")
              .asString());
      assertFalse(h.consumed(response.path("refresh_token").asString()));
      assertEquals(
          "invalid_grant",
          h.f
              .call(
                  () ->
                      legacy.token(
                          Map.of(
                              "grant_type",
                              OAuthProvider.DEVICE,
                              "client_id",
                              "desktop-example",
                              "device_code",
                              pending.path("device_code").asString())))
              .path("error")
              .asString());
    }
  }

  @Test
  void sourceIdleProviderAndMembershipRemainAuthoritativeAndConcurrentRefreshNeverRevokesSource()
      throws Exception {
    try (var h = new Harness()) {
      var response = h.exchange(h.issue());
      var ai = h.refresh(response.path("refresh_token").asString(), DeviceAiResource.RESOURCE);
      doReturn(
              GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(MemberStatus.MEMBER_STATUS_DISABLED)
                  .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                  .addAllPermissions(h.effective)
                  .build())
          .when(h.authorization)
          .getEffectivePermissions(any());
      assertNull(
          h.f.call(
              () ->
                  h.oauth
                      .standardDevices()
                      .access(
                          ai.path("access_token").asString(),
                          "auriga.ai-gateway",
                          DeviceAiResource.INVOKE)));
      doAnswer(
              i ->
                  GetEffectivePermissionsResponse.newBuilder()
                      .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                      .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                      .addAllPermissions(h.effective)
                      .build())
          .when(h.authorization)
          .getEffectivePermissions(any());
      h.f.call(
          () ->
              store.tx(
                  () -> {
                    var provider = store.get("idp_provider", h.f.provider, true);
                    provider.put("enabled", false);
                    store.save("idp_provider", provider);
                    return null;
                  }));
      assertNull(
          h.f.call(
              () ->
                  h.oauth
                      .standardDevices()
                      .access(
                          ai.path("access_token").asString(),
                          "auriga.ai-gateway",
                          DeviceAiResource.INVOKE)));
      h.f.call(
          () ->
              store.tx(
                  () -> {
                    var provider = store.get("idp_provider", h.f.provider, true);
                    provider.put("enabled", true);
                    store.save("idp_provider", provider);
                    return null;
                  }));
      String refresh = ai.path("refresh_token").asString();
      try (var executor = Executors.newFixedThreadPool(2)) {
        var a = executor.submit(() -> h.refresh(refresh, null));
        var b = executor.submit(() -> h.refresh(refresh, DeviceAiResource.RESOURCE));
        var results = List.of(a.get(15, TimeUnit.SECONDS), b.get(15, TimeUnit.SECONDS));
        assertEquals(1, results.stream().filter(r -> r.has("access_token")).count());
      }
      assertFalse(h.f.session(h.sid).path("revoked").asBoolean());
      var live = h.exchange(h.issue());
      h.f.call(
          () ->
              store.tx(
                  () -> {
                    var source = store.byId("session", h.sid, true);
                    source.put("idleExpiresAt", h.f.time.instant().plusSeconds(2).toString());
                    store.save("session", source);
                    return null;
                  }));
      h.f.time.advance(3);
      assertThrows(AurigaException.class, () -> h.identity(live));
      assertEquals(
          "invalid_grant",
          h.refresh(live.path("refresh_token").asString(), null).path("error").asString());
    }
  }

  @Test
  void grpcRequiresGatewayAndSignedCurrentPermissionAndHttpRejectsRepeatedFields()
      throws Exception {
    try (var h = new Harness()) {
      var response = h.exchange(h.issue());
      var ai = h.refresh(response.path("refresh_token").asString(), DeviceAiResource.RESOURCE);
      var service =
          new IamGrpcDevices(h.oauth.deviceAuthority()).standardDevices(h.oauth.standardDevices());
      var gateway =
          new AurigaPrincipal(
              "svc:ai-gateway",
              TenantContext.PLATFORM_TENANT,
              PrincipalType.INTERNAL_SERVICE,
              TokenContext.SERVICE,
              null,
              null,
              null,
              List.of(),
              Set.of(),
              null,
              null,
              UUID.randomUUID().toString());
      RequestContext.start(null, null)
          .withPrincipal(gateway)
          .run(
              () ->
                  h.f.call(
                      () -> {
                        for (String permission : StandardDevices.AI) {
                          var result = new Observer();
                          service.verifyStandardDeviceAiAccess(
                              VerifyStandardDeviceAiAccessRequest.newBuilder()
                                  .setAccessToken(ai.path("access_token").asString())
                                  .setPermission(permission)
                                  .build(),
                              result);
                          assertTrue(result.value.getActive());
                          assertEquals(h.f.tenant.toString(), result.value.getTenantId());
                          h.effective.remove(permission);
                          var denied = new Observer();
                          service.verifyStandardDeviceAiAccess(
                              VerifyStandardDeviceAiAccessRequest.newBuilder()
                                  .setAccessToken(ai.path("access_token").asString())
                                  .setPermission(permission)
                                  .build(),
                              denied);
                          assertEquals(
                              VerifyStandardDeviceAiAccessResponse.getDefaultInstance(),
                              denied.value);
                          h.effective.add(permission);
                        }
                        assertThrows(
                            AurigaException.class,
                            () ->
                                service.verifyStandardDeviceAiAccess(
                                    VerifyStandardDeviceAiAccessRequest.newBuilder()
                                        .setPermission("iam.accounts.admin")
                                        .build(),
                                    new Observer()));
                        return null;
                      }));
      assertThrows(
          AurigaException.class,
          () ->
              h.f.call(
                  () -> {
                    service.verifyStandardDeviceAiAccess(
                        VerifyStandardDeviceAiAccessRequest.getDefaultInstance(), new Observer());
                    return null;
                  }));
      var access = spy(HttpBoundaryTest.fixture("domain-proxy.json"));
      doReturn(h.f.tenant.toString()).when(access).tenantId();
      var requests =
          new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
      var mvc =
          MockMvcBuilders.standaloneSetup(
                  new OAuthController(
                      h.oauth, mock(SigningKeys.class), requests, store, h.f.sessions),
                  new StandardDeviceController(h.oauth, requests))
              .build();
      var entry = HttpBoundaryTest.request(access, "portal");
      var read =
          get("/api/iam/v1/me/device-session")
              .header("Authorization", "Bearer " + response.path("access_token").asString())
              .with(
                  request -> {
                    request.setRemoteAddr("127.0.0.1");
                    for (var name : Collections.list(entry.getHeaderNames()))
                      for (var value : Collections.list(entry.getHeaders(name)))
                        request.addHeader(name, value);
                    return request;
                  });
      mvc.perform(read)
          .andExpect(status().isOk())
          .andExpect(header().string("Cache-Control", "no-store"))
          .andExpect(jsonPath("$.authorizationMode").value("standard_device"))
          .andExpect(jsonPath("$.accountId").value(h.f.account.toString()))
          .andExpect(jsonPath("$.uacSessionBinding.sourceSessionId").value(h.sid));
      mvc.perform(
              post("/oauth2/token")
                  .contentType(MediaType.APPLICATION_FORM_URLENCODED)
                  .param("grant_type", "refresh_token")
                  .param("client_id", "desktop-example")
                  .param("refresh_token", ai.path("refresh_token").asString())
                  .param("resource", DeviceAiResource.RESOURCE, DeviceAiResource.RESOURCE))
          .andExpect(status().isBadRequest())
          .andExpect(jsonPath("$.error").value("invalid_target"));
      mvc.perform(
              post("/oauth2/token")
                  .contentType(MediaType.APPLICATION_FORM_URLENCODED)
                  .param("grant_type", "refresh_token")
                  .param("client_id", "desktop-example", "other-example")
                  .param("refresh_token", ai.path("refresh_token").asString()))
          .andExpect(status().isBadRequest())
          .andExpect(jsonPath("$.error").value("invalid_request"));
      assertFalse(h.consumed(ai.path("refresh_token").asString()));
    }
  }

  @Test
  void networkAdmissionRechecksOnlinePermissionsOutsideLocksAndRejectsRevokedConnections()
      throws Exception {
    try (var h = new Harness()) {
      var response = h.exchange(h.issue());
      var network =
          new IamNetworkAuthority(
              store,
              h.oauth.deviceAuthority(),
              mock(auriga.common.audit.AuditLogger.class),
              h.f.time,
              Set.of("gateway-example"));
      String raw = response.path("access_token").asString();
      var node = h.f.call(() -> network.register(raw));
      var ticket = h.f.call(() -> network.prepare(raw, node.node(), "gateway-example"));
      var gateway =
          new AurigaPrincipal(
              "svc:ai-gateway",
              TenantContext.PLATFORM_TENANT,
              PrincipalType.INTERNAL_SERVICE,
              TokenContext.SERVICE,
              null,
              null,
              null,
              List.of(),
              Set.of(),
              null,
              null,
              UUID.randomUUID().toString());
      RequestContext.start(null, null)
          .withPrincipal(gateway)
          .run(
              () ->
                  h.f.call(
                      () -> {
                        var connection =
                            network.redeem(
                                ticket.ticket(),
                                node.node(),
                                node.device(),
                                "gateway-example",
                                NetworkAuthorityTest.binding(ticket.ticket()));
                        assertNotNull(connection);
                        var reference =
                            new auriga.iam.api.NetworkAuthority.NodeReference(
                                node.realm(), node.node());
                        assertTrue(network.active(node.realm(), node.node()));
                        assertEquals(
                            Map.of(reference, true), network.activeBatch(List.of(reference)));
                        var check =
                            new auriga.iam.api.NetworkAuthority.Check(
                                node.realm(),
                                node.node(),
                                node.node(),
                                "0",
                                auriga.iam.api.NetworkAuthority.Sender.HUMAN,
                                auriga.iam.api.NetworkAuthority.Operation.CONNECT,
                                0,
                                1,
                                1);
                        assertTrue(network.check(check).allowed());
                        h.effective.remove("auriga.connect.collab.use");
                        assertFalse(network.active(node.realm(), node.node()));
                        assertEquals(
                            Map.of(reference, false), network.activeBatch(List.of(reference)));
                        assertFalse(network.check(check).allowed());
                        h.effective.add("auriga.connect.collab.use");
                        assertTrue(network.active(node.realm(), node.node()));
                        h.oauth.revoke(Map.of("client_id", "desktop-example", "token", raw));
                        assertFalse(network.active(node.realm(), node.node()));
                        assertEquals(
                            Map.of(reference, false), network.activeBatch(List.of(reference)));
                        assertFalse(network.check(check).allowed());
                        assertFalse(h.f.session(h.sid).path("revoked").asBoolean());
                        return null;
                      }));
    }
  }

  @Test
  void rejectsSignedBindingSubstitutionAndDoesNotIssueAnAlreadyExpiredRoundedToken()
      throws Exception {
    try (var h = new Harness()) {
      var issued = h.exchange(h.issue());
      var device = h.claims(issued).path("did").asString();
      var source = h.f.session(h.sid);
      ((ObjectNode) source.path("uacSessionBinding"))
          .put(
              "authGeneration",
              source.path("uacSessionBinding").path("authGeneration").asLong() + 1);
      var forged =
          h.f.call(
              () ->
                  h.tokens.withSigningKey(
                      () ->
                          h.tokens.standardDevice(
                              new DeviceAccessAuthority.Identity(
                                  h.f.tenant.toString(),
                                  h.f.account.toString(),
                                  device,
                                  h.f.time.instant().plusSeconds(300)),
                              source,
                              "auriga",
                              "auriga.connect",
                              Set.of("auriga.connect", "auriga.connect.collab.use"),
                              "member")));
      assertNull(h.f.call(() -> h.oauth.deviceAuthority().access(forged.accessToken())));
      h.f.call(
          () ->
              store.tx(
                  () -> {
                    var current = store.byId("session", h.sid, true);
                    current.put("expiresAt", h.f.time.instant().plusMillis(500).toString());
                    current.put("idleExpiresAt", h.f.time.instant().plusMillis(500).toString());
                    store.save("session", current);
                    return null;
                  }));
      String refresh = issued.path("refresh_token").asString();
      assertEquals("invalid_grant", h.refresh(refresh, null).path("error").asString());
      assertFalse(h.consumed(refresh));
    }
  }

  @Test
  void mixedPublicClientKeepsBrowserCodeRefreshSeparateFromItsStandardDevices() throws Exception {
    try (var h = new Harness()) {
      var browserTokens =
          new TokenService(
              h.signing,
              new ContractSchemas(),
              (t, a, c) -> Set.of("auriga.connect", "auriga.connect.collab.use"),
              h.f.time,
              "https://portal.example.com",
              Map.of());
      var mixed =
          new OAuthProvider(
              store,
              h.f.sessions,
              browserTokens,
              h.f.time,
              List.of(
                  new OAuthClient(
                      "mixed-example",
                      h.f.tenant,
                      null,
                      Set.of("https://client.example.com/callback"),
                      Set.of("auriga.connect", "openid", "offline_access"),
                      Set.of("authorization_code", OAuthProvider.DEVICE, "refresh_token"),
                      true,
                      false)),
              h.authorization);
      String verifier = Secrets.random();
      var request =
          Map.of(
              "client_id",
              "mixed-example",
              "response_type",
              "code",
              "redirect_uri",
              "https://client.example.com/callback",
              "code_challenge_method",
              "S256",
              "code_challenge",
              OAuthProvider.challenge(verifier),
              "scope",
              "openid offline_access");
      String redirect = h.f.call(() -> mixed.authorize(request, h.browser.cookie(), "portal"));
      String code =
          java.net.URLDecoder.decode(
              java.net.URI.create(redirect).getRawQuery().split("code=")[1].split("&")[0],
              java.nio.charset.StandardCharsets.UTF_8);
      var first =
          h.f.call(
              () ->
                  browserTokens.withSigningKey(
                      () ->
                          mixed.token(
                              Map.of(
                                  "client_id",
                                  "mixed-example",
                                  "grant_type",
                                  "authorization_code",
                                  "code",
                                  code,
                                  "code_verifier",
                                  verifier,
                                  "redirect_uri",
                                  "https://client.example.com/callback"))));
      assertTrue(first.has("refresh_token"), first.toString());
      var refresh =
          new HashMap<>(
              Map.of(
                  "client_id",
                  "mixed-example",
                  "grant_type",
                  "refresh_token",
                  "refresh_token",
                  first.path("refresh_token").asString()));
      var next = h.f.call(() -> browserTokens.withSigningKey(() -> mixed.token(refresh)));
      assertTrue(next.has("access_token"), next.toString());
      var claims = h.f.call(() -> browserTokens.verify(next.path("access_token").asString()));
      assertEquals("user", claims.path("ptyp").asString());
      assertEquals("portal", claims.path("ctx").asString());
      assertEquals(h.sid, claims.path("sid").asString());
      assertFalse(claims.has("did"));
      refresh.put("refresh_token", next.path("refresh_token").asString());
      assertFalse(h.f.call(() -> mixed.standardToken(refresh)));
      refresh.put("resource", DeviceAiResource.RESOURCE);
      assertEquals("invalid_target", h.f.call(() -> mixed.token(refresh)).path("error").asString());
      assertFalse(h.consumed(refresh.get("refresh_token")));
      assertTrue(
          h.f.call(
              () ->
                  mixed.standardToken(
                      Map.of("client_id", "mixed-example", "grant_type", OAuthProvider.DEVICE))));
    }
  }

  static final class Observer implements StreamObserver<VerifyStandardDeviceAiAccessResponse> {
    VerifyStandardDeviceAiAccessResponse value;

    public void onNext(VerifyStandardDeviceAiAccessResponse value) {
      this.value = value;
    }

    public void onError(Throwable error) {
      fail(error);
    }

    public void onCompleted() {}
  }
}
