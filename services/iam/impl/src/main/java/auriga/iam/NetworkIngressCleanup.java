package auriga.iam;

import java.time.Clock;
import java.util.UUID;
import java.util.concurrent.*;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.context.SmartLifecycle;

/** Bounded retention even for nodes that never reconnect. Payload always stays under RLS. */
public final class NetworkIngressCleanup implements SmartLifecycle {
  private static final Logger log = LoggerFactory.getLogger(NetworkIngressCleanup.class);
  private final IamStore store;
  private final Clock clock;
  private UUID cursor;
  private volatile ScheduledExecutorService worker;

  public NetworkIngressCleanup(IamStore store, Clock clock) {
    this.store = store;
    this.clock = clock;
  }

  synchronized int sweepOnce() {
    var jdbc = store.networkJdbc();
    var tenants =
        jdbc.query(
            "SELECT tenant_id FROM iam.network_ingress_tenant WHERE (?::uuid IS NULL OR tenant_id>?) ORDER BY tenant_id LIMIT 32",
            (rs, i) -> rs.getObject(1, UUID.class),
            cursor,
            cursor);
    if (tenants.isEmpty()) {
      cursor = null;
      return 0;
    }
    int removed = 0;
    for (var tenant : tenants) {
      if (Thread.currentThread().isInterrupted()) break;
      cursor = tenant;
      try {
        removed +=
            store.tenant(
                tenant,
                () -> {
                  jdbc.execute("SET LOCAL statement_timeout='2000ms'");
                  return jdbc.update(
                      """
            DELETE FROM iam.network_ingress WHERE tenant_id=? AND id IN (
              SELECT id FROM iam.network_ingress WHERE tenant_id=? AND expires_at<=?
              ORDER BY expires_at,id LIMIT 256 FOR UPDATE SKIP LOCKED)
            """,
                      tenant,
                      tenant,
                      java.sql.Timestamp.from(clock.instant()));
                });
      } catch (RuntimeException failure) {
        log.warn("Network ingress cleanup deferred: {}", failure.getClass().getSimpleName());
      }
    }
    return removed;
  }

  @Override
  public synchronized void start() {
    if (worker != null) return;
    worker =
        Executors.newSingleThreadScheduledExecutor(
            Thread.ofPlatform().daemon().name("iam-network-retention").factory());
    worker.scheduleWithFixedDelay(
        () -> {
          try {
            sweepOnce();
          } catch (RuntimeException failure) {
            log.warn("Network ingress retention deferred: {}", failure.getClass().getSimpleName());
          }
        },
        60,
        60,
        TimeUnit.SECONDS);
  }

  @Override
  public void stop() {
    var running = worker;
    worker = null;
    if (running != null) {
      running.shutdownNow();
      try {
        running.awaitTermination(5, TimeUnit.SECONDS);
      } catch (InterruptedException interrupted) {
        Thread.currentThread().interrupt();
      }
    }
  }

  @Override
  public boolean isRunning() {
    return worker != null;
  }
}
