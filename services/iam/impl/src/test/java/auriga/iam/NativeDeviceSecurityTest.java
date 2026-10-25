package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.*;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import io.grpc.stub.StreamObserver;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.boot.context.properties.bind.*;
import org.springframework.mock.env.MockEnvironment;

class NativeDeviceSecurityTest {
  @Test
  void revokeVerifiesJwksBeforeBorrowingTenantConnection() {
    UUID tenant = UUID.randomUUID();
    var store = mock(IamStore.class);
    var tokens = mock(TokenService.class);
    var inside = new java.util.concurrent.atomic.AtomicBoolean();
    when(store.tx(any()))
        .thenAnswer(
            i -> {
              inside.set(true);
              try {
                return ((java.util.function.Supplier<?>) i.getArgument(0)).get();
              } finally {
                inside.set(false);
              }
            });
    when(tokens.verify("synthetic-example"))
        .thenAnswer(
            i -> {
              assertFalse(inside.get());
              return Json.object(
                  "aud",
                  "auriga",
                  "ptyp",
                  "device",
                  "ctx",
                  "device",
                  "scope",
                  "auriga.connect",
                  "tid",
                  tenant.toString(),
                  "did",
                  UUID.randomUUID(),
                  "sub",
                  UUID.randomUUID());
            });
    var oauth =
        new OAuthProvider(
            store,
            mock(SessionManager.class),
            tokens,
            java.time.Clock.systemUTC(),
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
    TenantContext.run(
        tenant,
        () ->
            assertEquals(
                Json.object(),
                oauth.revoke(Map.of("client_id", "native-example", "token", "synthetic-example"))));
    verify(tokens).verify("synthetic-example");
    verify(store).tx(any());
  }

  @Test
  void installationBindingDefaultsOffAndValidatesNativeClient() {
    var env =
        new MockEnvironment()
            .withProperty("auriga.iam.clients[0].id", "native-example")
            .withProperty("auriga.iam.clients[0].tenant", UUID.randomUUID().toString())
            .withProperty("auriga.iam.clients[0].redirect-uris", "")
            .withProperty("auriga.iam.clients[0].scopes", "auriga.connect")
            .withProperty("auriga.iam.clients[0].grants", OAuthProvider.DEVICE + ",refresh_token")
            .withProperty("auriga.iam.clients[0].platform", "true");
    assertFalse(
        Binder.get(env)
            .bind("auriga.iam.clients", Bindable.listOf(OAuthClient.class))
            .orElseThrow(AssertionError::new)
            .getFirst()
            .nativeConnect());
    env.withProperty("auriga.iam.clients[0].native-connect", "true");
    var client =
        Binder.get(env)
            .bind("auriga.iam.clients", Bindable.listOf(OAuthClient.class))
            .orElseThrow(AssertionError::new)
            .getFirst();
    assertTrue(client.nativeConnect());
    assertEquals("auriga", client.audience());
    env.withProperty("auriga.iam.clients[0].secret-hash", "not-a-public-client");
    assertThrows(
        BindException.class,
        () -> Binder.get(env).bind("auriga.iam.clients", Bindable.listOf(OAuthClient.class)));
  }

  @Test
  void nativeRegistrationRejectsOtherAudiencesScopesGrantsAndRedirects() {
    UUID tenant = UUID.randomUUID();
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new OAuthClient(
                "native-example",
                tenant,
                null,
                Set.of(),
                Set.of("auriga.connect"),
                Set.of(OAuthProvider.DEVICE, "refresh_token"),
                false,
                true));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new OAuthClient(
                "native-example",
                tenant,
                null,
                Set.of(),
                Set.of("auriga.connect", "openid"),
                Set.of(OAuthProvider.DEVICE, "refresh_token"),
                true,
                true));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new OAuthClient(
                "native-example",
                tenant,
                null,
                Set.of(),
                Set.of("auriga.connect"),
                Set.of(OAuthProvider.DEVICE, "refresh_token", "authorization_code"),
                true,
                true));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new OAuthClient(
                "native-example",
                tenant,
                null,
                Set.of("https://client.example.com/cb"),
                Set.of("auriga.connect"),
                Set.of(OAuthProvider.DEVICE, "refresh_token"),
                true,
                true));
  }

  @Test
  void controlPlaneFaultsAreUnavailableAndNeverInactive() {
    var authority = mock(DeviceAccessAuthority.class);
    when(authority.access(anyString()))
        .thenThrow(new IllegalStateException("control plane unavailable"));
    var service = new IamGrpcDevices(authority);
    var caller =
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
            "test");
    var observer = mock(StreamObserver.class);
    var failure =
        assertThrows(
            AurigaException.class,
            () ->
                RequestContext.start("native-fault", null)
                    .withPrincipal(caller)
                    .call(
                        () ->
                            TenantContext.call(
                                UUID.randomUUID(),
                                () -> {
                                  service.verifyDeviceAccess(
                                      VerifyDeviceAccessRequest.newBuilder()
                                          .setAccessToken("synthetic-example")
                                          .build(),
                                      observer);
                                  return null;
                                })));
    assertEquals("common.unavailable", failure.errorCode().code());
    verifyNoInteractions(observer);
    auriga.common.grpc.GrpcAuthorizationCheck.verify(List.of(service), Set.of());
  }
}
