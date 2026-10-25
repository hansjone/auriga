package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyPairGenerator;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import org.junit.jupiter.api.Test;

class NetworkKeyRestartTest {
  @Test
  void restartRetainsVersionPublicationAndRevocationWithoutPrivateBytes() throws Exception {
    Path directory = Files.createTempDirectory(Path.of("target"), "network-keys-");
    Path state = directory.resolve("state.json");
    Instant now = Instant.parse("2026-10-25T00:00:00Z");
    Clock clock = Clock.fixed(now, ZoneOffset.UTC);
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    String before;
    try {
      try (var keys = new NetworkSigningKeys(clock, 50, state)) {
        keys.add(
            "2",
            "business_delivery",
            "business",
            pair.getPublic(),
            pair.getPrivate(),
            now,
            now.plusSeconds(86400),
            NetworkSigningKeys.State.ACTIVE);
        keys.publicKeysJson("2");
        keys.revoke("business");
        before = keys.publicKeysJson("2");
        assertThrows(IllegalStateException.class, () -> new NetworkSigningKeys(clock, 1, state));
      }
      try (var restarted =
          new NetworkSigningKeys(Clock.offset(clock, java.time.Duration.ofHours(1)), 1, state)) {
        restarted.add(
            "2",
            "business_delivery",
            "business",
            pair.getPublic(),
            pair.getPrivate(),
            now,
            now.plusSeconds(86400),
            NetworkSigningKeys.State.ACTIVE);
        assertEquals(before, restarted.publicKeysJson("2"));
        assertEquals(
            "revoked", NetworkJson.parse(before).path("keys").get(0).path("state").asString());
        restarted.refreshPublication();
        assertTrue(
            Long.parseLong(
                    NetworkJson.parse(restarted.publicKeysJson("2"))
                        .path("keyset_version")
                        .asString())
                > Long.parseLong(NetworkJson.parse(before).path("keyset_version").asString()));
      }
      assertFalse(
          Files.readString(state)
              .contains(
                  java.util.Base64.getEncoder().encodeToString(pair.getPrivate().getEncoded())));
    } finally {
      Files.deleteIfExists(state);
      Files.deleteIfExists(directory.resolve("state.json.lock"));
      Files.deleteIfExists(directory);
    }
  }
}
