package auriga.workspace.app;

import auriga.common.error.*;
import auriga.contracts.resource.v1.UnifiedResourceRef;

/** Temporary K2 boundary: never invent an installation revision while generated code is older. */
public final class UnifiedInstallationRevision {
  private UnifiedInstallationRevision() {}

  public static long read(UnifiedResourceRef resource) {
    var field = resource.getDescriptorForType().findFieldByName("installation_revision");
    if (field == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    long value = ((Number) resource.getField(field)).longValue();
    if (value < 1 || value > 9007199254740991L)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    return value;
  }

  public static String desired(UnifiedResourceRef resource) {
    var field = resource.getDescriptorForType().findFieldByName("desired_state");
    if (field == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    Object value = resource.getField(field);
    String text =
        value instanceof com.google.protobuf.Descriptors.EnumValueDescriptor e
            ? e.getName()
            : value.toString();
    for (String state : java.util.List.of("ENABLED", "DISABLED", "REMOVED"))
      if (text.equals(state) || text.endsWith("_" + state)) return state;
    throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }
}
