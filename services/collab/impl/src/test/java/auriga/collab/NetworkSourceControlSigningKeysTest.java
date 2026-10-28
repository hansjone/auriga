package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkSigningKeys;
import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.util.Arrays;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

/** Exercises the actual PG fixture's key factory without starting its PG extension or servers. */
class NetworkSourceControlSigningKeysTest {
  @Test
  void postgresFixtureCreatesIndependentProtocolAndDeliveryKeyMaterial() throws Exception {
    try (var keys = NetworkSourceControlPostgresTest.signingKeys()) {
      assertFalse(
          Arrays.equals(
              keys.publicKey("source-test").getEncoded(),
              keys.publicKey("send-delivery-test").getEncoded()),
          "each purpose needs an independent Ed25519 pair");
      var entries = NetworkJson.parse(keys.publicKeysJson("2")).path("keys");
      assertEquals(2, entries.size());
      assertEquals("protocol_response", entries.get(0).path("purpose").asString());
      assertEquals("business_delivery", entries.get(1).path("purpose").asString());
    }
  }

  @Test
  void baselineMainProtocolOnlyFixtureStillInitializes() throws Exception {
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    try (var keys = new NetworkSigningKeys(NetworkSourceControlPostgresTest.CLOCK, 1)) {
      assertDoesNotThrow(() -> add(keys, pair, "2", "protocol_response", "source-test"));
      assertEquals(1, NetworkJson.parse(keys.publicKeysJson("2")).path("keys").size());
    }
  }

  @ParameterizedTest
  @CsvSource({"2,business_delivery", "3,protocol_response"})
  void productionGuardRejectsReusedMaterialAcrossRealmOrPurpose(String realm, String purpose)
      throws Exception {
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    try (var keys = new NetworkSigningKeys(NetworkSourceControlPostgresTest.CLOCK, 1)) {
      add(keys, pair, "2", "protocol_response", "source-test");
      var failure =
          assertThrows(
              IllegalArgumentException.class, () -> add(keys, pair, realm, purpose, "other-test"));
      assertEquals(
          "Signing key material must be isolated by realm and purpose", failure.getMessage());
    }
  }

  private static void add(
      NetworkSigningKeys keys, KeyPair pair, String realm, String purpose, String kid) {
    keys.add(
        realm,
        purpose,
        kid,
        pair.getPublic(),
        pair.getPrivate(),
        NetworkSourceControlPostgresTest.now().minusSeconds(30),
        NetworkSourceControlPostgresTest.now().plusSeconds(3600),
        NetworkSigningKeys.State.ACTIVE);
  }
}
