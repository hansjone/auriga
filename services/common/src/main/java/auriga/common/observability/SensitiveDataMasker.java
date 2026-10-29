package auriga.common.observability;

import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Last line of defense for logs: masks bearer tokens, JWTs, instance tokens, cookies and
 * password-like key/value pairs that slipped into a message or stack trace.
 */
public final class SensitiveDataMasker {
  private static final String MASK = "****";

  private record Rule(Pattern pattern, String replacement) {}

  private static final String SECRET_KEYS =
      "password|passwd|pwd|secret|client_secret|token|access_token|refresh_token|id_token|"
          + "api[_-]?key|private[_-]?key|credential|cookie|authorization|session[_-]?token|totp|otp|"
          + "[a-z][a-z0-9_]*(?:_password|_passwd|_pass|_secret|_token|_api_key|_private_key|_credential|_totp|_access_key_id|_secret_access_key|_encryption_key)(?:_value)?";

  private static final Pattern SECRET_KEY = Pattern.compile("(?i)^(?:" + SECRET_KEYS + ")$");

  private static final List<Rule> RULES =
      List.of(
          new Rule(
              Pattern.compile(
                  "-----BEGIN (?:[A-Z ]*PRIVATE KEY)-----[\\s\\S]*?-----END (?:[A-Z ]*PRIVATE KEY)-----"),
              MASK),
          // Authorization: Bearer xxx / Basic xxx
          new Rule(
              Pattern.compile("(?i)\\b(bearer|basic)\\s+[A-Za-z0-9._~+/=-]{6,}"), "$1 " + MASK),
          // JWT / JWS compact serialization
          new Rule(
              Pattern.compile("\\beyJ[A-Za-z0-9_-]{5,}\\.[A-Za-z0-9_-]{5,}\\.[A-Za-z0-9_-]*"),
              MASK),
          // instance tokens (contracts: ait_...)
          new Rule(Pattern.compile("\\bait_[A-Za-z0-9_-]{6,}"), "ait_" + MASK),
          // URL userinfo password: scheme://user:secret@host
          new Rule(
              Pattern.compile("(?i)\\b([a-z][a-z0-9+.-]*://[^/\\s:@]*):[^/\\s@]+@"),
              "$1:" + MASK + "@"),
          // Cookie / Set-Cookie headers: every pair up to the end of the line
          new Rule(
              Pattern.compile("(?i)(\\b(?:set-)?cookie\"?\\s*[:=]\\s*\"?)[^\"\\r\\n]+"),
              "$1" + MASK),
          // quoted values may contain spaces: "password": "two words"
          new Rule(
              Pattern.compile(
                  "(?i)(\"(?:" + SECRET_KEYS + ")\"\\s*[:=]\\s*\")(?:[^\"\\\\]|\\\\.)*\""),
              "$1" + MASK + "\""),
          // key=value / "key":"value" for secret-looking keys
          new Rule(
              Pattern.compile("(?i)(\"?(?:" + SECRET_KEYS + ")\"?\\s*[:=]\\s*\"?)([^\"\\s,;&}]+)"),
              "$1" + MASK));

  private SensitiveDataMasker() {}

  /** Explicit credential fields are masked in full, including whitespace/multiline values. */
  public static String maskField(String key, String value) {
    return SECRET_KEY.matcher(key).matches() ? MASK : mask(value);
  }

  public static String mask(String text) {
    if (text == null || text.isEmpty()) return text;
    String out = text;
    for (Rule rule : RULES) {
      Matcher m = rule.pattern().matcher(out);
      if (m.find()) out = m.replaceAll(rule.replacement());
    }
    return out;
  }
}
