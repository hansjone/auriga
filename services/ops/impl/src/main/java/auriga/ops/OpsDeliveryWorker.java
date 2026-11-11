package auriga.ops;

import java.util.*;
import java.util.concurrent.*;
import java.util.function.*;
import org.springframework.context.SmartLifecycle;

/** Bounded, per-tenant work: a slow destination cannot serialize the whole service. */
public final class OpsDeliveryWorker implements SmartLifecycle {
  private final Supplier<List<UUID>> tenants;
  private final Consumer<UUID> deliver;
  private final Semaphore slots = new Semaphore(8);
  private final Set<UUID> active = ConcurrentHashMap.newKeySet();
  private final Set<Thread> tasks = ConcurrentHashMap.newKeySet();
  private volatile boolean running;
  private Thread worker;
  private int cursor;

  public OpsDeliveryWorker(OpsStore store) {
    this(store::tenants, t -> new OpsDelivery(store).processOne(t));
  }

  OpsDeliveryWorker(Supplier<List<UUID>> tenants, Consumer<UUID> deliver) {
    this.tenants = tenants;
    this.deliver = deliver;
  }

  synchronized void poll() {
    if (!running) return;
    var all = tenants.get();
    for (int i = 0; i < all.size(); i++) {
      var tenant = all.get(Math.floorMod(cursor++, all.size()));
      if (!slots.tryAcquire()) break;
      if (!active.add(tenant)) {
        slots.release();
        continue;
      }
      var task =
          Thread.ofVirtual()
              .name("ops-tenant-delivery")
              .unstarted(
                  () -> {
                    try {
                      deliver.accept(tenant);
                    } catch (RuntimeException e) {
                      warn();
                    } finally {
                      active.remove(tenant);
                      slots.release();
                      tasks.remove(Thread.currentThread());
                    }
                  });
      tasks.add(task);
      task.start();
    }
  }

  private static void warn() {
    org.slf4j.LoggerFactory.getLogger(OpsDeliveryWorker.class).warn("Ops delivery poll failed");
  }

  public synchronized void start() {
    if (running) return;
    running = true;
    worker =
        Thread.ofVirtual()
            .name("ops-delivery")
            .start(
                () -> {
                  while (running) {
                    try {
                      poll();
                      Thread.sleep(1000);
                    } catch (InterruptedException e) {
                      Thread.currentThread().interrupt();
                      break;
                    } catch (RuntimeException e) {
                      warn();
                      try {
                        Thread.sleep(1000);
                      } catch (InterruptedException interrupted) {
                        Thread.currentThread().interrupt();
                        break;
                      }
                    }
                  }
                });
  }

  public void stop() {
    Thread poller;
    synchronized (this) {
      running = false;
      poller = worker;
      if (poller != null) poller.interrupt();
      tasks.forEach(Thread::interrupt);
    }
    long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(11);
    var remaining = new ArrayList<>(tasks);
    if (poller != null) remaining.add(poller);
    for (var task : remaining)
      try {
        long millis = TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime());
        if (millis > 0) task.join(millis);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        break;
      }
  }

  public boolean isRunning() {
    return running;
  }
}
