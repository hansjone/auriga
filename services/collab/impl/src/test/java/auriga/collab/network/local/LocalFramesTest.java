package auriga.collab.network.local;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.common.error.AurigaException;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

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
  void rejectsDuplicateKeysAndRetainsUntrustedOptionalIdentityFields() {
    error(register("1", "0").replace("\"epoch\":\"1\"", "\"epoch\":\"1\",\"epoch\":\"2\""));
    var frame =
        LocalFrames.read(
            "LocalRegister",
            register("1", "0").replace("\"boot_id\":", "\"tenant\":\"other\",\"boot_id\":"));
    assertEquals("other", frame.path("tenant").asString());
    assertEquals("boot", frame.path("boot_id").asString());
  }

  @Test
  void localRegisterConsumerRetainsUnknownFieldsAcrossMinorVersions() {
    for (String version : new String[] {"1.0", "1.1"}) {
      var frame = (ObjectNode) NetworkJson.parse(register("1", "0"));
      frame.put("app_version", version).put("future_flag", true).put("future_count", 42);
      frame
          .putObject("future_object")
          .put("label", "opaque")
          .putArray("nested")
          .addNull()
          .add("value")
          .addObject()
          .put("enabled", false);
      assertEquals(frame, LocalFrames.read("LocalRegister", frame.toString()));
    }
  }

  @Test
  void localRegisterExtensionsCannotRelaxKnownConstraints() {
    var valid = (ObjectNode) NetworkJson.parse(register("1", "0"));
    valid.putObject("future_object").put("address_type", "future");
    assertNotNull(LocalFrames.read("LocalRegister", valid.toString()));
    for (String field :
        new String[] {
          "type",
          "address_type",
          "candidate_local",
          "epoch",
          "expected_registration_revision",
          "allocation_fingerprint",
          "boot_id",
          "app_version"
        }) {
      var invalid = valid.deepCopy().put(field, "invalid value");
      error(invalid.toString());
      invalid = valid.deepCopy();
      invalid.remove(field);
      error(invalid.toString());
      error(valid.deepCopy().putNull(field).toString());
    }
  }

  @Test
  void localRegisterExtensionsStillObeyRawSyntaxAndBudgets() {
    for (String value :
        new String[] {
          "{\"key\":1,\"key\":2}",
          "1.0",
          "1e0",
          "9007199254740992",
          "NaN",
          "\"\\uD800\"",
          "\"raw\nnewline\""
        }) error(withOptional(value));
    error(withOptional("\"" + "a".repeat(16385) + "\""));
    error(withOptional("[" + "0,".repeat(256) + "0]"));
    error(withOptional("[".repeat(17) + "0" + "]".repeat(17)));
    var many = (ObjectNode) NetworkJson.parse(register("1", "0"));
    var object = many.putObject("future_object");
    for (int i = 0; i < 129; i++) object.put("key" + i, i);
    error(many.toString());
    var large = (ObjectNode) NetworkJson.parse(register("1", "0"));
    var strings = large.putArray("future_strings");
    for (int i = 0; i < 5; i++) strings.add("a".repeat(14000));
    error(large.toString()); // Whole-frame 64KiB limit, with every individual string within budget.
    var values = (ObjectNode) NetworkJson.parse(register("1", "0"));
    var arrays = values.putArray("future_values");
    for (int i = 0; i < 32; i++) {
      var array = arrays.addArray();
      for (int j = 0; j < 256; j++) array.add(j);
    }
    error(values.toString()); // Total value budget, with all individual arrays within budget.
    error(register("1", "0") + " {}");
  }

  @Test
  void compatibilityIsLimitedToLocalRegisterConsumption() throws Exception {
    var fixtures =
        NetworkJson.parse(
                java.nio.file.Files.readString(
                    java.nio.file.Path.of("../../../contracts/network/fixtures/frames.json")))
            .path("frames");
    for (String name :
        new String[] {"ready_pending_registration", "register_result", "contact_assign"}) {
      var fixture = fixtures.path(name);
      assertFalse(fixture.isMissingNode(), name);
      String definition = fixture.path("def").asString().split("#")[1];
      var frame = (ObjectNode) fixture.path("value").deepCopy();
      frame.put("future_field", true);
      assertEquals(
          "network.protocol_error",
          assertThrows(AurigaException.class, () -> LocalFrames.read(definition, frame.toString()))
              .errorCode()
              .code());
    }
  }

  private static String withOptional(String value) {
    return register("1", "0")
        .replace(
            "\"request_id\":\"request\"",
            "\"future_field\":" + value + ",\"request_id\":\"request\"");
  }

  private static void error(String json) {
    assertEquals(
        "network.protocol_error",
        assertThrows(AurigaException.class, () -> LocalFrames.read("LocalRegister", json))
            .errorCode()
            .code());
  }
}
