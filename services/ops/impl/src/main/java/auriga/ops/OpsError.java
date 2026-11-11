package auriga.ops;

import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import io.grpc.Status;

final class OpsError {
  static AurigaException error(String code, int status) {
    return new AurigaException(
        ErrorCode.of("ops." + code, status, Status.Code.FAILED_PRECONDITION));
  }

  static void require(boolean ok) {
    if (!ok) throw error("invalid", 400);
  }

  private OpsError() {}
}
