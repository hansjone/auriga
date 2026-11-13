package auriga.iam;

import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
import javax.crypto.Cipher;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Versioned UAC envelope; key material stays outside the public keyring projection. */
final class UacCredentialCipher {
  private static final SecureRandom RANDOM = new SecureRandom();
  private final ContractSchemas schemas;
  private final ObjectNode keyring;
  private final Map<String, SecretKey> keys;

  UacCredentialCipher(ContractSchemas schemas, ObjectNode keyring, Map<String, String> material) {
    schemas.validate("auth/uac-control.schema.json#/$defs/UacDecryptionKeyring", keyring);
    this.schemas = schemas;
    this.keyring = keyring.deepCopy();
    Map<String, SecretKey> decoded = new HashMap<>();
    for (JsonNode keyId : keyring.path("decryptKeyIds")) {
      String id = keyId.asString();
      if (!id.matches("[A-Za-z0-9_-]{1,64}")) throw invalidKeyring();
      try {
        byte[] raw = Base64.getDecoder().decode(material.getOrDefault(id, ""));
        if (raw.length != 32) throw invalidKeyring();
        decoded.put(id, new SecretKeySpec(raw, "AES"));
        java.util.Arrays.fill(raw, (byte) 0);
      } catch (IllegalArgumentException e) {
        throw invalidKeyring();
      }
    }
    if (!decoded.containsKey(keyring.path("activeKeyId").asString())
        || keyring.has("legacyKeyId")
            && !decoded.containsKey(keyring.path("legacyKeyId").asString())) throw invalidKeyring();
    keys = Map.copyOf(decoded);
  }

  ObjectNode encrypt(String plain, UUID tenant, UUID provider) {
    if (plain == null || plain.getBytes(StandardCharsets.UTF_8).length > 48 * 1024 - 16)
      throw new IllegalArgumentException("UAC credential exceeds envelope limit");
    String id = keyring.path("activeKeyId").asString();
    byte[] nonce = new byte[12];
    RANDOM.nextBytes(nonce);
    ObjectNode aad = aad(tenant, provider);
    try {
      Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(Cipher.ENCRYPT_MODE, keys.get(id), new GCMParameterSpec(128, nonce));
      cipher.updateAAD(aad.toString().getBytes(StandardCharsets.UTF_8));
      ObjectNode envelope =
          Json.object(
              "keyId",
              id,
              "algorithm",
              "A256GCM",
              "nonce",
              Base64.getUrlEncoder().withoutPadding().encodeToString(nonce),
              "ciphertext",
              Base64.getEncoder()
                  .encodeToString(cipher.doFinal(plain.getBytes(StandardCharsets.UTF_8))),
              "aad",
              aad);
      schemas.validate("auth/uac-control.schema.json#/$defs/UacCredentialEnvelope", envelope);
      return envelope;
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException("UAC encryption unavailable");
    }
  }

  String decrypt(JsonNode envelope, UUID tenant, UUID provider) {
    schemas.validate("auth/uac-control.schema.json#/$defs/UacCredentialEnvelope", envelope);
    ObjectNode expected = aad(tenant, provider);
    SecretKey key = keys.get(envelope.path("keyId").asString());
    if (key == null || !expected.equals(envelope.path("aad"))) throw invalidCiphertext();
    try {
      Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
      cipher.init(
          Cipher.DECRYPT_MODE,
          key,
          new GCMParameterSpec(
              128, Base64.getUrlDecoder().decode(envelope.path("nonce").asString())));
      cipher.updateAAD(expected.toString().getBytes(StandardCharsets.UTF_8));
      return new String(
          cipher.doFinal(Base64.getDecoder().decode(envelope.path("ciphertext").asString())),
          StandardCharsets.UTF_8);
    } catch (GeneralSecurityException | IllegalArgumentException e) {
      throw invalidCiphertext();
    }
  }

  ObjectNode reencrypt(JsonNode envelope, UUID tenant, UUID provider) {
    return encrypt(decrypt(envelope, tenant, provider), tenant, provider);
  }

  String activeKeyId() {
    return keyring.path("activeKeyId").asString();
  }

  boolean canDecrypt(String id) {
    return id != null && keys.containsKey(id);
  }

  private static ObjectNode aad(UUID tenant, UUID provider) {
    return Json.object(
        "tenantId",
        tenant.toString(),
        "providerId",
        provider.toString(),
        "purpose",
        "uac_credential");
  }

  private static IllegalArgumentException invalidKeyring() {
    return new IllegalArgumentException("Invalid UAC decryption keyring");
  }

  private static IllegalStateException invalidCiphertext() {
    return new IllegalStateException("Invalid encrypted UAC credential");
  }
}
