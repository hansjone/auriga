package auriga.platform;

import auriga.ops.api.OpsSignals;
import auriga.workspace.app.WorkspaceService;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.context.SmartLifecycle;

/**
 * Poll workspace-owned heartbeat state through its transaction boundary; ops never reads workspace
 * tables.
 */
public final class PlatformOpsNodeSignals implements SmartLifecycle {
  private final ObjectProvider<WorkspaceService> workspace;
  private final OpsSignals signals;
  private volatile boolean running;
  private Thread thread;

  PlatformOpsNodeSignals(ObjectProvider<WorkspaceService> workspace, OpsSignals signals) {
    this.workspace = workspace;
    this.signals = signals;
  }

  void poll() {
    var service = workspace.getIfAvailable();
    if (service == null) return;
    for (var tenant : service.store.tenants()) {
      var rows =
          service.store.in(
              tenant,
              () ->
                  service.store.jdbc.queryForList(
                      "SELECT id,last_seen_at,revoked FROM node WHERE tenant_id=?", tenant));
      for (var row : rows) {
        var seen = (java.sql.Timestamp) row.get("last_seen_at");
        boolean offline =
            !(Boolean) row.get("revoked")
                && (seen == null
                    || !seen.toInstant().isAfter(service.clock.instant().minusSeconds(60)));
        signals.observe(tenant, "node_offline", row.get("id").toString(), "critical", offline);
      }
    }
  }

  public synchronized void start() {
    if (running) return;
    running = true;
    thread =
        Thread.ofVirtual()
            .name("ops-node-signals")
            .start(
                () -> {
                  while (running) {
                    try {
                      poll();
                      Thread.sleep(15000);
                    } catch (InterruptedException e) {
                      Thread.currentThread().interrupt();
                      break;
                    } catch (RuntimeException e) {
                      org.slf4j.LoggerFactory.getLogger(PlatformOpsNodeSignals.class)
                          .warn("Ops node signal poll failed");
                      try {
                        Thread.sleep(15000);
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
    if (thread != null) {
      thread.interrupt();
      try {
        thread.join(10000);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
      }
    }
  }

  public boolean isRunning() {
    return running;
  }
}
