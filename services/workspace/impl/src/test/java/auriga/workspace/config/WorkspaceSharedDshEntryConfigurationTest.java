package auriga.workspace.config;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyPairGenerator;
import java.security.Signature;
import java.util.Base64;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class WorkspaceSharedDshEntryConfigurationTest {
  @TempDir Path directory;

  @Test
  void fixedPkcs8SignerMatchesItsProvisionedPublicKeyAndRejectsOtherInput() throws Exception {
    var keys = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    Path file = directory.resolve("ticket-private.pem");
    Files.writeString(
        file,
        "-----BEGIN PRIVATE KEY-----\n"
            + Base64.getEncoder().encodeToString(keys.getPrivate().getEncoded())
            + "\n-----END PRIVATE KEY-----\n");
    var loaded = WorkspaceSharedDshEntryConfiguration.loadSigningKey(file, "fixture");
    var signature = Signature.getInstance("Ed25519");
    byte[] message =
        "synthetic startup key check".getBytes(java.nio.charset.StandardCharsets.UTF_8);
    signature.initSign(loaded.key());
    signature.update(message);
    byte[] signed = signature.sign();
    signature.initVerify(keys.getPublic());
    signature.update(message);
    assertTrue(signature.verify(signed));
    assertEquals("SigningKey[redacted]", loaded.toString());
    assertThrows(
        IllegalArgumentException.class,
        () -> WorkspaceSharedDshEntryConfiguration.loadSigningKey(directory, "fixture"));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            WorkspaceSharedDshEntryConfiguration.loadSigningKey(
                Path.of("relative.pem"), "fixture"));
    Files.writeString(file, "-----BEGIN PUBLIC KEY-----\nsynthetic\n-----END PUBLIC KEY-----");
    assertThrows(
        IllegalArgumentException.class,
        () -> WorkspaceSharedDshEntryConfiguration.loadSigningKey(file, "fixture"));
  }
}
