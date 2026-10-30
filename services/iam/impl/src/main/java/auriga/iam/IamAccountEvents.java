package auriga.iam;

import auriga.common.events.*;
import auriga.contracts.iam.v1.*;
import java.util.Set;

public final class IamAccountEvents implements EventHandler {
  private final IamStore store;
  private final SessionManager sessions;
  private final IamNetworkPolicyRevision policyRevision;

  public IamAccountEvents(IamStore store, SessionManager sessions) {
    this.store = store;
    this.sessions = sessions;
    this.policyRevision = new IamNetworkPolicyRevision(store);
  }

  @Override
  public String name() {
    return "account-lifecycle";
  }

  @Override
  public Set<String> eventTypes() {
    return Set.of("user.disabled", "user.offboarded", "user.enabled", "user.permissions_changed");
  }

  @Override
  public void handle(ReceivedEvent event) {
    store.tx(
        () -> {
          // Same FIRST business lock as network target/source final verification. Session/device
          // effects must precede account FOR UPDATE, never account -> session.
          store.lock("network-authority");
          String id = event.data().path("userId").asString();
          if (store.get("account", id, false) == null) return null;
          boolean permissions = event.type().equals("user.permissions_changed");
          if (permissions) sessions.permissionsChanged(id);
          else if (!event.type().equals("user.enabled"))
            sessions.revoke(
                RevokeSessionsRequest.newBuilder()
                    .setTenantId(event.tenantId().toString())
                    .setAccountId(id)
                    .setIncludeDevices(true)
                    .setReason(
                        event.type().equals("user.offboarded")
                            ? RevokeReason.REVOKE_REASON_USER_OFFBOARDED
                            : RevokeReason.REVOKE_REASON_USER_DISABLED)
                    .build());
          var account = store.get("account", id, true);
          if (account != null && !permissions) {
            account.put("status", event.type().equals("user.enabled") ? "active" : "disabled");
            store.save("account", account);
          }
          policyRevision.bump();
          return null;
        });
  }
}
