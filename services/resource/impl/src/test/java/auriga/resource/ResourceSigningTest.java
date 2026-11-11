package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.resource.app.ResourceSigning;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.util.Base64;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;

class ResourceSigningTest {
  @Test
  void shortestTimestampMappingAndLegacySignatureUseTheSameInstant() throws Exception {
    var json = JsonMapper.builder().build();
    var release = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var revoke = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var signing =
        new ResourceSigning("release", release.getPrivate(), "revoke", revoke.getPrivate());
    var vectors = json.createArrayNode();
    byte[] archive = "plugin-timestamp-vector".getBytes(StandardCharsets.UTF_8);
    String digest =
        "sha256:"
            + java.util.HexFormat.of()
                .formatHex(MessageDigest.getInstance("SHA-256").digest(archive));
    for (String time :
        java.util.List.of(
            "2026-11-12T01:02:03.123450Z",
            "2026-11-12T01:02:03.120Z",
            "2026-11-12T01:02:03.000000Z")) {
      var instant = java.time.Instant.parse(time);
      String shortest = ResourceSigning.time(instant);
      assertFalse(shortest.matches(".*\\.\\d*0Z"));
      assertEquals(instant, java.time.Instant.parse(shortest));
      var statement =
          json.createObjectNode()
              .put("purpose", "plugin")
              .put("name", "example")
              .put("version", "1.0.0")
              .put("os", "any")
              .put("arch", "any")
              .put("digest", digest)
              .put("issuedAt", time);
      var envelope =
          json.createObjectNode()
              .put("schemaVersion", 1)
              .put("tenantId", java.util.UUID.randomUUID().toString())
              .put("packageId", java.util.UUID.randomUUID().toString())
              .put("versionId", java.util.UUID.randomUUID().toString())
              .put("releaseSequence", 1)
              .put("notAfter", ResourceSigning.time(instant.plusSeconds(86400)));
      envelope.set("statement", statement);
      var signed = signing.sign(envelope, false);
      var plugin =
          auriga.resource.app.ResourceProtocol.plugin(
              signed, "l3", "https://example.com/", "id", signing);
      // The old protobuf Timestamp is reconstructed by verify.js using its shortest decimal.
      var legacyBody = statement.deepCopy().put("issuedAt", shortest);
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(release.getPublic());
      verifier.update(ResourceSigning.canonical(legacyBody).getBytes(StandardCharsets.UTF_8));
      assertTrue(verifier.verify(plugin.getSigned().getSignature().toByteArray()));
      assertEquals(time, plugin.getResourceEnvelope().getStatement().getIssuedAt());
      var vector =
          json.createObjectNode()
              .put(
                  "publicKey", Base64.getEncoder().encodeToString(release.getPublic().getEncoded()))
              .put("archive", Base64.getEncoder().encodeToString(archive))
              .put(
                  "legacySignature",
                  Base64.getEncoder()
                      .encodeToString(plugin.getSigned().getSignature().toByteArray()));
      vector.set("envelope", signed);
      vector.set("legacyBody", legacyBody);
      vectors.add(vector);
    }
    java.nio.file.Files.writeString(
        java.nio.file.Path.of("target/resource-time-vectors.json"),
        vectors.toString(),
        StandardCharsets.UTF_8);
    assertEquals(
        "{\"v\":\"\\u000b\\u001a\"}",
        ResourceSigning.canonical(json.createObjectNode().put("v", "\u000b\u001a")));
  }

  @Test
  void matchesEverySharedCanonicalVectorWithoutNormalizingSignedTimes() throws Exception {
    var json = JsonMapper.builder().build();
    var vectors =
        json.readTree(
            java.nio.file.Files.readAllBytes(
                java.nio.file.Path.of("../../../contracts/resource/signature-vectors.json")));
    for (var vector : vectors.path("cases")) {
      var document = (tools.jackson.databind.node.ObjectNode) vector.path("document").deepCopy();
      document.remove("signature");
      assertEquals(
          vector.path("canonical").asString(),
          ResourceSigning.canonical(document),
          vector.path("name").asString());
    }
    assertThrows(
        IllegalArgumentException.class,
        () -> ResourceSigning.canonical(json.createObjectNode().put("invalid", "\ud800")));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            ResourceSigning.canonical(json.createObjectNode().put("overflow", 9007199254740992L)));
  }

  @Test
  void signsCompleteJcsObjectAndUsesSeparateRevocationKey() throws Exception {
    var release = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var revoke = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var signing =
        new ResourceSigning("release", release.getPrivate(), "revoke", revoke.getPrivate());
    var json = JsonMapper.builder().build();
    var body = json.createObjectNode().put("z", 1).put("a", "line\n");
    assertEquals("{\"a\":\"line\\n\",\"z\":1}", ResourceSigning.canonical(body));
    var signed = signing.sign(body, false);
    var verifier = Signature.getInstance("Ed25519");
    verifier.initVerify(release.getPublic());
    verifier.update(ResourceSigning.canonical(body).getBytes(StandardCharsets.UTF_8));
    assertTrue(
        verifier.verify(
            Base64.getUrlDecoder().decode(signed.path("signature").path("value").asString())));
    var revoked = signing.sign(body, true);
    assertEquals("revoke", revoked.path("signature").path("kid").asString());
    verifier.initVerify(revoke.getPublic());
    verifier.update(ResourceSigning.canonical(body).getBytes(StandardCharsets.UTF_8));
    assertTrue(
        verifier.verify(
            Base64.getUrlDecoder().decode(revoked.path("signature").path("value").asString())));
    assertThrows(
        IllegalArgumentException.class,
        () -> ResourceSigning.canonical(json.readTree("{\"n\":0.5}")));
    assertThrows(
        IllegalArgumentException.class,
        () -> new ResourceSigning("same", release.getPrivate(), "same", release.getPrivate()));
  }
}
