package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import org.junit.jupiter.api.Test;

class NetworkSubmitContractTest {
  @Test
  void frozenJdbcFactsRetainBinaryTimeAndMaximumCountersAsSafeJson() {
    var map = new java.util.LinkedHashMap<String, Object>();
    map.put("observation", new byte[] {0, 1, -1});
    map.put("revision", new java.math.BigDecimal("18446744073709551615"));
    map.put(
        "observed_at",
        java.sql.Timestamp.from(java.time.Instant.parse("2026-01-01T00:00:00.123456Z")));
    map.put("empty", null);
    var snapshot = NetworkDurableSubmit.snapshot(map);
    assertEquals("AAH/", snapshot.path("observation").asString());
    assertEquals("18446744073709551615", snapshot.path("revision").asString());
    assertEquals("2026-01-01T00:00:00.123456Z", snapshot.path("observed_at").asString());
    assertTrue(snapshot.path("empty").isNull());
    assertEquals(snapshot, NetworkJson.parse(NetworkJson.canonical(snapshot)));
  }

  @Test
  void validatesSubmitAndRejectsClaimedIdentityBeforeIssuingAnything() {
    var request =
        NetworkJson.parse(
            """
        {"kind":"message","to":{"version":1,"realm":"2","node":"3","local":"1","type":"session"},
        "durability":"durable","intent":"information","body":{"subtype":"text","text":"hello"},
        "source":{"source_binding":"source-test"},"idempotency":{"producer_epoch":"1","key":"send-test"}}
        """);
    assertDoesNotThrow(() -> NetworkSchemas.validate("send", "SubmitRequest", request));
    var forged =
        ((tools.jackson.databind.node.ObjectNode) request).deepCopy().put("from", "attacker");
    assertThrows(AurigaException.class, () -> NetworkDurableSubmit.validate(forged));
  }
}
