package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Base64;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class UacCredentialCipherTest {
  @Test
  void rejectsAuthenticatedPayloadTamperingAndEnforcesEnvelopeSize() {
    var cipher = cipher("active", List.of("active"), Map.of("active", key()));
    UUID tenant = UUID.randomUUID(), provider = UUID.randomUUID();
    var encrypted = cipher.encrypt(Secrets.random(), tenant, provider);
    var bytes = Base64.getDecoder().decode(encrypted.path("ciphertext").asString());
    bytes[0] ^= 1;
    encrypted.put("ciphertext", Base64.getEncoder().encodeToString(bytes));
    assertThrows(IllegalStateException.class, () -> cipher.decrypt(encrypted, tenant, provider));
    String boundary = "x".repeat(48 * 1024 - 16);
    assertEquals(
        boundary, cipher.decrypt(cipher.encrypt(boundary, tenant, provider), tenant, provider));
    assertThrows(
        IllegalArgumentException.class, () -> cipher.encrypt(boundary + "x", tenant, provider));
  }

  @Test
  void rotationRetainsOldDecryptionAndReencryptionMovesToActiveKey() {
    String oldKey = key(), newKey = key(), token = Secrets.random();
    UUID tenant = UUID.randomUUID(), provider = UUID.randomUUID();
    var old = cipher("old", List.of("old"), Map.of("old", oldKey));
    var encrypted = old.encrypt(token, tenant, provider);
    var distributed = cipher("old", List.of("old", "new"), Map.of("old", oldKey, "new", newKey));
    assertEquals(token, distributed.decrypt(encrypted, tenant, provider));
    var switched = cipher("new", List.of("old", "new"), Map.of("old", oldKey, "new", newKey));
    assertEquals(token, switched.decrypt(encrypted, tenant, provider));
    var migrated = switched.reencrypt(encrypted, tenant, provider);
    assertEquals("new", migrated.path("keyId").asString());
    var retired = cipher("new", List.of("new"), Map.of("new", newKey));
    assertEquals(token, retired.decrypt(migrated, tenant, provider));
    assertThrows(IllegalStateException.class, () -> retired.decrypt(encrypted, tenant, provider));
    assertFalse(migrated.toString().contains(token));
    assertNotEquals(
        migrated.path("nonce"), switched.encrypt(token, tenant, provider).path("nonce"));
  }

  @Test
  void ciphertextCannotBeSwappedBetweenTenantsProvidersOrKeys() {
    var cipher = cipher("active", List.of("active"), Map.of("active", key()));
    UUID tenant = UUID.randomUUID(), provider = UUID.randomUUID();
    var value = cipher.encrypt(Secrets.random(), tenant, provider);
    assertThrows(
        IllegalStateException.class, () -> cipher.decrypt(value, UUID.randomUUID(), provider));
    assertThrows(
        IllegalStateException.class, () -> cipher.decrypt(value, tenant, UUID.randomUUID()));
    var tampered = value.deepCopy();
    tampered.withObject("/aad").put("tenantId", UUID.randomUUID().toString());
    UUID otherTenant = UUID.fromString(tampered.path("aad").path("tenantId").asString());
    assertThrows(
        IllegalStateException.class, () -> cipher.decrypt(tampered, otherTenant, provider));
    var wrong = cipher("active", List.of("active"), Map.of("active", key()));
    assertThrows(IllegalStateException.class, () -> wrong.decrypt(value, tenant, provider));
  }

  @Test
  void rejectsMissingOrInvalidKeyMaterialWithoutEchoingIt() {
    assertThrows(
        IllegalArgumentException.class,
        () -> cipher("active", List.of("old"), Map.of("old", key())));
    assertThrows(
        IllegalArgumentException.class, () -> cipher("active", List.of("active"), Map.of()));
    assertEquals(
        "Invalid UAC decryption keyring",
        assertThrows(
                IllegalArgumentException.class,
                () ->
                    cipher(
                        "active", List.of("active"), Map.of("active", "private-invalid-material")))
            .getMessage());
  }

  private static UacCredentialCipher cipher(
      String active, List<String> ids, Map<String, String> keys) {
    return new UacCredentialCipher(
        new ContractSchemas(),
        Json.object("activeKeyId", active, "decryptKeyIds", ids, "revision", 1),
        keys);
  }

  private static String key() {
    byte[] raw = new byte[32];
    new java.security.SecureRandom().nextBytes(raw);
    return Base64.getEncoder().encodeToString(raw);
  }
}
