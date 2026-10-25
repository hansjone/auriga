package auriga.collab.app;

import auriga.common.error.ErrorCode;
import io.grpc.Status;

/**
 * network.* codes used by the mailbox core. Codes must already exist in
 * contracts/network/errors.json (read-only reference).
 */
public final class NetworkErrors {
  public static final ErrorCode STALE_EPOCH =
      ErrorCode.of("network.stale_epoch", 412, Status.Code.FAILED_PRECONDITION);
  public static final ErrorCode CONFLICT =
      ErrorCode.of("network.conflict", 409, Status.Code.ABORTED);
  public static final ErrorCode UNAUTHORIZED =
      ErrorCode.of("network.unauthorized", 403, Status.Code.PERMISSION_DENIED);
  public static final ErrorCode QUOTA_EXCEEDED =
      ErrorCode.of("network.quota_exceeded", 429, Status.Code.RESOURCE_EXHAUSTED);
  public static final ErrorCode CONTACT_EXPIRED =
      ErrorCode.of("network.contact_expired", 410, Status.Code.FAILED_PRECONDITION);
  public static final ErrorCode CONTACT_DENIED =
      ErrorCode.of("network.contact_denied", 403, Status.Code.PERMISSION_DENIED);
  public static final ErrorCode PROTOCOL_ERROR =
      ErrorCode.of("network.protocol_error", 400, Status.Code.INVALID_ARGUMENT);

  private NetworkErrors() {}
}
