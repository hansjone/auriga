package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.iam.api.InstanceAiAuthorization;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import tools.jackson.databind.node.ObjectNode;

/**
 * Profile projection and source recheck tests. Access verification is isolated with a spy;
 * StandardDevicePostgresTest owns full signed-device/online-authority integration.
 */
class StandardDeviceProfileTest {
  final UUID tenant = UUID.randomUUID();
  final String account = UUID.randomUUID().toString(),
      deviceId = UUID.randomUUID().toString(),
      provider = UUID.randomUUID().toString(),
      sessionId = UUID.randomUUID().toString();
  final Clock clock = Clock.fixed(Instant.parse("2026-11-15T00:00:00Z"), ZoneOffset.UTC);
  final Instant expires = clock.instant().plusSeconds(300);
  final IamStore store = mock(IamStore.class);
  final SessionManager sessions = mock(SessionManager.class);
  StandardDevices devices;
  ObjectNode accountRow, device;

  @BeforeEach
  void setup() {
    var client =
        new OAuthClient(
            "desktop-example",
            tenant,
            null,
            Set.of(),
            Set.of("auriga.connect"),
            Set.of(OAuthProvider.DEVICE, "refresh_token"),
            true);
    devices =
        spy(
            new StandardDevices(
                store,
                sessions,
                mock(TokenService.class),
                clock,
                Map.of(client.id(), client),
                mock(InstanceAiAuthorization.class)));
    doReturn(new DeviceAccessAuthority.Identity(tenant.toString(), account, deviceId, expires))
        .when(devices)
        .access("platform-device", "auriga", null);
    when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    var session =
        Json.object(
            "sessionId",
            sessionId,
            "accountId",
            account,
            "providerId",
            provider,
            "audience",
            "portal",
            "expiresAt",
            expires.toString(),
            "idleExpiresAt",
            expires.toString());
    when(sessions.activeById(sessionId)).thenReturn(session);
    device =
        Json.object(
            "deviceId",
            deviceId,
            "accountId",
            account,
            "providerId",
            provider,
            "sessionId",
            sessionId,
            "authorizationMode",
            "standard_device",
            "clientId",
            client.id(),
            "scope",
            "auriga.connect",
            "expiresAt",
            expires.toString(),
            "revoked",
            false);
    when(store.get(eq("device"), eq(deviceId), anyBoolean())).thenReturn(device);
    when(store.get("idp_provider", provider, false)).thenReturn(Json.object("enabled", true));
    accountRow = Json.object("status", "active", "displayName", "Current name", "email", "private");
    when(store.get("account", account, false)).thenReturn(accountRow);
  }

  ObjectNode profile() {
    return TenantContext.call(tenant, () -> devices.profile("platform-device"));
  }

  @Test
  void profileReadsOnlyCurrentAccountFieldsWithoutBrowserSessionOrTouch() {
    var result = profile();
    assertEquals(
        Json.object(
            "account",
            Json.object(
                "id", account, "tenantId", tenant.toString(), "displayName", "Current name")),
        result);
    accountRow.put("displayName", "New name");
    assertEquals("New name", profile().path("account").path("displayName").asString());
    verify(sessions, never()).touchNativeSession(anyString());
    verify(devices, times(2)).access("platform-device", "auriga", null);
  }

  @Test
  void staleSourceAndDisabledOrMissingAccountFailClosed() {
    accountRow.put("status", "disabled");
    assertThrows(AurigaException.class, this::profile);
    accountRow.put("status", "active");
    device.put("revoked", true);
    assertThrows(AurigaException.class, this::profile);
    device.put("revoked", false);
    when(sessions.activeById(sessionId)).thenReturn(null);
    assertThrows(AurigaException.class, this::profile);
  }

  @Test
  void invalidAccessAndOversizedNameNeverReturnProfile() {
    accountRow.put("displayName", "x".repeat(257));
    assertThrows(AurigaException.class, this::profile);
    doReturn(null).when(devices).access("platform-device", "auriga", null);
    assertThrows(AurigaException.class, this::profile);
  }

  @Test
  void controllerUsesOnlyBearerRejectsSelectorsAndDuplicatesAndSetsNoStore() {
    var oauth = mock(OAuthProvider.class);
    when(oauth.standardDevices()).thenReturn(devices);
    var requests = mock(RequestIdentity.class);
    when(requests.call(any(), any()))
        .thenAnswer(i -> TenantContext.call(tenant, () -> ((Supplier<?>) i.getArgument(1)).get()));
    var controller = new StandardDeviceController(oauth, requests);
    var request = new MockHttpServletRequest();
    request.addHeader("Authorization", "Bearer platform-device");
    var response = new MockHttpServletResponse();
    assertEquals(
        account, controller.profile(request, response).path("account").path("id").asString());
    assertEquals("no-store", response.getHeader("Cache-Control"));
    assertEquals(0, response.getCookies().length);
    request.addParameter("deviceId", UUID.randomUUID().toString());
    assertThrows(AurigaException.class, () -> controller.profile(request, response));
    request.removeParameter("deviceId");
    request.addHeader("Authorization", "Bearer another-device");
    assertThrows(AurigaException.class, () -> controller.profile(request, response));
  }
}
