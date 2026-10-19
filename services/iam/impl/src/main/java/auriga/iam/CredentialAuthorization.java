package auriga.iam;

import auriga.common.security.AuthorizationRules;
import java.time.*;
import java.util.UUID;

/** Keep credential mutation checks effective for callers other than HTTP controllers. */
final class CredentialAuthorization {
  static void requireRecent(UUID accountId, Clock clock) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> IamError.error("step_up_required", 403));
    IamError.require(
        principal.subject().equals(accountId.toString())
            && principal.tenantId().equals(auriga.common.context.TenantContext.require())
            && principal.sessionId() != null
            && principal.steppedUpWithin(Duration.ofMinutes(10), clock.instant()),
        "step_up_required",
        403);
  }

  private CredentialAuthorization() {}
}
