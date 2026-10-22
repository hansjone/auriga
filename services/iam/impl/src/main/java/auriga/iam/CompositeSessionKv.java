package auriga.iam;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.node.ObjectNode;

/**
 * PostgreSQL is authoritative. NATS is projected only after commit. Each projection takes a
 * cross-process advisory lock and re-reads the latest committed value, so delayed callbacks cannot
 * restore a pre-revocation snapshot. IP management authorization never relies on NATS.
 */
public final class CompositeSessionKv implements SessionKv {
  private static final Logger log = LoggerFactory.getLogger(CompositeSessionKv.class);
  private static final int DELETE_ATTEMPTS = 3;

  private final SessionKv primary;
  private final SessionKv secondary;
  private final IamStore store;

  public CompositeSessionKv(IamStore store, SessionKv primary, SessionKv secondary) {
    this.store = store;
    this.primary = primary;
    this.secondary = secondary;
  }

  @Override
  public ObjectNode get(String hash) {
    return primary.get(hash);
  }

  @Override
  public void put(String hash, ObjectNode value) {
    primary.put(hash, value);
    project(hash);
  }

  @Override
  public void delete(String hash) {
    primary.delete(hash);
    project(hash);
  }

  private void project(String hash) {
    var tenant = auriga.common.context.TenantContext.require();
    store.afterCommit(
        () -> {
          try {
            store.isolated(
                tenant,
                () -> {
                  store.lock("session-projection:" + hash);
                  ObjectNode latest = primary.get(hash);
                  if (latest == null) deleteSecondary(hash);
                  else secondary.put(hash, latest);
                  return null;
                });
          } catch (RuntimeException e) {
            // The session transaction has committed; never misreport it as rolled back.
            log.warn("session KV secondary projection failed: {}", e.getClass().getSimpleName());
          }
        });
  }

  private void deleteSecondary(String hash) {
    RuntimeException last = null;
    for (int i = 1; i <= DELETE_ATTEMPTS; i++) {
      try {
        secondary.delete(hash);
        return;
      } catch (RuntimeException e) {
        last = e;
        log.warn(
            "session KV secondary delete attempt {}/{} failed: {}",
            i,
            DELETE_ATTEMPTS,
            e.toString());
        if (i < DELETE_ATTEMPTS) {
          try {
            Thread.sleep(50L * i);
          } catch (InterruptedException ie) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("session KV secondary delete interrupted", ie);
          }
        }
      }
    }
    throw new IllegalStateException(
        "session KV secondary delete failed after " + DELETE_ATTEMPTS + " attempts", last);
  }
}
