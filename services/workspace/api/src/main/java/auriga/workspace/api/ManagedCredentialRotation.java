package auriga.workspace.api;

import java.util.*;

/** Tenant-scoped lifecycle authority. No device credentials cross this boundary. */
public interface ManagedCredentialRotation {
  /** Missing IDs mean absent or deleted instances; source failures throw instead. */
  Map<UUID, Long> currentVersions(UUID tenant, Collection<UUID> instances);

  long currentVersion(UUID tenant, UUID instance);

  /** Retry of an already advanced version returns the same successor without another rotation. */
  long rotate(UUID tenant, UUID instance, long expectedVersion);
}
