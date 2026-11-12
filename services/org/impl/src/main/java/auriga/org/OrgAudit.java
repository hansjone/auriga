package auriga.org;

import auriga.common.audit.*;
import auriga.common.context.RequestContext;
import auriga.common.events.EventPublisher;
import auriga.common.security.PrincipalType;
import java.util.UUID;

/** Fixed metadata only: business fields and invitation codes never enter the audit payload. */
final class OrgAudit {
  private final AuditLogger audit;

  OrgAudit(EventPublisher events) {
    audit = new OutboxAuditLogger(events);
  }

  void record(String action, String targetType, Object id, UUID fallbackActor) {
    var actor =
        RequestContext.currentPrincipal()
            .map(
                p ->
                    new AuditEntry.Actor(
                        p.type() == PrincipalType.INTERNAL_SERVICE
                            ? "system"
                            : p.type().claimValue(),
                        p.subject()))
            .orElse(
                fallbackActor == null
                    ? AuditEntry.Actor.SYSTEM
                    : new AuditEntry.Actor("user", fallbackActor.toString()));
    var request = RequestContext.current().orElseGet(() -> RequestContext.start(null, null));
    var safe =
        new RequestContext(
            request.trace().traceId(),
            request.trace(),
            request.principal(),
            request.clientIp(),
            null);
    safe.run(
        () ->
            audit.record(
                AuditEntry.builder(action).actor(actor).target(targetType, id.toString()).build()));
  }
}
