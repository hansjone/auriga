package io.github.hansjone.auriga.common.audit;

import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * Before/after values for an audit record. Secret fields keep only their name: {@link #secret}
 * marks them explicitly, and any field whose name looks secret (password, secret, token, key, ...)
 * is redacted even if passed to {@link #change}.
 *
 * <p>Rendered as {@code {"email": {"before": "a@example.com", "after": "b@example.com"},
 * "password": {"redacted": true}}}.
 */
public final class AuditChanges {
  private static final List<String> SECRET_MARKERS =
      List.of(
          "password",
          "passwd",
          "secret",
          "token",
          "apikey",
          "api_key",
          "privatekey",
          "private_key",
          "credential",
          "otp",
          "totp",
          "recovery",
          "cookie",
          "authorization",
          "passphrase",
          "pin");

  private final Map<String, Object> fields = new LinkedHashMap<>();

  public static AuditChanges create() {
    return new AuditChanges();
  }

  public AuditChanges change(String field, Object before, Object after) {
    if (isSecretName(field)) return secret(field);
    Map<String, Object> diff = new LinkedHashMap<>();
    diff.put("before", before);
    diff.put("after", after);
    fields.put(field, diff);
    return this;
  }

  public AuditChanges secret(String field) {
    fields.put(field, Map.of("redacted", true));
    return this;
  }

  public Map<String, Object> asMap() {
    return Collections.unmodifiableMap(fields);
  }

  /** Name-based secret detection (case/separator insensitive). */
  public static boolean isSecretName(String name) {
    String n = name.toLowerCase(Locale.ROOT).replace("-", "_");
    String compact = n.replace("_", "");
    for (String marker : SECRET_MARKERS) {
      String m = marker.replace("_", "");
      if (marker.equals("pin") || marker.equals("otp")) {
        // short markers only as whole words: "pin", "otp_code", "totp", not "shipping"/"option"
        for (String word : n.split("_")) if (word.equals(marker)) return true;
        continue;
      }
      if (compact.contains(m)) return true;
    }
    return false;
  }

  /** Recursively replaces values of secret-looking keys with {@code "[redacted]"}. */
  @SuppressWarnings("unchecked")
  static Object redact(Object value) {
    if (value instanceof Map<?, ?> map) {
      Map<String, Object> out = new LinkedHashMap<>();
      map.forEach(
          (k, v) -> {
            String key = String.valueOf(k);
            out.put(key, isSecretName(key) ? "[redacted]" : redact(v));
          });
      return out;
    }
    if (value instanceof List<?> list) return list.stream().map(AuditChanges::redact).toList();
    return value;
  }
}
