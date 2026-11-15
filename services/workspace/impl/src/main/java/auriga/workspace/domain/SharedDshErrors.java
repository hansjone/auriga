package auriga.workspace.domain;

import auriga.common.error.*;
import io.grpc.Status;

public final class SharedDshErrors {
  public static final ErrorCode BINDING_MISMATCH =
      ErrorCode.of("shared.binding_mismatch", 403, Status.Code.PERMISSION_DENIED);
  public static final ErrorCode REVOKED =
      ErrorCode.of("shared.revoked", 403, Status.Code.PERMISSION_DENIED);
  public static final ErrorCode STOP_UNCONFIRMED =
      ErrorCode.of("shared.stop_unconfirmed", 409, Status.Code.FAILED_PRECONDITION);

  private SharedDshErrors() {}

  public static void matched(boolean valid) {
    if (!valid) throw new AurigaException(BINDING_MISMATCH);
  }

  public static void live(boolean valid) {
    if (!valid) throw new AurigaException(REVOKED);
  }
}
