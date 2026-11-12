package auriga.resource.app;

import auriga.common.audit.*;
import auriga.common.context.TenantContext;
import auriga.resource.persistence.ResourceStore;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

/** Receipt and audit outbox commit together, allowing the durable file queue to retry. */
public final class DesktopAuditWriter {
  private final ResourceStore transactions;
  private final JdbcTemplate jdbc;
  private final AuditLogger audit;
  private final UUID tenant;

  public DesktopAuditWriter(
      ResourceStore transactions, JdbcTemplate jdbc, AuditLogger audit, UUID tenant) {
    this.transactions = transactions;
    this.jdbc = jdbc;
    this.audit = audit;
    this.tenant = tenant;
  }

  public void record(ObjectNode event) {
    TenantContext.run(
        tenant,
        () ->
            transactions.write(
                () -> {
                  int inserted =
                      jdbc.update(
                          "INSERT INTO resource.desktop_audit_receipt(id,tenant_id,created_at) VALUES (?,?,?::timestamptz) ON CONFLICT DO NOTHING",
                          UUID.fromString(event.path("id").asString()),
                          tenant,
                          event.path("at").asString());
                  if (inserted == 1)
                    audit.record(
                        AuditEntry.builder("resource.desktop." + event.path("action").asString())
                            .actor(new AuditEntry.Actor("user", event.path("actor").asString()))
                            .target("desktop_release", event.path("sequence").asString())
                            .detail("sequence", event.path("sequence").asLong())
                            .detail("occurredAt", event.path("at").asString())
                            .build());
                  return true;
                }));
  }
}
