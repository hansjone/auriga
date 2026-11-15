package auriga.iam;

import java.util.concurrent.*;
import org.springframework.context.SmartLifecycle;

/** Bounded round-robin enforcement also runs while a node is offline. */
final class MessageNetworkIsolationJob implements SmartLifecycle {
  private final java.util.List<Runnable> targets;
  private ScheduledExecutorService worker;
  private int cursor;

  MessageNetworkIsolationJob(
      MessageNetworkTargets targets, MessageNetworkIsolationExecution execution) {
    var work = new java.util.ArrayList<Runnable>();
    var hosts = new java.util.HashSet<String>();
    for (var binding : targets.bindings()) {
      if (binding.host() != null && hosts.add(binding.tenant() + "/" + binding.host()))
        work.add(() -> execution.beforeControl(binding.tenant(), binding.host()));
      work.add(() -> execution.reconcileDevice(binding));
    }
    this.targets = java.util.List.copyOf(work);
  }

  void sweep() {
    var bindings = targets;
    for (int count = 0; count < Math.min(32, bindings.size()); count++) {
      if (Thread.currentThread().isInterrupted()) return;
      var binding = bindings.get(cursor++ % bindings.size());
      if (cursor == bindings.size()) cursor = 0;
      try {
        binding.run();
      } catch (RuntimeException failure) {
        org.slf4j.LoggerFactory.getLogger(getClass())
            .warn("Message isolation enforcement deferred", failure);
      }
    }
  }

  @Override
  public synchronized void start() {
    if (worker != null) return;
    worker =
        Executors.newSingleThreadScheduledExecutor(
            Thread.ofPlatform().daemon().name("iam-message-isolation").factory());
    worker.scheduleWithFixedDelay(this::sweep, 0, 5, TimeUnit.SECONDS);
  }

  @Override
  public synchronized void stop() {
    if (worker == null) return;
    worker.shutdownNow();
    worker = null;
  }

  @Override
  public synchronized boolean isRunning() {
    return worker != null;
  }
}
