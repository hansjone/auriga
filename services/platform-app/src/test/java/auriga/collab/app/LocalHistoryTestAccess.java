package auriga.collab.app;

import java.util.Map;

/** Test-only local integrity resealing; this cannot create an authoritative IAM anchor. */
public final class LocalHistoryTestAccess {
  private LocalHistoryTestAccess() {}

  public static void seal(Map<String, Object> row) {
    row.put("local_history_digest", NetworkLocalSourceHistory.digest(row));
  }
}
