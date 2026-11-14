package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.common.error.AurigaException;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.MessageDigest;
import java.security.spec.NamedParameterSpec;
import java.security.spec.XECPrivateKeySpec;
import java.security.spec.XECPublicKeySpec;
import java.util.ArrayList;
import java.util.List;
import javax.crypto.KeyAgreement;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.TestFactory;
import tools.jackson.databind.JsonNode;

class MsgnetProductionVectorsTest {
  @org.junit.jupiter.api.Test
  void hpkeSupportsFullSizeProjectionCiphertext() throws Exception {
    try (var input =
        getClass()
            .getClassLoader()
            .getResourceAsStream("network-fixtures/auto-renewal-v2-vectors.json")) {
      var vectors = KeyTestMaterial.JSON.readTree(input);
      var sample =
          vectors
              .path("boundary_cases")
              .valueStream()
              .filter(v -> "hpke".equals(text(v, "operation")))
              .findFirst()
              .orElseThrow();
      var metadata =
          (tools.jackson.databind.node.ObjectNode) sample.path("input").path("sealed").deepCopy();
      metadata.remove("enc");
      metadata.remove("ciphertext");
      var recipient = java.security.KeyPairGenerator.getInstance("X25519").generateKeyPair();
      var plaintext = KeyTestMaterial.JSON.createObjectNode().put("data", "a".repeat(50000));
      var sealed = MsgnetHpke.seal(metadata, plaintext, recipient.getPublic());
      var encoded = NetworkJson.canonical(sealed, 1048576);
      byte[] clear =
          MsgnetHpke.open(
              NetworkJson.parse(encoded, 1048576), recipient.getPrivate(), recipient.getPublic());
      try {
        assertEquals(
            NetworkJson.canonical(plaintext, 1048576), new String(clear, StandardCharsets.UTF_8));
      } finally {
        java.util.Arrays.fill(clear, (byte) 0);
      }
    }
  }

  @TestFactory
  List<DynamicTest> allConsumerAndBoundaryVectors() throws Exception {
    try (var input =
        getClass()
            .getClassLoader()
            .getResourceAsStream("network-fixtures/auto-renewal-v2-vectors.json")) {
      assertNotNull(input);
      var vectors = KeyTestMaterial.JSON.readTree(input);
      var result = new ArrayList<DynamicTest>();
      for (var vector : vectors.path("cases")) result.add(test(vector, false));
      for (var vector : vectors.path("boundary_cases")) result.add(test(vector, true));
      assertEquals(267, result.size());
      return result;
    }
  }

  private DynamicTest test(JsonNode vector, boolean boundary) {
    return DynamicTest.dynamicTest(
        text(vector, "id"),
        () -> {
          String actual;
          try {
            if (boundary) boundary(vector);
            else consumer(vector.path("input"));
            actual = "accept";
          } catch (AurigaException e) {
            actual = e.errorCode().code().replace("collab.msgnet_", "");
          }
          assertEquals(text(vector, "expected"), actual);
        });
  }

  private static MsgnetV2Verifier verifier(JsonNode context) {
    return new MsgnetV2Verifier(
        text(context, "tenant_id"),
        text(context, "realm"),
        text(context.path("latest_control").path("root_auth"), "kid"),
        RootKeyPublication.publicKey(text(context, "root_spki")));
  }

  private static void consumer(JsonNode i) {
    var verifier = verifier(i);
    var seen =
        new Seen(
            text(i, "seen_control_revision"),
            text(i, "seen_generation"),
            text(i, "seen_version"),
            text(i, "seen_digest"),
            text(i, "previous_digest"),
            list(i.path("first_publication_ledger")));
    verifier.verify(i.path("publication"), i.path("latest_control"), seen, time(i, "now"));
    if (i.has("transport"))
      verifier.verifyTransport(
          i.path("transport"),
          i.path("publication").path("authority"),
          text(i, "seen_bundle_version"),
          text(i, "previous_transport_phase"),
          i.path("readers_persisted").asBoolean(),
          i.path("handshakes_verified").asBoolean(),
          i.path("previous_transport"));
  }

