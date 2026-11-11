package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import java.util.*;
import java.util.concurrent.*;
import org.junit.jupiter.api.Test;

class OpsDeliveryWorkerTest {
  @Test
  void slowTenantDoesNotBlockAnotherTenantAndIsNotScheduledTwice() throws Exception {
    UUID slow = UUID.randomUUID(), fast = UUID.randomUUID();
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var done = new CountDownLatch(1);
    var calls = new java.util.concurrent.atomic.AtomicInteger();
    var worker =
        new OpsDeliveryWorker(
            () -> List.of(slow, fast),
            t -> {
              if (t.equals(slow)) {
                calls.incrementAndGet();
                entered.countDown();
                try {
                  release.await();
                } catch (InterruptedException e) {
                  Thread.currentThread().interrupt();
                }
              } else done.countDown();
            });
    try {
      worker.start();
      assertTrue(entered.await(2, TimeUnit.SECONDS));
      assertTrue(done.await(2, TimeUnit.SECONDS));
      worker.poll();
      assertEquals(1, calls.get());
    } finally {
      release.countDown();
      worker.stop();
    }
  }
}
