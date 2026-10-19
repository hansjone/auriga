package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.JsonNodeFactory;
import tools.jackson.databind.node.ObjectNode;

class CompositeSessionKvTest {
  @Test
  void deleteRetriesThenFailsWhenSecondaryKeepsFailing() {
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
    CompositeSessionKv kv = new CompositeSessionKv(primary, secondary);
    primary.put("h", JsonNodeFactory.instance.objectNode().put("id", "1"));
    assertThrows(IllegalStateException.class, () -> kv.delete("h"));
    assertEquals(3, attempts.get());
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
    CompositeSessionKv kv = new CompositeSessionKv(primary, secondary);
    primary.put("h", JsonNodeFactory.instance.objectNode());
    assertDoesNotThrow(() -> kv.delete("h"));
    assertEquals(2, attempts.get());
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
