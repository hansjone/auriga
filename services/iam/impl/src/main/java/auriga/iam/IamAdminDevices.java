package auriga.iam;

import auriga.common.audit.AuditEntry;
import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import java.time.Clock;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Tenant-scoped field-terminal administration; never operates on managed-device credentials. */
public class IamAdminDevices {
  private final IamStore store;
  private final Clock clock;
  private final AuditLogger audit;

  public IamAdminDevices(IamStore store, Clock clock, AuditLogger audit) {
    this.store = store;
    this.clock = clock;
    this.audit = audit;
  }

  public ObjectNode list(String member, String online) {
    IamError.validate(member == null || member.length() <= 128);
    IamError.validate(online == null || Set.of("unknown", "online", "offline").contains(online));
    return store.tx(
        () ->
            Json.object(
                "items",
                store.all("device").stream()
                    .filter(this::field)
                    .map(this::view)
                    .filter(
                        d ->
                            member == null
                                || member.isBlank()
                                || d.path("accountId").asString().equals(member)
                                || d.path("memberName")
                                    .asString()
                                    .toLowerCase(Locale.ROOT)
                                    .contains(member.toLowerCase(Locale.ROOT)))
                    .filter(d -> online == null || d.path("online").asString().equals(online))
                    .sorted(Comparator.comparing(d -> d.path("id").asString()))
                    .toList()));
  }

  public ObjectNode get(String id) {
    return store.tx(() -> view(require(id, false)));
  }

  /** Authenticated edge delegation only; never exposes tickets or device credentials. */
  public ObjectNode connectionBindings() {
    return store.tx(
        () -> {
          var rows =
              store
                  .networkJdbc()
                  .query(
                      "SELECT i.lookup_key,i.data::text FROM iam.network_ingress i JOIN iam.device d ON d.tenant_id=i.tenant_id AND d.id::text=i.data->>'device'"
                          + " WHERE i.tenant_id=? AND d.data->>'kind'='dsh_connect' AND d.data->>'scope'='auriga.connect'"
                          + " AND COALESCE(d.data->>'deviceType','')<>'managed' ORDER BY i.id LIMIT 10001",
                      (rs, index) -> {
                        var data = Json.read(rs.getString(2));
                        return Json.object(
                            "device",
                            data.path("device").asString(),
                            "serverId",
                            data.path("serverId").asString(),
                            "clientId",
                            data.path("clientId").asString(),
                            "account",
                            data.path("account").asString(),
                            "ingress",
                            rs.getString(1));
                      },
                      TenantContext.require());
          if (rows.size() > 10000)
            throw new auriga.common.error.AurigaException(
                auriga.common.error.CommonErrors.UNAVAILABLE);
          return Json.object("items", rows);
        });
  }

  public ObjectNode revoke(String id) {
    return store.tx(
        () -> {
          // Same first lock as network redemption; invalidate policy before publishing success.
          store.lock("network-authority");
          ObjectNode d = require(id, true);
          boolean changed = !d.path("revoked").asBoolean();
          if (changed) {
            d.put("revoked", true);
            d.put("revokedAt", clock.instant().toString());
            store.save("device", d);
          }
          int nodes =
              store
                  .networkJdbc()
                  .update(
                      "UPDATE iam.network_node SET revoked=true WHERE tenant_id=? AND device_id=? AND NOT revoked",
                      TenantContext.require(),
                      UUID.fromString(id));
          if (changed || nodes > 0) {
            new IamNetworkPolicyRevision(store).bump();
            audit.record(AuditEntry.builder("iam.device.revoke").target("device", id).build());
          }
          return view(d);
        });
  }

  private boolean field(ObjectNode d) {
    return "dsh_connect".equals(d.path("kind").asString())
        && !"managed".equals(d.path("deviceType").asString())
        && "auriga.connect".equals(d.path("scope").asString());
  }

  private ObjectNode require(String id, boolean lock) {
    ObjectNode d = store.byId("device", id, lock);
    IamError.require(d != null && field(d), "not_found", 404);
    return d;
  }

  private ObjectNode view(ObjectNode d) {
    String account = d.path("accountId").asString();
    ObjectNode owner = store.byId("account", account, false);
    String mode = d.path("authorizationMode").asString();
    var v =
        Json.object(
            "id",
            d.path("id").asString(),
            "accountId",
            account,
            "memberName",
            owner == null ? account : owner.path("displayName").asString(account),
            "name",
            d.path("name").asString("DSH"),
            "status",
            d.path("revoked").asBoolean() ? "revoked" : "active",
            "online",
            "unknown",
            "accessMode",
            Set.of("native_tx", "manual").contains(mode) ? mode : "unknown");
    for (String key : List.of("platform", "clientVersion", "createdAt", "lastSeenAt", "revokedAt"))
      if (d.has(key)) v.put(key, d.path(key).asString());
    var nodes =
        store
            .networkJdbc()
            .query(
                "SELECT node::text FROM iam.network_node WHERE tenant_id=? AND device_id=?",
                (rs, i) -> rs.getString(1),
                TenantContext.require(),
                UUID.fromString(d.path("id").asString()));
    if (!nodes.isEmpty()) v.put("networkNode", nodes.getFirst());
    return v;
  }
}
