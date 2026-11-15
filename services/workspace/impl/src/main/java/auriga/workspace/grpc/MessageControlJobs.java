package auriga.workspace.grpc;

import java.util.concurrent.*;

/** Bounded message-only I/O; neither a slow ledger nor its queue owns a node session lock. */
final class MessageControlJobs implements AutoCloseable {
  private final Semaphore slots;
  private final ExecutorService executor = Executors.newVirtualThreadPerTaskExecutor();

  MessageControlJobs(int limit) {
    slots = new Semaphore(limit);
  }

  boolean submit(Runnable task) {
    if (!slots.tryAcquire()) return false;
    try {
      executor.execute(
          () -> {
            try {
              task.run();
            } finally {
              slots.release();
            }
          });
      return true;
    } catch (RejectedExecutionException stopped) {
      slots.release();
      return false;
    }
  }

  @Override
  public void close() {
    executor.shutdownNow();
  }
}
