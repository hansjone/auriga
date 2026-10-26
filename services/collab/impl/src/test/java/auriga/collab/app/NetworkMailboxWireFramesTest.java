package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.NetworkMailboxWireFrames;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

class NetworkMailboxWireFramesTest {
  @Test
  void sourceRpcsRejectWrongMethodDuplicateKeysAndInvalidUtf8BeforeIamOrStorage() throws Exception {
    var source = mock(NetworkSourceGrpcAuthority.class);
    var jdbc = mock(JdbcTemplate.class);
    var wire = wire(source, jdbc);
    var frames = NetworkSourceSchemasTest.frames();
    for (var definition : List.of("SourceRegister", "SourceGet", "SourceRevoke")) {
      var frame = frames.get(definition).deepCopy().put("type", "head");
      reject(
          "network.protocol_error", () -> sourceInvoke(wire, definition, bytes(frame.toString())));
      byte[] duplicate =
          bytes(
              frames
                  .get(definition)
                  .toString()
                  .replace("\"app_version\":", "\"app_version\":\"1.0\",\"app_version\":"));
      reject("network.protocol_error", () -> sourceInvoke(wire, definition, duplicate));
      reject(
          "network.protocol_error", () -> sourceInvoke(wire, definition, new byte[] {(byte) 0xff}));
    }
    verifyNoInteractions(source, jdbc);
  }

  private static void sourceInvoke(
      NetworkMailboxWireService wire, String definition, byte[] bytes) {
    switch (definition) {
      case "SourceRegister" -> wire.sourceRegister(null, bytes);
      case "SourceGet" -> wire.sourceGet(null, bytes);
      case "SourceRevoke" -> wire.sourceRevoke(null, bytes);
      default -> throw new IllegalArgumentException();
    }
  }

  @Test
  void receiptFixtureAndItsConflictVariantsAreSchemaValid() {
    String frame = receipt();
    String changed = frame.replace("host-r", "host-changed");
    assertEquals(
        NetworkJson.parse(frame).path("operation_id"),
        NetworkJson.parse(changed).path("operation_id"));
    assertNotEquals(NetworkJson.parse(frame), NetworkJson.parse(changed));
    for (String valid :
        List.of(
            frame,
            frame.replace("host-r", "host-changed"),
            frame.replace("\"expected_state_version\":\"1\"", "\"expected_state_version\":\"0\"")))
      assertDoesNotThrow(
          () -> NetworkSchemas.validate("protocol", "Receipt", NetworkJson.parse(valid)));
  }

  @Test
  void malformedReceiptsFailBeforeIamOrPersistence() {
    var source = mock(NetworkSourceGrpcAuthority.class);
    var jdbc = mock(JdbcTemplate.class);
    var wire = wire(source, jdbc);
    String valid = receipt();
    for (String invalid :
        List.of(
            valid.replace("\"version\":1,", ""),
            valid.replace("\"expected_state_version\":\"1\"", "\"expected_state_version\":1"),
            valid.replace("\"realm\":\"2\"", "\"realm\":\"2\",\"realm\":\"2\""),
            valid.replace(
                "\"operation_id\":\"op-r\"",
                "\"operation_id\":\"op-r\",\"operation_id\":\"op-r\"")))
      reject("network.protocol_error", () -> wire.receipt(null, bytes(invalid)));
    verifyNoInteractions(source, jdbc);
  }

  @Test
  void everyFramedRpcRejectsWrongTypeAndDuplicateTypeBeforeIamOrPersistence() {
    var source = mock(NetworkSourceGrpcAuthority.class);
    var jdbc = mock(JdbcTemplate.class);
    var wire = wire(source, jdbc);
    String ready =
        """
        {"app_version":"1.0","type":"ready","request_id":"ready-a","channel_nonce":"nonce-a","boot_id":"boot-a","features":[],"adapter_capabilities":[],"resume_cursor":"0","local_high_water":"0","registration_revision":"0","receipt_checkpoint":{"checkpoint_seq":"0","checkpoint_revision":"0","summary_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"keyset_version":"1","pending_registration":null}
        """;
    String fetch =
        """
        {"app_version":"1.0","type":"fetch","request_id":"fetch-a","epoch":"1","resume_cursor":"0","continuation":null,"max_items":1,"max_bytes":65536}
        """;
    String receipt = receipt();
    List<String> frames = List.of(ready, fetch, receipt);
    List<String> definitions = List.of("Ready", "Fetch", "Receipt");
    for (int rpc = 0; rpc < frames.size(); rpc++) {
      String expected = definitions.get(rpc).toLowerCase(Locale.ROOT);
      var frame = (tools.jackson.databind.node.ObjectNode) NetworkJson.parse(frames.get(rpc));
      frame.put("type", "head");
      // Validate all other fields against the matching schema, changing only type.
      NetworkSchemas.validate("protocol", definitions.get(rpc), NetworkJson.parse(frames.get(rpc)));
      int method = rpc;
      reject("network.protocol_error", () -> invoke(wire, method, frame.toString()));
      String duplicate =
          frames
              .get(rpc)
              .replace(
                  "\"type\":\"" + expected + "\"",
                  "\"type\":\"" + expected + "\",\"type\":\"" + expected + "\"");
      reject("network.protocol_error", () -> invoke(wire, method, duplicate));
    }
    verifyNoInteractions(source, jdbc);
  }

  private static NetworkMailboxWireService wire(
      NetworkSourceGrpcAuthority source, JdbcTemplate jdbc) {
    return new NetworkMailboxWireService(
        mock(NetworkMailboxService.class),
        mock(auriga.collab.network.local.LocalRegistry.class),
        jdbc,
        mock(TenantTransactionManager.class),
        source,
        new NetworkSigningKeys(Clock.systemUTC(), 1),
        Clock.systemUTC());
  }

  private static void invoke(NetworkMailboxWireService wire, int method, String frame) {
    switch (method) {
      case 0 -> wire.ready(null, bytes(frame));
      case 1 -> wire.fetch(null, bytes(frame));
      case 2 -> wire.receipt(null, bytes(frame));
      default -> throw new IllegalArgumentException();
    }
  }

  private static String receipt() {
    return NetworkMailboxWireFrames.receipt(
        "20", UUID.randomUUID(), UUID.randomUUID(), NetworkJson.parse("{}"));
  }

  private static byte[] bytes(String frame) {
    return frame.getBytes(StandardCharsets.UTF_8);
  }

  private static void reject(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }
}
