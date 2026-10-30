package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import java.security.Signature;
import java.time.Clock;
import java.time.ZoneOffset;
import java.util.Base64;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class NetworkRootPublicationTest {
  final KeyTestMaterial m = new KeyTestMaterial();
  final Clock clock = Clock.fixed(KeyTestMaterial.NOW, ZoneOffset.UTC);

  @Test
  void offlineRootSignsExistingContractAndIndependentAnnouncement() throws Exception {
    var release = m.release(m.payload(5, KeyTestMaterial.NOW));
    var verified = m.verifier(clock).verify(release.signedKeyset(), release.announcement());
    assertEquals("5", verified.payload().path("keyset_version").asString());
    var signed = NetworkJson.parse(release.signedKeyset());
    var proof = Signature.getInstance("Ed25519");
    proof.initVerify(m.root.getPublic());
    proof.update(
        ("Auriga.NetworkKeyset.v1\0" + NetworkJson.canonical(signed.path("keyset")))
            .getBytes(java.nio.charset.StandardCharsets.UTF_8));
    assertTrue(
        proof.verify(
            Base64.getUrlDecoder().decode(signed.path("root_auth").path("signature").asString())));
    assertFalse(
        release
            .signedKeyset()
            .contains(
                Base64.getUrlEncoder()
                    .withoutPadding()
                    .encodeToString(m.root.getPrivate().getEncoded())));
  }

  @Test
  void tamperingAlgorithmsWrongRealmAndUnknownRootsAreRejected() {
    var r = m.release(m.payload(1, KeyTestMaterial.NOW));
    var signed = (ObjectNode) NetworkJson.parse(r.signedKeyset());
    ((ObjectNode) signed.path("keyset")).put("realm", "3");
    assertThrows(
        RuntimeException.class,
        () -> m.verifier(clock).verify(signed.toString(), r.announcement()));
    assertThrows(
        RuntimeException.class,
        () ->
            m.verifier(clock)
                .verify(r.signedKeyset().replace("Ed25519", "none"), r.announcement()));
    assertThrows(
        RuntimeException.class,
        () ->
            m.verifier(clock)
                .verify(
                    r.signedKeyset().replace("\"kid\":\"root\"", "\"kid\":\"unknown\""),
                    r.announcement()));
    assertThrows(
        RuntimeException.class,
        () ->
            m.verifier(clock)
                .verify(r.signedKeyset(), r.announcement().replace("sha256:", "sha256:0")));
    assertThrows(
        RuntimeException.class,
        () ->
            m.verifier(clock)
                .verify(r.signedKeyset().replaceFirst("\\{", "{\"keyset\":{},"), r.announcement()));
  }

  @Test
  void expiredFutureAndExtendedCacheLifetimesAreRejected() {
    var valid = m.release(m.payload(1, KeyTestMaterial.NOW));
    assertThrows(
        RuntimeException.class,
        () ->
            m.verifier(Clock.offset(clock, java.time.Duration.ofDays(1)))
                .verify(valid.signedKeyset(), valid.announcement()));
    var future = m.release(m.payload(2, KeyTestMaterial.NOW.plusSeconds(1)));
    assertThrows(
        RuntimeException.class,
        () -> m.verifier(clock).verify(future.signedKeyset(), future.announcement()));
    var longLived = m.payload(2, KeyTestMaterial.NOW);
    longLived.put("valid_until", KeyTestMaterial.NOW.plusSeconds(86401).toString());
    assertThrows(RuntimeException.class, () -> m.release(longLived));
  }

  @Test
  void fingerprintDuplicateMaterialAndRootAsBusinessKeyAreRejected() {
    assertThrows(
        RuntimeException.class,
        () ->
            new RootKeyPublication(
                "2", "root", m.root.getPublic(), "sha256:" + "0".repeat(64), clock));
    var p = m.payload(1, KeyTestMaterial.NOW);
    ((ObjectNode) p.path("keys").get(0))
        .put(
            "public_key_spki",
            Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(m.root.getPublic().getEncoded()));
    var r = m.release(p);
    assertThrows(
        RuntimeException.class, () -> m.verifier(clock).verify(r.signedKeyset(), r.announcement()));
    var duplicate = m.payload(1, KeyTestMaterial.NOW);
    ((ObjectNode) duplicate.path("keys").get(1))
        .put("public_key_spki", duplicate.path("keys").get(0).path("public_key_spki").asString());
    assertThrows(RuntimeException.class, () -> m.release(duplicate));
  }
}
