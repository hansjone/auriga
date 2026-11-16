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
    assertTrue(projected.contains(READ));
    assertEquals(
        Set.of(READ),
        projected.stream()
            .filter(p -> p.startsWith("resource."))
            .collect(java.util.stream.Collectors.toSet()));
    assertFalse(projected.stream().anyMatch(p -> p.matches(".*\\.(write|admin|grant)(\\..*)?")));
    assertFalse(TokenService.allowedPermission(READ, "device"));
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
}
