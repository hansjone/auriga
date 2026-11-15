package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.security.*;
import auriga.iam.api.*;
import java.time.*;

/** No cached JWT permission or timestamp can replace the current IAM session and role state. */
final class IamPlacementAuthorization implements PlacementAuthorization {
  private final IamStore store;
  private final SessionManager sessions;
  private final PermissionResolver permissions;
  private final Clock clock;

  IamPlacementAuthorization(
      IamStore store, SessionManager sessions, PermissionResolver permissions, Clock clock) {
    this.store = store;
    this.sessions = sessions;
    this.permissions = permissions;
    this.clock = clock;
  }

  public void requireRead() {
    require(false);
  }

  public void requireWrite() {
    require(true);
  }

  private void require(boolean write) {
    var caller =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> IamError.error("unauthenticated", 401));
    IamError.require(
        caller.type() == PrincipalType.USER
            && caller.isAdminContext()
            && TenantContext.require().equals(caller.tenantId())
            && caller.sessionId() != null,
        "forbidden",
        403);
    store.tx(
        () -> {
          var session = sessions.activeById(caller.sessionId());
          IamError.require(
              session != null
                  && caller.subject().equals(session.path("accountId").asString())
                  && "admin".equals(session.path("audience").asString())
                  && !session.has("managedInstance"),
              "unauthenticated",
              401);
          String permission = "workspace.placements." + (write ? "write" : "read");
          IamError.require(
              permissions
                  .resolve(caller.tenantId(), caller.subject(), "admin")
                  .contains(permission),
              "forbidden",
              403);
          if (write) {
            Instant step =
                session.has("stepUpAt") ? Instant.parse(session.path("stepUpAt").asString()) : null;
            Instant now = clock.instant();
            IamError.require(
                step != null && !step.isAfter(now) && step.isAfter(now.minusSeconds(600)),
                "step_up_required",
                403);
          }
          return null;
        });
  }
}
