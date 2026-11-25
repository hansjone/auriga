package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;

class UacProofTest {
  private UacTokenVerification.Decision proof(String payload) {
    return UacTokenVerification.evaluateProof(
        200, payload.getBytes(StandardCharsets.UTF_8), "001Ab");
  }

  @Test
  void nativeProofNeverBorrowsTheQrMfaExceptionAndMissingMarkersFailClosed() {
    var tx = Json.object("nativeGrantHash", "synthetic-grant");
    for (var marker :
        java.util.List.of(
            Json.object(),
            Json.object("uacProofLogin", true),
            Json.object("uacProofLogin", "false"),
            Json.object("uacProofLogin", null))) {
      var candidate = tx.deepCopy();
      candidate.setAll(marker);
      assertFalse(LoginManager.uacMaySkipPlatformMfa(candidate, "new_device"));
      assertFalse(LoginManager.uacMaySkipPlatformMfa(candidate, "always"));
      assertTrue(LoginManager.uacMaySkipPlatformMfa(candidate, "never"));
    }
    tx.put("uacProofLogin", false);
    assertTrue(LoginManager.uacMaySkipPlatformMfa(tx, "new_device"));
    assertFalse(LoginManager.uacMaySkipPlatformMfa(tx, "always"));
    assertFalse(LoginManager.uacMaySkipPlatformMfa(tx, "unknown"));
  }

  @Test
  void requiresAnExplicitAuthoritativeSubjectAndPreservesLeadingZeros() {
    String prefix =
        "{\"code\":{\"code\":\"0000\"},\"bo\":{\"code\":\"0000\",\"token\":\"synthetic-token\"}";
    assertFalse(proof(prefix + "}").success());
    assertEquals("missing_subject", proof(prefix + "}").faultReason());
    var verified = proof(prefix + ",\"other\":{\"account\":\" 001AB \"}}");
    assertTrue(verified.success());
    assertEquals("001AB", verified.employee());
    assertFalse(proof(prefix + ",\"other\":{\"account\":\"1Ab\"}}").success());
    assertFalse(proof(prefix + ",\"other\":{\"account\":null}}").success());
    assertFalse(
        proof(
                "{\"code\":{\"code\":\"0000\"},\"bo\":{\"code\":\"0000\",\"user\":\"001Ab\",\"token\":\"synthetic-token\"},\"other\":{\"account\":\"OTHER\"}}")
            .success());
    assertTrue(
        proof(
                "{\"code\":{\"code\":\"0000\"},\"bo\":{\"code\":\"0000\",\"user\":\"001Ab\",\"token\":\"synthetic-token\"}}")
            .success());
    assertTrue(
        UacTokenVerification.evaluate(200, (prefix + "}").getBytes(StandardCharsets.UTF_8), "001Ab")
            .success());
  }
}
