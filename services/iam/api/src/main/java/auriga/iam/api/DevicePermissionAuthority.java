package auriga.iam.api;

import java.util.UUID;

/** Online device lineage and current owner permission check, after JWT authentication. */
public interface DevicePermissionAuthority {
  boolean allowed(UUID tenant, String account, String device, String permission);
}
