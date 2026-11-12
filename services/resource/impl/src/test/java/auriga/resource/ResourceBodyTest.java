package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.resource.web.ResourceBody;
import java.nio.charset.StandardCharsets;
import java.util.Set;
import org.junit.jupiter.api.Test;

class ResourceBodyTest {
  @Test
  void manifestParserRejectsMalformedUtf8DuplicateKeysAndTrailingJson() {
    for (String body :
        new String[] {
          "{",
          "[]",
          "{\"name\":\"a\",\"name\":\"b\",\"version\":\"1\"}",
          "{\"name\":\"a\",\"version\":\"1\"} {}"
        })
      assertThrows(
          AurigaException.class,
          () ->
              ResourceBody.parse(
                  body.getBytes(StandardCharsets.UTF_8), 1024, null, Set.of("name", "version")));
    assertThrows(
        AurigaException.class,
        () -> ResourceBody.parse(new byte[] {(byte) 0xc3, 0x28}, 1024, null, Set.of("name")));
    var body =
        ResourceBody.parse(
            "{\"name\":\"example\",\"version\":\"1\",\"scripts\":{\"install\":\"never executed\"}}"
                .getBytes(StandardCharsets.UTF_8),
            1024,
            null,
            Set.of("name", "version"));
    assertEquals("example", ResourceBody.text(body, "name", 214));
    assertThrows(
        AurigaException.class,
        () ->
            ResourceBody.text(
                ResourceBody.JSON.createObjectNode().put("reason", "\ud800"), "reason", 1024));
  }
}
