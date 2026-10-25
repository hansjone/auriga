package auriga.collab.app;

import auriga.collab.api.NetworkPublicKeys;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.GeneralSecurityException;
import java.security.KeyFactory;
import java.security.PrivateKey;
import java.security.PublicKey;
import java.security.Signature;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.Arrays;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;
import tools.jackson.databind.node.ObjectNode;

/**
 * Server memory only. Supply keys from a protected secret mount; never persist private material.
 */
public final class NetworkSigningKeys implements NetworkPublicKeys {
  public enum State {
    ACTIVE,
    NEXT,
    RETIRED,
    REVOKED
  }

  private static final Set<String> PURPOSES =
      Set.of("business_delivery", "protocol_response", "management_state");
  private final Map<String, Key> keys = new LinkedHashMap<>();
  private final Clock clock;
  private long version;
  private boolean published;
  private Instant publicationTime;

  public NetworkSigningKeys(Clock clock, long initialVersion) {
    if (initialVersion < 1) throw new IllegalArgumentException("Invalid keyset version");
    this.clock = java.util.Objects.requireNonNull(clock);
    this.version = initialVersion;
    this.publicationTime = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
  }

  /**
   * Load DER PKCS#8 and SPKI files. Paths and private bytes are deliberately excluded from errors.
   */
  public synchronized void addFromFiles(
      String realm,
      String purpose,
      String kid,
      Path publicFile,
      Path privateFile,
      Instant notBefore,
      Instant notAfter,
      State state) {
    byte[] secret = null;
    try {
      var factory = KeyFactory.getInstance("Ed25519");
      PublicKey publicKey =
          factory.generatePublic(new X509EncodedKeySpec(Files.readAllBytes(publicFile)));
      PrivateKey privateKey = null;
      if (privateFile != null) {
        secret = Files.readAllBytes(privateFile);
        privateKey = factory.generatePrivate(new PKCS8EncodedKeySpec(secret));
      }
      add(realm, purpose, kid, publicKey, privateKey, notBefore, notAfter, state);
    } catch (IOException | GeneralSecurityException e) {
      throw new IllegalArgumentException("Cannot load network signing key");
    } finally {
      if (secret != null) Arrays.fill(secret, (byte) 0);
    }
  }

  /** Initial configuration import. Duplicate kid or ambiguous active realm/purpose is rejected. */
  public synchronized void add(
      String realm,
      String purpose,
      String kid,
      PublicKey publicKey,
      PrivateKey privateKey,
      Instant notBefore,
      Instant notAfter,
      State state) {
    if (!uint64(realm)
        || "0".equals(realm)
        || !PURPOSES.contains(purpose)
        || kid == null
        || !kid.matches("[A-Za-z0-9._-]{1,128}")
        || keys.containsKey(kid)
        || keys.size() >= 128
        || publicKey == null
        || notBefore == null
        || notAfter == null
        || !notAfter.isAfter(notBefore)
        || state == null
        || ((state == State.ACTIVE || state == State.NEXT) && privateKey == null)) {
      throw new IllegalArgumentException("Invalid network signing key configuration");
    }
    if (state == State.ACTIVE
        && keys.values().stream()
            .anyMatch(
                k ->
                    k.realm.equals(realm)
                        && k.purpose.equals(purpose)
                        && k.state == State.ACTIVE)) {
      throw new IllegalArgumentException("Ambiguous active signing key");
    }
    if (keys.values().stream()
        .anyMatch(k -> Arrays.equals(k.publicKey.getEncoded(), publicKey.getEncoded()))) {
      throw new IllegalArgumentException(
          "Signing key material must be isolated by realm and purpose");
    }
    try {
      var signature = Signature.getInstance("Ed25519");
      if (!(publicKey instanceof java.security.interfaces.EdECPublicKey ed)
          || !"Ed25519".equals(ed.getParams().getName())) throw new GeneralSecurityException();
      if (privateKey != null) {
        signature.initSign(privateKey);
        signature.update(new byte[] {0});
        byte[] proof = signature.sign();
        signature.initVerify(publicKey);
        signature.update(new byte[] {0});
        if (!signature.verify(proof)) throw new GeneralSecurityException();
      }
    } catch (GeneralSecurityException e) {
      throw new IllegalArgumentException("Invalid Ed25519 key pair");
    }
    if (published) bumpVersion();
    keys.put(
        kid,
        new Key(
            realm,
            purpose,
            kid,
            publicKey,
            state == State.ACTIVE || state == State.NEXT ? privateKey : null,
            notBefore,
            notAfter,
            state));
  }

