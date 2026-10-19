package io.github.hansjone.auriga.common.grpc;

import io.github.hansjone.auriga.common.error.AurigaException;
import io.github.hansjone.auriga.common.error.CommonErrors;
import io.github.hansjone.auriga.common.error.ErrorCode;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.function.Supplier;
import tools.jackson.databind.json.JsonMapper;

/**
 * Error mapping for gRPC: {@link AurigaException} ⇄ status code + {@code auriga-error-code} (+
 * params) metadata. Remote adapters wrap stub calls in {@link #call} so callers see the same
 * exception in local and remote deployments.
 */
public final class GrpcErrors {
  private static final JsonMapper JSON = JsonMapper.builder().build();

  private GrpcErrors() {}

  public static StatusRuntimeException toStatus(Throwable error) {
    if (error instanceof StatusRuntimeException sre) return sre;
    if (!(error instanceof AurigaException e)) {
      // Never leak internal messages to callers.
      return toStatus(new AurigaException(CommonErrors.INTERNAL));
    }
    Metadata trailers = new Metadata();
    trailers.put(GrpcMetadata.ERROR_CODE, e.errorCode().code());
    if (!e.params().isEmpty()) {
      try {
        trailers.put(GrpcMetadata.ERROR_PARAMS, JSON.writeValueAsBytes(e.params()));
      } catch (RuntimeException ignored) {
        // params are best effort
      }
    }
    return Status.fromCode(e.errorCode().grpcStatus())
        .withDescription(e.errorCode().code())
        .asRuntimeException(trailers);
  }

  @SuppressWarnings("unchecked")
  public static AurigaException toAurigaException(StatusRuntimeException e) {
    Metadata trailers = e.getTrailers();
    Status.Code grpc = e.getStatus().getCode();
    String code = trailers == null ? null : trailers.get(GrpcMetadata.ERROR_CODE);
    ErrorCode errorCode;
    try {
      errorCode =
          code != null ? ErrorCode.of(code, ErrorCode.httpStatusOf(grpc), grpc) : fallback(grpc);
    } catch (IllegalArgumentException invalid) {
      errorCode = fallback(grpc);
    }
    Map<String, Object> params = new LinkedHashMap<>();
    byte[] raw = trailers == null ? null : trailers.get(GrpcMetadata.ERROR_PARAMS);
    if (raw != null) {
      try {
        params.putAll(JSON.readValue(raw, Map.class));
      } catch (RuntimeException ignored) {
        // ignore malformed params
      }
    }
    return new AurigaException(errorCode, params, null, e);
  }

  private static ErrorCode fallback(Status.Code grpc) {
    return switch (grpc) {
      case UNAUTHENTICATED -> CommonErrors.UNAUTHENTICATED;
      case PERMISSION_DENIED -> CommonErrors.FORBIDDEN;
      case NOT_FOUND -> CommonErrors.NOT_FOUND;
      case INVALID_ARGUMENT -> CommonErrors.VALIDATION_FAILED;
      case ALREADY_EXISTS, ABORTED -> CommonErrors.CONFLICT;
      case FAILED_PRECONDITION -> CommonErrors.PRECONDITION_FAILED;
      case RESOURCE_EXHAUSTED -> CommonErrors.RATE_LIMITED;
      case UNAVAILABLE, DEADLINE_EXCEEDED -> CommonErrors.UNAVAILABLE;
      default -> CommonErrors.INTERNAL;
    };
  }

  /** Runs a blocking stub call, translating gRPC failures to {@link AurigaException}. */
  public static <T> T call(Supplier<T> stubCall) {
    try {
      return stubCall.get();
    } catch (StatusRuntimeException e) {
      throw toAurigaException(e);
    }
  }
}
