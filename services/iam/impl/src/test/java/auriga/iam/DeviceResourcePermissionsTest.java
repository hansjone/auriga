package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.iam.api.M3PermissionCatalog;
import java.util.*;
import org.junit.jupiter.api.Test;

class DeviceResourcePermissionsTest {
  static final String USE = "auriga.connect.resources.use";
  static final String READ = "resource.packages.read";

  @Test
  void catalogMapsOnlyDesktopReadAndInstallationPermission() {
    assertEquals(Set.of(READ), M3PermissionCatalog.devicePermissions(USE));
    assertTrue(M3PermissionCatalog.forRole("member").contains(USE));
    assertFalse(M3PermissionCatalog.forRole("external").contains(USE));
    assertEquals(
        Set.of("ai.models.invoke", "ai.usage.read"),
        M3PermissionCatalog.devicePermissions("auriga.connect.models.invoke"));
  }

  @Test
  void projectionRequiresBothCapabilityAndActualAccountPermission() {
    assertEquals(Set.of(USE, READ), TokenService.devicePermissions(Set.of(USE, READ)));
    assertEquals(Set.of(USE), TokenService.devicePermissions(Set.of(USE)));
    assertEquals(Set.of(), TokenService.devicePermissions(Set.of(READ)));
    assertEquals(Set.of(), TokenService.devicePermissions(Set.of()));
  }

