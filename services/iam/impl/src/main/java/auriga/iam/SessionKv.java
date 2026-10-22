package auriga.iam;

import tools.jackson.databind.node.ObjectNode;

/**
 * The primary projection shares the session transaction; external projection runs only after
 * commit.
 */
public interface SessionKv {
  ObjectNode get(String cookieHash);

  void put(String cookieHash, ObjectNode value);

  void delete(String cookieHash);

  /**
   * Confirm external visibility after the response's database transaction has released resources.
   */
  default void awaitVisible(String cookieHash) {}

  /** Confirm every committed update or deletion within one bounded response wait. */
  default void awaitProjected(java.util.Collection<String> cookieHashes) {}
}
