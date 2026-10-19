package io.github.hansjone.auriga.common.error;

import io.grpc.Status;

/** The generic error codes defined in contracts/README.md, plus codes raised by common itself. */
public final class CommonErrors {
  public static final ErrorCode UNAUTHENTICATED =
      ErrorCode.of("common.unauthenticated", 401, Status.Code.UNAUTHENTICATED);
  public static final ErrorCode FORBIDDEN =
      ErrorCode.of("common.forbidden", 403, Status.Code.PERMISSION_DENIED);
  public static final ErrorCode NOT_FOUND =
      ErrorCode.of("common.not_found", 404, Status.Code.NOT_FOUND);
  public static final ErrorCode VALIDATION_FAILED =
      ErrorCode.of("common.validation_failed", 400, Status.Code.INVALID_ARGUMENT);
  public static final ErrorCode CONFLICT =
      ErrorCode.of("common.conflict", 409, Status.Code.ABORTED);
  public static final ErrorCode PRECONDITION_FAILED =
      ErrorCode.of("common.precondition_failed", 412, Status.Code.FAILED_PRECONDITION);
  public static final ErrorCode RATE_LIMITED =
      ErrorCode.of("common.rate_limited", 429, Status.Code.RESOURCE_EXHAUSTED);
  public static final ErrorCode INTERNAL =
      ErrorCode.of("common.internal", 500, Status.Code.INTERNAL);
  public static final ErrorCode UNAVAILABLE =
      ErrorCode.of("common.unavailable", 503, Status.Code.UNAVAILABLE);

  /**
   * Raised by the shared step-up check. The code belongs to iam (iam.yaml documents 403
   * iam.step_up_required) but every service enforces it through common.
   */
  public static final ErrorCode STEP_UP_REQUIRED =
      ErrorCode.of("iam.step_up_required", 403, Status.Code.PERMISSION_DENIED);

  private CommonErrors() {}
}
