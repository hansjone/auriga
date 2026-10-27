package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.LinkedHashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

public class NetworkSourceSchemasTest {
  public static Map<String, ObjectNode> frames() throws Exception {
    var register =
        (ObjectNode)
            NetworkJson.parse(
                """
      {"app_version":"1.0","type":"source_register","request_id":"r","epoch":"1","boot_id":"boot","operation_id":"op","mode":"issue","scope":"durable_unicast_send","predecessor_binding":"","expected_revision":"0","registration":{"registration_id":"reg","allocation_op_id":"alloc","allocation_fingerprint":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","registration_revision":"1","local":"1","address_type":"session"},"native_witness":{"witness_id":"w","binding_id":"b","binding_revision":"1","observed_at":"2026-10-25T00:00:00Z","valid_until":"2026-10-25T00:00:30Z"}}
      """);
    var source =
        NetworkJson.object()
            .put("source_binding", "source")
            .put("scope", "durable_unicast_send")
            .put("revision", "1")
            .put("state", "active")
            .put("not_before", "2026-10-25T00:00:00Z")
            .put("expires_at", "2026-10-25T00:30:00Z")
            .put("predecessor_binding", "");
    source.set("registration", register.path("registration"));
    source.set(
        "native_binding", NetworkJson.parse("{\"binding_id\":\"b\",\"binding_revision\":\"1\"}"));
    JsonNode auth =
        NetworkJson.parse(
                Files.readString(Path.of("../../../contracts/network/fixtures/frames.json")))
            .path("frames")
            .path("delivery_first_contact")
            .path("value")
            .path("server_auth");
    assertTrue(auth.isObject());
    Map<String, ObjectNode> frames = new LinkedHashMap<>();
    frames.put("SourceRegister", register);
    var get = register.deepCopy().put("type", "source_get").put("source_binding", "source");
    for (String key :
        new String[] {"operation_id", "mode", "scope", "expected_revision", "predecessor_binding"})
      get.remove(key);
    frames.put("SourceGet", get);
    var revoke = get.deepCopy().put("type", "source_revoke").put("operation_id", "revoke");
    revoke.remove("registration");
    revoke.remove("native_witness");
    frames.put("SourceRevoke", revoke);
    var registered =
        response("source_register_result", auth)
            .put("operation_id", "op")
            .put("result", "registered")
            .putNull("failure_evidence");
    registered.set("source", source);
    frames.put("SourceRegisterResult", registered);
    var got =
        response("source_get_result", auth)
            .put("policy_revision", "1")
            .put("observed_at", "2026-10-25T00:00:00Z")
            .put("valid_until", "2026-10-25T00:00:30Z")
            .put("witness_id", "w");
    got.set("source", source);
    frames.put("SourceGetResult", got);
    frames.put(
        "SourceRevokeResult",
        response("source_revoke_result", auth)
            .put("operation_id", "revoke")
            .put("source_binding", "source")
            .put("revision", "2")
            .put("state", "revoked")
            .put("lineage_stopped", true));
    return frames;
  }

  private static ObjectNode response(String type, JsonNode auth) {
    var result =
        NetworkJson.object().put("app_version", "1.0").put("type", type).put("request_id", "r");
    result.set("server_auth", auth);
    return result;
  }

  @Test
  void allSixSourceFramesResolveThroughProtocolAndSourceResources() throws Exception {
    for (var entry : frames().entrySet()) {
      NetworkSchemas.validate("source-control", entry.getKey(), entry.getValue());
      NetworkSchemas.validate("protocol", entry.getKey(), entry.getValue());
      var invalid = entry.getValue().deepCopy().put("type", "head");
      assertThrows(
          AurigaException.class,
          () -> NetworkSchemas.validate("source-control", entry.getKey(), invalid));
    }
  }

  @Test
  void referencesDoNotRelaxNativePresenceTerminalValidityOrKnownUnsignedConstraints()
      throws Exception {
    var frames = frames();
    var register = frames.get("SourceRegister").deepCopy().putNull("native_witness");
    reject("SourceRegister", register);
    register = frames.get("SourceRegister").deepCopy();
    ((ObjectNode) register.path("registration")).put("registration_revision", 1);
    reject("SourceRegister", register);
    for (String terminal : new String[] {"expired", "revoked", "stopped"}) {
      var get = frames.get("SourceGetResult").deepCopy();
      ((ObjectNode) get.path("source")).put("state", terminal);
      reject("SourceGetResult", get);
      get.remove("valid_until");
      NetworkSchemas.validate("source-control", "SourceGetResult", get);
    }
  }

  private static void reject(String definition, ObjectNode value) {
    assertEquals(
        "network.protocol_error",
        assertThrows(
                AurigaException.class,
                () -> NetworkSchemas.validate("source-control", definition, value))
            .errorCode()
            .code());
  }
}
