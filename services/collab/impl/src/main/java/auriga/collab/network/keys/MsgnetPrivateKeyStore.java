package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.PrivateKey;
import java.security.Signature;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.util.Arrays;
import java.util.Base64;
import java.util.UUID;
import java.util.function.Supplier;
import javax.crypto.KeyAgreement;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;

/**
 * Internal collab repository. No transport/controller may expose this unrestricted decrypt port.
 */
public final class MsgnetPrivateKeyStore {
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final UUID tenant;
  private final String realm;
  private final MsgnetKeyEncryption encryption;

  public MsgnetPrivateKeyStore(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      UUID tenant,
      String realm,
      MsgnetKeyEncryption encryption) {
    this.jdbc = java.util.Objects.requireNonNull(jdbc);
    this.tx = new TransactionTemplate(manager);
    this.tenant = java.util.Objects.requireNonNull(tenant);
    this.realm = java.util.Objects.requireNonNull(realm);
    this.encryption = java.util.Objects.requireNonNull(encryption);
  }

  /**
   * Joins the caller's publication transaction. A retry keeps the originally committed ciphertext.
   */
  public void install(JsonNode aad, String publicSpki, byte[] pkcs8) {
    scope(aad);
    match(publicSpki, pkcs8, aad.path("alg").asString());
    run(
        () -> {
          lock();
          var previous = find(aad);
          if (previous != null) {
            if (!previous.aad().equals(NetworkJson.canonical(aad))
                || !previous.spki().equals(publicSpki)) throw invalid();
            byte[] existing = encryption.decrypt(NetworkJson.parse(previous.encrypted()), aad);
            try {
              if (!java.security.MessageDigest.isEqual(existing, pkcs8)) throw invalid();
            } finally {
              Arrays.fill(existing, (byte) 0);
            }
            return null;
          }
          var encrypted = encryption.encrypt(aad, pkcs8);
          jdbc.update(
              """
          INSERT INTO collab.msgnet_private_key
            (id, tenant_id, realm, kid, aad, public_key_spki, encrypted_key, kek_id)
          VALUES (?, ?, ?, ?, ?, ?, ?, ?)
          """,
              UuidV7.next(Clock.systemUTC()),
              tenant,
              realm,
              aad.path("kid").asString(),
              NetworkJson.canonical(aad),
              publicSpki,
              NetworkJson.canonical(encrypted),
              encrypted.path("kek_id").asString());
          return null;
        });
  }

  /** The caller must authorize the complete expected AAD before entering the repository. */
  public byte[] read(JsonNode expectedAad) {
    scope(expectedAad);
    return run(
        () -> {
          var row = find(expectedAad);
          if (row == null || !row.aad().equals(NetworkJson.canonical(expectedAad))) throw invalid();
          byte[] plaintext = encryption.decrypt(NetworkJson.parse(row.encrypted()), expectedAad);
          try {
            match(row.spki(), plaintext, expectedAad.path("alg").asString());
            return plaintext;
          } catch (RuntimeException e) {
            Arrays.fill(plaintext, (byte) 0);
            throw e;
          }
        });
  }

  /** One row per short transaction, so interrupted whole-ring rotation is safely resumable. */
  public void rewrap(JsonNode expectedAad) {
    scope(expectedAad);
    run(
        () -> {
          lock();
          var row = find(expectedAad);
          if (row == null || !row.aad().equals(NetworkJson.canonical(expectedAad))) throw invalid();
          var replacement = encryption.reencrypt(NetworkJson.parse(row.encrypted()), expectedAad);
          jdbc.update(
              """
          UPDATE collab.msgnet_private_key SET encrypted_key = ?, kek_id = ?
          WHERE tenant_id = ? AND realm = ? AND kid = ?
          """,
              NetworkJson.canonical(replacement),
              replacement.path("kek_id").asString(),
              tenant,
              realm,
              expectedAad.path("kid").asString());
          return null;
        });
  }

  private void scope(JsonNode aad) {
    MsgnetSchema.validate("PrivateKeyAad", aad);
    if (!tenant.toString().equals(aad.path("tenant_id").asString())
        || !realm.equals(aad.path("realm").asString())) throw invalid();
  }

  private void lock() {
    jdbc.queryForObject(
        "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
        Object.class,
        tenant + ":msgnet-private-keys:" + realm);
  }

  private Stored find(JsonNode aad) {
    var rows =
        jdbc.query(
            """
        SELECT aad, public_key_spki, encrypted_key FROM collab.msgnet_private_key
        WHERE tenant_id = ? AND realm = ? AND kid = ?
        """,
            (rs, index) -> new Stored(rs.getString(1), rs.getString(2), rs.getString(3)),
            tenant,
            realm,
            aad.path("kid").asString());
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private <T> T run(Supplier<T> operation) {
    return TenantContext.call(
        tenant,
        () ->
            tx.execute(
                status -> {
                  TenantTransactionManager.verifyTransactionTenant();
                  return operation.get();
                }));
  }

  private static void match(String publicSpki, byte[] pkcs8, String algorithm) {
    if (publicSpki == null
        || publicSpki.length() > 2048
        || pkcs8 == null
        || pkcs8.length == 0
        || pkcs8.length > 16384) throw invalid();
    try {
      var factory = KeyFactory.getInstance(algorithm);
      PrivateKey secret = factory.generatePrivate(new PKCS8EncodedKeySpec(pkcs8));
      var publicKey =
          factory.generatePublic(new X509EncodedKeySpec(Base64.getUrlDecoder().decode(publicSpki)));
      if (!Base64.getUrlEncoder()
              .withoutPadding()
              .encodeToString(publicKey.getEncoded())
              .equals(publicSpki)
          || !Arrays.equals(secret.getEncoded(), pkcs8)) throw invalid();
      if ("X25519".equals(algorithm)) {
        var challenge = KeyPairGenerator.getInstance(algorithm).generateKeyPair();
        var actual = KeyAgreement.getInstance(algorithm);
        actual.init(secret);
        actual.doPhase(challenge.getPublic(), true);
        byte[] left = actual.generateSecret();
        var expected = KeyAgreement.getInstance(algorithm);
        expected.init(challenge.getPrivate());
        expected.doPhase(publicKey, true);
        byte[] right = expected.generateSecret();
        try {
          if (!java.security.MessageDigest.isEqual(left, right)) throw invalid();
        } finally {
          Arrays.fill(left, (byte) 0);
          Arrays.fill(right, (byte) 0);
        }
        return;
      }
      var signer = Signature.getInstance(algorithm);
      signer.initSign(secret);
      byte[] challenge =
          "Auriga.MsgnetPrivateKeyMatch.v2".getBytes(java.nio.charset.StandardCharsets.UTF_8);
      signer.update(challenge);
      byte[] proof = signer.sign();
      var verifier = Signature.getInstance(algorithm);
      verifier.initVerify(publicKey);
      verifier.update(challenge);
      if (!verifier.verify(proof)) throw invalid();
    } catch (java.security.GeneralSecurityException e) {
      throw invalid();
    }
  }

  private static IllegalArgumentException invalid() {
    return new IllegalArgumentException("Invalid msgnet private material binding");
  }

  private record Stored(String aad, String spki, String encrypted) {}
}
