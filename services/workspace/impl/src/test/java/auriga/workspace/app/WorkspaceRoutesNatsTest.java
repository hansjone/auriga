package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.testsupport.NatsExtension;
import io.nats.client.Nats;
import io.nats.client.api.KeyValueConfiguration;
import java.nio.charset.StandardCharsets;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

@ExtendWith(NatsExtension.class)
class WorkspaceRoutesNatsTest {
  @Test
  void publishesChangesDeletesAndRestoresWithoutDuplicateRevisions() throws Exception {
    try (var connection = Nats.connect(NatsExtension.url())) {
      String bucket = "routes_test_" + UUID.randomUUID().toString().replace("-", "");
      connection.keyValueManagement().create(KeyValueConfiguration.builder().name(bucket).build());
      var kv = connection.keyValue(bucket);
      try {
        String id = UUID.randomUUID().toString();
        byte[] first = "initial".getBytes(StandardCharsets.UTF_8),
            second = "migrated".getBytes(StandardCharsets.UTF_8);
        WorkspaceRoutes.write(kv, id, first);
        long revision = kv.get(id).getRevision();
        WorkspaceRoutes.write(kv, id, first);
        assertEquals(revision, kv.get(id).getRevision());
        WorkspaceRoutes.write(kv, id, second);
        assertArrayEquals(second, kv.get(id).getValue());
        WorkspaceRoutes.write(kv, id, null);
        assertNull(kv.get(id));
        WorkspaceRoutes.write(kv, id, null);
        WorkspaceRoutes.write(kv, id, second);
        assertArrayEquals(second, kv.get(id).getValue());
      } finally {
        connection.keyValueManagement().delete(bucket);
      }
    }
  }
}