  private static void boundary(JsonNode vector) throws Exception {
    var i = vector.path("input");
    switch (text(vector, "operation")) {
      case "signature_domain" ->
          signature(
              i.path("signed"),
              text(i, "verification_domain"),
              RootKeyPublication.publicKey(text(i, "verification_spki")));
      case "announcement" -> {
        consumer(i.path("context"));
        MsgnetPolicy.announcement(
            i.path("announcement"),
            i.path("context").path("publication"),
            i.path("persisted").asBoolean());
      }
      case "control_receipt" ->
          MsgnetPolicy.receipt(
              i.path("request"),
              i.path("receipt"),
              RootKeyPublication.publicKey(text(i, "identity_spki")),
              text(i, "identity_id"),
              i.path("persisted").asBoolean(),
              i.path("used_challenge").asBoolean(),
              i.path("exact_replay").asBoolean(),
              i.path("identity_compromised").asBoolean(),
              i.path("new_chain_ready").asBoolean());
      case "projection" -> {
        List<String> purposes = new ArrayList<>();
        i.path("exported_purposes").forEach(p -> purposes.add(p.asString()));
        MsgnetPolicy.projection(
            i.path("request"),
            i.path("response"),
            RootKeyPublication.publicKey(text(i, "identity_spki")),
            i.path("used_nonce").asBoolean(),
            purposes);
      }
      case "hpke" -> hpke(i);
      case "migration" ->
          MsgnetPolicy.migration(
              i.path("permit"),
              RootKeyPublication.publicKey(text(i, "root_spki")),
              i.path("members"),
              text(i, "current_version"),
              text(i, "current_digest"),
              i.path("frozen_elapsed_seconds").asLong(),
              i.path("remaining_seconds").asLong(),
              text(i, "first_v2_version"),
              i.path("barrier_committed").asBoolean(),
              i.path("legacy_disabled").asBoolean());
      case "archive" ->
          MsgnetPolicy.archive(
              time(i, "issued_at"),
              time(i, "before"),
              i.path("grace_seconds").asLong(),
              i.path("removed"),
              i.path("tombstones"),
              i.path("reappeared"));
      case "config" -> MsgnetPolicy.config(i.path("config"));
      default -> fail("Unknown frozen boundary operation");
    }
  }

  private static void hpke(JsonNode i) throws Exception {
    // Reconstruct only the explicitly public synthetic recipient from the frozen fixture
    // specification.
    var factory = KeyFactory.getInstance("X25519");
    byte[] seed =
        MessageDigest.getInstance("SHA-256")
            .digest("Auriga synthetic HPKE test only receiver".getBytes(StandardCharsets.UTF_8));
    var secret = factory.generatePrivate(new XECPrivateKeySpec(NamedParameterSpec.X25519, seed));
    var base =
        factory.generatePublic(
            new XECPublicKeySpec(NamedParameterSpec.X25519, java.math.BigInteger.valueOf(9)));
    var agreement = KeyAgreement.getInstance("X25519");
    agreement.init(secret);
    agreement.doPhase(base, true);
    byte[] raw = agreement.generateSecret();
    for (int l = 0, r = raw.length - 1; l < r; l++, r--) {
      byte b = raw[l];
      raw[l] = raw[r];
      raw[r] = b;
    }
    var publicKey =
        factory.generatePublic(
            new XECPublicKeySpec(NamedParameterSpec.X25519, new java.math.BigInteger(1, raw)));
    byte[] clear = MsgnetHpke.open(i.path("sealed"), secret, publicKey);
    try {
      require(
          new String(clear, StandardCharsets.UTF_8)
              .equals(NetworkJson.canonical(i.path("expected_plaintext"))),
          "invalid_chain");
      for (String field :
          List.of(
              "tenant_id",
              "realm",
              "installation_id",
              "component",
              "nonce",
              "generation",
              "keyset_version"))
        require(
            i.path("expected_plaintext").path(field).equals(i.path("sealed").path(field)),
            "scope_denied");
    } finally {
      java.util.Arrays.fill(clear, (byte) 0);
      java.util.Arrays.fill(seed, (byte) 0);
    }
  }
}
