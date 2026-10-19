package auriga.iam;

import auriga.common.events.*;
import auriga.contracts.iam.v1.*;
import java.util.Set;

public final class IamAccountEvents implements EventHandler {
  private final IamStore store;
  private final SessionManager sessions;

  public IamAccountEvents(IamStore store, SessionManager sessions) {
    this.store = store;
    this.sessions = sessions;
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
          String id = event.data().path("userId").asString();
          var account = store.get("account", id, true);
          if (account == null) return null;
          if (event.type().equals("user.permissions_changed")) {
            sessions.permissionsChanged(id);
            return null;
          }
          account.put("status", event.type().equals("user.enabled") ? "active" : "disabled");
          store.save("account", account);
          if (!event.type().equals("user.enabled"))
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
          return null;
        });
  }
}
