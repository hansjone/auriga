package io.github.hansjone.auriga.org;

import io.github.hansjone.auriga.common.error.AurigaException;
import io.github.hansjone.auriga.common.error.ErrorCode;
import io.grpc.Status;

final class OrgError {
  static AurigaException error(String code, int status) {
    return new AurigaException(
        ErrorCode.of(
            "org." + code,
            status,
            status == 404
                ? Status.Code.NOT_FOUND
                : status == 401
                    ? Status.Code.UNAUTHENTICATED
                    : status == 403
                        ? Status.Code.PERMISSION_DENIED
                        : status == 409
                            ? Status.Code.FAILED_PRECONDITION
                            : status == 412
                                ? Status.Code.FAILED_PRECONDITION
                                : Status.Code.FAILED_PRECONDITION));
  }

  static void require(boolean condition, String code, int status) {
    if (!condition) throw error(code, status);
  }

  private OrgError() {}
}
