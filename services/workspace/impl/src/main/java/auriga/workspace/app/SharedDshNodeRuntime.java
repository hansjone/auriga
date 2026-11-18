package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshPolicy.intent;

import auriga.contracts.node.v1.*;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.*;

/** Initial preparation over the existing mTLS, tenant-transaction and node-session fence. */
public final class SharedDshNodeRuntime {
  public static final String CAPABILITY = "auriga.shared-dsh-runtime";
  public static final String PREPARATION = "prepare-v1";

  public record Delivery(UUID id, SharedDshRuntimeIntent intent) {}

  public record Batch(List<Delivery> deliveries, UUID next) {}

  private final WorkspaceStore store;
  private final SharedDshPlacements placements;

  public SharedDshNodeRuntime(WorkspaceStore store, SharedDshPlacements placements) {
    this.store = store;
    this.placements = placements;
  }

  public static boolean supports(Hello hello) {
    return hello.getOs().equals("linux")
        && hello.getDriversList().contains(RuntimeKind.RUNTIME_KIND_DOCKER)
        && PREPARATION.equals(hello.getLabelsOrDefault(CAPABILITY, ""));
  }

  /** Called only inside NodeSessionLease.guard, after Hello and Inventory. */
  public Batch pending(UUID node, UUID after) {
    var authority = store.node(node);
    if ((boolean) authority.get("revoked")
        || !(boolean) authority.get("schedulable")
        || (boolean) authority.get("draining")) return new Batch(List.of(), null);
    var result = new ArrayList<Delivery>();
    var page = placements.pendingRuntime(after);
    for (var delivery : page) {
      SharedDshRuntimeIntent command;
      try {
        command = SharedDshRuntimeIntent.parseFrom(delivery.payload());
      } catch (com.google.protobuf.InvalidProtocolBufferException e) {
        throw new IllegalStateException("Invalid shared runtime outbox payload", e);
      }
      if (!current(command)) continue;
      UUID pool = UUID.fromString(command.getBinding().getPhysicalInstanceId());
      var owner = owners(pool);
      if (owner.isEmpty()) {
        // The tenant lock held by the node-session guard serializes competing first claims.
        store.jdbc.update(
            "INSERT INTO shared_dsh_pool_node(tenant_id,pool_id,node_id) VALUES(?,?,?)",
            store.tenant(),
            pool,
            node);
      } else if (!node.equals(owner.getFirst().get("node_id"))) continue;
      result.add(new Delivery(delivery.id(), command));
    }
    return new Batch(List.copyOf(result), page.size() < 100 ? null : page.getLast().id());
  }

  /** Receipt may advance only the exact immutable command sent on this fenced session. */
  public boolean receipt(UUID node, Delivery sent, SharedDshRuntimeReceipt receipt) {
    var command = sent.intent();
    if (!receipt.getOperationId().equals(command.getOperationId())
        || !receipt.getBinding().equals(command.getBinding())
        || !receipt.getAction().equals(command.getAction())
        || !current(command)) return false;
    var owner = owners(UUID.fromString(command.getBinding().getPhysicalInstanceId()));
    if (owner.isEmpty() || !node.equals(owner.getFirst().get("node_id"))) return false;
    if (!placements.confirmInitial(receipt)) return false;
    // Same tenant transaction as confirmInitial. Sending a frame never acknowledges the outbox.
    placements.delivered(sent.id());
    return true;
  }

  private List<Map<String, Object>> owners(UUID pool) {
    return store.jdbc.queryForList(
        "SELECT node_id FROM shared_dsh_pool_node WHERE tenant_id=? AND pool_id=?",
        store.tenant(),
        pool);
  }

  private boolean current(SharedDshRuntimeIntent command) {
    var binding = command.getBinding();
    if (!command.getOperationId().equals(binding.getPlacementId())
        || !binding.getTenantId().equals(store.tenant().toString())
        || binding.getMode() != DshInstanceMode.DSH_INSTANCE_MODE_SHARED
        || !auriga.workspace.domain.SharedDshPolicy.uuid(command.getOperationId())) return false;
    var rows =
        store.jdbc.queryForList(
            "SELECT x.* FROM shared_dsh_placement x JOIN shared_dsh_pool p ON"
                + " p.tenant_id=x.tenant_id AND p.id=x.pool_id WHERE x.tenant_id=? AND x.id=?"
                + " AND x.state='pending' AND p.state IN ('pending','ready')",
            store.tenant(),
            UUID.fromString(command.getOperationId()));
    return !rows.isEmpty()
        && command.equals(
            intent(
                command.getOperationId(),
                SharedDshPlacements.binding(rows.getFirst()),
                "prepare-empty"));
  }
}
