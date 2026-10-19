package auriga.iam;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.node.ObjectNode;

/**
 * Primary (PostgreSQL) is authoritative. Secondary (NATS) put is best-effort for edge cache; delete
 * retries then fails the revoke so edge cannot keep a live session after PG revocation.
 */
public final class CompositeSessionKv implements SessionKv {
  private static final Logger log = LoggerFactory.getLogger(CompositeSessionKv.class);
  private static final int DELETE_ATTEMPTS = 3;

  private final SessionKv primary;
  private final SessionKv secondary;

  public CompositeSessionKv(SessionKv primary, SessionKv secondary) {
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
    try {
      secondary.put(hash, value);
    } catch (RuntimeException e) {
      log.warn(
          "session KV secondary put failed (edge may miss cache until introspect): {}",
          e.toString());
    }
  }

  @Override
  public void delete(String hash) {
    primary.delete(hash);
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
