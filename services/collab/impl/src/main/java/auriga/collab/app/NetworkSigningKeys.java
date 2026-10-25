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
public final class NetworkSigningKeys implements NetworkPublicKeys, AutoCloseable {
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
  private final NetworkKeyPublication persistence;
  private boolean closed;

  /**
   * In-memory registry for explicit test/configuration use; production uses the state-file
   * overload.
   */
  public NetworkSigningKeys(Clock clock, long initialVersion) {
    this(clock, initialVersion, null);
  }

  public NetworkSigningKeys(Clock clock, long initialVersion, Path metadataFile) {
    if (initialVersion < 1) throw new IllegalArgumentException("Invalid keyset version");
    this.clock = java.util.Objects.requireNonNull(clock);
    this.version = initialVersion;
    this.publicationTime = clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    persistence = metadataFile == null ? null : new NetworkKeyPublication(metadataFile);
    if (persistence != null) {
      try {
        restore(persistence.read());
      } catch (RuntimeException e) {
        persistence.close();
        throw new IllegalStateException("Invalid network key metadata");
      }
    }
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
    ensureOpen();
    Key restored = keys.get(kid);
    if (!uint64(realm)
        || "0".equals(realm)
        || !PURPOSES.contains(purpose)
        || kid == null
        || !kid.matches("[A-Za-z0-9._-]{1,128}")
        || (restored != null && persistence == null)
        || (restored == null && keys.size() >= 128)
        || publicKey == null
        || notBefore == null
        || notAfter == null
        || !notAfter.isAfter(notBefore)
        || state == null
        || ((state == State.ACTIVE || state == State.NEXT) && privateKey == null)) {
      throw new IllegalArgumentException("Invalid network signing key configuration");
    }
    if (restored == null
        && state == State.ACTIVE
        && keys.values().stream()
            .anyMatch(
                k ->
                    k.realm.equals(realm)
                        && k.purpose.equals(purpose)
                        && k.state == State.ACTIVE)) {
      throw new IllegalArgumentException("Ambiguous active signing key");
    }
    if (restored == null
        && keys.values().stream()
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
    if (restored != null) {
      if (!restored.realm.equals(realm)
          || !restored.purpose.equals(purpose)
          || !Arrays.equals(restored.publicKey.getEncoded(), publicKey.getEncoded())
          || !restored.notBefore.equals(notBefore)
          || !restored.notAfter.equals(notAfter))
        throw new IllegalArgumentException(
            "Signing key identifier cannot change material or scope");
      restored.privateKey =
          restored.state == State.ACTIVE || restored.state == State.NEXT ? privateKey : null;
      return;
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
    persist();
  }

  /** Atomic rotation; retired/revoked identifiers can never be reactivated or reused. */
  public synchronized void activate(String kid) {
    ensureOpen();
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
    persist();
  }

  public synchronized void revoke(String kid) {
    ensureOpen();
    Key key = requireKey(kid);
    if (key.state == State.REVOKED) return;
    bumpVersion();
    key.state = State.REVOKED;
    key.privateKey = null;
    persist();
  }

  public synchronized PublicKey publicKey(String kid) {
    return requireKey(kid).publicKey;
  }

  @Override
  public synchronized String publicKeysJson(String realm) {
    ensureOpen();
    markPublished();
    var out = NetworkJson.object();
    out.put("schema_version", 1).put("realm", realm).put("keyset_version", Long.toString(version));
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
          .put(
              "not_before",
              key.notBefore.truncatedTo(java.time.temporal.ChronoUnit.SECONDS).toString())
          .put(
              "not_after",
              key.notAfter.truncatedTo(java.time.temporal.ChronoUnit.SECONDS).toString())
          .put(
              "public_key_spki",
              Base64.getUrlEncoder().withoutPadding().encodeToString(key.publicKey.getEncoded()));
    }
    NetworkSchemas.validate("keyset", "KeysetPayload", out);
    return NetworkJson.canonical(out);
  }

  /** Signing and rotation share this monitor: revoke cannot race a later signature. */
  synchronized ObjectNode sign(
      String realm,
      String purpose,
      Instant now,
      java.util.function.BiFunction<Key, Long, ObjectNode> work) {
    ensureOpen();
    markPublished();
    Key key =
        keys.values().stream()
            .filter(
                k ->
                    k.realm.equals(realm)
                        && k.purpose.equals(purpose)
                        && k.state == State.ACTIVE
                        && k.privateKey != null
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
    ensureOpen();
    bumpVersion();
    persist();
  }

  private void markPublished() {
    if (!clock.instant().isBefore(publicationTime.plusSeconds(86400))) {
      bumpVersion();
      persist();
    }
    if (!published) {
      published = true;
      persist();
    }
  }

  private void persist() {
    if (persistence == null) return;
    var snapshot =
        NetworkJson.object()
            .put("version", Long.toString(version))
            .put("issued_at", publicationTime.toString());
    var entries = snapshot.putArray("keys");
    for (var key : keys.values())
      entries
          .addObject()
          .put("kid", key.kid)
          .put("realm", key.realm)
          .put("purpose", key.purpose)
          .put("state", key.state.name())
          .put("not_before", key.notBefore.toString())
          .put("not_after", key.notAfter.toString())
          .put(
              "public_key_spki",
              Base64.getUrlEncoder().withoutPadding().encodeToString(key.publicKey.getEncoded()));
    try {
      persistence.save(snapshot);
    } catch (RuntimeException e) {
      close();
      throw new IllegalStateException("Network key publication failed closed");
    }
  }

  private void restore(ObjectNode snapshot) {
    if (snapshot == null) return;
    version = Long.parseLong(snapshot.path("version").asString());
    if (version < 1) throw new IllegalArgumentException("Invalid stored keyset version");
    publicationTime = Instant.parse(snapshot.path("issued_at").asString());
    if (snapshot.path("keys").size() > 128)
      throw new IllegalArgumentException("Too many stored keys");
    try {
      var factory = KeyFactory.getInstance("Ed25519");
      for (var entry : snapshot.path("keys")) {
        String kid = entry.path("kid").asString();
        var key =
            new Key(
                entry.path("realm").asString(),
                entry.path("purpose").asString(),
                kid,
                factory.generatePublic(
                    new X509EncodedKeySpec(
                        Base64.getUrlDecoder().decode(entry.path("public_key_spki").asString()))),
                null,
                Instant.parse(entry.path("not_before").asString()),
                Instant.parse(entry.path("not_after").asString()),
                State.valueOf(entry.path("state").asString()));
        if (keys.putIfAbsent(kid, key) != null)
          throw new IllegalArgumentException("Duplicate stored key identifier");
      }
      published = true; // A restart cannot prove an older version was never observed.
    } catch (GeneralSecurityException e) {
      throw new IllegalArgumentException("Invalid stored public key");
    }
  }

  private void ensureOpen() {
    if (closed) throw new IllegalStateException("Network signing key registry is closed");
  }

  @Override
  public synchronized void close() {
    if (closed) return;
    closed = true;
    keys.values().forEach(k -> k.privateKey = null);
    if (persistence != null) persistence.close();
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
