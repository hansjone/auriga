package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;

import java.security.SecureRandom;
import java.util.Map;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class MsgnetKeyEncryptionTest {
  static byte[] kek() {
    byte[] value = new byte[32];
    new SecureRandom().nextBytes(value);
    return value;
  }

  static ObjectNode aad() {
    return KeyTestMaterial.JSON
        .createObjectNode()
        .put("tenant_id", "example-tenant")
        .put("realm", "2")
        .put("kid", "example-key")
        .put("alg", "Ed25519")
        .put("purpose", "protocol_response")
        .put("component", "network");
  }

  @Test
  void oldAndNewKeksReadWhileOnlyNewKekWritesAndRewraps() {
    byte[] old = kek();
    byte[] next = kek();
    byte[] secret = KeyTestMaterial.pair().getPrivate().getEncoded();
    try (var original = new MsgnetKeyEncryption("old", Map.of("old", old));
        var rolling = new MsgnetKeyEncryption("new", Map.of("old", old, "new", next));
        var completed = new MsgnetKeyEncryption("new", Map.of("new", next))) {
      var encrypted = original.encrypt(aad(), secret);
      assertArrayEquals(secret, rolling.decrypt(encrypted, aad()));
      var rewrapped = rolling.reencrypt(encrypted, aad());
      assertEquals("new", rewrapped.path("kek_id").asString());
      assertArrayEquals(secret, completed.decrypt(rewrapped, aad()));
      assertThrows(IllegalArgumentException.class, () -> completed.decrypt(encrypted, aad()));
      assertNotEquals(encrypted.path("nonce"), original.encrypt(aad(), secret).path("nonce"));
    }
  }

  @Test
  void rejectsAadSwapEvenWhenBothClaimedAndExpectedAadAreChanged() {
    byte[] secret = KeyTestMaterial.pair().getPrivate().getEncoded();
    try (var encryption = new MsgnetKeyEncryption("current", Map.of("current", kek()))) {
      var encrypted = encryption.encrypt(aad(), secret);
      for (var entry :
          Map.of(
                  "tenant_id",
                  "other",
                  "realm",
                  "3",
                  "kid",
                  "other",
                  "alg",
                  "X25519",
                  "purpose",
                  "business_delivery",
                  "component",
                  "collab")
              .entrySet()) {
        var changed = aad().put(entry.getKey(), entry.getValue());
        assertThrows(IllegalArgumentException.class, () -> encryption.decrypt(encrypted, changed));
        var forged = encrypted.deepCopy();
        forged.set("aad", changed);
        assertThrows(IllegalArgumentException.class, () -> encryption.decrypt(forged, changed));
      }
      var padded = encrypted.deepCopy();
      padded.put("ciphertext", padded.path("ciphertext").asString() + "=");
      assertThrows(IllegalArgumentException.class, () -> encryption.decrypt(padded, aad()));
      var unknown = encrypted.deepCopy().put("unrecognized", true);
      assertThrows(IllegalArgumentException.class, () -> encryption.decrypt(unknown, aad()));
      var changedCipher = encrypted.deepCopy().put("ciphertext", "AAAA");
      assertThrows(IllegalArgumentException.class, () -> encryption.decrypt(changedCipher, aad()));
    }
  }

  @Test
  void closedRingAndWrongLengthKekReject() {
    assertThrows(
        IllegalArgumentException.class,
        () -> new MsgnetKeyEncryption("current", Map.of("current", new byte[16])));
    var encryption = new MsgnetKeyEncryption("current", Map.of("current", kek()));
    encryption.close();
    assertThrows(
        IllegalStateException.class,
        () -> encryption.encrypt(aad(), KeyTestMaterial.pair().getPrivate().getEncoded()));
  }
}
