package auriga.collab.app;

import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import java.util.UUID;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.transaction.support.TransactionTemplate;

/** Restart-safe tenant wakeups; IAM calls run after the discovery transaction has ended. */
public final class NetworkSourceMaintenance {
  private static final Logger LOG = LoggerFactory.getLogger(NetworkSourceMaintenance.class);
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final NetworkMailboxWireService wire;
  private UUID cursor = TenantContext.PLATFORM_TENANT;

  public NetworkSourceMaintenance(
      JdbcTemplate jdbc, TenantTransactionManager manager, NetworkMailboxWireService wire) {
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    this.wire = wire;
  }

  @Scheduled(fixedDelay = 1000, initialDelay = 1000)
  public synchronized void retry() {
    try {
      var tenants =
          TenantContext.call(
              TenantContext.PLATFORM_TENANT,
              () ->
                  tx.execute(
                      s ->
                          jdbc.queryForList(
                              "SELECT business_tenant FROM collab.network_source_worker_tenant WHERE tenant_id=? AND business_tenant>? ORDER BY business_tenant LIMIT 32",
                              UUID.class,
                              TenantContext.PLATFORM_TENANT,
                              cursor)));
      if (tenants.isEmpty()) {
        cursor = TenantContext.PLATFORM_TENANT;
        return;
      }
      for (UUID tenant : tenants) {
        cursor = tenant;
        try {
          TenantContext.call(tenant, wire::retrySourceRevocations);
        } catch (RuntimeException e) {
          LOG.warn("Network source revocation remains pending");
        }
      }
    } catch (RuntimeException e) {
      LOG.warn("Network source outbox discovery unavailable; durable work retained");
    }
  }
}
