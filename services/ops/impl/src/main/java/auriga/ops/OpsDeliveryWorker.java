package auriga.ops;

import org.springframework.context.SmartLifecycle;

/** One bounded poll loop per process; database leases coordinate multiple replicas. */
public final class OpsDeliveryWorker implements SmartLifecycle {
  private final OpsStore store;
  private final OpsDelivery delivery;
  private volatile boolean running;
  private Thread worker;

  public OpsDeliveryWorker(OpsStore store) {
    this.store = store;
    this.delivery = new OpsDelivery(store);
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
                      for (var tenant : store.tenants()) {
                        if (!running) break;
                        delivery.processOne(tenant);
                      }
                      Thread.sleep(1000);
                    } catch (InterruptedException e) {
                      Thread.currentThread().interrupt();
                      break;
                    } catch (RuntimeException e) {
                      org.slf4j.LoggerFactory.getLogger(OpsDeliveryWorker.class)
                          .warn("Ops delivery poll failed");
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

  public synchronized void stop() {
    running = false;
    if (worker != null) {
      worker.interrupt();
      try {
        worker.join(10000);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
      }
    }
  }

  public boolean isRunning() {
    return running;
  }
}
