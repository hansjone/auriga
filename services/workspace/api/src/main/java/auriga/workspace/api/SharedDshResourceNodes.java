package auriga.workspace.api;

import auriga.contracts.node.v1.SharedDshBinding;
import java.util.UUID;

/**
 * Current physical node and placement authority, including initial pending resource installation.
 */
public interface SharedDshResourceNodes {
  /** Returns the pool's authoritative authorization digest; every mismatch fails closed. */
  String requireCurrent(UUID tenant, UUID authenticatedNode, SharedDshBinding binding);
}
