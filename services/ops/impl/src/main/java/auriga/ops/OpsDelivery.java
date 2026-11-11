package auriga.ops;

import auriga.common.context.TenantContext;
import java.util.*;

/** Durable at-least-once delivery. Never holds a database transaction during external I/O. */
public final class OpsDelivery {
  private final OpsStore store;

  public OpsDelivery(OpsStore store) {
    this.store = store;
  }

  public boolean processOne(UUID tenant) {
    if (org.springframework.transaction.support.TransactionSynchronizationManager
        .isActualTransactionActive())
      throw new IllegalStateException("Delivery must run outside caller transactions");
    if (!store.secrets.available()) return false;
    return TenantContext.call(
        tenant,
        () -> {
          var job =
              store.transaction(
                  () -> {
                    store.jdbc.update(
                        "UPDATE ops.notification_delivery d SET status='cancelled',lease_id=NULL,lease_until=NULL WHERE d.tenant_id=? AND d.status IN ('pending','sending') AND NOT EXISTS(SELECT 1 FROM ops.alert a JOIN ops.notification_channel c ON c.tenant_id=a.tenant_id AND c.id=d.channel_id WHERE a.tenant_id=d.tenant_id AND a.id=d.alert_id AND a.resolved_at IS NULL AND a.acknowledged_at IS NULL AND (a.silenced_until IS NULL OR a.silenced_until<=now()) AND c.enabled AND (c.min_severity='warning' OR d.payload::jsonb->>'severity'='critical'))",
                        tenant);
                    store.jdbc.update(
                        "UPDATE ops.notification_delivery SET status='failed',last_error='ops.delivery_failed',lease_id=NULL,lease_until=NULL WHERE tenant_id=? AND attempts>=5 AND status='sending' AND lease_until<=now()",
                        tenant);
                    var rows =
                        store.jdbc.queryForList(
                            "SELECT d.*,c.encrypted_target FROM ops.notification_delivery d JOIN ops.notification_channel c ON c.tenant_id=d.tenant_id AND c.id=d.channel_id WHERE d.tenant_id=? AND d.attempts<5 AND ((d.status='pending' AND d.next_attempt_at<=now()) OR (d.status='sending' AND d.lease_until<=now())) AND (c.last_delivery_at IS NULL OR c.last_delivery_at<=now()-interval '30 seconds') AND (c.last_test_at IS NULL OR c.last_test_at<=now()-interval '30 seconds') ORDER BY d.next_attempt_at,d.id LIMIT 1 FOR UPDATE OF d,c SKIP LOCKED",
                            tenant);
                    if (rows.isEmpty()) return null;
                    var row = rows.getFirst();
                    UUID lease = UUID.randomUUID();
                    row.put("lease_id", lease);
                    row.put("attempts", ((Number) row.get("attempts")).intValue() + 1);
                    store.jdbc.update(
                        "UPDATE ops.notification_delivery SET status='sending',attempts=attempts+1,lease_id=?,lease_until=now()+interval '90 seconds' WHERE tenant_id=? AND id=?",
                        lease,
                        tenant,
                        row.get("id"));
                    store.jdbc.update(
                        "UPDATE ops.notification_channel SET last_delivery_at=now() WHERE tenant_id=? AND id=?",
                        tenant,
                        row.get("channel_id"));
                    return row;
                  });
          if (job == null) return false;
          String error = null;
          try {
            var target =
                OpsStore.JSON.readTree(
                    store.secrets.open(
                        tenant + ":" + job.get("channel_id"),
                        job.get("encrypted_target").toString()));
            store.webhook.send(
                target.path("url").asText(),
                target.path("bearer").asText(),
                job.get("payload").toString());
          } catch (RuntimeException e) {
            error = "ops.delivery_failed";
          }
          String failure = error;
          int attempts = ((Number) job.get("attempts")).intValue();
          store.transaction(
              () -> {
                store.jdbc.update(
                    "UPDATE ops.notification_delivery SET status=?,last_error=?,delivered_at=CASE WHEN ? THEN now() ELSE NULL END,next_attempt_at=now()+interval '1 second'*?,lease_id=NULL,lease_until=NULL WHERE tenant_id=? AND id=? AND lease_id=? AND status='sending'",
                    failure == null ? "sent" : attempts >= 5 ? "failed" : "pending",
                    failure,
                    failure == null,
                    30 * (1 << (attempts - 1)),
                    tenant,
                    job.get("id"),
                    job.get("lease_id"));
                return null;
              });
          return true;
        });
  }
}
