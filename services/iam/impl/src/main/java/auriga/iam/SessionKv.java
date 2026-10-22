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
}
