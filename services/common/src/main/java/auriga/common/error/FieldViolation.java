package auriga.common.error;

import java.util.Objects;

/**
 * One field level error ({@code FieldError} in openapi/common.yaml).
 *
 * @param field JSON Pointer of the offending field, e.g. {@code /email}
 * @param code machine readable reason, e.g. {@code required}
 * @param message optional human readable text
 */
public record FieldViolation(String field, String code, String message) {
  public FieldViolation {
    Objects.requireNonNull(field, "field");
    Objects.requireNonNull(code, "code");
  }

  /** Converts a bean property path ({@code items[0].name}) into a JSON Pointer. */
  public static String pointer(String propertyPath) {
    if (propertyPath == null || propertyPath.isEmpty()) return "";
    StringBuilder out = new StringBuilder();
    for (String part : propertyPath.replace("[", ".").replace("]", "").split("\\.")) {
      if (part.isEmpty()) continue;
      out.append('/').append(part.replace("~", "~0").replace("/", "~1"));
    }
    return out.toString();
  }
}
