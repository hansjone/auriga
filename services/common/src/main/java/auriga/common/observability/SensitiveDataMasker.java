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
  private static final int MAX_LOG_LENGTH = 65536;
  private static final Pattern KEYS =
      Pattern.compile("(?<![A-Za-z0-9_-])[\"']?([A-Za-z][A-Za-z0-9_-]*)[\"']?\\s*[:=]\\s*");
  private static final Pattern PEM_BEGIN =
      Pattern.compile("-----BEGIN (?:[A-Z]{1,16} )?PRIVATE KEY-----");
  private static final Pattern PEM_END =
      Pattern.compile("-----END (?:[A-Z]{1,16} )?PRIVATE KEY-----");

  private record Rule(Pattern pattern, String replacement) {}

  private static final String SECRET_KEYS =
      "password|passwd|pwd|secret|client_secret|token|access_token|refresh_token|id_token|"
          + "api[_-]?key|private[_-]?key|credential|cookie|authorization|session[_-]?token|totp|otp|"
          + "[a-z][a-z0-9_]*(?:_password|_passwd|_pass|_secret|_token|_api_key|_private_key|_credential|_totp|_access_key_id|_secret_access_key|_encryption_key)(?:_value)?";

  private static final Pattern SECRET_KEY = Pattern.compile("(?i)^(?:" + SECRET_KEYS + ")$");

  private static final List<Rule> RULES =
      List.of(
          // Authorization: Bearer xxx / Basic xxx
          new Rule(
              Pattern.compile("(?i)\\b(bearer|basic)\\s+[A-Za-z0-9._~+/=-]{6,}"), "$1 " + MASK),
          // instance tokens (contracts: ait_...)
          new Rule(Pattern.compile("\\bait_[A-Za-z0-9_-]{6,}"), "ait_" + MASK),
          // URL userinfo password: scheme://user:secret@host
          new Rule(
              Pattern.compile("(?i)\\b([a-z][a-z0-9+.-]{0,31}://[^/\\s:@]*):[^/\\s@]+@"),
              "$1:" + MASK + "@"),
          // Cookie / Set-Cookie headers: every pair up to the end of the line
          new Rule(
              Pattern.compile("(?i)(\\b(?:set-)?cookie\"?\\s*[:=]\\s*\"?)[^\"\\r\\n]+"),
              "$1" + MASK));

  private SensitiveDataMasker() {}

  /** Explicit credential fields are masked in full, including whitespace/multiline values. */
  public static String maskField(String key, String value) {
    return SECRET_KEY.matcher(key).matches() ? MASK : mask(value);
  }

  public static String mask(String text) {
    if (text == null || text.isEmpty()) return text;
    if (text.length() > MAX_LOG_LENGTH) return "[log omitted: size limit]";
    // Plain path/identifier tokens contain neither a credential assignment nor
    // any of the standalone token/header forms. Avoid invoking every regex on
    // an attacker-controlled, delimiter-free request segment.
    if (text.indexOf(':') < 0
        && text.indexOf('=') < 0
        && (!text.contains("eyJ") || text.indexOf('.') < 0)
        && !text.contains("ait_")
        && !text.contains("-----BEGIN")
        && text.chars().noneMatch(Character::isWhitespace)) return text;
    String out = maskJWTs(maskPrivateKeys(text));
    for (Rule rule : RULES) {
      Matcher m = rule.pattern().matcher(out);
      if (m.find()) out = m.replaceAll(rule.replacement());
    }
    return maskFields(out);
  }

  private static String maskPrivateKeys(String text) {
    Matcher begin = PEM_BEGIN.matcher(text), end = PEM_END.matcher(text);
    StringBuilder out = new StringBuilder();
    int cursor = 0;
    while (begin.find(cursor)) {
      out.append(text, cursor, begin.start()).append(MASK);
      cursor = end.find(begin.end()) ? end.end() : text.length();
      if (cursor == text.length()) break;
    }
    return out.append(text, cursor, text.length()).toString();
  }

  private static String maskJWTs(String text) {
    StringBuilder out = new StringBuilder();
    int cursor = 0, start = 0;
    while (start < text.length()) {
      if (!jwtChar(text.charAt(start))) {
        start++;
        continue;
      }
      int first = jwtEnd(text, start);
      if (!text.substring(start, first).contains("eyJ")
          || first == text.length()
          || text.charAt(first) != '.') {
        start = first;
        continue;
      }
      int second = jwtEnd(text, first + 1);
      if (second == first + 1 || second == text.length() || text.charAt(second) != '.') {
        start = first;
        continue;
      }
      int end = jwtEnd(text, second + 1);
      out.append(text, cursor, start).append(MASK);
      cursor = end;
      start = end;
    }
    return out.append(text, cursor, text.length()).toString();
  }

  private static int jwtEnd(String text, int start) {
    while (start < text.length() && jwtChar(text.charAt(start))) start++;
    return start;
  }

  private static boolean jwtChar(char c) {
    return c >= 'A' && c <= 'Z'
        || c >= 'a' && c <= 'z'
        || c >= '0' && c <= '9'
        || c == '_'
        || c == '-';
  }

  /** Match each complete key once instead of retrying secret suffixes at every character. */
  private static String maskFields(String text) {
    Matcher keys = KEYS.matcher(text);
    StringBuilder out = new StringBuilder();
    int cursor = 0;
    while (keys.find()) {
      if (keys.start() < cursor) continue;
      String key = keys.group(1);
      boolean escapedLine =
          keys.start() > 0
              && text.charAt(keys.start() - 1) == '\\'
              && "nrt".indexOf(key.charAt(0)) >= 0;
      if (!SECRET_KEY.matcher(key).matches()
          && !SECRET_KEY.matcher(key.replace('-', '_')).matches()
          && !(escapedLine && SECRET_KEY.matcher(key.substring(1)).matches())) continue;
      int start = keys.end(), end = start;
      char quote = start < text.length() ? text.charAt(start) : 0;
      if (quote == '"' || quote == '\'') {
        end++;
        while (end < text.length()) {
          char c = text.charAt(end++);
          if (c == '\\') end++;
          else if (c == quote) break;
        }
        end = Math.min(end, text.length());
        out.append(text, cursor, start).append(quote).append(MASK).append(quote);
      } else {
        while (end < text.length()
            && !Character.isWhitespace(text.charAt(end))
            && ",;&}\"'".indexOf(text.charAt(end)) < 0) end++;
        out.append(text, cursor, start).append(MASK);
      }
      cursor = end;
    }
    return out.append(text, cursor, text.length()).toString();
  }
}
