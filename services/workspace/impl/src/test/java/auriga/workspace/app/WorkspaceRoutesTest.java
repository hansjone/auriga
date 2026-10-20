package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;

import java.sql.Timestamp;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.Test;

class WorkspaceRoutesTest {
  @Test
  void validatesOnlyIngressLocators() {
    for (var raw :
        List.of(
            "https://node.example.com:7443",
            "unix:///var/lib/auriga/ingress.sock",
            "npipe:////./pipe/auriga")) assertTrue(WorkspaceRoutes.validEndpoint(raw), raw);
    for (var raw :
        List.of(
            "http://node.example.com",
            "file:///etc/passwd",
            "https://user@node.example.com",
            "https://node.example.com/admin",
            "https://node.example.com?q=secret",
            "unix:relative",
            "npipe:relative",
            "")) assertFalse(WorkspaceRoutes.validEndpoint(raw), raw);
  }

  @Test
  void serializesContractAndFailsClosedForUnavailableNodes() throws Exception {
    var row = new HashMap<String, Object>();
    var now = Timestamp.from(Instant.now());
    for (var key : List.of("id", "tenant_id", "owner_id", "node_id"))
      row.put(key, UUID.randomUUID());
    row.put("ingress_endpoint", "https://node.example.com:7443");
    row.put("generation", 2L);
    row.put("updated_at", now);
    row.put("node_seen", now);
    row.put("phase", "running");
    row.put("ready", true);
    var value = WorkspaceService.JSON.readTree(WorkspaceRoutes.encode(row));
    assertEquals(2, value.path("generation").asInt());
    assertTrue(value.path("host").isNull());
    assertEquals("running", value.path("state").asText());
    row.put("migration_pending", true);
    assertEquals(
        "failed",
        WorkspaceService.JSON.readTree(WorkspaceRoutes.encode(row)).path("state").asText());
    row.put("migration_pending", false);
    row.put("node_seen", Timestamp.from(Instant.now().minusSeconds(61)));
    assertEquals(
        "failed",
        WorkspaceService.JSON.readTree(WorkspaceRoutes.encode(row)).path("state").asText());
    row.put("node_revoked", true);
    assertNull(WorkspaceRoutes.encode(row));
    row.put("node_revoked", false);
    row.put("deleted", true);
    assertNull(WorkspaceRoutes.encode(row));
  }
}
