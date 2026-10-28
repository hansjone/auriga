package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

class NetworkMailboxProducerEpochTest {
  @Test
  void preSendMigrationReadyKeepsOptionalEpochAbsent() {
    var result = NetworkJson.object().put("type", "ready_result");
    var original = result.deepCopy();
    assertDoesNotThrow(
        () ->
            NetworkMailboxWireService.appendProducerEpoch(result, Map.of("connection_epoch", 1L)));
    assertEquals(original, result);
    assertFalse(result.has("producer_epoch"));
  }

  @Test
  void migratedReadyReturnsPersistedEpochAsAnExactDecimalString() {
    var result = NetworkJson.object();
    NetworkMailboxWireService.appendProducerEpoch(result, Map.of("producer_epoch", Long.MAX_VALUE));
    assertEquals("9223372036854775807", result.path("producer_epoch").asString());
  }

  @Test
  void presentButInvalidEpochIsNeverTreatedAsAnOlderSchema() {
    for (Object value : new Object[] {null, 0L, -1L, "1"}) {
      var row = new HashMap<String, Object>();
      row.put("producer_epoch", value);
      var result = NetworkJson.object();
      var failure =
          assertThrows(
              AurigaException.class,
              () -> NetworkMailboxWireService.appendProducerEpoch(result, row));
      assertEquals("common.unavailable", failure.errorCode().code());
      assertFalse(result.has("producer_epoch"));
    }
  }
}
