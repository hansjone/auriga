package auriga.iam.api;

import auriga.contracts.node.v1.SharedDshSessionBinding;
import java.util.UUID;

/** Live Workspace ownership; adapters revalidate the stored session and its ready placement. */
public interface SharedTaskOwnership {
  SharedDshSessionBinding lookup(UUID physicalInstanceId, UUID sessionId);
}
