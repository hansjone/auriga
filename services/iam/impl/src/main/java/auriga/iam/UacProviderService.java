package auriga.iam;

import auriga.common.audit.*;
import auriga.common.error.*;
import auriga.iam.api.UacEnterpriseAdapter;
import java.time.Clock;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Same provider row and ETag as the generic API; no separate configuration fact source. */
final class UacProviderService {
  private final IamStore store;
  private final ProviderManager providers;
  private final UacEnterpriseAdapter adapter;
  private final SessionManager sessions;
  private final AuditLogger audit;
  private final Clock clock;

  UacProviderService(
      IamStore store,
      ProviderManager providers,
      UacEnterpriseAdapter adapter,
      SessionManager sessions,
      AuditLogger audit,
      Clock clock) {
    this.store = store;
    this.providers = providers;
    this.adapter = adapter;
    this.sessions = sessions;
    this.audit = audit;
    this.clock = clock;
    providers.uacDisable(this::disable);
  }

  ObjectNode row(String id, boolean lock) {
    ObjectNode row = store.get("idp_provider", id, lock);
    if (row == null || !row.path("connectorType").asString().equals("uac"))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return row;
  }

  ObjectNode get(String id) {
    return store.tx(
        () -> {
          var row = row(id, false);
          if (!row.path("uacConfigured").asBoolean())
            throw new AurigaException(CommonErrors.NOT_FOUND);
          return projection(row);
        });
  }

  static String etag(JsonNode row) {
    return "\"" + Secrets.hash(row.toString()) + "\"";
  }

  static void match(JsonNode row, String match) {
    if (!etag(row).equals(match)) throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
  }

  ObjectNode put(String id, JsonNode input, String match) {
    IamError.validate(
        input.isObject()
            && input.size() == 3
            && input.path("enabled").isBoolean()
            && input.path("baseUrl").isString()
            && input.path("systemCode").isString());
    ObjectNode config =
        Json.object("baseUrl", input.path("baseUrl"), "systemCode", input.path("systemCode"));
    adapter.validateConfig(config);
    return store.tx(
        () -> {
          ObjectNode row = row(id, true);
          match(row, match);
          providers.encryptConfig(row, config);
          row.put("enabled", input.path("enabled").asBoolean());
          row.put("uacConfigured", true);
          row.put("updatedAt", clock.instant().toString());
          row.put("revision", row.path("revision").asLong() + 1);
          store.save("idp_provider", row);
          if (!row.path("enabled").asBoolean()) disable(id);
          IamAudit.record(
              audit,
              AuditEntry.builder("iam.provider.update")
                  .target("provider", id)
                  .detail("fields", List.of("enabled", "config.baseUrl", "config.systemCode"))
                  .build());
          return projection(row);
        });
  }

  ObjectNode projection(ObjectNode row) {
    ObjectNode config = providers.decryptedConfig(row);
    return Json.object(
        "enabled",
        row.path("enabled").asBoolean(),
        "baseUrl",
        config.path("baseUrl"),
        "systemCode",
        config.path("systemCode"));
  }

  void disable(String providerId) {
    for (ObjectNode challenge : store.all("uac_challenge"))
      if (providerId.equals(challenge.path("providerId").asString())) {
        challenge = store.byId("uac_challenge", challenge.path("id").asString(), true);
        challenge.put("state", "cancelled");
        challenge.remove("credential");
        store.save("uac_challenge", challenge);
      }
    for (ObjectNode tx : store.all("login_tx"))
      if (providerId.equals(tx.path("providerId").asString())
          && "pending".equals(tx.path("status").asString())) {
        tx = store.byId("login_tx", tx.path("id").asString(), true);
        tx.put("status", "cancelled");
        tx.remove("pendingProfile");
        tx.remove("uacCredential");
        store.save("login_tx", tx);
      }
    for (ObjectNode session : store.all("session"))
      if (providerId.equals(session.path("providerId").asString())
          && !session.path("revoked").asBoolean()) sessions.revokeOne(session, "provider_disabled");
  }
}
