package auriga.iam;

import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.node.ObjectNode;

/** Primary (PostgreSQL) is authoritative; secondary (NATS) is best-effort for edge cache. */
public final class CompositeSessionKv implements SessionKv {
  private static final Logger log = LoggerFactory.getLogger(CompositeSessionKv.class);

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
    try {
      secondary.delete(hash);
    } catch (RuntimeException e) {
      log.warn("session KV secondary delete failed: {}", e.toString());
    }
  }
}
