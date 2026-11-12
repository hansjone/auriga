package auriga.ops.upgrade;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.nio.file.*;
import java.time.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class OpsRuntimeStoreTest {
  @TempDir Path directory;

  @Test
  void persistsAndSerializesMutations() throws Exception {
    var store = new OpsRuntimeStore(directory);
    store.change(
        s -> {
          s.put("counter", 1);
          return null;
        });
    assertEquals(1, new OpsRuntimeStore(directory).read().path("counter").asInt());
    try (var executor = java.util.concurrent.Executors.newFixedThreadPool(4)) {
      var futures = new java.util.ArrayList<java.util.concurrent.Future<?>>();
      for (int i = 0; i < 20; i++)
        futures.add(
            executor.submit(
                () ->
                    store.change(
                        s -> {
                          s.put("counter", s.path("counter").asInt() + 1);
                          return null;
                        })));
      for (var future : futures) future.get();
    }
    assertEquals(21, store.read().path("counter").asInt());
  }

  @Test
  void refusesSymlinkState() throws Exception {
    Path outside = directory.resolve("other.json");
    Files.writeString(outside, "{}");
    try {
      Files.createSymbolicLink(directory.resolve("state.json"), outside);
    } catch (UnsupportedOperationException | java.io.IOException e) {
      org.junit.jupiter.api.Assumptions.abort("Symlink creation unavailable");
    }
    assertThrows(AurigaException.class, () -> new OpsRuntimeStore(directory).read());
  }
}
