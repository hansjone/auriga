package auriga.collab.app;

import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.NetworkAuthority;
import java.util.Map;
import tools.jackson.databind.JsonNode;

/** Test-only local integrity resealing; this cannot create an authoritative IAM anchor. */
public final class LocalHistoryTestAccess {
  private LocalHistoryTestAccess() {}

  public static void seal(Map<String, Object> row) {
    row.put("local_history_digest", NetworkLocalSourceHistory.digest(row));
  }

  public static void validateSourceFrame(String definition, JsonNode frame) {
    NetworkSchemas.validate("source-control", definition, frame);
  }

  public static NetworkAuthority.Decision checkOutsideDatabase(
      NetworkAuthority authority,
      TenantTransactionManager manager,
      NetworkAuthority.Check request) {
    return new NetworkIamChecks(authority, manager).check(request);
  }
}
