package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import java.util.List;
import org.junit.jupiter.api.Test;

class UacTokenVerificationTest {
  private static UacTokenVerification.Decision decide(int status, String body, String account) {
    return UacTokenVerification.evaluate(status, body.getBytes(StandardCharsets.UTF_8), account);
  }

  @Test
  void onlyCompleteBusinessCodesOnHttp200CanReject() {
    String denied = "{\"code\":{\"code\":\"ANY_FAILURE\"},\"bo\":{\"code\":\"0000\"}}";
    assertEquals("business_rejected", decide(200, denied, "001Ab").failureClass());
    for (int status : List.of(201, 302, 400, 401, 403, 429, 500)) {
      var result = decide(status, denied, "001Ab");
      assertEquals("upstream_fault", result.failureClass());
      assertEquals("http_error", result.faultReason());
      assertFalse(result.rejected());
    }
    for (String body :
        List.of(
            "{\"code\":{\"code\":\"DENIED\"}}",
            "{\"code\":{\"code\":\"DENIED\"},\"bo\":{\"code\":null}}",
            "{\"code\":{\"code\":\"DENIED\"},\"bo\":{\"code\":0}}",
            "{\"code\":{\"code\":\"DENIED\"},\"bo\":{\"code\":\"\"}}")) {
      assertEquals("upstream_fault", decide(200, body, "001Ab").failureClass());
    }
  }

  @Test
  void malformedAndAmbiguousResponsesNeverRevoke() {
    for (String body :
        List.of(
            "",
            "[]",
            "null",
            "<html>error</html>",
            "{\"code\":{},\"code\":{}}",
            "{} {}",
            "{\"code\":{\"code\":\"0000\"}"))
      assertEquals("upstream_fault", decide(200, body, "001Ab").failureClass());
    assertEquals(
        "invalid_json",
        UacTokenVerification.evaluate(200, new byte[UacTokenVerification.MAX_BODY + 1], "001Ab")
            .faultReason());
    assertEquals("transport_error", UacTokenVerification.transportFailure().faultReason());
  }

  @Test
  void validatesSuccessfulPayloadBeforeCheckingIdentityAndKeepsLeadingZeroes() {
    String token = Secrets.random();
    for (String account : List.of("001aB", " 001Ab ", "001AB")) {
      var result = decide(200, success("001ab", token), account);
      assertTrue(result.success());
      assertEquals(token, result.replacementToken());
      assertFalse(result.toString().contains(token));
    }
    assertEquals("identity_mismatch", decide(200, success("1ab", token), "001Ab").failureClass());
    assertEquals("identity_mismatch", decide(200, success("İ", token), "i").failureClass());
    for (String user : List.of("", " ", "a".repeat(129)))
      assertEquals(
          "invalid_success_payload", decide(200, success(user, token), "001Ab").faultReason());
    for (String value : List.of("", " ", "a".repeat(16385)))
      assertEquals(
          "invalid_success_payload", decide(200, success("001Ab", value), "001Ab").faultReason());
    assertEquals(
        "invalid_success_payload",
        decide(
                200,
                "{\"code\":{\"code\":\"0000\"},\"bo\":{\"code\":\"0000\",\"user\":42,\"token\":true}}",
                "001Ab")
            .faultReason());
  }

  @Test
  void requestUsesExactProtocolFieldNamesAndDigestOrdering() throws Exception {
    var request = UacTokenVerification.request("001Ab", "synthetic-token", "", "test-system");
    assertEquals(5, request.size());
    assertTrue(request.has("clientIP"));
    assertFalse(request.has("clientIp"));
    assertEquals("", request.path("clientIP").asString());
    assertEquals(
        java.util.HexFormat.of()
            .formatHex(
                java.security.MessageDigest.getInstance("MD5")
                    .digest("001Absynthetic-tokentest-system".getBytes(StandardCharsets.UTF_8))),
        request.path("verifyCode").asString());
    assertEquals(
        "",
        UacTokenVerification.request("001Ab", "synthetic-token", null, null)
            .path("systemCode")
            .asString());
  }

  private static String success(String user, String token) {
    return Json.object(
            "code",
            Json.object("code", "0000"),
            "bo",
            Json.object("code", "0000", "user", user, "token", token))
        .toString();
  }
}
