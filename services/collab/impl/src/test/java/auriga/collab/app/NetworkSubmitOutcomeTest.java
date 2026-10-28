package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

class NetworkSubmitOutcomeTest {
  @Test
  void unfinishedReservationHasNoInventedOutcome() {
    var pending = NetworkDurableSubmit.outcome(Map.of());
    assertFalse(pending.terminal());
    assertNull(pending.replay());
  }

  @Test
  void storedResultReplaysItsOriginalBytesAfterReservationEnds() {
    String original = "{ \"status\" : \"platform_stored\" }";
    var row = new HashMap<String, Object>();
    row.put("result_json", original);
    row.put("reservation_until", null);
    var saved = NetworkDurableSubmit.outcome(row);
    assertTrue(saved.terminal());
    assertEquals(original, saved.replay());
  }

  @Test
  void rejectedOriginalKeyAlwaysReplaysTheSameTerminalError() {
    var rejected = NetworkDurableSubmit.outcome(Map.of("failure_code", "network.conflict"));
    assertTrue(rejected.terminal());
    for (int retry = 0; retry < 2; retry++)
      assertEquals(
          "network.conflict",
          assertThrows(AurigaException.class, rejected::replay).errorCode().code());
  }

  @Test
  void requestCancellationIsVisibleToTheFinalStorageFence() {
    assertFalse(NetworkDurableSubmit.cancelled());
    try (var context = io.grpc.Context.current().withCancellation()) {
      context.cancel(new java.util.concurrent.CancellationException("cancel SEND"));
      context.run(() -> assertTrue(NetworkDurableSubmit.cancelled()));
    }
    assertFalse(NetworkDurableSubmit.cancelled());
  }
}
