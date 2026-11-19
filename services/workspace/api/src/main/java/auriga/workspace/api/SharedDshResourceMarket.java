package auriga.workspace.api;

import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceReport;
import auriga.contracts.node.v1.SharedDshResourceView;
import java.util.Optional;
import java.util.UUID;

/** Read-only market authority. It never creates a placement or grants dedicated mode. */
public interface SharedDshResourceMarket {
  SharedDshBinding resolve(UUID member, UUID placement, UUID platformSession);

  Optional<SharedDshResourceReport> report(SharedDshResourceView expected);
}
