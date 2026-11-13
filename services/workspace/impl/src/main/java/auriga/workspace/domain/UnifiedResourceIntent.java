package auriga.workspace.domain;

import auriga.common.error.*;
import auriga.contracts.resource.v1.*;

/** Validation of generated K2 intent fields; expected values never come from reports. */
public final class UnifiedResourceIntent {
  private UnifiedResourceIntent() {}

  public static long revision(UnifiedResourceRef resource) {
    long value = resource.getInstallationRevision();
    if (value < 1 || value > 9007199254740991L)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    return value;
  }

  public static String desired(UnifiedResourceRef resource) {
    if (!resource.hasDesiredState()) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    String value =
        switch (resource.getDesiredState()) {
          case UNIFIED_RESOURCE_DESIRED_STATE_ENABLED -> "ENABLED";
          case UNIFIED_RESOURCE_DESIRED_STATE_DISABLED -> "DISABLED";
          case UNIFIED_RESOURCE_DESIRED_STATE_REMOVED -> "REMOVED";
          default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
        };
    if (resource.getEnabled() != value.equals("ENABLED"))
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    return value;
  }

  public static boolean reportMatches(UnifiedResourceRef resource, String state) {
    return switch (state) {
      case "applied" -> desired(resource).equals("ENABLED");
      case "disabled" -> desired(resource).equals("DISABLED");
      case "removed" -> desired(resource).equals("REMOVED");
      default -> true;
    };
  }
}
