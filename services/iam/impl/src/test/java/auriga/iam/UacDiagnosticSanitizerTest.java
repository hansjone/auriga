package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import java.util.List;
import org.junit.jupiter.api.Test;

class UacDiagnosticSanitizerTest {
  @Test
  void aPrivateCredentialRepeatedAsCodeIsRedactedWithoutPriorKnowledgeOfItsValue() {
    String token = Secrets.random();
    var response =
        Json.object(
            "privateCredentialField",
            token,
            "bo",
            Json.object("code", token),
            "code",
            Json.object("code", "0000"));
    String output = sanitize(response.toString(), List.of()).toString();
    assertFalse(output.contains(token));
    assertTrue(output.contains("0000"));
  }

  @Test
  void removesSecretsAndPiiFromKeysValuesArraysMessagesAndHeaders() {
    String token = Secrets.random();
    var secrets = List.of(token, "001Ab", "测试姓名", "测试部门", "test@example.com", "+1-202-555-0100");
    var root = Json.object("code", Json.object("code", "0000", "message", "success"));
    for (String key :
        List.of(
            "token",
            "Token",
            "Authorization",
            "Cookie",
            "verifyCode",
            "clientIP",
            "orgId",
            "name",
            "department",
            "email",
            "phone",
            "verifier",
            "binding")) root.put(key, String.join("\n", secrets));
    root.put(token, "hidden");
    root.set(
        "bo",
        Json.object(
            "code",
            token,
            "message",
            "error " + token,
            "data",
            List.of(
                Json.object("name", secrets.get(2)),
                secrets.get(3),
                Json.object(secrets.get(4), secrets.get(5)))));
    String output = sanitize(root.toString(), secrets).toString();
    for (String value : secrets) assertFalse(output.contains(value), output);
    assertTrue(output.contains("0000"));
    assertTrue(output.contains("messageRedacted\":true"));
    assertFalse(output.contains("Authorization"));
    assertTrue(output.contains("_unknown_"));
  }

  @Test
  void arbitraryScalarValuesAreNeverPreservedAndMalformedInputIsOmitted() {
    for (String body :
        List.of(
            "{\"code\":{\"code\":\"0000\"},\"message\":\"success\\nsecret\"}",
            "{\"bo\":\"secret\",\"list\":[\"secret\",42,true,null]}",
            "{\"code\":\"0000\",\"code\":\"secret\"}",
            "<html>secret</html>",
            "{} {\"secret\":1}",
            "{\"secret\":")) assertFalse(sanitize(body, List.of()).toString().contains("secret"));
    var large =
        UacDiagnosticSanitizer.sanitize(new byte[UacTokenVerification.MAX_BODY + 1], List.of());
    assertTrue(large.path("truncated").asBoolean());
    assertFalse(large.has("udsRawSanitized"));
    assertFalse(sanitize("[".repeat(100) + "]".repeat(100), List.of()).has("udsRawSanitized"));
  }

  @Test
  void boundsEventSizeAndHashesExactUtf8Token() {
    var root = Json.object();
    for (int i = 0; i < 4000; i++) root.put("private-field-" + i, "private-value");
    var out = sanitize(root.toString(), List.of());
    assertTrue(out.path("truncated").asBoolean());
    assertTrue(out.toString().getBytes(StandardCharsets.UTF_8).length <= 64 * 1024);
    String token = Secrets.random() + "中文";
    assertEquals(
        Secrets.hash(token).substring(0, 12), UacDiagnosticSanitizer.tokenHashPrefix(token));
    assertNull(UacDiagnosticSanitizer.tokenHashPrefix(null));
  }

  private static tools.jackson.databind.node.ObjectNode sanitize(String body, List<String> values) {
    return UacDiagnosticSanitizer.sanitize(body.getBytes(StandardCharsets.UTF_8), values);
  }
}
