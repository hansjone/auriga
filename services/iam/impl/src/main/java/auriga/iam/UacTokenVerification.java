package auriga.iam;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.HexFormat;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.DeserializationFeature;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Pure protocol boundary: no network, persistence, logging or session revocation. */
final class UacTokenVerification {
  static final int MAX_BODY = 2 * 1024 * 1024;
  private static final JsonMapper MAPPER =
      JsonMapper.builder(
              JsonFactory.builder()
                  .streamReadConstraints(
                      StreamReadConstraints.builder().maxNestingDepth(64).build())
                  .build())
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();

  // This is an internal decision, not a replacement for a generated wire model.
  record Decision(
      String failureClass, String faultReason, String replacementToken, String employee) {
    boolean success() {
      return failureClass == null;
    }

    boolean rejected() {
      return "business_rejected".equals(failureClass) || "identity_mismatch".equals(failureClass);
    }

    @Override
    public String toString() {
      return "Decision[failureClass=" + failureClass + ", faultReason=" + faultReason + "]";
    }
  }

  static ObjectNode request(String account, String token, String clientIP, String systemCode) {
    IamError.validate(validUser(account) && validToken(token));
    String employee = stripWhitespace(account);
    String ip = clientIP == null ? "" : clientIP;
    String system = systemCode == null ? "" : systemCode;
    try {
      String digest =
          HexFormat.of()
              .formatHex(
                  MessageDigest.getInstance("MD5")
                      .digest((employee + token + ip + system).getBytes(StandardCharsets.UTF_8)));
      return Json.object(
          "account",
          employee,
          "token",
          token,
          "clientIP",
          ip,
          "systemCode",
          system,
          "verifyCode",
          digest);
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException("Required protocol digest unavailable");
    }
  }

  static Decision transportFailure() {
    return fault("transport_error");
  }

  static Decision evaluate(int status, byte[] body, String verifiedAccount) {
    IamError.validate(validUser(verifiedAccount));
    if (status != 200) return fault("http_error");
    if (body == null || body.length == 0 || body.length > MAX_BODY) return fault("invalid_json");
    JsonNode root;
    try {
      root = MAPPER.readTree(body);
    } catch (RuntimeException e) {
      return fault("invalid_json");
    }
    if (root == null || !root.isObject()) return fault("invalid_json");
    JsonNode outer = root.path("code").path("code"), inner = root.path("bo").path("code");
    if (outer.isMissingNode() || inner.isMissingNode()) return fault("missing_code");
    if (!outer.isString()
        || outer.asString().isBlank()
        || !inner.isString()
        || inner.asString().isBlank()) return fault("invalid_code");
    if (!outer.asString().equals("0000") || !inner.asString().equals("0000"))
      return new Decision("business_rejected", null, null, null);
    // Only an absent field permits the legacy token location. Use a validated
    // echo's spelling for the same identity that QR login would bind.
    JsonNode bo = root.path("bo"), other = root.path("other");
    if (!other.isMissingNode() && !other.isObject()) return fault("invalid_success_payload");
    JsonNode user = bo.path("user"), token = other.path("token");
    if (token.isMissingNode()) token = bo.path("token");
    boolean echoed = !user.isMissingNode();
    if ((echoed && (!user.isString() || !validUser(user.asString())))
        || !token.isString()
        || !validToken(token.asString())) return fault("invalid_success_payload");
    if (echoed && !fold(user.asString()).equals(fold(verifiedAccount)))
      return new Decision("identity_mismatch", null, null, null);
    return new Decision(
        null, null, token.asString(), stripWhitespace(echoed ? user.asString() : verifiedAccount));
  }

  private static Decision fault(String reason) {
    return new Decision("upstream_fault", reason, null, null);
  }

  // Same fixed set as the desktop UAC adapter: Unicode White_Space plus BOM
  // and the legacy Java separators, so formerly blank tokens stay invalid.
  private static boolean isWhitespace(char c) {
    return c >= 0x0009 && c <= 0x000d
        || c >= 0x001c && c <= 0x0020
        || c >= 0x2000 && c <= 0x200a
        || switch (c) {
          case 0x0085, 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff -> true;
          default -> false;
        };
  }

  static String stripWhitespace(String value) {
    int begin = 0, end = value.length();
    while (begin < end && isWhitespace(value.charAt(begin))) begin++;
    while (end > begin && isWhitespace(value.charAt(end - 1))) end--;
    return value.substring(begin, end);
  }

  static boolean validUser(String value) {
    if (value == null) return false;
    String user = stripWhitespace(value);
    return !user.isEmpty() && user.length() <= 128;
  }

  static boolean validToken(String value) {
    return value != null && value.length() <= 16384 && !stripWhitespace(value).isEmpty();
  }

  private static String fold(String value) {
    StringBuilder out = new StringBuilder();
    stripWhitespace(value)
        .chars()
        .forEach(c -> out.append((char) (c >= 'A' && c <= 'Z' ? c + 32 : c)));
    return out.toString();
  }

  private UacTokenVerification() {}
}
