package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.SecureRandom;
import java.util.Arrays;
import java.util.Base64;
import java.util.HashMap;
import java.util.Map;
import javax.crypto.Cipher;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Realm-local KEK ring. Reads any installed KEK, always writes the selected current KEK. */
public final class MsgnetKeyEncryption implements AutoCloseable {
  private final Map<String, byte[]> ring = new HashMap<>();
  private final String current;
  private final SecureRandom random = new SecureRandom();
  private boolean closed;

  public MsgnetKeyEncryption(String current, Map<String, byte[]> keys) {
    if (current == null || !keys.containsKey(current) || keys.isEmpty()) throw invalid();
    for (var entry : keys.entrySet()) {
      if (entry.getKey() == null
          || entry.getKey().isEmpty()
          || entry.getKey().length() > 128
          || entry.getValue() == null
          || entry.getValue().length != 32) throw invalid();
    }
    keys.forEach((id, key) -> ring.put(id, key.clone()));
    this.current = current;
  }

  String currentId() {
    return current;
  }

  java.util.Set<String> keyIds() {
    return java.util.Set.copyOf(ring.keySet());
  }

  public synchronized ObjectNode encrypt(JsonNode aad, byte[] privateKey) {
    checkOpen();
    MsgnetSchema.validate("PrivateKeyAad", aad);
    if (privateKey == null || privateKey.length == 0 || privateKey.length > 16384) throw invalid();
    byte[] nonce = new byte[12];
    random.nextBytes(nonce);
    var result = JsonMapper.builder().build().createObjectNode();
    result.put("kek_id", current).put("alg", "AES-256-GCM").put("nonce", encode(nonce));
    result.set("aad", aad.deepCopy());
    result.put("ciphertext", encode(crypt(Cipher.ENCRYPT_MODE, current, nonce, aad, privateKey)));
    return result;
  }

  /** Expected AAD comes from the authorized lookup, never from the encrypted row itself. */
  public synchronized byte[] decrypt(JsonNode encrypted, JsonNode expectedAad) {
    checkOpen();
    MsgnetSchema.validate("EncryptedPrivateKey", encrypted);
    MsgnetSchema.validate("PrivateKeyAad", expectedAad);
    if (!encrypted.path("aad").equals(expectedAad)) throw invalid();
    byte[] nonce = decode(encrypted.path("nonce").asString());
    byte[] ciphertext = decode(encrypted.path("ciphertext").asString());
    if (nonce.length != 12 || ciphertext.length <= 16) throw invalid();
    return crypt(
        Cipher.DECRYPT_MODE, encrypted.path("kek_id").asString(), nonce, expectedAad, ciphertext);
  }

  public synchronized ObjectNode reencrypt(JsonNode encrypted, JsonNode expectedAad) {
    byte[] plaintext = decrypt(encrypted, expectedAad);
    try {
      return encrypt(expectedAad, plaintext);
    } finally {
      Arrays.fill(plaintext, (byte) 0);
    }
  }

  private byte[] crypt(int mode, String id, byte[] nonce, JsonNode aad, byte[] input) {
    byte[] key = ring.get(id);
    if (key == null) throw invalid();
    try {
      var cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(mode, new SecretKeySpec(key, "AES"), new GCMParameterSpec(128, nonce));
      cipher.updateAAD(NetworkJson.canonical(aad).getBytes(StandardCharsets.UTF_8));
      return cipher.doFinal(input);
    } catch (GeneralSecurityException e) {
      throw invalid();
    }
  }

  private static String encode(byte[] bytes) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
  }

  private static byte[] decode(String text) {
    try {
      byte[] bytes = Base64.getUrlDecoder().decode(text);
      if (!encode(bytes).equals(text)) throw invalid();
      return bytes;
    } catch (IllegalArgumentException e) {
      throw invalid();
    }
  }

  private void checkOpen() {
    if (closed) throw new IllegalStateException("Msgnet KEK ring closed");
  }

  private static IllegalArgumentException invalid() {
    return new IllegalArgumentException("Invalid msgnet encrypted key");
  }

  @Override
  public synchronized void close() {
    ring.values().forEach(key -> Arrays.fill(key, (byte) 0));
    ring.clear();
    closed = true;
  }
}
