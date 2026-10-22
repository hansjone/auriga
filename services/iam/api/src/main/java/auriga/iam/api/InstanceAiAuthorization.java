package auriga.iam.api;

import auriga.contracts.org.v1.GetEffectivePermissionsRequest;
import auriga.contracts.org.v1.GetEffectivePermissionsResponse;

/**
 * Authoritative org membership and permissions, never installation-configured permission strings.
 */
@FunctionalInterface
public interface InstanceAiAuthorization {
  GetEffectivePermissionsResponse getEffectivePermissions(GetEffectivePermissionsRequest request);
}
