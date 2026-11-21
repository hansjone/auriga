package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.*;
import auriga.workspace.domain.WorkspaceCrypto;
import auriga.workspace.persistence.WorkspaceStore;
import java.math.BigInteger;
import java.time.Clock;
import java.util.*;

/** Called only inside the owning NodeSessionLease.guard; all state is tenant-transactional. */
final class SharedDshTaskAdmission {
  private static final long SAFE = 9007199254740991L;
  private final WorkspaceStore store;
  private final SharedDshPlacements placements;
  private final Clock clock;

  SharedDshTaskAdmission(WorkspaceStore store, SharedDshPlacements placements, Clock clock) {
    this.store = store;
    this.placements = placements;
    this.clock = clock;
  }

  private long now() {
    return clock.instant().getEpochSecond();
  }

  private UUID tenant() {
    return store.tenant();
  }

  private void owner(UUID node, UUID pool) {
    if (Boolean.TRUE.equals(store.node(node).get("revoked")))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    var owners =
        store.jdbc.queryForList(
            "SELECT node_id FROM shared_dsh_pool_node WHERE tenant_id=? AND pool_id=?",
            tenant(),
            pool);
    if (owners.size() != 1 || !node.equals(owners.getFirst().get("node_id")))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static long number(Map<String, Object> row, String key) {
    return ((Number) row.get(key)).longValue();
  }

  private static void binding(SharedDshSessionBinding binding, String task) {
    valid(
        binding.getUnknownFields().asMap().isEmpty()
            && uuid(binding.getTenantId())
            && uuid(binding.getMemberId())
            && uuid(binding.getPlacementId())
            && uuid(binding.getPhysicalInstanceId())
            && uuid(binding.getPlatformSessionId())
            && uuid(binding.getSessionId())
            && uuid(task)
            && binding.getEpoch() > 0
            && binding.getEpoch() <= SAFE
            && binding.getRevision() > 0
            && binding.getRevision() <= SAFE
            && binding.getAuthGeneration() > 0
            && binding.getAuthGeneration() <= SAFE);
    mode(binding.getMode());
  }

  private void request(UUID node, SharedDshSessionBinding binding, String task) {
    binding(binding, task);
    if (!binding.getTenantId().equals(tenant().toString()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    owner(node, UUID.fromString(binding.getPhysicalInstanceId()));
  }

  void memory(UUID node, UUID holder, long connectedAt, SharedDshPoolMemorySample sample) {
    long now = now();
    valid(
        sample.getUnknownFields().asMap().isEmpty()
            && sample.getSchemaVersion() == 1
            && uuid(sample.getTenantId())
            && uuid(sample.getPhysicalInstanceId())
            && sample.getObservedGeneration() > 0
            && sample.getObservedGeneration() <= SAFE
            && sample.getObservedAt() >= 0
            && sample.getObservedAt() <= SAFE
            && sample.getObservedAt() <= now
            && sample.getObservedAt() >= connectedAt
            && sample.getValidUntil() > now
            && sample.getValidUntil() <= SAFE
            && sample.getValidUntil() - sample.getObservedAt() <= 30
            && sample.getMemoryUsedBytes() >= 0
            && sample.getMemoryUsedBytes() <= SAFE
            && sample.getMemoryLimitBytes() > 0
            && sample.getMemoryLimitBytes() <= SAFE);
    if (!sample.getTenantId().equals(tenant().toString()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    UUID pool = UUID.fromString(sample.getPhysicalInstanceId());
    owner(node, pool);
    var prior =
        store.jdbc.queryForList(
            "SELECT generation,observed_at FROM shared_dsh_pool_memory WHERE tenant_id=? AND pool_id=?",
            tenant(),
            pool);
    if (!prior.isEmpty())
      conflict(
          sample.getObservedGeneration() >= number(prior.getFirst(), "generation")
              && sample.getObservedAt() >= number(prior.getFirst(), "observed_at"));
    store.jdbc.update(
        "INSERT INTO shared_dsh_pool_memory(tenant_id,pool_id,node_id,holder,generation,observed_at,valid_until,used_bytes,limit_bytes)"
            + " VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(tenant_id,pool_id) DO UPDATE SET"
            + " node_id=excluded.node_id,holder=excluded.holder,generation=excluded.generation,"
            + " observed_at=excluded.observed_at,valid_until=excluded.valid_until,"
            + " used_bytes=excluded.used_bytes,limit_bytes=excluded.limit_bytes",
        tenant(),
        pool,
        node,
        holder,
        sample.getObservedGeneration(),
        sample.getObservedAt(),
        sample.getValidUntil(),
        sample.getMemoryUsedBytes(),
        sample.getMemoryLimitBytes());
  }

  void admit(UUID node, UUID holder, SharedDshSessionBinding binding, String task) {
    request(node, binding, task);
    var currentNode = store.node(node);
    if (!Boolean.TRUE.equals(currentNode.get("schedulable"))
        || Boolean.TRUE.equals(currentNode.get("draining")))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (!new SharedDshEntryQueries(store, placements)
        .lookup(
            UUID.fromString(binding.getPhysicalInstanceId()),
            UUID.fromString(binding.getSessionId()))
        .equals(binding)) throw new AurigaException(CommonErrors.NOT_FOUND);
    String digest = digest(binding);
    conflict(ends(task, digest).isEmpty());
    var active =
        store.jdbc.queryForList(
            "SELECT task_id,binding,node_id FROM shared_dsh_active WHERE tenant_id=? AND member_id=?",
            tenant(),
            UUID.fromString(binding.getMemberId()));
    if (!active.isEmpty()) {
      var recorded = active.getFirst();
      conflict(
          UUID.fromString(task).equals(recorded.get("task_id"))
              && Arrays.equals(binding.toByteArray(), (byte[]) recorded.get("binding"))
              && node.equals(recorded.get("node_id")));
      return;
    }
    // All live tenant pools need a current owning connection and a fresh sample.
    var pools =
        store.jdbc.queryForList(
            "SELECT p.id,p.mode,m.*,s.holder AS current_holder,s.expires_at>CURRENT_TIMESTAMP AS lease_live"
                + " FROM shared_dsh_pool p LEFT JOIN shared_dsh_pool_node n ON n.tenant_id=p.tenant_id AND n.pool_id=p.id"
                + " LEFT JOIN shared_dsh_pool_memory m ON m.tenant_id=p.tenant_id AND m.pool_id=p.id AND m.node_id=n.node_id"
                + " LEFT JOIN node_session s ON s.tenant_id=n.tenant_id AND s.node_id=n.node_id"
                + " WHERE p.tenant_id=? AND p.state='ready' AND EXISTS(SELECT 1 FROM shared_dsh_placement x"
                + " WHERE x.tenant_id=p.tenant_id AND x.pool_id=p.id AND x.state='ready')",
            tenant());
    Map<String, Object> target = null;
    for (var pool : pools) {
      if (!Boolean.TRUE.equals(pool.get("lease_live"))
          || pool.get("holder") == null
          || !pool.get("holder").equals(pool.get("current_holder"))
          || number(pool, "observed_at") > now()
          || number(pool, "valid_until") <= now())
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      if (pool.get("id").toString().equals(binding.getPhysicalInstanceId())) target = pool;
    }
    if (target == null || !holder.equals(target.get("holder")))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    BigInteger used = BigInteger.valueOf(number(target, "used_bytes"));
    BigInteger limit = BigInteger.valueOf(number(target, "limit_bytes"));
    conflict(
        used.multiply(BigInteger.valueOf(100)).compareTo(limit.multiply(BigInteger.valueOf(80)))
            < 0);
    UUID pool = UUID.fromString(binding.getPhysicalInstanceId());
    long registered =
        store.jdbc.queryForObject(
            "SELECT count(*) FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=?",
            Long.class,
            tenant(),
            pool);
    long count =
        store.jdbc.queryForObject(
            "SELECT count(*) FROM shared_dsh_active WHERE tenant_id=? AND pool_id=?",
            Long.class,
            tenant(),
            pool);
    conflict(
        registered
            <= (binding.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_SHARED ? REGISTERED : 1));
    conflict(count < (binding.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_SHARED ? ACTIVE : 1));
    store.jdbc.update(
        "INSERT INTO shared_dsh_active(tenant_id,member_id,pool_id,task_id,binding,node_id) VALUES(?,?,?,?,?,?)",
        tenant(),
        UUID.fromString(binding.getMemberId()),
        pool,
        UUID.fromString(task),
        binding.toByteArray(),
        node);
  }

  void release(UUID node, SharedDshSessionBinding binding, String task) {
    request(node, binding, task);
    var active =
        store.jdbc.queryForList(
            "SELECT task_id,binding,node_id FROM shared_dsh_active WHERE tenant_id=? AND task_id=?",
            tenant(),
            UUID.fromString(task));
    if (!active.isEmpty()
        && (!node.equals(active.getFirst().get("node_id"))
            || !Arrays.equals(binding.toByteArray(), (byte[]) active.getFirst().get("binding"))))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    String digest = digest(binding);
    if (ends(task, digest).isEmpty())
      store.jdbc.update(
          "INSERT INTO shared_dsh_task_end(id,tenant_id,task_id,binding_digest,binding,node_id,ended_at) VALUES(?,?,?,?,?,?,?)",
          UuidV7.next(),
          tenant(),
          UUID.fromString(task),
          digest,
          binding.toByteArray(),
          node,
          java.sql.Timestamp.from(clock.instant()));
    store.jdbc.update(
        "DELETE FROM shared_dsh_active WHERE tenant_id=? AND task_id=? AND node_id=? AND binding=?",
        tenant(),
        UUID.fromString(task),
        node,
        binding.toByteArray());
  }

  private List<Map<String, Object>> ends(String task, String digest) {
    return store.jdbc.queryForList(
        "SELECT binding FROM shared_dsh_task_end WHERE tenant_id=? AND task_id=? AND binding_digest=?",
        tenant(),
        UUID.fromString(task),
        digest);
  }

  private static String digest(SharedDshSessionBinding binding) {
    return HexFormat.of().formatHex(WorkspaceCrypto.hash(binding.toByteArray()));
  }
}