  /** Atomic rotation; retired/revoked identifiers can never be reactivated or reused. */
  public synchronized void activate(String kid) {
    Key next = requireKey(kid);
    Instant now = clock.instant();
    if (next.state != State.NEXT || now.isBefore(next.notBefore) || !now.isBefore(next.notAfter))
      throw new IllegalArgumentException("Key is not eligible for activation");
    bumpVersion();
    keys.values().stream()
        .filter(
            k ->
                k.realm.equals(next.realm)
                    && k.purpose.equals(next.purpose)
                    && k.state == State.ACTIVE)
        .forEach(
            k -> {
              k.state = State.RETIRED;
              k.privateKey = null;
            });
    next.state = State.ACTIVE;
  }

  public synchronized void revoke(String kid) {
    Key key = requireKey(kid);
    if (key.state == State.REVOKED) return;
    bumpVersion();
    key.state = State.REVOKED;
    key.privateKey = null;
  }

  public synchronized PublicKey publicKey(String kid) {
    return requireKey(kid).publicKey;
  }

  @Override
  public synchronized String publicKeysJson(String realm) {
    published = true;
    var out = NetworkJson.object();
    out.put("realm", realm).put("keyset_version", Long.toString(version));
    Instant issued = publicationTime;
    out.put("issued_at", issued.toString())
        .put("valid_until", issued.plusSeconds(86400).toString());
    var array = out.putArray("keys");
    for (Key key : keys.values()) {
      if (!key.realm.equals(realm)) continue;
      var entry = array.addObject();
      entry
          .put("kid", key.kid)
          .put("alg", "Ed25519")
          .put("purpose", key.purpose)
          .put("state", key.state.name().toLowerCase(java.util.Locale.ROOT))
          .put("not_before", key.notBefore.toString())
          .put("not_after", key.notAfter.toString())
          .put(
              "public_key_spki",
              Base64.getUrlEncoder().withoutPadding().encodeToString(key.publicKey.getEncoded()));
    }
    return NetworkJson.canonical(out);
  }

  /** Signing and rotation share this monitor: revoke cannot race a later signature. */
  synchronized ObjectNode sign(
      String realm,
      String purpose,
      Instant now,
      java.util.function.BiFunction<Key, Long, ObjectNode> work) {
    published = true;
    Key key =
        keys.values().stream()
            .filter(
                k ->
                    k.realm.equals(realm)
                        && k.purpose.equals(purpose)
                        && k.state == State.ACTIVE
                        && !now.isBefore(k.notBefore)
                        && now.isBefore(k.notAfter))
            .findFirst()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAVAILABLE));
    return work.apply(key, version);
  }

  private void bumpVersion() {
    if (version == Long.MAX_VALUE) throw new IllegalStateException("Keyset version exhausted");
    version++;
    publicationTime = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
  }

  /** Explicit new publication; downloading the same version must never extend cache validity. */
  public synchronized void refreshPublication() {
    bumpVersion();
  }

  private Key requireKey(String kid) {
    Key key = keys.get(kid);
    if (key == null) throw new IllegalArgumentException("Unknown signing key identifier");
    return key;
  }

  static boolean uint64(String value) {
    return value != null
        && value.matches("0|[1-9][0-9]{0,19}")
        && (value.length() < 20 || value.compareTo("18446744073709551615") <= 0);
  }

  static final class Key {
    final String realm;
    final String purpose;
    final String kid;
    final PublicKey publicKey;
    PrivateKey privateKey;
    final Instant notBefore;
    final Instant notAfter;
    State state;

    Key(
        String realm,
        String purpose,
        String kid,
        PublicKey publicKey,
        PrivateKey privateKey,
        Instant notBefore,
        Instant notAfter,
        State state) {
      this.realm = realm;
      this.purpose = purpose;
      this.kid = kid;
      this.publicKey = publicKey;
      this.privateKey = privateKey;
      this.notBefore = notBefore;
      this.notAfter = notAfter;
      this.state = state;
    }
  }
}
