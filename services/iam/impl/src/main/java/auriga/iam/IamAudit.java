package auriga.iam;

import auriga.common.audit.*;
import auriga.common.context.RequestContext;
import auriga.common.events.EventPublisher;
import auriga.common.security.PrincipalType;

/** Security metadata only; internal service identities use the contract's system actor type. */
final class IamAudit {
  static AuditEntry.Builder entry(String action, String targetType, String id, String account) {
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
                account == null ? AuditEntry.Actor.SYSTEM : new AuditEntry.Actor("user", account));
    return AuditEntry.builder(action).actor(actor).target(targetType, id);
  }

  static void mfa(EventPublisher events, String account, String method, String change) {
    record(
        new OutboxAuditLogger(events),
        entry("iam.mfa." + method + "." + change, "account", account, account).build());
  }

  static void record(AuditLogger logger, AuditEntry entry) {
    var request = RequestContext.current().orElseGet(() -> RequestContext.start(null, null));
    var actor = entry.actor();
    if (actor == null && request.principal() != null) {
      var principal = request.principal();
      actor =
          new AuditEntry.Actor(
              principal.type() == PrincipalType.INTERNAL_SERVICE
                  ? "system"
                  : principal.type().claimValue(),
              principal.subject());
    }
    var safeEntry =
        new AuditEntry(
            entry.action(),
            entry.target(),
            entry.result(),
            entry.errorCode(),
            entry.changes(),
            entry.details(),
            actor);
    // Only fixed correlation metadata is allowed; a caller's user-agent/request ID may contain
    // secrets.
    new RequestContext(
            request.trace().traceId(),
            request.trace(),
            request.principal(),
            request.clientIp(),
            null)
        .run(() -> logger.record(safeEntry));
  }

  private IamAudit() {}
}
