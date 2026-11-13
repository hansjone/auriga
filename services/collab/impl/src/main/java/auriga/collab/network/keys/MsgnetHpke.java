package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.PrivateKey;
import java.security.PublicKey;
import java.security.spec.NamedParameterSpec;
import java.security.spec.XECPublicKeySpec;
import java.util.Arrays;
import java.util.Base64;
import javax.crypto.Cipher;
import javax.crypto.KeyAgreement;
import javax.crypto.Mac;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** RFC 9180 base-mode X25519/HKDF-SHA256/AES-256-GCM sealed projection materials. */
public final class MsgnetHpke {
  private static final byte[] KEM = new byte[] {'K', 'E', 'M', 0, 32};
  private static final byte[] SUITE = new byte[] {'H', 'P', 'K', 'E', 0, 32, 0, 1, 0, 2};

  private MsgnetHpke() {}

  public static ObjectNode seal(ObjectNode metadata, JsonNode material, PublicKey receiver) {
    try {
      var sender = KeyPairGenerator.getInstance("X25519").generateKeyPair();
      byte[] enc = raw(sender.getPublic());
      byte[][] schedule = schedule(dh(sender.getPrivate(), receiver), enc, raw(receiver));
      try {
        var result = metadata.deepCopy();
        byte[] ciphertext =
            crypt(
                Cipher.ENCRYPT_MODE,
                schedule,
                NetworkJson.canonical(metadata).getBytes(StandardCharsets.UTF_8),
                NetworkJson.canonical(material, 1048576).getBytes(StandardCharsets.UTF_8));
        result.put("enc", encode(enc)).put("ciphertext", encode(ciphertext));
        schema("SealedMaterial", result, "invalid_chain");
        return result;
      } finally {
        wipe(schedule);
      }
    } catch (java.security.GeneralSecurityException e) {
      throw failure("invalid_chain");
    }
  }

  public static byte[] open(JsonNode sealed, PrivateKey receiver, PublicKey receiverPublic) {
    schema("SealedMaterial", sealed, "invalid_chain");
    try {
      byte[] enc = decode(text(sealed, "enc"));
      require(enc.length == 32, "invalid_chain");
      byte[] reversed = enc.clone();
      for (int l = 0, r = reversed.length - 1; l < r; l++, r--) {
        byte b = reversed[l];
        reversed[l] = reversed[r];
        reversed[r] = b;
      }
      var sender =
          KeyFactory.getInstance("X25519")
              .generatePublic(
                  new XECPublicKeySpec(
                      NamedParameterSpec.X25519, new java.math.BigInteger(1, reversed)));
      var metadata = ((ObjectNode) sealed).deepCopy();
      metadata.remove("enc");
      metadata.remove("ciphertext");
      byte[][] schedule = schedule(dh(receiver, sender), enc, raw(receiverPublic));
      try {
        return crypt(
            Cipher.DECRYPT_MODE,
            schedule,
            NetworkJson.canonical(metadata).getBytes(StandardCharsets.UTF_8),
            decode(text(sealed, "ciphertext")));
      } finally {
        wipe(schedule);
      }
    } catch (java.security.GeneralSecurityException | IllegalArgumentException e) {
      throw failure("invalid_chain");
    }
  }

  private static byte[][] schedule(byte[] dh, byte[] enc, byte[] receiver)
      throws java.security.GeneralSecurityException {
    byte[] eae = extract(KEM, new byte[0], "eae_prk", dh);
    byte[] shared = expand(KEM, eae, "shared_secret", concat(enc, receiver), 32);
    byte[] context =
        concat(
            new byte[] {0},
            extract(SUITE, new byte[0], "psk_id_hash", new byte[0]),
            extract(SUITE, new byte[0], "info_hash", bytes("Auriga.NetworkSealedMaterial.v2")));
    byte[] secret = extract(SUITE, shared, "secret", new byte[0]);
    try {
      return new byte[][] {
        expand(SUITE, secret, "key", context, 32), expand(SUITE, secret, "base_nonce", context, 12)
      };
    } finally {
      wipe(new byte[][] {dh, eae, shared, secret});
    }
  }

  private static byte[] extract(byte[] suite, byte[] salt, String label, byte[] ikm)
      throws java.security.GeneralSecurityException {
    return hmac(
        salt.length == 0 ? new byte[32] : salt, concat(bytes("HPKE-v1"), suite, bytes(label), ikm));
  }

  private static byte[] expand(byte[] suite, byte[] prk, String label, byte[] info, int len)
      throws java.security.GeneralSecurityException {
    byte[] fullInfo =
        concat(
            new byte[] {(byte) (len >> 8), (byte) len},
            bytes("HPKE-v1"),
            suite,
            bytes(label),
            info);
    return Arrays.copyOf(hmac(prk, concat(fullInfo, new byte[] {1})), len);
  }

  private static byte[] hmac(byte[] key, byte[] input)
      throws java.security.GeneralSecurityException {
    var mac = Mac.getInstance("HmacSHA256");
    mac.init(new SecretKeySpec(key, "HmacSHA256"));
    return mac.doFinal(input);
  }

  private static byte[] dh(PrivateKey secret, PublicKey key)
      throws java.security.GeneralSecurityException {
    var agreement = KeyAgreement.getInstance("X25519");
    agreement.init(secret);
    agreement.doPhase(key, true);
    return agreement.generateSecret();
  }

  private static byte[] raw(PublicKey key) {
    byte[] encoded = key.getEncoded();
    return Arrays.copyOfRange(encoded, encoded.length - 32, encoded.length);
  }

  private static byte[] crypt(int mode, byte[][] schedule, byte[] aad, byte[] input)
      throws java.security.GeneralSecurityException {
    var cipher = Cipher.getInstance("AES/GCM/NoPadding");
    cipher.init(
        mode, new SecretKeySpec(schedule[0], "AES"), new GCMParameterSpec(128, schedule[1]));
    cipher.updateAAD(aad);
    return cipher.doFinal(input);
  }

  private static byte[] concat(byte[]... arrays) {
    var out = new java.io.ByteArrayOutputStream();
    for (byte[] array : arrays) out.writeBytes(array);
    return out.toByteArray();
  }

  private static byte[] bytes(String value) {
    return value.getBytes(StandardCharsets.UTF_8);
  }

  private static String encode(byte[] value) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(value);
  }

  private static byte[] decode(String value) {
    byte[] result = Base64.getUrlDecoder().decode(value);
    require(encode(result).equals(value), "invalid_chain");
    return result;
  }

  private static void wipe(byte[][] values) {
    for (byte[] value : values) Arrays.fill(value, (byte) 0);
  }
}
