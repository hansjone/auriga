package auriga.workspace.api;

import auriga.contracts.org.v1.InstanceQuota;
import auriga.contracts.org.v1.Member;
import auriga.org.web.model.WorkspacePolicy;
import java.util.UUID;

/**
 * Boundary for local or remote organization data; implementations must bind the supplied tenant.
 */
public interface WorkspaceInputs {
  Member member(UUID tenant, UUID owner);

  InstanceQuota quota(UUID tenant, UUID owner);

  WorkspacePolicy policy(UUID tenant);
}
