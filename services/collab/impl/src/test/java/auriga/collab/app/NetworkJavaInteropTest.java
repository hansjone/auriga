package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.MessageDigest;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.util.Base64;
import java.util.HexFormat;
import java.util.List;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class NetworkJavaInteropTest {
  @Test
  void realJavaSignerProducesSharedSamplesAndNodeVerifiesThem() throws Exception {
    var vectors = NetworkEnvelopeSigningTest.fixture("signature-vectors.json");
    var clock = NetworkEnvelopeSigningTest.CLOCK;
    var now = clock.instant();
    try (var keys = new NetworkSigningKeys(clock, 5)) {
      var factory = KeyFactory.getInstance("Ed25519");
      for (var key : vectors.path("keys")) {
        byte[] seed =
            MessageDigest.getInstance("SHA-256")
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
            now.minusSeconds(86400),
            now.plusSeconds(86400),
            NetworkSigningKeys.State.ACTIVE);
      }
      var signer = new NetworkEnvelopeSigner(keys, clock);
      var samples = NetworkJson.object().put("test_only", true);
      samples
          .putObject("context")
          .put("realm", "2")
          .put("node", "64")
          .put("incarnation", "inc-64-1")
          .put("epoch", "42")
          .put("channelNonce", "nonce-ready-0001")
          .put("now", now.plusSeconds(60).toString());
      var frames = samples.putObject("frames");
      for (var vector : vectors.path("vectors")) {
        var original = (ObjectNode) NetworkJson.parse(vector.path("canonical").asString());
        var auth = original.path("server_auth");
        var context =
            new NetworkEnvelopeSigner.Context(
                "2",
                "64",
                "inc-64-1",
                42,
                "nonce-ready-0001",
                auth.path("request_id").isNull() ? null : auth.path("request_id").asString());
        var signed =
            signer.sign(original, auth.path("purpose").asString(), context, now.plusSeconds(300));
        assertEquals(
            vector.path("signature").asString(),
            signed.path("server_auth").path("signature").asString());
        frames.set(vector.path("name").asString(), signed);
      }
      var context =
          new NetworkEnvelopeSigner.Context(
              "2", "64", "inc-64-1", 42, "nonce-ready-0001", "req-fetch-0001");
      var envelopeFactory = new NetworkMailboxEnvelopeFactory(signer);
      var page =
          envelopeFactory.page(
              List.of(
                  (ObjectNode) frames.path("delivery_first_contact"),
                  (ObjectNode) frames.path("delivery_broadcast")),
              new NetworkMailboxService.NodeRoute("2", "64", 42, 1207, 1204),
              1207,
              context,
              now.plusSeconds(300));
      frames.set("fetch_result", page);
      samples.set("keyset", NetworkJson.parse(keys.publicKeysJson("2")));
      var snapshot =
          Path.of("../../../contracts/network/fixtures/java-signed-samples.json")
              .toAbsolutePath()
              .normalize();
      if (Boolean.getBoolean("auriga.network.export-fixture"))
        Files.writeString(
            snapshot,
            NetworkJson.JSON.writerWithDefaultPrettyPrinter().writeValueAsString(samples) + "\n");
      assertTrue(
          Files.isRegularFile(snapshot),
          "Generate the shared Java sample fixture before running the comparison");
      assertEquals(samples, NetworkJson.JSON.readTree(Files.readString(snapshot)));
      Path log = Path.of("target/node-java-interop.log");
      Path bundledNode =
          Path.of(
              "../../contracts/target/node",
              System.getProperty("os.name").startsWith("Windows") ? "node.exe" : "node");
      String node =
          System.getProperty(
              "auriga.network.node",
              Files.isRegularFile(bundledNode)
                  ? bundledNode.toAbsolutePath().normalize().toString()
                  : "node");
      var process =
          new ProcessBuilder(node, snapshot.resolveSibling("verify-java.mjs").toString())
              .redirectErrorStream(true)
              .redirectOutput(log.toFile())
              .start();
      if (!process.waitFor(30, TimeUnit.SECONDS)) {
        process.destroyForcibly().waitFor(5, TimeUnit.SECONDS);
        fail("Node interop verification timed out");
      }
      assertEquals(0, process.exitValue(), Files.readString(log));
    }
  }
}
