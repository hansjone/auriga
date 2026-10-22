package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.JsonNodeFactory;
import tools.jackson.databind.node.ObjectNode;

class CompositeSessionKvTest {
  @Test
  void deleteRetriesWithoutRollingBackCommittedRevocation() {
    AtomicInteger attempts = new AtomicInteger();
    SessionKv primary = new MemKv();
    SessionKv secondary =
        new SessionKv() {
          @Override
          public ObjectNode get(String hash) {
            return null;
          }

          @Override
          public void put(String hash, ObjectNode value) {}

          @Override
          public void delete(String hash) {
            attempts.incrementAndGet();
            throw new IllegalStateException("nats down");
          }
        };
    IamStore store = store();
    CompositeSessionKv kv = new CompositeSessionKv(store, primary, secondary);
    primary.put("h", JsonNodeFactory.instance.objectNode().put("id", "1"));
    auriga.common.context.TenantContext.run(
        java.util.UUID.randomUUID(), () -> assertDoesNotThrow(() -> kv.delete("h")));
    assertEquals(0, attempts.get());
    for (int i = 0; i < 3; i++) assertEquals(0, kv.forwardOnce());
    assertEquals(3, attempts.get());
    verify(store, times(3)).retrySessionProjection(any(), any());
    verify(store, never()).completeSessionProjection(any());
    assertNull(primary.get("h"));
  }

  @Test
  void deleteSucceedsAfterSecondaryRetry() {
    AtomicInteger attempts = new AtomicInteger();
    SessionKv primary = new MemKv();
    SessionKv secondary =
        new SessionKv() {
          @Override
          public ObjectNode get(String hash) {
            return null;
          }

          @Override
          public void put(String hash, ObjectNode value) {}

          @Override
          public void delete(String hash) {
            if (attempts.incrementAndGet() < 2) {
              throw new IllegalStateException("transient");
            }
          }
        };
    CompositeSessionKv kv = new CompositeSessionKv(store(), primary, secondary);
    primary.put("h", JsonNodeFactory.instance.objectNode());
    auriga.common.context.TenantContext.run(
        java.util.UUID.randomUUID(), () -> assertDoesNotThrow(() -> kv.delete("h")));
    assertEquals(0, kv.forwardOnce());
    assertEquals(1, kv.forwardOnce());
    assertEquals(2, attempts.get());
  }

  private static IamStore store() {
    IamStore store = mock(IamStore.class);
    when(store.sessionProjectionTenants(any(), anyInt()))
        .thenReturn(java.util.List.of(java.util.UUID.randomUUID()));
    when(store.tenant(any(), any()))
        .thenAnswer(i -> ((java.util.function.Supplier<?>) i.getArgument(1)).get());
    when(store.claimSessionProjection(any()))
        .thenReturn(new IamStore.SessionProjection(java.util.UUID.randomUUID(), "h", 0));
    return store;
  }

  private static final class MemKv implements SessionKv {
    private ObjectNode value;

    @Override
    public ObjectNode get(String hash) {
      return value;
    }

    @Override
    public void put(String hash, ObjectNode v) {
      value = v;
    }

    @Override
    public void delete(String hash) {
      value = null;
    }
  }
}
