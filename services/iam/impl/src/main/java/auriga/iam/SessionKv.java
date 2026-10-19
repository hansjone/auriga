package auriga.iam;

import tools.jackson.databind.node.ObjectNode;

/**
 * PostgreSQL adapter now; a future NATS KV adapter must share the surrounding outbox transaction.
 */
public interface SessionKv {
  ObjectNode get(String cookieHash);

  void put(String cookieHash, ObjectNode value);

  void delete(String cookieHash);
}
