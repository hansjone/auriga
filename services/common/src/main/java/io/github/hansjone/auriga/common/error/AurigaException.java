package io.github.hansjone.auriga.common.error;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;

/**
 * Business error carrying a contract error code. Mapped to RFC 9457 problem details over HTTP and
 * to a gRPC status plus {@code auriga-error-code} metadata over gRPC.
 */
public class AurigaException extends RuntimeException {
  private final ErrorCode errorCode;
  private final Map<String, Object> params;
  private final List<FieldViolation> violations;

  public AurigaException(ErrorCode errorCode) {
    this(errorCode, Map.of(), List.of(), null);
  }

  public AurigaException(ErrorCode errorCode, Map<String, ?> params) {
    this(errorCode, params, List.of(), null);
  }

  public AurigaException(
      ErrorCode errorCode,
      Map<String, ?> params,
      List<FieldViolation> violations,
      Throwable cause) {
    super(errorCode.code(), cause);
    this.errorCode = Objects.requireNonNull(errorCode);
    this.params =
        Collections.unmodifiableMap(new LinkedHashMap<>(params == null ? Map.of() : params));
    this.violations = List.copyOf(violations == null ? List.of() : violations);
  }

  public static AurigaException of(ErrorCode code, Object... keyValues) {
    if (keyValues.length % 2 != 0) {
      throw new IllegalArgumentException("params must be key/value pairs");
    }
    Map<String, Object> params = new LinkedHashMap<>();
    for (int i = 0; i < keyValues.length; i += 2) {
      params.put(String.valueOf(keyValues[i]), keyValues[i + 1]);
    }
    return new AurigaException(code, params);
  }

  public static AurigaException validation(List<FieldViolation> violations) {
    return new AurigaException(CommonErrors.VALIDATION_FAILED, Map.of(), violations, null);
  }

  public ErrorCode errorCode() {
    return errorCode;
  }

  public Map<String, Object> params() {
    return params;
  }

  public List<FieldViolation> violations() {
    return violations;
  }
}
