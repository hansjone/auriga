package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.*;
import auriga.iam.api.NetworkAuthority;
import java.time.Clock;
import java.util.Set;

/**
 * Device-only HTTPS use cases. Neither request fields nor HTTP headers select a Broker audience.
 */
public final class IamNetworkClient {
  private final NetworkAuthority authority;
  private final IamStore store;
  private final Clock clock;
  private final String audience;

  public IamNetworkClient(
      NetworkAuthority authority,
      IamStore store,
      Clock clock,
      String selected,
      Set<String> servers) {
    this.authority = authority;
    this.store = store;
    this.clock = clock;
    if (selected != null && !selected.isBlank() && !servers.contains(selected))
      throw new IllegalArgumentException("Network client audience must be server-allowlisted");
    this.audience =
        selected == null || selected.isBlank()
            ? (servers.size() == 1 ? servers.iterator().next() : null)
            : selected;
  }

  public NetworkAuthority.RegistrationOutcome register(String accessToken) {
    limit();
    try {
      return authority.registerWithOutcome(accessToken);
    } catch (UnsupportedOperationException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  public NetworkAuthority.Ticket prepare(String accessToken, String node) {
    limit();
    if (!IamNetworkAuthority.validNumber(node, false))
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    if (audience == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    return authority.prepare(accessToken, node, audience);
  }

  private void limit() {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (principal.type() != PrincipalType.DEVICE
        || principal.context() != TokenContext.DEVICE
        || !principal.tenantId().equals(TenantContext.require())
        || TenantContext.PLATFORM_TENANT.equals(principal.tenantId())
        || principal.deviceId() == null
        || principal.deviceId().isBlank()
        || !principal.hasPermission("auriga.connect.collab.use"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    store.requireTransactionReleased();
    var now = clock.instant();
    // Fixed bounded identities, no attacker-selected keys. Counters commit before any denial.
    store.limit("network-client:tenant", now, 3000);
    store.limit("network-client:account:" + principal.subject(), now, 120);
    store.limit("network-client:device:" + principal.deviceId(), now, 30);
  }
}
