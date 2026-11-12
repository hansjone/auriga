package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.resource.app.ResourceSemver;
import org.junit.jupiter.api.Test;

class ResourceSemverTest {
  @Test
  void ordersNumericAndPrereleaseVersionsWithoutAllowingControlCharacters() {
    var ordered =
        java.util.List.of(
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
            "1.1.0",
            "1.10.0",
            "2.0.0");
    for (int i = 1; i < ordered.size(); i++)
      assertTrue(ResourceSemver.compare(ordered.get(i - 1), ordered.get(i)) < 0);
    assertEquals(0, ResourceSemver.compare("1.0.0+first", "1.0.0+second"));
    for (String invalid : java.util.List.of("1.0", "01.0.0", "1.0.0-01", "1.0.0\u000b", "1.0.0\n"))
      assertFalse(ResourceSemver.valid(invalid));
  }
}
