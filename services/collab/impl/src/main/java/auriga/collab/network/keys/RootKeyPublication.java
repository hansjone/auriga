package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import com.networknt.schema.SchemaLocation;
import com.networknt.schema.SchemaRegistry;
import com.networknt.schema.SchemaRegistryConfig;
import com.networknt.schema.SpecificationVersion;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.MessageDigest;
import java.security.PublicKey;
import java.security.Signature;
import java.security.interfaces.EdECPublicKey;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.Base64;
import java.util.HashMap;
import java.util.HashSet;
import java.util.HexFormat;
import java.util.Map;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Verifies installed trust; network input can never add a root or choose a URL. */
public final class RootKeyPublication {
  static final String KEYSET_DOMAIN = "Auriga.NetworkKeyset.v1\0";
  static final String ANNOUNCEMENT_DOMAIN = "Auriga.NetworkKeysetAnnouncement.v1\0";
  private static final SchemaRegistry SCHEMAS = schemas();
  private final String realm;
  private final String rootKid;
  private final PublicKey root;
  private final Clock clock;

  public RootKeyPublication(
      String realm, String rootKid, PublicKey root, String fingerprint, Clock clock) {
    if (realm == null
        || !realm.matches("[1-9][0-9]{0,19}")
        || new java.math.BigInteger(realm).bitLength() > 64
        || rootKid == null
        || !rootKid.matches("[A-Za-z0-9._-]{1,128}")
        || !fingerprint(root).equals(fingerprint)) throw invalid();
    this.realm = realm;
    this.rootKid = rootKid;
    this.root = root;
    this.clock = java.util.Objects.requireNonNull(clock);
  }

  /** Canonical strings are the exact persisted/served bytes; no expiry is recomputed. */
  public record Release(String signedKeyset, String announcement) {}

  public record Verified(Release release, JsonNode payload) {}

  public Verified verify(String signedJson, String announcementJson) {
    return verify(signedJson, announcementJson, true);
  }

  Verified verifyArchived(String signedJson, String announcementJson) {
    return verify(signedJson, announcementJson, false);
  }

  private Verified verify(String signedJson, String announcementJson, boolean current) {
    try {
      var signed = NetworkJson.parse(signedJson);
      validate("keyset", "SignedKeyset", signed);
      var payload = signed.path("keyset");
      checkPayload(payload);
      var auth = signed.path("root_auth");
      if (!rootKid.equals(auth.path("kid").asString())
          || !realm.equals(payload.path("realm").asString())) throw invalid();
      verifySignature(KEYSET_DOMAIN, payload, auth.path("signature").asString());
      Instant issued = Instant.parse(payload.path("issued_at").asString());
      Instant until = Instant.parse(payload.path("valid_until").asString());
      if (current && (clock.instant().isBefore(issued) || !clock.instant().isBefore(until)))
        throw invalid();
      for (var key : payload.path("keys")) {
        if (java.util.Arrays.equals(
            root.getEncoded(), publicKey(key.path("public_key_spki").asString()).getEncoded()))
          throw invalid();
      }
      var announcement = NetworkJson.parse(announcementJson);
      validate("protocol", "KeysetAnnouncement", announcement);
      if (!rootKid.equals(announcement.path("root_kid").asString())
          || !payload.path("keyset_version").equals(announcement.path("keyset_version"))
          || !payload.path("valid_until").equals(announcement.path("valid_until"))
          || !NetworkJson.digest(payload).equals(announcement.path("manifest_digest").asString()))
        throw invalid();
      var body = ((ObjectNode) announcement).deepCopy();
      body.remove("root_signature");
      verifySignature(ANNOUNCEMENT_DOMAIN, body, announcement.path("root_signature").asString());
      return new Verified(
          new Release(NetworkJson.canonical(signed), NetworkJson.canonical(announcement)),
          payload.deepCopy());
    } catch (RuntimeException e) {
      throw invalid();
    }
  }

  static void checkPayload(JsonNode payload) {
    validate("keyset", "KeysetPayload", payload);
    Instant issued = Instant.parse(payload.path("issued_at").asString());
    Instant until = Instant.parse(payload.path("valid_until").asString());
    if (!until.isAfter(issued) || until.isAfter(issued.plusSeconds(86400))) throw invalid();
    var kids = new HashSet<String>();
    var material = new HashSet<String>();
    var active = new HashSet<String>();
    for (var key : payload.path("keys")) {
      if (!kids.add(key.path("kid").asString())
          || !material.add(key.path("public_key_spki").asString())
          || !Instant.parse(key.path("not_after").asString())
              .isAfter(Instant.parse(key.path("not_before").asString()))) throw invalid();
      publicKey(key.path("public_key_spki").asString());
      if ("active".equals(key.path("state").asString())
          && !active.add(key.path("purpose").asString())) throw invalid();
    }
  }

  private void verifySignature(String domain, JsonNode body, String encoded) {
    try {
      byte[] bytes = Base64.getUrlDecoder().decode(encoded);
      if (bytes.length != 64
          || !Base64.getUrlEncoder().withoutPadding().encodeToString(bytes).equals(encoded))
        throw invalid();
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(root);
      verifier.update((domain + NetworkJson.canonical(body)).getBytes(StandardCharsets.UTF_8));
      if (!verifier.verify(bytes)) throw invalid();
    } catch (java.security.GeneralSecurityException e) {
      throw invalid();
    }
  }

  public static PublicKey publicKey(String encoded) {
    try {
      byte[] bytes = Base64.getUrlDecoder().decode(encoded);
      var key = KeyFactory.getInstance("Ed25519").generatePublic(new X509EncodedKeySpec(bytes));
      if (!(key instanceof EdECPublicKey ed)
          || !"Ed25519".equals(ed.getParams().getName())
          || !Base64.getUrlEncoder()
              .withoutPadding()
              .encodeToString(key.getEncoded())
              .equals(encoded)) throw invalid();
      return key;
    } catch (java.security.GeneralSecurityException e) {
      throw invalid();
    }
  }

  public static String fingerprint(PublicKey key) {
    try {
      if (!(key instanceof EdECPublicKey ed) || !"Ed25519".equals(ed.getParams().getName()))
        throw invalid();
      return "sha256:"
          + HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(key.getEncoded()));
    } catch (java.security.GeneralSecurityException e) {
      throw invalid();
    }
  }

  private static void validate(String file, String definition, JsonNode value) {
    if (!SCHEMAS
        .getSchema(
            SchemaLocation.of(
                "https://auriga.example.com/contracts/network/"
                    + file
                    + ".schema.json#/$defs/"
                    + definition))
        .validate(value)
        .isEmpty()) throw invalid();
  }

  private static SchemaRegistry schemas() {
    Map<String, String> definitions = new HashMap<>();
    for (String file : new String[] {"keyset", "envelope", "protocol", "address"}) {
      String path = "auriga/contracts/network/" + file + ".schema.json";
      try (var input = RootKeyPublication.class.getClassLoader().getResourceAsStream(path)) {
        if (input == null) throw invalid();
        String json = new String(input.readAllBytes(), StandardCharsets.UTF_8);
        definitions.put(
            "https://auriga.example.com/contracts/network/" + file + ".schema.json", json);
      } catch (java.io.IOException e) {
        throw invalid();
      }
    }
    return SchemaRegistry.withDefaultDialect(
        SpecificationVersion.DRAFT_2020_12,
        b ->
            b.schemaRegistryConfig(
                    SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build())
                .schemas(definitions));
  }

  static IllegalArgumentException invalid() {
    return new IllegalArgumentException("Invalid network root publication");
  }
}
