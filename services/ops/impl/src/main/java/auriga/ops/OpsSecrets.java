package auriga.ops;

import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import java.util.Base64;
import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;

final class OpsSecrets {
  private final byte[] key;

  OpsSecrets(String value) {
    key = value.isBlank() ? null : Base64.getDecoder().decode(value);
    if (key != null && key.length != 32)
      throw new IllegalArgumentException("ops encryption key must be 32 bytes");
  }

  String seal(String tenant, String value) {
    if (key == null) throw OpsError.error("unavailable", 503);
    byte[] nonce = new byte[12];
    new SecureRandom().nextBytes(nonce);
    try {
      var c = Cipher.getInstance("AES/GCM/NoPadding");
      c.init(Cipher.ENCRYPT_MODE, new SecretKeySpec(key, "AES"), new GCMParameterSpec(128, nonce));
      c.updateAAD(tenant.getBytes(StandardCharsets.UTF_8));
      return Base64.getEncoder().encodeToString(nonce)
          + ":"
          + Base64.getEncoder().encodeToString(c.doFinal(value.getBytes(StandardCharsets.UTF_8)));
    } catch (java.security.GeneralSecurityException e) {
      throw OpsError.error("unavailable", 503);
    }
  }

  String open(String tenant, String value) {
    if (key == null) throw OpsError.error("unavailable", 503);
    try {
      String[] parts = value.split(":", 2);
      var c = Cipher.getInstance("AES/GCM/NoPadding");
      c.init(
          Cipher.DECRYPT_MODE,
          new SecretKeySpec(key, "AES"),
          new GCMParameterSpec(128, Base64.getDecoder().decode(parts[0])));
      c.updateAAD(tenant.getBytes(StandardCharsets.UTF_8));
      return new String(c.doFinal(Base64.getDecoder().decode(parts[1])), StandardCharsets.UTF_8);
    } catch (java.security.GeneralSecurityException | IllegalArgumentException e) {
      throw OpsError.error("unavailable", 503);
    }
  }
}
