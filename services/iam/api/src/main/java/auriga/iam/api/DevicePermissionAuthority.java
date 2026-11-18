package auriga.iam.api;

import java.util.Optional;
import java.util.UUID;

/** Online device lineage and current owner permission check, after JWT authentication. */
public interface DevicePermissionAuthority {
  boolean allowed(UUID tenant, String account, String device, String permission);

  /** Resolves the current managed installation target. Empty means a verified desktop device. */
  default Optional<UUID> installationInstance(UUID tenant, String account, String device) {
    throw new UnsupportedOperationException("Device installation authority is unavailable");
  }
}
