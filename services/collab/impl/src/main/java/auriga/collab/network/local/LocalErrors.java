package auriga.collab.network.local;

import auriga.common.error.ErrorCode;
import io.grpc.Status;

/** Own-node errors already defined in contracts/network/errors.json. */
final class LocalErrors {
  static final ErrorCode REGISTRATION_REQUIRED =
      ErrorCode.of("network.local_registration_required", 412, Status.Code.FAILED_PRECONDITION);
  static final ErrorCode ALLOCATION_CONFLICT =
      ErrorCode.of("network.local_allocation_conflict", 409, Status.Code.FAILED_PRECONDITION);
  static final ErrorCode RECOVERY_CONFLICT =
      ErrorCode.of("network.recovery_conflict", 409, Status.Code.FAILED_PRECONDITION);
  static final ErrorCode IDENTITY_ROLLBACK =
      ErrorCode.of("network.identity_rollback", 409, Status.Code.FAILED_PRECONDITION);

  private LocalErrors() {}
}
