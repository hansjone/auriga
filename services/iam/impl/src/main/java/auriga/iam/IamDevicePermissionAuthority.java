package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.*;
import java.util.*;

/** Rechecks IAM lineage around the current Org snapshot without retaining IAM locks. */
public final class IamDevicePermissionAuthority implements DevicePermissionAuthority {
  private final DeviceAccessAuthority devices;
  private final InstanceAiAuthorization authorization;

  public IamDevicePermissionAuthority(
      DeviceAccessAuthority devices, InstanceAiAuthorization authorization) {
    this.devices = devices;
    this.authorization = authorization;
  }

  @Override
  public boolean allowed(UUID tenant, String account, String device, String permission) {
    if (!TenantContext.require().equals(tenant)) throw new AurigaException(CommonErrors.FORBIDDEN);
    if (devices.credential(tenant.toString(), account, device) == null) return false;
    GetEffectivePermissionsResponse effective;
    try {
      effective =
          authorization.getEffectivePermissions(
              GetEffectivePermissionsRequest.newBuilder()
                  .setTenantId(tenant.toString())
                  .setAccountId(account)
                  .build());
    } catch (AurigaException error) {
      if (Set.of("common.not_found", "common.forbidden").contains(error.errorCode().code()))
        return false;
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException error) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    if (effective == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (effective.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
        || !Set.of(MemberType.MEMBER_TYPE_MEMBER, MemberType.MEMBER_TYPE_EXTERNAL)
            .contains(effective.getMemberType())) return false;
    Set<String> permissions = Set.copyOf(effective.getPermissionsList());
    boolean mapped =
        permissions.stream()
            .filter(p -> TokenService.allowedPermission(p, "device"))
            .anyMatch(p -> M3PermissionCatalog.devicePermissions(p).contains(permission));
    return mapped
        && permissions.contains(permission)
        && devices.credential(tenant.toString(), account, device) != null;
  }
}
