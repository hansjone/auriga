package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.resource.app.CatalogTrust;
import auriga.resource.web.ResourceBody;
import java.nio.file.*;
import org.junit.jupiter.api.Test;

class CatalogTrustTest {
  @Test
  void executesEveryFrozenCaseInJava() throws Exception {
    var vectors =
        ResourceBody.JSON.readTree(
            Files.readAllBytes(Path.of("../../../contracts/resource/unified-vectors.json")));
    var trust = new CatalogTrust(vectors.path("trust"));
    int count = 0;
    for (var c : vectors.path("cases")) {
      String reason = "ok";
      try {
        trust.verify(c.path("operation").asString(), c.path("input"));
      } catch (IllegalArgumentException e) {
        reason = e.getMessage();
      }
      assertEquals(c.path("expect").path("reason").asString(), reason, c.path("id").asString());
      count++;
    }
    assertEquals(47, count);
  }
}
