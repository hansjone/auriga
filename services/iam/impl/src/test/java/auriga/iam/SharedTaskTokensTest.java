package auriga.iam;

import static auriga.iam.SharedTaskTokens.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.node.v1.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.mock.env.MockEnvironment;

class SharedTaskTokensTest {
  @Test
  void signsExactCanonicalKClaimsWithTheAudienceKey() throws Exception {
    var generator = KeyPairGenerator.getInstance("Ed25519");
    var enterprise = generator.generateKeyPair();
    var model = generator.generateKeyPair();
    var message = generator.generateKeyPair();
    var tokens =
        new SharedTaskTokens(
            Map.of(
                ENTERPRISE, new SharedTaskTokens.Key("enterprise", enterprise.getPrivate()),
                MODEL, new SharedTaskTokens.Key("model", model.getPrivate()),
                MESSAGE, new SharedTaskTokens.Key("message", message.getPrivate())));
    var capability = capability();
    var parts = tokens.sign(capability).split("\\.");
    assertEquals(3, parts.length);
    var decoder = Base64.getUrlDecoder();
    assertEquals(
        "{\"alg\":\"EdDSA\",\"kid\":\"enterprise\",\"typ\":\"auriga-shared-capability+jwt\"}",
        new String(decoder.decode(parts[0]), StandardCharsets.UTF_8));
    assertArrayEquals(Json.MAPPER.writeValueAsBytes(claims(capability)), decoder.decode(parts[1]));
    var verifier = Signature.getInstance("Ed25519");
    verifier.initVerify(enterprise.getPublic());
    verifier.update((parts[0] + "." + parts[1]).getBytes(StandardCharsets.US_ASCII));
    assertTrue(verifier.verify(decoder.decode(parts[2])));
    verifier.initVerify(model.getPublic());
    verifier.update((parts[0] + "." + parts[1]).getBytes(StandardCharsets.US_ASCII));
    assertFalse(verifier.verify(decoder.decode(parts[2])));
    assertEquals(
        "Key[redacted]",
        new SharedTaskTokens.Key("enterprise", enterprise.getPrivate()).toString());
  }

  @Test
  void rejectsPurposeKeyReuseAndInvalidOrCrossAudienceScopes() throws Exception {
    var key = KeyPairGenerator.getInstance("Ed25519").generateKeyPair().getPrivate();
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new SharedTaskTokens(
                Map.of(
                    ENTERPRISE, new SharedTaskTokens.Key("enterprise", key),
                    MODEL, new SharedTaskTokens.Key("model", key),
                    MESSAGE, new SharedTaskTokens.Key("message", key))));
    assertThrows(
        RuntimeException.class,
        () -> validate(capability().toBuilder().setAudience(MODEL).build()));
    assertThrows(
        RuntimeException.class,
        () -> validate(capability().toBuilder().addScopes("outbound").build()));
    assertThrows(
        RuntimeException.class,
        () -> validate(capability().toBuilder().setExpiresAt(1601).build()));
    assertThrows(
        RuntimeException.class,
        () ->
            validate(
                capability().toBuilder()
                    .setBinding(capability().getBinding().toBuilder().setAuthGeneration(0))
                    .build()));
  }

  @Test
  void runtimeRequiresExplicitBoundedEd25519KeyFilesForEachPurpose(@TempDir Path directory)
      throws Exception {
    var environment = new MockEnvironment();
    var configuration = new SharedTaskRuntimeConfiguration();
    assertThrows(IllegalArgumentException.class, () -> configuration.sharedTaskTokens(environment));
    for (String purpose : List.of("enterprise", "model", "message")) {
      var key = KeyPairGenerator.getInstance("Ed25519").generateKeyPair().getPrivate();
      var path = directory.resolve(purpose + ".pem");
      Files.writeString(
          path,
          String.join(" ", "-----BEGIN", "PRIVATE", "KEY-----")
              + "\n"
              + Base64.getEncoder().encodeToString(key.getEncoded())
              + "\n"
              + String.join(" ", "-----END", "PRIVATE", "KEY-----")
              + "\n");
      environment
          .withProperty("auriga.iam.shared-tasks.signing-keys." + purpose + ".kid", purpose)
          .withProperty(
              "auriga.iam.shared-tasks.signing-keys." + purpose + ".private-key-file",
              path.toString());
    }
    assertNotNull(configuration.sharedTaskTokens(environment));
    Files.writeString(directory.resolve("enterprise.pem"), "synthetic-private-text-".repeat(1000));
    var error =
        assertThrows(
            IllegalArgumentException.class, () -> configuration.sharedTaskTokens(environment));
    assertEquals("Invalid shared task signing configuration", error.getMessage());
    assertNull(error.getCause());
  }

  private static SharedDshCapability capability() {
    var binding =
        SharedDshSessionBinding.newBuilder()
            .setTenantId(UUID.randomUUID().toString())
            .setMemberId(UUID.randomUUID().toString())
            .setPlacementId(UUID.randomUUID().toString())
            .setPhysicalInstanceId(UUID.randomUUID().toString())
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
            .setEpoch(1)
            .setRevision(1)
            .setSessionId(UUID.randomUUID().toString())
            .setPlatformSessionId(UUID.randomUUID().toString())
            .setAuthGeneration(1)
            .build();
    return SharedDshCapability.newBuilder()
        .setSchemaVersion(1)
        .setBinding(binding)
        .setTaskId(UUID.randomUUID().toString())
        .setJti(UUID.randomUUID().toString())
        .setAudience(ENTERPRISE)
        .addScopes("outbound")
        .setCredentialVersion(1)
        .setIssuedAt(1000)
        .setExpiresAt(1600)
        .build();
  }
}
