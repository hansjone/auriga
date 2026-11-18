package auriga.workspace.api;

import auriga.contracts.node.v1.SharedDshSessionBinding;
import java.util.UUID;

/** Live durable session ownership within an already authenticated delegated TenantContext. */
public interface SharedDshSessions {
  SharedDshSessionBinding lookup(UUID physicalInstanceId, UUID sessionId);
}
