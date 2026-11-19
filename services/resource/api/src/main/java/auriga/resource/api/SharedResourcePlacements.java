package auriga.resource.api;

import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceReport;
import auriga.contracts.node.v1.SharedDshResourceView;
import java.util.Optional;
import java.util.UUID;

/** Trusted W adapter. Browser identity comes from its live portal session, never a request body. */
public interface SharedResourcePlacements {
  SharedDshBinding resolve(UUID tenant, UUID member, UUID placement, UUID platformSession);

  Optional<SharedDshResourceReport> report(SharedDshResourceView expected);
}
