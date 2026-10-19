package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import io.grpc.Status;

final class IamError {
  static AurigaException error(String code, int status) {
    return new AurigaException(
        ErrorCode.of(
            "iam." + code,
            status,
            status == 404
                ? Status.Code.NOT_FOUND
                : status == 401
                    ? Status.Code.UNAUTHENTICATED
                    : status == 403
                        ? Status.Code.PERMISSION_DENIED
                        : Status.Code.FAILED_PRECONDITION));
  }

  static void require(boolean condition, String code, int status) {
    if (!condition) throw error(code, status);
  }

  private IamError() {}
}
