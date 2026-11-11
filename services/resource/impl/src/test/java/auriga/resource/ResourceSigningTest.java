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
    assertEquals("revoke", signing.sign(body, true).path("signature").path("kid").asString());
    assertThrows(
        IllegalArgumentException.class,
        () -> ResourceSigning.canonical(json.readTree("{\"n\":0.5}")));
    assertThrows(
        IllegalArgumentException.class,
        () -> new ResourceSigning("same", release.getPrivate(), "same", release.getPrivate()));
  }
}
