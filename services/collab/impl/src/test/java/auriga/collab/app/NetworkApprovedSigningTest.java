package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.security.KeyPairGenerator;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;

class NetworkApprovedSigningTest {
  @Test
  void guardedHardExpiryNeverInventsAnUnsignedVersionOrChangesPersistedState() throws Exception {
    var directory =
        java.nio.file.Files.createTempDirectory(
            java.nio.file.Path.of("target"), "approved-expiry-");
    var state = directory.resolve("state.json");
    Instant now = Instant.parse("2026-10-25T00:00:00Z");
    Clock clock = Clock.fixed(now, ZoneOffset.UTC);
    try {
      try (var keys =
          new NetworkSigningKeys(clock, 7, state, (registry, work) -> work.get(), 3600)) {
        keys.publicKeysJson("2");
      }
      String before = java.nio.file.Files.readString(state);
      try (var expired =
          new NetworkSigningKeys(
              Clock.offset(clock, java.time.Duration.ofSeconds(3600)),
              1,
              state,
              (registry, work) -> work.get(),
              3600)) {
        assertEquals(
            "common.unavailable",
            assertThrows(AurigaException.class, () -> expired.publicKeysJson("2"))
                .errorCode()
                .code());
      }
      assertEquals(before, java.nio.file.Files.readString(state));
    } finally {
      java.nio.file.Files.deleteIfExists(state);
      java.nio.file.Files.deleteIfExists(directory.resolve("state.json.lock"));
      java.nio.file.Files.deleteIfExists(directory);
    }
  }

  @Test
  void rootApprovalGuardSurroundsRealSignatureAndRejectsAfterRevocation() throws Exception {
    Instant now = Instant.parse("2026-10-25T00:00:00Z");
    Clock clock = Clock.fixed(now, ZoneOffset.UTC);
    var approved = new AtomicBoolean(true);
    int[] signatures = {0};
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    try (var keys =
        new NetworkSigningKeys(
            clock,
            7,
            null,
            (registry, work) -> {
              if (!approved.get()) throw new AurigaException(CommonErrors.UNAVAILABLE);
              assertEquals(
                  "7",
                  NetworkJson.parse(registry.publicKeysJson("2"))
                      .path("keyset_version")
                      .asString());
              var result = work.get();
              signatures[0]++;
              assertTrue(result.path("server_auth").has("signature"));
              return result;
            },
            3600)) {
      keys.add(
          "2",
          "protocol_response",
          "protocol",
          pair.getPublic(),
          pair.getPrivate(),
          now,
          now.plusSeconds(86400),
          NetworkSigningKeys.State.ACTIVE);
      var signer = new NetworkEnvelopeSigner(keys, clock);
      var context =
          new NetworkEnvelopeSigner.Context("2", "64", "incarnation", 1, "request", "nonce");
      var signed =
          signer.sign(
              NetworkJson.object().put("type", "error"),
              "protocol_response",
              context,
              now.plusSeconds(60));
      assertEquals("protocol", signed.path("server_auth").path("kid").asString());
      assertEquals(
          now.plusSeconds(3600).toString(),
          NetworkJson.parse(keys.publicKeysJson("2")).path("valid_until").asString());
      approved.set(false);
      assertEquals(
          "common.unavailable",
          assertThrows(
                  AurigaException.class,
                  () ->
                      signer.sign(
                          NetworkJson.object().put("type", "error"),
                          "protocol_response",
                          context,
                          now.plusSeconds(60)))
              .errorCode()
              .code());
      assertEquals(1, signatures[0]);
    }
  }
}
