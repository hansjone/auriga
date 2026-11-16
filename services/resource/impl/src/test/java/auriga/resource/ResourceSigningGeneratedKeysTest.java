package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.resource.app.ResourceSigning;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.*;
import java.security.spec.X509EncodedKeySpec;
import java.util.Base64;
import java.util.HashMap;
import java.util.List;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import tools.jackson.databind.json.JsonMapper;

class ResourceSigningGeneratedKeysTest {
  @TempDir Path temporary;

  @Test
  void loadsGeneratedPkcs8KeysAndVerifiesWithEmittedSpkiRoots() throws Exception {
    Path repository = Path.of("../../..").toRealPath();
    Path node =
        repository
            .resolve("services/contracts/target/node")
            .resolve(System.getProperty("os.name").startsWith("Windows") ? "node.exe" : "node");
    assertTrue(Files.isRegularFile(node), "Contract build must provide the pinned Node runtime");
    Path output = temporary.toRealPath().resolve("generated keys");
    Path log = temporary.resolve("generator.log");
    Process child =
        new ProcessBuilder(
                node.toString(),
                repository.resolve("scripts/resource-keys/generate.mjs").toString(),
                "--out",
                output.toString(),
                "--kid-prefix",
                "java-test")
            .redirectErrorStream(true)
            .redirectOutput(log.toFile())
            .start();
    try {
      assertTrue(child.waitFor(30, TimeUnit.SECONDS), "Resource key generator timed out");
      assertEquals(0, child.exitValue(), Files.readString(log));
    } finally {
      if (child.isAlive()) {
        child.descendants().forEach(ProcessHandle::destroyForcibly);
        child.destroyForcibly();
        assertTrue(child.waitFor(10, TimeUnit.SECONDS));
      }
    }

    var json = JsonMapper.builder().build();
    var roots = json.readTree(Files.readAllBytes(output.resolve("resource-roots.json")));
    var privateKeys = new HashMap<String, PrivateKey>();
    var publicKeys = new HashMap<String, PublicKey>();
    for (String role : List.of("release", "revocation", "downgrade")) {
      privateKeys.put(
          role,
          ResourceSigning.load(
              output
                  .resolve(role.equals("downgrade") ? "offline" : "platform")
                  .resolve(role + ".pk8")));
      publicKeys.put(
          role,
          KeyFactory.getInstance("Ed25519")
              .generatePublic(
                  new X509EncodedKeySpec(
                      Base64.getDecoder()
                          .decode(roots.path(role).path("publicKeySpkiBase64").asString()))));
    }
    var signing =
        new ResourceSigning(
            roots.path("release").path("kid").asString(), privateKeys.get("release"),
            roots.path("revocation").path("kid").asString(), privateKeys.get("revocation"));
    var body = json.createObjectNode().put("check", "generated-pkcs8");
    byte[] message = ResourceSigning.canonical(body).getBytes(StandardCharsets.UTF_8);
    for (String role : List.of("release", "revocation")) {
      var signed = signing.sign(body, role.equals("revocation"));
      assertEquals(
          roots.path(role).path("kid").asString(), signed.path("signature").path("kid").asString());
      byte[] signature =
          Base64.getUrlDecoder().decode(signed.path("signature").path("value").asString());
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(publicKeys.get(role));
      verifier.update(message);
      assertTrue(verifier.verify(signature), role);
      verifier.initVerify(publicKeys.get("downgrade"));
      verifier.update(message);
      assertFalse(verifier.verify(signature), "Another purpose must not verify " + role);
    }
    var downgrade = Signature.getInstance("Ed25519");
    downgrade.initSign(privateKeys.get("downgrade"));
    downgrade.update(message);
    byte[] signature = downgrade.sign();
    downgrade.initVerify(publicKeys.get("downgrade"));
    downgrade.update(message);
    assertTrue(downgrade.verify(signature));
  }
}
