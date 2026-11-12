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
}
