package auriga.common.error;

import io.grpc.Status;
import java.util.Objects;
import java.util.regex.Pattern;

/**
 * A machine readable error code ({@code <domain>.<snake_case>}, see contracts/README.md) with its
 * HTTP status and gRPC status code. Localized texts live in message bundles keyed by {@link
 * #code()}.
 */
public record ErrorCode(String code, int httpStatus, Status.Code grpcStatus) {
  private static final Pattern FORMAT = Pattern.compile("^[a-z][a-z0-9_]*\\.[a-z][a-z0-9_]*$");

  public ErrorCode {
    Objects.requireNonNull(code, "code");
    Objects.requireNonNull(grpcStatus, "grpcStatus");
    if (!FORMAT.matcher(code).matches()) {
      throw new IllegalArgumentException(
          "Error code must look like <domain>.<snake_case>: " + code);
    }
    if (httpStatus < 400 || httpStatus > 599) {
      throw new IllegalArgumentException("Error codes must map to a 4xx/5xx status: " + httpStatus);
    }
  }

  public static ErrorCode of(String code, int httpStatus, Status.Code grpcStatus) {
    return new ErrorCode(code, httpStatus, grpcStatus);
  }

  /** The domain prefix, e.g. {@code iam} for {@code iam.login_tx_expired}. */
  public String domain() {
    return code.substring(0, code.indexOf('.'));
  }

  /** Default HTTP status for a gRPC status, used when a remote error code is not known locally. */
  public static int httpStatusOf(Status.Code grpc) {
    return switch (grpc) {
      case INVALID_ARGUMENT, OUT_OF_RANGE -> 400;
      case UNAUTHENTICATED -> 401;
      case PERMISSION_DENIED -> 403;
      case NOT_FOUND -> 404;
      case ALREADY_EXISTS, ABORTED -> 409;
      case FAILED_PRECONDITION -> 412;
      case RESOURCE_EXHAUSTED -> 429;
      case UNAVAILABLE -> 503;
      case DEADLINE_EXCEEDED -> 504;
      default -> 500;
    };
  }
}
