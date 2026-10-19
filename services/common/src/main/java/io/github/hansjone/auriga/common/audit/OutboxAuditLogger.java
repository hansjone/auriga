package io.github.hansjone.auriga.common.audit;

import io.github.hansjone.auriga.common.context.RequestContext;
import io.github.hansjone.auriga.common.events.EventEnvelope;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.security.AurigaPrincipal;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;

/** {@link AuditLogger} that builds {@code AuditRecorded} data and writes it through the outbox. */
public final class OutboxAuditLogger implements AuditLogger {
  private final EventPublisher publisher;

  public OutboxAuditLogger(EventPublisher publisher) {
    this.publisher = Objects.requireNonNull(publisher);
  }

  @Override
  public void record(AuditEntry entry) {
    Map<String, Object> data = toData(entry, RequestContext.current().orElse(null));
    String subject = entry.target() != null ? entry.target().id() : entry.action();
    publisher.publish(EventEnvelope.AUDIT_TYPE, subject, data);
  }

  static Map<String, Object> toData(AuditEntry entry, RequestContext request) {
    Map<String, Object> data = new LinkedHashMap<>();
    data.put("actor", actor(entry, request));
    data.put("action", entry.action());
    if (entry.target() != null) {
      Map<String, Object> target = new LinkedHashMap<>();
      target.put("type", entry.target().type());
      target.put("id", entry.target().id());
      if (entry.target().name() != null) target.put("name", entry.target().name());
      data.put("target", target);
    }
    data.put("result", entry.result().name().toLowerCase(Locale.ROOT));
    if (entry.errorCode() != null) data.put("errorCode", entry.errorCode());
    if (entry.changes() != null) data.put("changes", entry.changes().asMap());
    if (entry.details() != null && !entry.details().isEmpty()) {
      data.put("details", AuditChanges.redact(entry.details()));
    }
    if (request != null) data.put("requestId", request.requestId());
    return data;
  }

  private static Map<String, Object> actor(AuditEntry entry, RequestContext request) {
    Map<String, Object> actor = new LinkedHashMap<>();
    AurigaPrincipal principal = request == null ? null : request.principal();
    if (entry.actor() != null) {
      actor.put("type", entry.actor().type());
      actor.put("id", entry.actor().id());
    } else if (principal != null) {
      actor.put("type", principal.type().claimValue());
      actor.put("id", principal.subject());
    } else {
      actor.put("type", AuditEntry.Actor.SYSTEM.type());
      actor.put("id", null);
    }
    if (request != null) {
      if (request.clientIp() != null) actor.put("ip", request.clientIp());
      if (request.userAgent() != null) actor.put("userAgent", request.userAgent());
      if (principal != null && principal.sessionId() != null && entry.actor() == null) {
        actor.put("sessionId", principal.sessionId());
      }
    }
    return actor;
  }
}
