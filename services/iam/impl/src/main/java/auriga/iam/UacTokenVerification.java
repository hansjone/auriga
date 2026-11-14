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
    String ip = clientIP == null ? "" : clientIP;
    String system = systemCode == null ? "" : systemCode;
    try {
      String digest =
          HexFormat.of()
              .formatHex(
                  MessageDigest.getInstance("MD5")
                      .digest((account + token + ip + system).getBytes(StandardCharsets.UTF_8)));
      return Json.object(
          "account",
          account,
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
    JsonNode user = root.path("bo").path("user"), token = root.path("bo").path("token");
    if (!user.isString()
        || !validUser(user.asString())
        || !token.isString()
        || !validToken(token.asString())) return fault("invalid_success_payload");
    if (!fold(user.asString()).equals(fold(verifiedAccount)))
      return new Decision("identity_mismatch", null, null, null);
    return new Decision(null, null, token.asString(), user.asString().strip());
  }

  private static Decision fault(String reason) {
    return new Decision("upstream_fault", reason, null, null);
  }

  private static boolean validUser(String value) {
    return value != null && !value.strip().isEmpty() && value.strip().length() <= 128;
  }

  private static boolean validToken(String value) {
    return value != null && !value.isBlank() && value.length() <= 16384;
  }

  private static String fold(String value) {
    StringBuilder out = new StringBuilder();
    value.strip().chars().forEach(c -> out.append((char) (c >= 'A' && c <= 'Z' ? c + 32 : c)));
    return out.toString();
  }

  private UacTokenVerification() {}
}
