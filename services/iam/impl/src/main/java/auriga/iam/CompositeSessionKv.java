package auriga.iam;

import java.time.Clock;
import java.util.UUID;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.context.SmartLifecycle;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import tools.jackson.databind.node.ObjectNode;

/**
 * Durable projection of committed PostgreSQL state. No request borrows a second connection to
 * publish its own commit. The single worker uses tenant RLS, a cross-process hash lock, a fresh
 * authoritative read, and the secondary's revision CAS, including tombstones.
 */
public final class CompositeSessionKv implements SessionKv, SmartLifecycle {
  private static final Logger log = LoggerFactory.getLogger(CompositeSessionKv.class);
  private static final int BATCH_SIZE = 32;
  private final SessionKv primary;
  private final SessionKv secondary;
  private final IamStore store;
  private final Clock clock;
  private final Semaphore signal = new Semaphore(0);
  private UUID cursor;
  private volatile boolean running;
  private volatile Thread worker;

  public CompositeSessionKv(IamStore store, SessionKv primary, SessionKv secondary) {
    this(store, primary, secondary, Clock.systemUTC());
  }

  public CompositeSessionKv(IamStore store, SessionKv primary, SessionKv secondary, Clock clock) {
    this.store = store;
    this.primary = primary;
    this.secondary = secondary;
    this.clock = clock;
  }

  @Override
  public ObjectNode get(String hash) {
    return primary.get(hash);
  }

  @Override
  public void put(String hash, ObjectNode value) {
    primary.put(hash, value);
    store.enqueueSessionProjection(hash, clock.instant());
  }

  @Override
  public void delete(String hash) {
    primary.delete(hash);
    store.enqueueSessionProjection(hash, clock.instant());
  }

  /** At most 32 tenants / keys per cycle, advancing even past empty or failed tenants. */
  synchronized int forwardOnce() {
    if (TransactionSynchronizationManager.isActualTransactionActive())
      throw new IllegalStateException("Projection worker must not join a caller transaction");
    var tenants = store.sessionProjectionTenants(cursor, BATCH_SIZE);
    if (tenants.isEmpty()) {
      cursor = null;
      tenants = store.sessionProjectionTenants(cursor, BATCH_SIZE);
    }
    int completed = 0;
    for (UUID tenant : tenants) {
      cursor = tenant;
      Boolean done = store.tenant(tenant, () -> projectOne());
      if (Boolean.TRUE.equals(done)) completed++;
    }
    return completed;
  }

  private boolean projectOne() {
    var row = store.claimSessionProjection(clock.instant());
    if (row == null) return false;
    // Primary is read without row locks: no session-row -> queue-row -> session-row lock cycle.
    store.lock("session-projection:" + row.hash());
    ObjectNode latest = primary.get(row.hash());
    try {
      if (latest == null) secondary.delete(row.hash());
      else secondary.put(row.hash(), latest);
    } catch (RuntimeException error) {
      long delay = Math.min(60, 1L << Math.min(row.attempts(), 6));
      store.retrySessionProjection(row, clock.instant().plusSeconds(delay));
      log.warn("session KV projection retained for retry: {}", error.getClass().getSimpleName());
      return false;
    }
    // A concurrent enqueue waits for this queue row and then commits its own work. Removing this
    // generation can never remove a writer's newer committed enqueue.
    store.completeSessionProjection(row.id());
    return true;
  }

  private void loop() {
    while (running) {
      try {
        forwardOnce();
      } catch (RuntimeException error) {
        if (!running) return;
        log.warn("session KV projection cycle will retry: {}", error.getClass().getSimpleName());
      }
      try {
        signal.tryAcquire(100, TimeUnit.MILLISECONDS);
      } catch (InterruptedException interrupted) {
        Thread.currentThread().interrupt();
        return;
      }
    }
  }

  @Override
  public synchronized void start() {
    if (running) return;
    running = true;
    worker = Thread.ofVirtual().name("auriga-session-projection").start(this::loop);
  }

  @Override
  public void stop() {
    running = false;
    Thread current = worker;
    signal.release();
    if (current != null) {
      current.interrupt();
      try {
        current.join(5000);
      } catch (InterruptedException interrupted) {
        Thread.currentThread().interrupt();
      }
    }
  }

  @Override
  public boolean isRunning() {
    return running;
  }
}
