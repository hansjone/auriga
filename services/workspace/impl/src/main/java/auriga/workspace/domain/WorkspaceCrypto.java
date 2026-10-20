package auriga.workspace.domain;

import auriga.contracts.node.v1.SealedSecret;
import com.google.protobuf.ByteString;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.Arrays;
import java.util.HexFormat;
import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import org.bouncycastle.crypto.hpke.HPKE;

/** At-rest AES-GCM and the contract's RFC 9180 HPKE suite. Never exposes keys via toString. */
public final class WorkspaceCrypto {
  private static final SecureRandom RANDOM = new SecureRandom();
  private final SecretKeySpec key;

  public WorkspaceCrypto(byte[] key) {
    if (key.length != 32)
      throw new IllegalArgumentException("workspace encryption key must contain 32 bytes");
    this.key = new SecretKeySpec(key, "AES");
  }

  public static byte[] random(int n) {
    byte[] b = new byte[n];
    RANDOM.nextBytes(b);
    return b;
  }

  public static byte[] hash(byte[] b) {
    try {
      return MessageDigest.getInstance("SHA-256").digest(b);
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
  }

  public static byte[] hash(String s) {
    return hash(s.getBytes(StandardCharsets.UTF_8));
  }

  public byte[] encrypt(byte[] value, String aad) {
    try {
      byte[] nonce = random(12);
      Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(Cipher.ENCRYPT_MODE, key, new GCMParameterSpec(128, nonce));
      cipher.updateAAD(aad.getBytes(StandardCharsets.UTF_8));
      byte[] encrypted = cipher.doFinal(value),
          result = Arrays.copyOf(nonce, 12 + encrypted.length);
      System.arraycopy(encrypted, 0, result, 12, encrypted.length);
      return result;
    } catch (Exception e) {
      throw new IllegalStateException("Cannot encrypt workspace secret", e);
    }
  }

  public byte[] decrypt(byte[] value, String aad) {
    try {
      Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(Cipher.DECRYPT_MODE, key, new GCMParameterSpec(128, Arrays.copyOf(value, 12)));
      cipher.updateAAD(aad.getBytes(StandardCharsets.UTF_8));
      return cipher.doFinal(value, 12, value.length - 12);
    } catch (Exception e) {
      throw new IllegalStateException("Cannot decrypt workspace secret", e);
    }
  }

  public static HPKE hpke() {
    return new HPKE(
        HPKE.mode_base, HPKE.kem_X25519_SHA256, HPKE.kdf_HKDF_SHA256, HPKE.aead_AES_GCM256);
  }

  public static SealedSecret seal(
      byte[] publicKey, String id, String name, long generation, byte[] secret) {
    var hpke = hpke();
    var sender =
        hpke.setupBaseS(
            hpke.deserializePublicKey(publicKey),
            ("auriga/sealed/v1/" + id + "/" + name).getBytes(StandardCharsets.UTF_8));
    try {
      return SealedSecret.newBuilder()
          .setAlg("hpke-x25519-sha256-aes256gcm")
          .setKeyId(HexFormat.of().formatHex(hash(publicKey)).substring(0, 16))
          .setEnc(ByteString.copyFrom(sender.getEncapsulation()))
          .setCiphertext(
              ByteString.copyFrom(
                  sender.seal(Long.toString(generation).getBytes(StandardCharsets.UTF_8), secret)))
          .build();
    } catch (Exception e) {
      throw new IllegalStateException("Cannot seal node secret", e);
    }
  }
}
