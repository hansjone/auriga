package auriga.iam;

import java.nio.charset.StandardCharsets;
import java.security.*;
import java.util.*;
import javax.crypto.*;
import javax.crypto.spec.*;

/** AES-256-GCM, with tenant/purpose AAD to prevent ciphertext swapping. */
public final class Secrets {
  private static final SecureRandom RANDOM = new SecureRandom();
  private final SecretKey key;

  public Secrets(String base64Key) {
    byte[] raw = Base64.getDecoder().decode(base64Key);
    if (raw.length != 32) throw new IllegalArgumentException("IAM encryption key must be 256 bits");
    key = new SecretKeySpec(raw, "AES");
  }

  public static String random() {
    byte[] b = new byte[32];
    RANDOM.nextBytes(b);
    return Base64.getUrlEncoder().withoutPadding().encodeToString(b);
  }

  public static String hash(String raw) {
    try {
      return HexFormat.of()
          .formatHex(
              MessageDigest.getInstance("SHA-256").digest(raw.getBytes(StandardCharsets.UTF_8)));
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  public static boolean same(String a, String b) {
    return a != null
        && b != null
        && MessageDigest.isEqual(
            a.getBytes(StandardCharsets.UTF_8), b.getBytes(StandardCharsets.UTF_8));
  }

  public String encrypt(String plain, String purpose) {
    try {
      byte[] iv = new byte[12];
      RANDOM.nextBytes(iv);
      Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(Cipher.ENCRYPT_MODE, key, new GCMParameterSpec(128, iv));
      cipher.updateAAD(purpose.getBytes(StandardCharsets.UTF_8));
      return Base64.getEncoder().encodeToString(iv)
          + "."
          + Base64.getEncoder()
              .encodeToString(cipher.doFinal(plain.getBytes(StandardCharsets.UTF_8)));
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  public String decrypt(String encrypted, String purpose) {
    try {
      String[] parts = encrypted.split("\\.");
      Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(
          Cipher.DECRYPT_MODE,
          key,
          new GCMParameterSpec(128, Base64.getDecoder().decode(parts[0])));
      cipher.updateAAD(purpose.getBytes(StandardCharsets.UTF_8));
      return new String(
          cipher.doFinal(Base64.getDecoder().decode(parts[1])), StandardCharsets.UTF_8);
    } catch (GeneralSecurityException | IllegalArgumentException e) {
      throw new IllegalStateException("Invalid encrypted IAM value", e);
    }
  }
}
