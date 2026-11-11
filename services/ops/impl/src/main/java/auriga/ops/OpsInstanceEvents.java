package auriga.ops;

import auriga.common.context.TenantContext;
import auriga.common.events.EventHandler;
import auriga.common.events.ReceivedEvent;
import auriga.ops.api.OpsSignals;
import java.util.Set;
import java.util.UUID;

/**
 * Runs in the ops inbox transaction; producer validation and event deduplication are done by
 * common.
 */
final class OpsInstanceEvents implements EventHandler {
  private final OpsSignals signals;

  OpsInstanceEvents(OpsSignals signals) {
    this.signals = signals;
  }

  public String name() {
    return "instance-alerts";
  }

  public Set<String> eventTypes() {
    return Set.of("instance.failed", "instance.started", "instance.deleted");
  }

  public void handle(ReceivedEvent event) {
    OpsError.require(
        eventTypes().contains(event.type()) && TenantContext.require().equals(event.tenantId()));
    String subject = UUID.fromString(event.data().path("instanceId").asText()).toString();
    signals.observe(
        event.tenantId(),
        "instance_failed",
        subject,
        "critical",
        event.type().equals("instance.failed"));
  }
}
