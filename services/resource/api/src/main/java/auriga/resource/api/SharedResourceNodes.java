package auriga.resource.api;

import auriga.contracts.node.v1.SharedDshBinding;
import java.util.UUID;

/**
 * Authenticated node identity is transport-owned; bindings come only from persisted resource views.
 */
public interface SharedResourceNodes {
  String requireCurrent(UUID tenant, UUID authenticatedNode, SharedDshBinding binding);
}
