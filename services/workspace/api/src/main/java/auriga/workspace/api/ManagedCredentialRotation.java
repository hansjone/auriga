package auriga.workspace.api;

import java.util.UUID;

/** Tenant-scoped lifecycle authority. No device credentials cross this boundary. */
public interface ManagedCredentialRotation {
  long currentVersion(UUID tenant, UUID instance);

  /** Retry of an already advanced version returns the same successor without another rotation. */
  long rotate(UUID tenant, UUID instance, long expectedVersion);
}
