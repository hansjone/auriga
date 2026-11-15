package auriga.iam.api;

import java.util.UUID;

/** Server-side message fence and verified disconnection before public node control delivery. */
@FunctionalInterface
public interface MessageNetworkIsolation {
  void beforeControl(UUID authenticatedTenant, UUID authenticatedNode);

  /** Adopted public node control plus C's actual message-reader readiness. */
  default auriga.contracts.msgnet.model.IsolationRecord ready(
      UUID tenant, UUID node, auriga.contracts.msgnet.model.IsolationRecord request) {
    throw new UnsupportedOperationException();
  }
}
