package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;

class UacProofTest {
  private UacTokenVerification.Decision proof(String payload) {
    return UacTokenVerification.evaluateProof(
        200, payload.getBytes(StandardCharsets.UTF_8), "001Ab", false);
  }

  @Test
  void onlyServerVerifiedUacLoginAvoidsAdditionalPlatformMfa() {
    var tx =
        Json.object(
            "purpose",
            "login",
            "uacVerified",
            true,
            "uacCredential",
            Json.object("encrypted", "synthetic"),
            "uacVerifiedRevision",
            "synthetic-provider-revision");
    for (String policy : java.util.List.of("never", "new_device", "always")) {
      var provider = Json.object("connectorType", "uac", "admission", Json.object("mfa", policy));
      assertTrue(LoginManager.verifiedUacLogin(tx, provider));
      for (boolean proof : java.util.List.of(false, true)) {
        var nativeTx =
            tx.deepCopy().put("nativeGrantHash", "synthetic").put("uacProofLogin", proof);
        assertTrue(LoginManager.verifiedUacLogin(nativeTx, provider));
      }
      for (String field :
          java.util.List.of("purpose", "uacVerified", "uacCredential", "uacVerifiedRevision")) {
        var missing = tx.deepCopy();
        missing.remove(field);
        assertFalse(LoginManager.verifiedUacLogin(missing, provider));
      }
      assertFalse(
          LoginManager.verifiedUacLogin(tx.deepCopy().put("uacVerified", "true"), provider));
      assertFalse(
          LoginManager.verifiedUacLogin(tx.deepCopy().put("uacCredential", "synthetic"), provider));
      assertFalse(LoginManager.verifiedUacLogin(tx.deepCopy().put("purpose", "stepup"), provider));
      assertFalse(
          LoginManager.verifiedUacLogin(
              tx, provider.deepCopy().put("connectorType", "team-account")));
      assertFalse(
          LoginManager.verifiedUacLogin(tx, provider.deepCopy().put("connectorType", "oidc")));
    }
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
