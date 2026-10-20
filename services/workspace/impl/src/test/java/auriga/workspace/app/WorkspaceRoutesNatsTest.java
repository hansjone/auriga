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
        String initiallyAbsent = UUID.randomUUID().toString();
        assertNull(WorkspaceRoutes.latest(kv, initiallyAbsent));
        WorkspaceRoutes.write(kv, initiallyAbsent, first);
        assertFalse(WorkspaceRoutes.write(kv, initiallyAbsent, second, null));
        WorkspaceRoutes.write(kv, initiallyAbsent, null);
        assertFalse(WorkspaceRoutes.write(kv, initiallyAbsent, second, null));
        assertNull(kv.get(initiallyAbsent));
        WorkspaceRoutes.write(kv, id, first);
        var stale = WorkspaceRoutes.latest(kv, id);
        long revision = kv.get(id).getRevision();
        WorkspaceRoutes.write(kv, id, first);
        assertEquals(revision, kv.get(id).getRevision());
        WorkspaceRoutes.write(kv, id, second);
        assertArrayEquals(second, kv.get(id).getValue());
        assertFalse(WorkspaceRoutes.write(kv, id, "stale".getBytes(StandardCharsets.UTF_8), stale));
        assertArrayEquals(second, kv.get(id).getValue());
        var beforeDelete = WorkspaceRoutes.latest(kv, id);
        WorkspaceRoutes.write(kv, id, null);
        assertNull(kv.get(id));
        assertFalse(WorkspaceRoutes.write(kv, id, first, beforeDelete));
        assertNull(kv.get(id));
        var deleted = WorkspaceRoutes.latest(kv, id);
        assertNotNull(deleted);
        WorkspaceRoutes.write(kv, id, null);
        WorkspaceRoutes.write(kv, id, second);
        assertArrayEquals(second, kv.get(id).getValue());
        assertFalse(WorkspaceRoutes.write(kv, id, first, deleted));
        assertFalse(WorkspaceRoutes.write(kv, id, null, beforeDelete));
        assertArrayEquals(second, kv.get(id).getValue());
        org.springframework.transaction.support.TransactionSynchronizationManager
            .setActualTransactionActive(true);
        try {
          assertThrows(IllegalStateException.class, () -> WorkspaceRoutes.write(kv, id, first));
        } finally {
          org.springframework.transaction.support.TransactionSynchronizationManager
              .setActualTransactionActive(false);
        }
        assertArrayEquals(second, kv.get(id).getValue());
      } finally {
        connection.keyValueManagement().delete(bucket);
      }
    }
  }
}
