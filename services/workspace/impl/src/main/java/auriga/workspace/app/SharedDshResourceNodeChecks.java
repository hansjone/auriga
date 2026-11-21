package auriga.workspace.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.workspace.api.SharedDshResourceNodes;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.UUID;

/**
 * Workspace owns both tables and checks the real prepared-node assignment in its own transaction.
 */
public final class SharedDshResourceNodeChecks implements SharedDshResourceNodes {
  private final WorkspaceStore store;

  public SharedDshResourceNodeChecks(WorkspaceStore store) {
    this.store = store;
  }

  @Override
  public String requireCurrent(UUID tenant, UUID node, SharedDshBinding binding) {
    if (!binding.getTenantId().equals(tenant.toString())
        || !binding.getUnknownFields().asMap().isEmpty())
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return store.in(
        tenant,
        () -> {
          store.lock();
          var rows =
              store.jdbc.queryForList(
                  "SELECT x.*,p.authorization_digest FROM shared_dsh_placement x JOIN shared_dsh_pool p"
                      + " ON p.tenant_id=x.tenant_id AND p.id=x.pool_id AND p.mode=x.mode"
                      + " JOIN shared_dsh_pool_node pn ON pn.tenant_id=p.tenant_id AND pn.pool_id=p.id"
                      + " JOIN node n ON n.tenant_id=pn.tenant_id AND n.id=pn.node_id"
                      + " WHERE x.tenant_id=? AND x.id=? AND pn.node_id=? AND NOT n.revoked"
                      + " AND x.state IN ('pending','ready') AND p.state IN ('pending','ready')",
                  tenant,
                  UUID.fromString(binding.getPlacementId()),
                  node);
          if (rows.size() != 1 || !SharedDshPlacements.binding(rows.getFirst()).equals(binding))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          String digest = rows.getFirst().get("authorization_digest").toString();
          if (!digest.matches("sha256:[a-f0-9]{64}"))
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          return digest;
        });
  }
}