  @Test
  void projectionRejectsResourceWritesAndAdministrativePermissions() {
    var effective = new HashSet<>(M3PermissionCatalog.forRole("owner"));
    effective.addAll(
        Set.of("auriga.connect.write", "auriga.connect.admin", "auriga.connect.grant"));
    var projected = TokenService.devicePermissions(effective);
    assertEquals(
        Set.of("auriga.connect.collab.use", "auriga.connect.models.invoke", USE, READ), projected);
    assertFalse(projected.stream().anyMatch(p -> p.matches(".*\\.(write|admin|grant)(\\..*)?")));
    assertFalse(TokenService.allowedPermission(READ, "device"));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(strings = {"member", "owner"})
  void allPlatformDeviceIssuersUseTheSameExactResourceProjection(String role) throws Exception {
    var effective = new HashSet<>(M3PermissionCatalog.forRole(role));
    effective.add("auriga.connect");
    var clock = java.time.Clock.systemUTC();
    var key =
        new com.nimbusds.jose.jwk.gen.ECKeyGenerator(com.nimbusds.jose.jwk.Curve.P_256)
            .keyID(UUID.randomUUID().toString())
            .generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    var tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, a, c) -> effective,
            clock,
            "https://issuer.example.com",
            Map.of());
    UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
    var expiry = clock.instant().plusSeconds(300);
    var identity =
        new DeviceAccessAuthority.Identity(
            tenant.toString(), account.toString(), device.toString(), expiry);
    var standard =
        tokens.standardDevice(
            identity,
            Json.object("authTime", clock.instant().toString()),
            "auriga",
            "auriga.connect",
            effective,
            "member");
    var nativeToken =
        tokens.access(
            tenant,
            account.toString(),
            "auriga",
            "device",
            "device",
            null,
            device,
            clock.instant(),
            null,
            "auriga.connect");
    var managed =
        tokens.managedDevice(tenant, account.toString(), device, expiry, "member", effective);
    var expected =
        Set.of(
            "auriga.connect",
            "auriga.connect.collab.use",
            "auriga.connect.models.invoke",
            USE,
            READ);
    for (var issued :
        Map.of(
                standard.accessToken(),
                expected,
                nativeToken,
                expected,
                managed.accessToken(),
                Set.of("auriga.connect.collab.use", USE, READ))
            .entrySet()) {
      var actual = new HashSet<String>();
      tokens.verify(issued.getKey()).path("perms").forEach(p -> actual.add(p.asString()));
      assertEquals(issued.getValue(), actual, role);
    }
  }

  @Test
  void catalogMappingsOnlyContainApprovedNonAdministrativeTargets() throws Exception {
    var approved =
        Map.of(
            USE,
            Set.of(READ),
            "auriga.connect.models.invoke",
            Set.of("ai.models.invoke", "ai.usage.read"));
    var actual = new HashMap<String, Set<String>>();
    try (var input =
        M3PermissionCatalog.class.getResourceAsStream("/auriga/contracts/catalog/m3.json")) {
      assertNotNull(input);
      for (var mapping : Json.MAPPER.readTree(input).path("deviceMappings").properties()) {
        var targets = new HashSet<String>();
        for (var target : mapping.getValue()) {
          String permission = target.asString();
          assertFalse(M3PermissionCatalog.adminOnly(permission), permission);
          assertFalse(
              permission.matches(
                  ".*\\.(write|admin|grant|upload|submit|review|security_review|publish|manage)(\\..*)?"),
              permission);
          targets.add(permission);
        }
        actual.put(mapping.getKey(), Set.copyOf(targets));
      }
    }
    assertEquals(approved, actual);
  }

  @Test
  void onlineAuthorityRequiresLiveDeviceMembershipCapabilityAndPermission() {
    UUID tenant = UUID.randomUUID();
    String account = UUID.randomUUID().toString(), device = UUID.randomUUID().toString();
    var devices = mock(DeviceAccessAuthority.class);
    var org = mock(InstanceAiAuthorization.class);
    var authority = new IamDevicePermissionAuthority(devices, org);
    var identity =
        new DeviceAccessAuthority.Identity(
            tenant.toString(), account, device, java.time.Instant.now().plusSeconds(300));
    when(devices.credential(tenant.toString(), account, device)).thenReturn(identity);
    TenantContext.run(
        tenant,
        () -> {
          var allowed =
              GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                  .setMemberType(MemberType.MEMBER_TYPE_EXTERNAL)
                  .addAllPermissions(Set.of(USE, READ));
          when(org.getEffectivePermissions(any())).thenReturn(allowed.build());
          assertTrue(authority.allowed(tenant, account, device, READ));
          assertFalse(authority.allowed(tenant, account, device, "resource.packages.upload"));
          for (var permissions : List.of(Set.of(USE), Set.of(READ), Set.<String>of())) {
            when(org.getEffectivePermissions(any()))
                .thenReturn(
                    allowed.clone().clearPermissions().addAllPermissions(permissions).build());
            assertFalse(authority.allowed(tenant, account, device, READ));
          }
          when(org.getEffectivePermissions(any()))
              .thenReturn(allowed.clone().setStatus(MemberStatus.MEMBER_STATUS_DISABLED).build());
          assertFalse(authority.allowed(tenant, account, device, READ));
          when(org.getEffectivePermissions(any())).thenReturn(allowed.build());
          when(devices.credential(tenant.toString(), account, device)).thenReturn(identity, null);
          assertFalse(authority.allowed(tenant, account, device, READ));
          when(devices.credential(tenant.toString(), account, device)).thenReturn(null);
          clearInvocations(org);
          assertFalse(authority.allowed(tenant, account, device, READ));
          verifyNoInteractions(org);
        });
  }

  @Test
  void installationTargetRequiresTheCurrentManagedLineageOrProvenDesktop() {
    var tenant = UUID.randomUUID();
    String account = UUID.randomUUID().toString(), device = UUID.randomUUID().toString();
    String instance = UUID.randomUUID().toString(), sessionId = UUID.randomUUID().toString();
    String expiry = java.time.Instant.now().plusSeconds(300).toString();
    var store = mock(IamStore.class);
    var sessions = mock(SessionManager.class);
    when(store.tx(any()))
        .thenAnswer(call -> call.<java.util.function.Supplier<?>>getArgument(0).get());
    var session =
        Json.object(
            "sessionId",
            sessionId,
            "accountId",
            account,
            "audience",
            "portal",
            "kind",
            "managed_device",
            "managedInstance",
            instance,
            "managedVersion",
            1,
            "expiresAt",
            expiry,
            "idleExpiresAt",
            expiry);
    var row =
        Json.object(
            "accountId",
            account,
            "sessionId",
            sessionId,
            "scope",
            "auriga.connect",
            "deviceId",
            device,
            "deviceType",
            "managed",
            "managedInstance",
            instance,
            "managedVersion",
            1);
    var lineage =
        Json.object("owner", account, "device", device, "version", 1, "expiresAt", expiry);
    when(store.get(eq("device"), eq(device), anyBoolean())).thenReturn(row);
    when(store.byId("session", sessionId, false)).thenReturn(session);
    when(sessions.activeById(sessionId)).thenReturn(session);
    when(store.get("managed_device", instance, false)).thenReturn(lineage);
    var authority = new DeviceAccessAuthority(store, sessions, null);
    TenantContext.run(
        tenant,
        () -> {
          assertEquals(
              Optional.of(UUID.fromString(instance)),
              authority.installationInstance(tenant.toString(), account, device));
          lineage.put("version", 2);
          assertEquals(
              CommonErrors.FORBIDDEN,
              assertThrows(
                      AurigaException.class,
                      () -> authority.installationInstance(tenant.toString(), account, device))
                  .errorCode());
          lineage.put("version", 1).put("revoked", true);
          assertEquals(
              CommonErrors.FORBIDDEN,
              assertThrows(
                      AurigaException.class,
                      () -> authority.installationInstance(tenant.toString(), account, device))
                  .errorCode());
          row.remove(List.of("deviceType", "managedInstance", "managedVersion"));
          assertEquals(
              CommonErrors.FORBIDDEN,
              assertThrows(
                      AurigaException.class,
                      () -> authority.installationInstance(tenant.toString(), account, device))
                  .errorCode());
          session.remove(List.of("kind", "managedInstance", "managedVersion"));
          assertTrue(authority.installationInstance(tenant.toString(), account, device).isEmpty());
          when(sessions.activeById(sessionId)).thenReturn(null);
          assertEquals(
              CommonErrors.FORBIDDEN,
              assertThrows(
                      AurigaException.class,
                      () -> authority.installationInstance(tenant.toString(), account, device))
                  .errorCode());
        });
  }
}
