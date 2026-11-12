package auriga.iam.api;

import auriga.contracts.deploymentconfig.DeploymentAccess;
import java.util.*;

/**
 * Installation operator tenant comes from validated deployment access, never the internal zero
 * tenant.
 */
public record CredentialInstallationScope(UUID tenant) {
  public CredentialInstallationScope {
    Objects.requireNonNull(tenant);
  }

  public static CredentialInstallationScope from(DeploymentAccess access) {
    return new CredentialInstallationScope(UUID.fromString(access.tenantId()));
  }

  public boolean permits(UUID current) {
    return tenant.equals(current);
  }
}
