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
      var result = decide(200, success(" 001AB ", token), account);
      assertTrue(result.success());
      assertEquals(token, result.replacementToken());
      assertEquals("001AB", result.employee());
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

  @Test
  void acceptsUnchangedOrRotatedPrimaryTokenWithoutAnEcho() {
    for (String token : List.of("synthetic-old-token", "synthetic-new-token")) {
      var result =
          decide(
              200,
              payload("\"code\":\"0000\"", ",\"other\":{\"token\":\"" + token + "\"}"),
              "001Ab");
      assertTrue(result.success());
      assertEquals(token, result.replacementToken());
      assertEquals("001Ab", result.employee());
    }
  }

  @Test
  void onlyMissingFieldsPermitFallbackAndExplicitInvalidFieldsAreFaults() {
    String legacy = "\"code\":\"0000\",\"user\":\"001AB\",\"token\":\"synthetic-legacy\"";
    assertEquals(
        "synthetic-legacy",
        decide(200, payload(legacy, ",\"other\":{}"), "001Ab").replacementToken());
    assertEquals(
        "synthetic-primary",
        decide(200, payload(legacy, ",\"other\":{\"token\":\"synthetic-primary\"}"), "001Ab")
            .replacementToken());
    for (String other :
        List.of("null", "[]", "{\"token\":null}", "{\"token\":42}", "{\"token\":\"\"}")) {
      var result = decide(200, payload(legacy, ",\"other\":" + other), "001Ab");
      assertEquals("invalid_success_payload", result.faultReason());
      assertFalse(result.rejected());
    }
    String primary = ",\"other\":{\"token\":\"synthetic-primary\"}";
    for (String user : List.of("null", "42", "\"\"")) {
      var result = decide(200, payload("\"code\":\"0000\",\"user\":" + user, primary), "001Ab");
      assertEquals("invalid_success_payload", result.faultReason());
      assertFalse(result.rejected());
    }
    assertEquals(
        "invalid_success_payload",
        decide(200, payload("\"code\":\"0000\"", ",\"other\":{}"), "001Ab").faultReason());
    assertEquals(
        "identity_mismatch",
        decide(200, payload("\"code\":\"0000\",\"user\":\"002Ab\"", primary), "001Ab")
            .failureClass());
    assertEquals(
        "business_rejected",
        decide(200, payload("\"code\":\"REJECTED\"", primary), "001Ab").failureClass());
  }

  @Test
  void verifiesPaddedNumericEmployeesAndPreservesTokensAcrossBothFields() {
    String account = "001234", padded = "\u3000001234\u00a0";
    String primary = "\u00a0synthetic-primary\u3000", legacy = "\u3000synthetic-legacy\u00a0";
    var bo = Json.object("code", "0000", "user", padded, "token", legacy);
    var response = Json.object("code", Json.object("code", "0000"), "bo", bo);
    response.set("other", Json.object("token", primary));
    var verified = decide(200, response.toString(), account);
    assertTrue(verified.success());
    assertEquals(account, verified.employee());
    assertArrayEquals(
        primary.getBytes(StandardCharsets.UTF_8),
        verified.replacementToken().getBytes(StandardCharsets.UTF_8));

    response.set("other", Json.object());
    verified = decide(200, response.toString(), account);
    assertTrue(verified.success());
    assertEquals(account, verified.employee());
    assertEquals(legacy, verified.replacementToken());

    response.withObject("/bo").remove("user");
    response.set("other", Json.object("token", primary));
    assertEquals(account, decide(200, response.toString(), padded).employee());
    var request = UacTokenVerification.request(padded, primary, "", "test-system");
    assertEquals(account, request.path("account").asString());
    assertEquals(primary, request.path("token").asString());
    assertEquals(
        UacTokenVerification.request(account, primary, "", "test-system").path("verifyCode"),
        request.path("verifyCode"));

    response.withObject("/bo").put("user", "\u300001234\u00a0");
    assertEquals("identity_mismatch", decide(200, response.toString(), account).failureClass());
  }

  @Test
  void whitespaceOnlyTokensAreFaultsAndNeverActivateLegacyFallback() {
    for (String blank : List.of("\u3000", "\u00a0", "\u3000\u00a0\t", "\ufeff", "\u001f")) {
      var bo = Json.object("code", "0000", "user", "001234", "token", "synthetic-legacy");
      var response = Json.object("code", Json.object("code", "0000"), "bo", bo);
      response.set("other", Json.object("token", blank));
      var result = decide(200, response.toString(), "001234");
      assertEquals("invalid_success_payload", result.faultReason());
      assertFalse(result.rejected());
      assertNull(result.replacementToken());

      response.remove("other");
      response.withObject("/bo").put("token", blank);
      result = decide(200, response.toString(), "001234");
      assertEquals("invalid_success_payload", result.faultReason());
      assertFalse(result.rejected());
    }
  }

  private static String payload(String bo, String extra) {
    return "{\"code\":{\"code\":\"0000\"},\"bo\":{" + bo + "}" + extra + "}";
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
