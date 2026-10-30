package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;

class NetworkRawBoundaryTest {
  @Test
  void sharedUtf8AndDepthBoundaryCasesMatchReference() throws Exception {
    var fixture = NetworkEnvelopeSigningTest.fixture("raw-boundaries.json");
    for (var c : fixture.path("cases")) {
      String raw = raw(c);
      if (c.path("accept").asBoolean())
        assertDoesNotThrow(() -> NetworkJson.parse(raw), c.path("name").asString());
      else
        assertEquals(
            "network.protocol_error",
            assertThrows(
                    AurigaException.class, () -> NetworkJson.parse(raw), c.path("name").asString())
                .errorCode()
                .code());
    }
  }

  static String raw(JsonNode c) {
    String kind = c.path("kind").asString();
    if (kind.equals("valueTree")) {
      var array = NetworkJson.JSON.createArrayNode();
      String names = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_";
      for (int i = 0; i < c.path("branches").asInt(); i++) {
        var entry = array.addObject();
        int members =
            i == c.path("branches").asInt() - 1
                ? c.path("lastMembers").asInt()
                : c.path("members").asInt();
        for (int n = 0; n < members; n++) entry.putObject(names.substring(n, n + 1));
      }
      return array.toString();
    }
    if (kind.startsWith("nested")) {
      int depth = c.path("containers").asInt();
      return "[".repeat(depth) + (kind.equals("nestedScalar") ? "0" : "") + "]".repeat(depth);
    }
    String value =
        c.path("unit").asString().repeat(c.path("repeat").asInt()) + c.path("suffix").asString("");
    return kind.equals("key")
        ? "{" + NetworkJson.JSON.writeValueAsString(value) + ":0}"
        : "{\"value\":" + NetworkJson.JSON.writeValueAsString(value) + "}";
  }
}
