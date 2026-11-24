package auriga.workspace.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.SharedDshSessionBinding;
import auriga.contracts.workspace.v1.GetSharedDshPoolResponse;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.api.SharedDshSessions;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.UUID;

/** Tenant-scoped entry reads. Runtime session ownership always comes from durable W state. */
public final class SharedDshEntryQueries implements SharedDshSessions {
  private final WorkspaceStore store;
  private final SharedDshAuthority authority;

  public SharedDshEntryQueries(WorkspaceStore store, SharedDshAuthority authority) {
    this.store = store;
    this.authority = authority;
  }

  public GetSharedDshPoolResponse pool(UUID physicalInstance) {
    return store.tx(
        () -> {
          var result =
              GetSharedDshPoolResponse.newBuilder()
                  .setTenantId(store.tenant().toString())
                  .setPhysicalInstanceId(physicalInstance.toString());
          var rows =
              store.jdbc.queryForList(
                  "SELECT mode,state FROM shared_dsh_pool WHERE tenant_id=? AND id=?",
                  store.tenant(),
                  physicalInstance);
          if (!rows.isEmpty()) {
            var row = rows.getFirst();
            result
                .setRegistered(true)
                .setMode(row.get("mode").toString())
                .setState(row.get("state").toString());
          }
          return result.build();
        });
  }

  @Override
  public SharedDshSessionBinding lookup(UUID physicalInstance, UUID session) {
    return store.tx(
        () -> {
          var rows =
              store.jdbc.queryForList(
                  "SELECT binding FROM shared_dsh_session WHERE tenant_id=? AND id=?",
                  store.tenant(),
                  session);
          if (rows.isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
          SharedDshSessionBinding stored;
          try {
            stored = SharedDshSessionBinding.parseFrom((byte[]) rows.getFirst().get("binding"));
          } catch (com.google.protobuf.InvalidProtocolBufferException corrupt) {
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          }
          if (!stored.getUnknownFields().asMap().isEmpty()
              || !stored.getTenantId().equals(store.tenant().toString())
              || !stored.getPhysicalInstanceId().equals(physicalInstance.toString())
              || !stored.getSessionId().equals(session.toString()))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          SharedDshAuthority.Placement current;
          try {
            current =
                authority.resolve(
                    UUID.fromString(stored.getMemberId()),
                    UUID.fromString(stored.getPlacementId()),
                    UUID.fromString(stored.getPlatformSessionId()));
          } catch (AurigaException ex) {
            if (ex.errorCode().equals(CommonErrors.UNAUTHENTICATED)
                || ex.errorCode().equals(CommonErrors.FORBIDDEN)
                || ex.errorCode().code().equals("shared.revoked")) {
              throw new AurigaException(CommonErrors.NOT_FOUND);
            }
            throw ex;
          }
          var binding = current.binding();
          if (!current.state().equals("ready")
              || current.authGeneration() != stored.getAuthGeneration()
              || !binding.getTenantId().equals(stored.getTenantId())
              || !binding.getMemberId().equals(stored.getMemberId())
              || !binding.getPlacementId().equals(stored.getPlacementId())
              || !binding.getPhysicalInstanceId().equals(stored.getPhysicalInstanceId())
              || binding.getMode() != stored.getMode()
              || binding.getEpoch() != stored.getEpoch()
              || binding.getRevision() != stored.getRevision())
            throw new AurigaException(CommonErrors.NOT_FOUND);
          return stored;
        });
  }
}
