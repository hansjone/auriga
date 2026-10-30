package auriga.collab.app;

import java.util.Map;

/** Test-only access to recompute local association hashes; it cannot manufacture IAM provenance. */
public final class LocalSourceHistoryFixtures {
  private LocalSourceHistoryFixtures() {}

  public static void seal(Map<String, Object> row) {
    row.put("local_history_digest", NetworkLocalSourceHistory.digest(row));
  }
}
