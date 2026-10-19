package auriga.common.context;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.util.List;
import java.util.Map;

/** Tenant scoped work was attempted without a tenant; this is a programming error (HTTP 500). */
public class MissingTenantException extends AurigaException {
  private final String reason;

  public MissingTenantException(String reason) {
    super(CommonErrors.INTERNAL, Map.of(), List.of(), null);
    this.reason = reason;
  }

  @Override
  public String getMessage() {
    return reason;
  }
}
