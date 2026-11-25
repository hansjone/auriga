package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.iam.api.PermissionResolver;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import tools.jackson.databind.node.ObjectNode;

/** Pure signing/projection regressions; UacFlowPostgresTest covers authoritative persistence. */
class NativeDeviceBindingTest {
  final UUID tenant = UUID.randomUUID();
  final String account = UUID.randomUUID().toString(),
      sid = UUID.randomUUID().toString(),
      did = UUID.randomUUID().toString(),
      provider = UUID.randomUUID().toString();
  final Clock clock = Clock.fixed(Instant.parse("2026-11-25T00:00:00Z"), ZoneOffset.UTC);
  final IamStore store = mock(IamStore.class);
  final SessionManager sessions = mock(SessionManager.class);
  TokenService tokens;
  OAuthProvider oauth;
  ObjectNode session, device, grant, binding, accountRow;

  @BeforeEach
  void setup() throws Exception {
    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    var permissions = mock(PermissionResolver.class);
    when(permissions.resolve(tenant, account, "device"))
        .thenReturn(Set.of("auriga.connect.collab.use"));
    tokens =
        new TokenService(
            keys, new ContractSchemas(), permissions, clock, "https://iam.example.com", Map.of());
    oauth =
        new OAuthProvider(
            store,
            sessions,
            tokens,
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
    when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    binding = Json.object("sourceSessionId", sid, "providerId", provider, "authGeneration", 7);
    session =
        Json.object(
            "sessionId",
            sid,
            "accountId",
            account,
            "providerId",
            provider,
            "audience",
            "portal",
            "authTime",
            clock.instant().minusSeconds(60).toString(),
            "expiresAt",
            clock.instant().plusSeconds(3600).toString(),
            "idleExpiresAt",
            clock.instant().plusSeconds(900).toString(),
            "uacCredentialId",
            UUID.randomUUID().toString(),
            "uacSessionBinding",
            binding);
    device =
        Json.object(
            "deviceId",
            did,
            "accountId",
            account,
            "sessionId",
            sid,
            "providerId",
            provider,
            "clientId",
            "native-example",
            "scope",
            "auriga.connect",
            "authorizationMode",
            "native_tx",
            "uacSessionBinding",
            binding.deepCopy());
    grant =
        Json.object(
            "kind",
            "refresh",
            "clientId",
            "native-example",
            "sessionId",
            sid,
            "deviceId",
            did,
            "scope",
            "auriga.connect",
            "expiresAt",
            session.path("expiresAt"),
            "authorizationMode",
            "native_tx",
            "uacSessionBinding",
            binding.deepCopy());
    when(store.get(eq("oauth_grant"), eq(Secrets.hash("refresh-example")), anyBoolean()))
        .thenReturn(grant);
    when(store.get(eq("device"), eq(did), anyBoolean())).thenReturn(device);
    when(sessions.activeById(sid)).thenReturn(session);
    when(sessions.touchNativeSession(sid)).thenReturn(session);
    accountRow =
        Json.object(
            "status",
            "active",
            "displayName",
            "Alice Example",
            "avatarUrl",
            "https://profile.example.com/avatar.png");
    when(store.get("account", account, false)).thenReturn(accountRow);
    when(store.get("idp_provider", provider, false)).thenReturn(Json.object("enabled", true));
  }

  ObjectNode refresh() {
    return TenantContext.call(
        tenant,
        () ->
            oauth.token(
                Map.of(
                    "client_id",
                    "native-example",
                    "grant_type",
                    "refresh_token",
                    "refresh_token",
                    "refresh-example")));
  }

  @Test
  void nativeRefreshSignsExactSourceBindingAndPreservesRefreshLineage() {
    var result = refresh();
    assertFalse(result.has("error"), result.toString());
    var claims = tokens.verify(result.path("access_token").asString());
    assertEquals(binding, claims.path("uacSessionBinding"));
    var identity =
        TenantContext.call(
            tenant, () -> oauth.nativeIdentity(result.path("access_token").asString(), false));
    assertEquals(binding, identity.path("uacSessionBinding"));
    for (String field : List.of("sourceSessionId", "providerId", "authGeneration"))
      assertEquals(
          session.path("uacSessionBinding").path(field),
          claims.path("uacSessionBinding").path(field));
    verify(store)
        .insert(
            eq("oauth_grant"),
            anyString(),
            argThat(
                row ->
                    binding.equals(row.path("uacSessionBinding"))
                        && sid.equals(row.path("sessionId").asString())
                        && did.equals(row.path("deviceId").asString())),
            any());
  }

  @Test
  void nonUacRefreshAndIdentityOmitBinding() {
    session.remove("uacCredentialId");
    session.remove("uacSessionBinding");
    device.remove("uacSessionBinding");
    grant.remove("uacSessionBinding");
    var raw = refresh().path("access_token").asString();
    assertFalse(tokens.verify(raw).has("uacSessionBinding"));
    assertFalse(
        TenantContext.call(tenant, () -> oauth.nativeIdentity(raw, false))
            .has("uacSessionBinding"));
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "sourceSessionId",
        "providerId",
        "authGeneration",
        "missing",
        "revoked",
        "expired"
      })
  void invalidSourceNeverSignsOrRotates(String failure) {
    if (failure.equals("missing")) grant.remove("uacSessionBinding");
    else if (failure.equals("revoked")) device.put("revoked", true);
    else if (failure.equals("expired")) when(sessions.activeById(sid)).thenReturn(null);
    else if (failure.equals("authGeneration"))
      grant.withObject("/uacSessionBinding").put(failure, 8);
    else grant.withObject("/uacSessionBinding").put(failure, UUID.randomUUID().toString());
    assertEquals("invalid_grant", refresh().path("error").asString());
    verify(store, never()).insert(eq("oauth_grant"), anyString(), any(), any());
    verify(sessions, never()).touchNativeSession(anyString());
  }

  @Test
  void nativeProfileReturnsOnlyAuthoritativeSafeAccountFields() {
    var raw = refresh().path("access_token").asString();
    var profile = TenantContext.call(tenant, () -> oauth.nativeIdentity(raw, true));
    assertEquals(
        Json.object(
            "account",
            Json.object(
                "id",
                account,
                "tenantId",
                tenant.toString(),
                "displayName",
                "Alice Example",
                "avatarUrl",
                "https://profile.example.com/avatar.png")),
        profile);
    accountRow.put("avatarUrl", "javascript:alert(1)");
    assertFalse(
        TenantContext.call(tenant, () -> oauth.nativeIdentity(raw, true))
            .path("account")
            .has("avatarUrl"));
  }

  @Test
  void nativeIdentityRejectsJwtFromDifferentSourceGeneration() {
    var raw = refresh().path("access_token").asString();
    session.withObject("/uacSessionBinding").put("authGeneration", 8);
    device.set("uacSessionBinding", session.path("uacSessionBinding").deepCopy());
    var error =
        assertThrows(
            AurigaException.class,
            () -> TenantContext.call(tenant, () -> oauth.nativeIdentity(raw, false)));
    assertEquals("iam.invalid_token", error.errorCode().code());
  }
}
