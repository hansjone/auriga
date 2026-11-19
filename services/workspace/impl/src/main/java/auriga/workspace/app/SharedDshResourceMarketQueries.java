package auriga.workspace.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceReport;
import auriga.contracts.node.v1.SharedDshResourceView;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.api.SharedDshResourceMarket;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import java.util.*;

/** W owns placement, prepared-node and resource receipts; Resource never reads these tables. */
public final class SharedDshResourceMarketQueries implements SharedDshResourceMarket {
  /** Observe an already-ready normal entry; initial placement admission is a separate port. */
  public interface ReadyEntryProbe {
    boolean observed(SharedDshBinding binding);
  }

  private final WorkspaceStore store;
  private final SharedDshAuthority authority;
  private final ReadyEntryProbe entry;
  private final Clock clock;

  public SharedDshResourceMarketQueries(
      WorkspaceStore store, SharedDshAuthority authority, ReadyEntryProbe entry, Clock clock) {
    this.store = store;
    this.authority = authority;
    this.entry = entry;
    this.clock = clock;
  }

  @Override
  public SharedDshBinding resolve(UUID member, UUID placement, UUID platformSession) {
    var current = authority.resolve(member, placement, platformSession);
    if (current == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var binding = current.binding();
    if (!current.state().equals("ready")
        || !binding.getTenantId().equals(store.tenant().toString())
        || !binding.getMemberId().equals(member.toString())
        || !binding.getPlacementId().equals(placement.toString()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return binding;
  }

  @Override
  public Optional<SharedDshResourceReport> report(SharedDshResourceView expected) {
    var before = stored(expected);
    if (before.isEmpty() || !entry.observed(expected.getBinding())) return Optional.empty();
    // The HTTP profile probe runs outside W's transaction, then the exact durable receipt is
    // fenced.
    var after = stored(expected);
    return after.filter(value -> value.equals(before.orElseThrow()));
  }

  private Optional<SharedDshResourceReport> stored(SharedDshResourceView expected) {
    var binding = expected.getBinding();
    if (expected.getSchemaVersion() != 1
        || !expected.getSource().equals("platform")
        || !binding.getTenantId().equals(store.tenant().toString())
        || !binding.getUnknownFields().asMap().isEmpty())
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return store.tx(
        () -> {
          store.lock();
          var rows =
              store.jdbc.queryForList(
                  "SELECT x.*,v.payload,v.report FROM shared_dsh_placement x"
                      + " JOIN shared_dsh_pool p ON p.tenant_id=x.tenant_id AND p.id=x.pool_id AND p.mode=x.mode"
                      + " JOIN shared_dsh_pool_node pn ON pn.tenant_id=p.tenant_id AND pn.pool_id=p.id"
                      + " JOIN node n ON n.tenant_id=pn.tenant_id AND n.id=pn.node_id AND NOT n.revoked"
                      + " JOIN shared_dsh_resource_view v ON v.tenant_id=x.tenant_id AND v.placement_id=x.id"
                      + " WHERE x.tenant_id=? AND x.id=? AND x.state='ready' AND p.state='ready' AND v.view_id=?",
                  store.tenant(),
                  UUID.fromString(binding.getPlacementId()),
                  UUID.fromString(expected.getViewId()));
          if (rows.size() != 1) return Optional.empty();
          var row = rows.getFirst();
          if (!SharedDshPlacements.binding(row).equals(binding)
              || !Arrays.equals((byte[]) row.get("payload"), expected.toByteArray())
              || row.get("report") == null) return Optional.empty();
          try {
            var report = SharedDshResourceReport.parseFrom((byte[]) row.get("report"));
            long now = clock.instant().getEpochSecond();
            if (report.getSchemaVersion() != 1
                || !report.getUnknownFields().asMap().isEmpty()
                || !report.getBinding().equals(binding)
                || !report.getViewId().equals(expected.getViewId())
                || !report.getStatus().equals("ready")
                || !report.getReason().equals("verified")
                || report.getObservedAt() < 0
                || report.getObservedAt() > now
                || expected.getResourcesList().stream()
                    .anyMatch(value -> value.getExpiresAt() <= now)) return Optional.empty();
            return Optional.of(report);
          } catch (com.google.protobuf.InvalidProtocolBufferException error) {
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          }
        });
  }
}
