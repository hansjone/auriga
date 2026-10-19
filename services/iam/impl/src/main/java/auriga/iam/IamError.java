package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.error.ErrorCode;
import io.grpc.Status;

final class IamError {
  static void validate(boolean condition) {
    if (!condition) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  static void requireAbsentCredential(boolean exists) {
    if (exists) throw new AurigaException(CommonErrors.CONFLICT);
  }

  static AurigaException error(String code, int status) {
    switch (code) {
      case "invalid_config", "invalid_password", "invalid_username" -> {
        return new AurigaException(CommonErrors.VALIDATION_FAILED);
      }
      case "username_taken" -> {
        return error("invalid_credentials", 401);
      }
      case "provider_unavailable", "not_found" -> {
        return new AurigaException(CommonErrors.NOT_FOUND);
      }
      case "rate_limited" -> {
        return new AurigaException(CommonErrors.RATE_LIMITED);
      }
      case "builtin_provider" -> {
        return new AurigaException(CommonErrors.FORBIDDEN);
      }
      default -> {}
    }
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
