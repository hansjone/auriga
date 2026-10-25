package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import com.nimbusds.jose.crypto.ECDSAVerifier;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.SignedJWT;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.http.MediaType;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;

class DeviceAiResourceTest {
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
  final Clock clock = Clock.fixed(Instant.parse("2026-10-25T00:00:00Z"), ZoneOffset.UTC);

  @Test
  void signsOnlyModelPermissionWithOneAiAudienceAndOnlineExpiry() throws Exception {
    var key = new ECKeyGenerator(Curve.P_256).keyID("test-key").generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    var tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> {
              fail("AI issuance must use online authorization, never configured permissions");
              return Set.of();
            },
            clock,
            "https://portal.example.com",
            Map.of());
    var devices = mock(DeviceAccessAuthority.class);
    var authorization = mock(InstanceAiAuthorization.class);
    when(authorization.getEffectivePermissions(any()))
        .thenReturn(
            GetEffectivePermissionsResponse.newBuilder()
                .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                .setMemberType(MemberType.MEMBER_TYPE_EXTERNAL)
                .addAllPermissions(
                    List.of(
                        "auriga.connect.models.invoke",
                        "auriga.connect.collab.use",
                        "iam.accounts.admin",
                        "ai.models.invoke",
                        "auriga.connect"))
                .build());
    var resource = new DeviceAiResource(devices, tokens, authorization);
    var session =
        Json.object("accountId", account, "authTime", clock.instant().minusSeconds(600).toString());
    var grant = Json.object("deviceId", device);
    var client =
        new OAuthClient(
            "native-example",
            tenant,
            null,
            Set.of(),
            Set.of("auriga.connect"),
            Set.of(OAuthProvider.DEVICE, "refresh_token"),
            true,
            true);
    for (int seconds : List.of(41, 900)) {
      when(devices.credential(tenant.toString(), account.toString(), device.toString()))
          .thenReturn(
              new DeviceAccessAuthority.Identity(
                  tenant.toString(),
                  account.toString(),
                  device.toString(),
                  clock.instant().plusSeconds(seconds)));
      var issued = resource.issue(client, grant, session);
      assertNotNull(issued);
      SignedJWT jwt = SignedJWT.parse(issued.accessToken());
      assertTrue(jwt.verify(new ECDSAVerifier(key.toPublicJWK())));
      assertEquals("at+jwt", jwt.getHeader().getType().toString());
      var claims = tokens.verify(issued.accessToken());
      assertEquals("auriga.ai-gateway", claims.path("aud").asString());
      assertFalse(DeviceAccessAuthority.platformAudience(claims));
      assertEquals("device", claims.path("ptyp").asString());
      assertEquals("device", claims.path("ctx").asString());
      assertEquals(device.toString(), claims.path("did").asString());
      assertEquals(tenant.toString(), claims.path("tid").asString());
      assertEquals(account.toString(), claims.path("sub").asString());
      assertEquals("external", claims.path("mtyp").asString());
      assertEquals("auriga.connect", claims.path("scope").asString());
      assertEquals(
          Json.MAPPER.valueToTree(List.of("auriga.connect.models.invoke")), claims.path("perms"));
      assertEquals(
          clock.instant().getEpochSecond() + Math.min(300, seconds), claims.path("exp").asLong());
      assertEquals(issued.expiresAt().getEpochSecond(), claims.path("exp").asLong());
      for (String field : List.of("roles", "sid", "instanceId", "credentialVersion"))
        assertFalse(claims.has(field));
    }
    verify(authorization, times(2))
        .getEffectivePermissions(
            GetEffectivePermissionsRequest.newBuilder()
                .setTenantId(tenant.toString())
                .setAccountId(account.toString())
                .build());
  }

  @Test
  void onlineDenialAndControlPlaneFailureCannotReachSigner() {
    var devices = mock(DeviceAccessAuthority.class);
    var tokens = mock(TokenService.class);
    var authorization = mock(InstanceAiAuthorization.class);
    var resource = new DeviceAiResource(devices, tokens, authorization);
    var client =
        new OAuthClient(
            "native-example",
            tenant,
            null,
            Set.of(),
            Set.of("auriga.connect"),
            Set.of(OAuthProvider.DEVICE, "refresh_token"),
            true,
            true);
    var grant = Json.object("deviceId", device);
    var session = Json.object("accountId", account, "authTime", clock.instant().toString());
    assertNull(resource.issue(client, grant, session));
    verifyNoInteractions(authorization, tokens);
    when(devices.credential(anyString(), anyString(), anyString()))
        .thenReturn(
            new DeviceAccessAuthority.Identity(
                tenant.toString(),
                account.toString(),
                device.toString(),
                clock.instant().plusSeconds(60)));
    for (MemberStatus status :
        List.of(
            MemberStatus.MEMBER_STATUS_ACTIVE,
            MemberStatus.MEMBER_STATUS_DISABLED,
            MemberStatus.MEMBER_STATUS_OFFBOARDED,
            MemberStatus.MEMBER_STATUS_UNSPECIFIED)) {
      when(authorization.getEffectivePermissions(any()))
          .thenReturn(
              GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(status)
                  .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                  .build());
      assertNull(resource.issue(client, grant, session));
    }
    when(authorization.getEffectivePermissions(any()))
        .thenReturn(
            GetEffectivePermissionsResponse.newBuilder()
                .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                .addPermissions(DeviceAiResource.INVOKE)
                .build());
    assertNull(resource.issue(client, grant, session));
    when(authorization.getEffectivePermissions(any())).thenReturn(null);
    assertEquals(
        "common.unavailable",
        assertThrows(AurigaException.class, () -> resource.issue(client, grant, session))
            .errorCode()
            .code());
    when(authorization.getEffectivePermissions(any()))
        .thenThrow(new IllegalStateException("offline"));
    assertEquals(
        "common.unavailable",
        assertThrows(AurigaException.class, () -> resource.issue(client, grant, session))
            .errorCode()
            .code());
    verifyNoInteractions(tokens);
  }

  @Test
  void nonNativeClientsAndOtherGrantsCannotSelectTheResource() {
    var store = mock(IamStore.class);
    when(store.tx(any()))
        .thenAnswer(i -> ((java.util.function.Supplier<?>) i.getArgument(0)).get());
    var oauth =
        new OAuthProvider(
            store,
            mock(SessionManager.class),
            mock(TokenService.class),
            clock,
            List.of(
                new OAuthClient(
                    "browser",
                    tenant,
                    null,
                    Set.of(),
                    Set.of(),
                    Set.of(
                        "refresh_token",
                        "authorization_code",
                        "client_credentials",
                        OAuthProvider.DEVICE),
                    true)));
    for (String type :
        List.of(
            "refresh_token", "authorization_code", "client_credentials", OAuthProvider.DEVICE)) {
      assertEquals(
          "invalid_target",
          TenantContext.call(
                  tenant,
                  () ->
                      oauth.token(
                          Map.of(
                              "client_id",
                              "browser",
                              "grant_type",
                              type,
                              "resource",
                              DeviceAiResource.RESOURCE)))
              .path("error")
              .asString());
    }
    verify(store, never()).get(anyString(), anyString(), anyBoolean());
  }

  @Test
  void nativeInvalidTargetsAreRejectedBeforeGrantLookup() {
    var store = mock(IamStore.class);
    when(store.tx(any()))
        .thenAnswer(i -> ((java.util.function.Supplier<?>) i.getArgument(0)).get());
    var oauth =
        new OAuthProvider(
            store,
            mock(SessionManager.class),
            mock(TokenService.class),
            clock,
            List.of(
                new OAuthClient(
                    "native-example",
                    tenant,
                    null,
                    Set.of(),
                    Set.of("auriga.connect"),
                    Set.of(OAuthProvider.DEVICE, "refresh_token"),
                    true,
                    true)));
    for (String target :
        List.of("", "auriga.ai-gateway", "urn:other", "urn:auriga:ai-gateway urn:other")) {
      assertEquals(
          "invalid_target",
          TenantContext.call(
                  tenant,
                  () ->
                      oauth.token(
                          Map.of(
                              "client_id",
                              "native-example",
                              "grant_type",
                              "refresh_token",
                              "resource",
                              target)))
              .path("error")
              .asString());
    }
    assertEquals(
        "invalid_target",
        TenantContext.call(
                tenant,
                () ->
                    oauth.token(
                        Map.of(
                            "client_id",
                            "native-example",
                            "grant_type",
                            "refresh_token",
                            "resource",
                            DeviceAiResource.RESOURCE,
                            "audience",
                            "auriga.ai-gateway")))
            .path("error")
            .asString());
    verify(store, never()).get(anyString(), anyString(), anyBoolean());
  }

  @Test
  void expiredOnlineIdentityCannotIssueEvenWithModelPermission() {
    var keys = mock(SigningKeys.class);
    var tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            "https://portal.example.com",
            Map.of());
    for (int seconds : List.of(0, -1)) {
      assertNull(
          tokens.deviceAi(
              new DeviceAccessAuthority.Identity(
                  tenant.toString(),
                  account.toString(),
                  device.toString(),
                  clock.instant().plusSeconds(seconds)),
              clock.instant(),
              "member",
              Set.of(DeviceAiResource.INVOKE)));
    }
    verifyNoInteractions(keys);
  }

  @Test
  void aiAndMultipleAudiencesCannotBecomePlatformDeviceCredentials() {
    var store = mock(IamStore.class);
    var tokens = mock(TokenService.class);
    var authority = new DeviceAccessAuthority(store, mock(SessionManager.class), tokens);
    for (Object aud :
        List.of(
            "auriga.ai-gateway",
            List.of("auriga", "auriga.ai-gateway"),
            List.of("auriga.ai-gateway"))) {
      when(tokens.verify("synthetic-example"))
          .thenReturn(
              Json.object(
                  "tid",
                  tenant,
                  "sub",
                  account,
                  "did",
                  device,
                  "aud",
                  aud,
                  "ptyp",
                  "device",
                  "ctx",
                  "device",
                  "scope",
                  "auriga.connect",
                  "perms",
                  List.of("auriga.connect.collab.use", DeviceAiResource.INVOKE)));
      assertNull(TenantContext.call(tenant, () -> authority.access("synthetic-example")));
    }
    verifyNoInteractions(store);
  }

  @Test
  void singleResourceFormIsForwardedUnchangedWithNoStoreResponse() throws Exception {
    var access = HttpBoundaryTest.fixture("domain-proxy.json");
    var identity =
        new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    var oauth = mock(OAuthProvider.class);
    var sessions = mock(SessionManager.class);
    when(sessions.response(any()))
        .thenAnswer(i -> ((java.util.function.Supplier<?>) i.getArgument(0)).get());
    when(oauth.token(anyMap()))
        .thenReturn(
            Json.object(
                "access_token",
                "synthetic-ai",
                "token_type",
                "Bearer",
                "expires_in",
                60,
                "scope",
                "auriga.connect",
                "refresh_token",
                "synthetic-next"));
    var mvc =
        MockMvcBuilders.standaloneSetup(
                new OAuthController(
                    oauth, mock(SigningKeys.class), identity, mock(IamStore.class), sessions))
            .build();
    var expected =
        Map.of(
            "grant_type",
            "refresh_token",
            "client_id",
            "native-example",
            "refresh_token",
            "synthetic-current",
            "resource",
            DeviceAiResource.RESOURCE);
    var request =
        post("/oauth2/token")
            .contentType(MediaType.APPLICATION_FORM_URLENCODED)
            .header("Host", java.net.URI.create(access.portalUrl()).getRawAuthority())
            .header("X-Auriga-Entry-Origin", access.portalUrl())
            .header("X-Auriga-Entry-Role", "portal")
            .header("X-Auriga-Client-IP", "192.0.2.11");
    expected.forEach(request::param);
    var response = mvc.perform(request).andReturn().getResponse();
    assertEquals(200, response.getStatus());
    assertEquals("no-store", response.getHeader("Cache-Control"));
    assertEquals("no-cache", response.getHeader("Pragma"));
    assertEquals(60, Json.read(response.getContentAsString()).path("expires_in").asInt());
    verify(oauth).token(expected);
  }

  @Test
  void duplicateResourceFormValuesAreRejectedBeforeCallingTheProvider() throws Exception {
    var access = HttpBoundaryTest.fixture("domain-proxy.json");
    var identity =
        new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    var oauth = mock(OAuthProvider.class);
    var mvc =
        MockMvcBuilders.standaloneSetup(
                new OAuthController(
                    oauth,
                    mock(SigningKeys.class),
                    identity,
                    mock(IamStore.class),
                    mock(SessionManager.class)))
            .build();
    for (String second : List.of("urn:auriga:ai-gateway", "urn:other", "")) {
      var result =
          mvc.perform(
                  post("/oauth2/token")
                      .contentType(MediaType.APPLICATION_FORM_URLENCODED)
                      .param("grant_type", "refresh_token")
                      .param("client_id", "native-example")
                      .param("refresh_token", "synthetic-example")
                      .param("resource", "urn:auriga:ai-gateway", second))
              .andReturn()
              .getResponse();
      assertEquals(400, result.getStatus());
      assertEquals(
          "invalid_target", Json.read(result.getContentAsString()).path("error").asString());
      assertEquals("no-store", result.getHeader("Cache-Control"));
    }
    verifyNoInteractions(oauth);
  }
}
