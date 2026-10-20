package auriga.workspace.domain;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.error.ErrorCode;
import io.grpc.Status;

public final class WorkspaceErrors {
  public static final ErrorCode ADMISSION_FULL =
      ErrorCode.of("workspace.admission_full", 429, Status.Code.RESOURCE_EXHAUSTED);
  public static final ErrorCode NOT_ALLOWED =
      ErrorCode.of("workspace.not_allowed", 403, Status.Code.PERMISSION_DENIED);
  public static final ErrorCode NODE_NOT_EMPTY =
      ErrorCode.of("workspace.node_not_empty", 409, Status.Code.FAILED_PRECONDITION);
  public static final ErrorCode PROTOCOL_UNSUPPORTED =
      ErrorCode.of("plugin.protocol_unsupported", 426, Status.Code.FAILED_PRECONDITION);

  private WorkspaceErrors() {}

  public static void valid(boolean condition) {
    if (!condition) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }
}
