package auriga.ops;

import auriga.common.context.TenantContext;
import auriga.common.events.EventHandler;
import auriga.common.events.ReceivedEvent;
import auriga.ops.api.OpsSignals;
import java.util.Set;
import java.util.UUID;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.JsonNode;

/**
 * Runs in the ops inbox transaction; producer validation and event deduplication are done by
 * common.
 */
final class OpsInstanceEvents implements EventHandler {
  private static final Logger LOG = LoggerFactory.getLogger(OpsInstanceEvents.class);

  private final OpsSignals signals;
  private final java.time.Instant since;

  OpsInstanceEvents(OpsSignals signals) {
    this(signals, java.time.Instant.now());
  }

  OpsInstanceEvents(OpsSignals signals, java.time.Instant since) {
    this.signals = signals;
    this.since = since;
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
    if (event.time() == null || event.time().isBefore(since)) return;
    String subject = extractInstanceId(event);
    if (subject == null) return;
    signals.observe(
        event.tenantId(),
        "instance_failed",
        subject,
        "critical",
        event.type().equals("instance.failed"));
  }

  private String extractInstanceId(ReceivedEvent event) {
    JsonNode data = event.data();
    if (data != null) {
      JsonNode nested = data.path("instance").path("instanceId");
      if (!nested.isMissingNode() && !nested.isNull()) {
        return parseUuidNode(event, "data.instance.instanceId", nested);
      }
      JsonNode flat = data.path("instanceId");
      if (!flat.isMissingNode() && !flat.isNull()) {
        return parseUuidNode(event, "data.instanceId", flat);
      }
    }
    String subject = event.subject();
    if (subject != null && !subject.isBlank()) {
      return parseUuidString(event, "subject", subject);
    }
    LOG.warn(
        "Ignoring instance event with missing instance id: eventId={}, type={}, missingField=data.instance.instanceId",
        event.id(),
        event.type());
    return null;
  }

  private String parseUuidNode(ReceivedEvent event, String field, JsonNode node) {
    if (!node.isString()) {
      LOG.warn(
          "Ignoring instance event with non-string UUID field: eventId={}, type={}, field={}",
          event.id(),
          event.type(),
          field);
      return null;
    }
    return parseUuidString(event, field, node.asString());
  }

  private String parseUuidString(ReceivedEvent event, String field, String raw) {
    try {
      return UUID.fromString(raw).toString();
    } catch (IllegalArgumentException ex) {
      LOG.warn(
          "Ignoring instance event with invalid UUID: eventId={}, type={}, field={}",
          event.id(),
          event.type(),
          field);
      return null;
    }
  }
}
