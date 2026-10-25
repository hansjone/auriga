package auriga.collab.network.local;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import org.junit.jupiter.api.Test;

class LocalFramesTest {
  static String register(String local, String revision) {
    return """
        {"app_version":"1.0","type":"local_register","request_id":"request","epoch":"1",
         "boot_id":"boot","address_type":"session","candidate_local":"%s","allocation_op_id":"op",
         "allocation_fingerprint":"sha256:%s","expected_registration_revision":"%s"}
        """
        .formatted(local, "a".repeat(64), revision);
  }

  @Test
  void rejectsMissingOrUnknownAddressCategory() {
    error(register("1", "0").replace("\"address_type\":\"session\",", ""));
    for (String type : new String[] {"system", "group", "future", ""})
      error(register("1", "0").replace("session", type));
  }

  @Test
  void acceptsUnsignedMaximumWithoutRounding() {
    var value = LocalFrames.read("LocalRegister", register("18446744073709551615", "0"));
    assertEquals("18446744073709551615", value.path("candidate_local").asString());
  }

  @Test
  void readyPendingAndSignedResultsRequireTheOriginalNarrowCategory() throws Exception {
    var fixtures =
        auriga.collab.app.NetworkJson.parse(
                java.nio.file.Files.readString(
                    java.nio.file.Path.of("../../../contracts/network/fixtures/frames.json")))
            .path("frames");
    for (String name :
        new String[] {
          "ready_pending_registration", "register_result", "register_result_subagent"
        }) {
      var fixture = fixtures.path(name);
      String definition = fixture.path("def").asString().split("#")[1];
      var frame = (tools.jackson.databind.node.ObjectNode) fixture.path("value").deepCopy();
      assertNotNull(LocalFrames.read(definition, frame.toString()));
      var typed =
          frame.has("pending_registration")
              ? (tools.jackson.databind.node.ObjectNode) frame.path("pending_registration")
              : frame;
      typed.remove("address_type");
      assertEquals(
          "network.protocol_error",
          assertThrows(AurigaException.class, () -> LocalFrames.read(definition, frame.toString()))
              .errorCode()
              .code());
      typed.put("address_type", "future");
      assertEquals(
          "network.protocol_error",
          assertThrows(AurigaException.class, () -> LocalFrames.read(definition, frame.toString()))
              .errorCode()
              .code());
    }
  }

  @Test
  void rejectsZeroOverflowNonCanonicalNumbersAndMalformedFingerprints() {
    for (String local : new String[] {"0", "01", "-1", "18446744073709551616", "A"}) {
      error(register(local, "0"));
    }
    error(register("1", "00"));
    error(register("1", "0").replace("sha256:", "SHA256:"));
    error(register("1", "0").replace("\"candidate_local\":\"1\"", "\"candidate_local\":1"));
  }

  @Test
  void rejectsDuplicateKeysAndForgedIdentityFields() {
    error(register("1", "0").replace("\"epoch\":\"1\"", "\"epoch\":\"1\",\"epoch\":\"2\""));
    error(register("1", "0").replace("\"boot_id\":", "\"tenant\":\"other\",\"boot_id\":"));
  }

  private static void error(String json) {
    assertEquals(
        "network.protocol_error",
        assertThrows(AurigaException.class, () -> LocalFrames.read("LocalRegister", json))
            .errorCode()
            .code());
  }
}
