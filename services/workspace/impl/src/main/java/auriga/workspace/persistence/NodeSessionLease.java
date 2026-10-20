package auriga.workspace.persistence;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import java.util.UUID;
import java.util.function.Supplier;

/**
 * Short transactions, with DB time and a unique holder fencing stale sessions. No held connection.
 */
public final class NodeSessionLease {
  private final WorkspaceStore store;
  private final UUID tenant, node, holder;

  public NodeSessionLease(WorkspaceStore store, UUID tenant, UUID node, UUID holder) {
    this.store = store;
    this.tenant = tenant;
    this.node = node;
    this.holder = holder;
  }

  public void acquire() {
    store.in(
        tenant,
        () -> {
          store.lock();
          var rows =
              store.jdbc.queryForList(
                  "INSERT INTO node_session(id,tenant_id,node_id,holder,expires_at) VALUES(?,?,?,?,CURRENT_TIMESTAMP+INTERVAL '60 seconds') ON CONFLICT(tenant_id,node_id) DO UPDATE SET holder=excluded.holder,expires_at=excluded.expires_at WHERE node_session.expires_at<=CURRENT_TIMESTAMP RETURNING holder",
                  UuidV7.next(),
                  tenant,
                  node,
                  holder);
          if (rows.isEmpty()) throw new AurigaException(CommonErrors.CONFLICT);
          if ((boolean) store.node(node).get("revoked"))
            throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          return null;
        });
  }

  public <T> T guard(Supplier<T> work) {
    return store.in(
        tenant,
        () -> {
          store.lock();
          // UPDATE locks the lease until all guarded tenant writes commit, fencing takeover.
          int updated =
              store.jdbc.update(
                  "UPDATE node_session SET expires_at=CURRENT_TIMESTAMP+INTERVAL '60 seconds' WHERE tenant_id=? AND node_id=? AND holder=? AND expires_at>CURRENT_TIMESTAMP",
                  tenant,
                  node,
                  holder);
          if (updated != 1) throw new AurigaException(CommonErrors.CONFLICT);
          return work.get();
        });
  }

  public void release() {
    store.in(
        tenant,
        () ->
            store.jdbc.update(
                "DELETE FROM node_session WHERE tenant_id=? AND node_id=? AND holder=?",
                tenant,
                node,
                holder));
  }
}
