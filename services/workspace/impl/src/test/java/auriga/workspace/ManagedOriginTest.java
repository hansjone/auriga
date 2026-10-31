package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;

import auriga.workspace.app.WorkspaceManagedInstallation;
import auriga.workspace.app.WorkspaceService;
import java.util.List;
import org.junit.jupiter.api.Test;

class ManagedOriginTest {
  @Test
  void internalOriginUsesTheSameApprovedOriginForIdentityNetworkAndModels() {
    var original = WorkspaceService.JSON.createObjectNode();
    original.put("platformOrigin", "https://portal.example.com");
    original.put("issuer", "https://portal.example.com");
    original.putObject("network").put("realm", "1").putArray("roots");
    var projected =
        WorkspaceManagedInstallation.project(
            original,
            "https://platform.internal:18446",
            List.of("https://platform.internal:18446"),
            "/etc/managed/ca.crt");
    assertEquals("https://portal.example.com", original.path("platformOrigin").asString());
    assertEquals("https://platform.internal:18446", projected.path("platformOrigin").asString());
    assertEquals(projected.path("platformOrigin"), projected.path("issuer"));
    assertEquals(
        "wss://platform.internal:18446/net/v1/ws",
        projected.path("network").path("wssUrl").asString());
    assertEquals(
        "https://platform.internal:18446/ai/v1", projected.path("modelBaseUrl").asString());
    assertEquals(original.path("network").path("roots"), projected.path("network").path("roots"));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            WorkspaceManagedInstallation.project(
                original,
                "https://other.internal:18446",
                List.of("https://platform.internal:18446"),
                "/etc/managed/ca.crt"));
    for (String origin :
        List.of(
            "http://platform.internal:18446",
            "https://user@platform.internal",
            "https://platform.internal/path",
            "https://platform.internal?",
            "https://platform.internal#x"))
      assertThrows(
          IllegalArgumentException.class,
          () ->
              WorkspaceManagedInstallation.project(
                  original, origin, List.of(origin), "/etc/managed/ca.crt"));
  }
}
