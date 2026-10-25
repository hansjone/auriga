package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.Signature;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Base64;
import java.util.HexFormat;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class NetworkEnvelopeSigningTest {
  static final Instant NOW = Instant.parse("2026-10-25T00:00:00Z");
  static final Clock CLOCK = Clock.fixed(NOW, ZoneOffset.UTC);

  @Test
  void downloadingOldKeysetDoesNotRenewItsHardDeadline() {
    var time = new java.util.concurrent.atomic.AtomicReference<>(NOW);
    Clock clock =
        new Clock() {
          public java.time.ZoneId getZone() {
            return ZoneOffset.UTC;
          }

          public Clock withZone(java.time.ZoneId zone) {
            return this;
          }

          public Instant instant() {
            return time.get();
          }
        };
    var keys = new NetworkSigningKeys(clock, 9);
    String original = keys.publicKeysJson("2");
    time.set(NOW.plusSeconds(3600));
    assertEquals(original, keys.publicKeysJson("2"));
    time.set(NOW.plusSeconds(86401));
    assertEquals(
        "10", NetworkJson.parse(keys.publicKeysJson("2")).path("keyset_version").asString());
    assertEquals(
        NOW.plusSeconds(86400).toString(),
        NetworkJson.parse(original).path("valid_until").asString());
    keys.refreshPublication();
    assertEquals(
        "11", NetworkJson.parse(keys.publicKeysJson("2")).path("keyset_version").asString());
    assertNotEquals(original, keys.publicKeysJson("2"));
  }

  @Test
  void allAuthoritativeVectorsMatchCanonicalBytesAndSignatures() throws Exception {
    var fixture = fixture("signature-vectors.json");
    var keys = new NetworkSigningKeys(CLOCK, 5);
    for (var key : fixture.path("keys")) {
      var factory = KeyFactory.getInstance("Ed25519");
      // Public fixture seed is derived at runtime; never copy key material into source.
      byte[] seed =
          java.security.MessageDigest.getInstance("SHA-256")
              .digest(key.path("label").asString().getBytes(StandardCharsets.UTF_8));
      byte[] pkcs8 = new byte[48];
      System.arraycopy(
          HexFormat.of().parseHex("302e020100300506032b657004220420"), 0, pkcs8, 0, 16);
      System.arraycopy(seed, 0, pkcs8, 16, 32);
      keys.add(
          "2",
          key.path("purpose").asString(),
          key.path("kid").asString(),
          factory.generatePublic(
              new X509EncodedKeySpec(
                  Base64.getUrlDecoder().decode(key.path("publicKeySpki").asString()))),
          factory.generatePrivate(new PKCS8EncodedKeySpec(pkcs8)),
          NOW.minusSeconds(86400),
          NOW.plusSeconds(86400),
          NetworkSigningKeys.State.ACTIVE);
    }
    var signer = new NetworkEnvelopeSigner(keys, CLOCK);
    for (var vector : fixture.path("vectors")) {
      var unsigned = (ObjectNode) NetworkJson.parse(vector.path("canonical").asString());
      assertEquals(vector.path("canonical").asString(), NetworkJson.canonical(unsigned));
      var auth = unsigned.path("server_auth");
      var signed =
          signer.sign(
              unsigned,
              auth.path("purpose").asString(),
              new NetworkEnvelopeSigner.Context(
                  "2",
                  "64",
                  "inc-64-1",
                  42,
                  "nonce-ready-0001",
                  auth.path("request_id").isNull() ? null : auth.path("request_id").asString()),
              NOW.plusSeconds(300));
      assertEquals(
          vector.path("signature").asString(),
          signed.path("server_auth").path("signature").asString(),
          vector.path("name").asString());
      var key = keys.publicKey(vector.path("kid").asString());
      Signature verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(key);
      verifier.update(NetworkEnvelopeSigner.signingInput(signed));
      assertTrue(
          verifier.verify(
              Base64.getUrlDecoder()
                  .decode(signed.path("server_auth").path("signature").asString())));
      var tampered = signed.deepCopy();
      tampered.put("app_version", "1.1");
      verifier.initVerify(key);
      verifier.update(NetworkEnvelopeSigner.signingInput(tampered));
      assertFalse(
          verifier.verify(
              Base64.getUrlDecoder()
                  .decode(signed.path("server_auth").path("signature").asString())));
      for (String field : signed.propertyNames()) {
        var changed = signed.deepCopy();
        if (field.equals("server_auth")) ((ObjectNode) changed.path(field)).put("kid", "other-key");
        else changed.put(field, "tampered");
        verifier.initVerify(key);
        verifier.update(NetworkEnvelopeSigner.signingInput(changed));
        assertFalse(
            verifier.verify(
                Base64.getUrlDecoder()
                    .decode(signed.path("server_auth").path("signature").asString())),
            field);
      }
    }
    for (var digest : fixture.path("digests")) {
      assertEquals(
          digest.path("digest").asString(),
          NetworkJson.digest(NetworkJson.parse(digest.path("canonical").asString())));
    }
  }

  @Test
  void strictParserRejectsAmbiguousOrUnboundedInput() {
    for (String json :
        new String[] {
          "{\"x\":1,\"x\":2}",
          "{\"x\":1e1}",
          "{\"x\":1.0}",
          "{\"x\":9007199254740992}",
          "{\"x\":NaN}",
          "{\"x\":\"\\ud800\"}",
          "{} {}",
          "[".repeat(17) + "0" + "]".repeat(17),
          "{\"x\":\"" + "a".repeat(16385) + "\"}"
        }) {
      assertEquals(
          "network.protocol_error",
          assertThrows(AurigaException.class, () -> NetworkJson.parse(json)).errorCode().code());
    }
    assertEquals(
        "{\"a\":0,\"b\":\"é\",\"x\":\"é\"}",
        NetworkJson.canonical(NetworkJson.parse("{\"x\":\"é\",\"b\":\"é\",\"a\":-0}")));
    assertThrows(AurigaException.class, () -> NetworkJson.parse(new byte[] {(byte) 0xc3, 0x28}));
  }

  @Test
  void allRawContractCasesRejectAndBoundaryCasesAccept() throws Exception {
    var raw = fixture("raw-cases.json");
    var frames = fixture("frames.json").path("frames");
    for (var c : raw.path("cases")) {
      String value = rawCase(c, frames);
      assertEquals(
          "network.protocol_error",
          assertThrows(
                  AurigaException.class, () -> NetworkJson.parse(value), c.path("name").asString())
              .errorCode()
              .code());
    }
    for (var c : raw.path("accept")) assertNotNull(NetworkJson.parse(rawCase(c, frames)));
  }

  static String rawCase(tools.jackson.databind.JsonNode c, tools.jackson.databind.JsonNode frames) {
    if (c.has("nest"))
      return "[".repeat(c.path("nest").asInt()) + "]".repeat(c.path("nest").asInt());
    if (c.has("members")) {
      var object = NetworkJson.object();
      for (int i = 0; i < c.path("members").asInt(); i++) object.put("k" + i, i);
      return object.toString();
    }
    String value = frames.path(c.path("base").asString()).path("value").toString();
    if (c.has("replace")) {
      String before = c.path("replace").get(0).asString();
      int offset = value.indexOf(before);
      assertTrue(offset >= 0, c.path("name").asString());
      value =
          value.substring(0, offset)
              + c.path("replace").get(1).asString()
              + value.substring(offset + before.length());
    }
    if (c.has("append"))
      value += c.path("append").asString().repeat(c.path("repeatAppend").asInt(1));
    return value;
  }

  static tools.jackson.databind.JsonNode fixture(String file) throws Exception {
    try (var input =
        NetworkEnvelopeSigningTest.class.getResourceAsStream("/network-fixtures/" + file)) {
      assertNotNull(input);
      return NetworkJson.JSON.readTree(input);
    }
  }

  @Test
  void signingClipsExpiryAndIsolatesRealmPurposeAndKeyMaterial() throws Exception {
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var keys = new NetworkSigningKeys(CLOCK, 1);
    keys.add(
        "2",
        "business_delivery",
        "limited",
        pair.getPublic(),
        pair.getPrivate(),
        NOW,
        NOW.plusSeconds(90),
        NetworkSigningKeys.State.ACTIVE);
    var signer = new NetworkEnvelopeSigner(keys, CLOCK);
    var context = new NetworkEnvelopeSigner.Context("2", "64", "inc", 1, "nonce", "request");
    var envelope =
        (ObjectNode)
            NetworkJson.parse("{\"type\":\"delivery\",\"expires_at\":\"2026-10-25T00:01:00Z\"}");
    assertEquals(
        NOW.plusSeconds(60).toString(),
        signer
            .sign(envelope, "business_delivery", context, NOW.plusSeconds(120))
            .path("server_auth")
            .path("not_after")
            .asString());
    assertThrows(
        AurigaException.class,
        () -> signer.sign(envelope, "protocol_response", context, NOW.plusSeconds(120)));
    assertThrows(
        AurigaException.class,
        () ->
            signer.sign(
                envelope,
                "business_delivery",
                new NetworkEnvelopeSigner.Context("3", "64", "inc", 1, "nonce", "request"),
                NOW.plusSeconds(120)));
    assertThrows(
        AurigaException.class, () -> signer.sign(envelope, "business_delivery", context, NOW));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            keys.add(
                "2",
                "protocol_response",
                "reuse",
                pair.getPublic(),
                pair.getPrivate(),
                NOW,
                NOW.plusSeconds(90),
                NetworkSigningKeys.State.ACTIVE));
    keys.revoke("limited");
    assertTrue(keys.publicKeysJson("2").contains("revoked"));
  }

  @Test
  void rotationRetainsOldVerificationKeysButRevocationStopsSigning() throws Exception {
    var generator = KeyPairGenerator.getInstance("Ed25519");
    var first = generator.generateKeyPair();
    var next = generator.generateKeyPair();
    var keys = new NetworkSigningKeys(CLOCK, 1);
    keys.add(
        "2",
        "business_delivery",
        "first",
        first.getPublic(),
        first.getPrivate(),
        NOW.minusSeconds(1),
        NOW.plusSeconds(600),
        NetworkSigningKeys.State.ACTIVE);
    keys.add(
        "2",
        "business_delivery",
        "next",
        next.getPublic(),
        next.getPrivate(),
        NOW.minusSeconds(1),
        NOW.plusSeconds(600),
        NetworkSigningKeys.State.NEXT);
    var signer = new NetworkEnvelopeSigner(keys, CLOCK);
    var context = new NetworkEnvelopeSigner.Context("2", "64", "inc", 1, "nonce", "request");
    var old =
        signer.sign(
            (ObjectNode) NetworkJson.parse("{\"type\":\"delivery\"}"),
            "business_delivery",
            context,
            NOW.plusSeconds(120));
    keys.activate("next");
    assertEquals(
        "retired",
        NetworkJson.parse(keys.publicKeysJson("2")).path("keys").get(0).path("state").asString());
    var fresh =
        signer.sign(
            (ObjectNode) NetworkJson.parse("{\"type\":\"delivery\"}"),
            "business_delivery",
            context,
            NOW.plusSeconds(120));
    assertEquals("next", fresh.path("server_auth").path("kid").asString());
    Signature verifier = Signature.getInstance("Ed25519");
    verifier.initVerify(keys.publicKey("first"));
    verifier.update(NetworkEnvelopeSigner.signingInput(old));
    assertTrue(
        verifier.verify(
            Base64.getUrlDecoder().decode(old.path("server_auth").path("signature").asString())));
    keys.revoke("next");
    assertThrows(
        AurigaException.class,
        () ->
            signer.sign(
                (ObjectNode) NetworkJson.parse("{\"type\":\"delivery\"}"),
                "business_delivery",
                context,
                NOW.plusSeconds(120)));
    assertThrows(IllegalArgumentException.class, () -> keys.activate("first"));
    assertFalse(keys.publicKeysJson("2").contains("PRIVATE"));
    assertFalse(
        keys.publicKeysJson("2")
            .contains(Base64.getEncoder().encodeToString(first.getPrivate().getEncoded())));
  }
}
