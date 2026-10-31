package auriga.iam;

import auriga.common.audit.*;
import auriga.common.context.TenantContext;
import auriga.common.persistence.UuidV7;
import java.time.Clock;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

public final class ProviderManager {
  private final IamStore store;
  private final ConnectorRegistry registry;
  private final Secrets secrets;
  private final AuditLogger audit;
  private final Clock clock;
  private final String issuer;

  public ProviderManager(
      IamStore store,
      ConnectorRegistry registry,
      Secrets secrets,
      AuditLogger audit,
      Clock clock,
      String issuer) {
    this.store = store;
    this.registry = registry;
    this.secrets = secrets;
    this.audit = audit;
    this.clock = clock;
    this.issuer = issuer;
  }

  public ObjectNode create(JsonNode input) {
    return store.tx(
        () -> {
          String type = input.path("connectorType").asString();
          IamError.require(!type.equals("emergency"), "builtin_provider", 409);
          registry.validate(type, input.path("config"));
          if (!registry.get(type).descriptor().path("multipleInstances").asBoolean(true))
            IamError.require(
                store.all("idp_provider").stream()
                    .noneMatch(p -> p.path("connectorType").asString().equals(type)),
                "duplicate_provider",
                409);
          String id = UuidV7.next().toString();
          String now = clock.instant().toString();
          JsonNode policy = input.path("admission");
          if (!policy.isObject()) policy = Json.object("mode", "invite_only", "mfa", "new_device");
          validateAdmission(policy);
          ObjectNode row =
              store.insert(
                  "idp_provider",
                  id,
                  Json.object(
                      "providerId",
                      id,
                      "connectorType",
                      type,
                      "builtin",
                      false,
                      "enabled",
                      input.path("enabled").asBoolean(true),
                      "display",
                      input.path("display"),
                      "config",
                      secrets.encrypt(
                          input.path("config").toString(),
                          "provider:" + TenantContext.require() + ":" + id),
                      "admission",
                      policy,
                      "createdAt",
                      now,
                      "updatedAt",
                      now),
                  null);
          audit.record(AuditEntry.builder("iam.provider.create").target("provider", id).build());
          return view(row);
        });
  }

  public ObjectNode get(String id) {
    return store.tx(
        () -> {
          ObjectNode row = store.get("idp_provider", id, false);
          IamError.require(row != null, "not_found", 404);
          return view(row);
        });
  }

  /** JSON Merge Patch for display / config / enabled / admission. */
  public ObjectNode update(String id, JsonNode patch) {
    return store.tx(
        () -> {
          ObjectNode row = store.get("idp_provider", id, true);
          IamError.require(row != null, "not_found", 404);
          IamError.require(!row.path("builtin").asBoolean(), "builtin_provider", 409);
          String type = row.path("connectorType").asString();
          if (patch.has("display")) row.set("display", patch.path("display"));
          if (patch.has("enabled")) row.put("enabled", patch.path("enabled").asBoolean());
          if (patch.has("admission")) {
            validateAdmission(patch.path("admission"));
            row.set("admission", patch.path("admission"));
          }
          if (patch.has("config")) {
            ObjectNode merged = decryptedConfig(row);
            JsonNode configPatch = patch.path("config");
            if (type.equals("oidc")
                && configPatch.has("issuer")
                && !merged.path("issuer").equals(configPatch.path("issuer")))
              IamError.require(
                  configPatch.path("clientSecret").isString()
                      && !configPatch.path("clientSecret").asString().isBlank()
                      && !configPatch.path("clientSecret").equals(merged.path("clientSecret")),
                  "invalid_config",
                  400);
            patch
                .path("config")
                .properties()
                .forEach(
                    e -> {
                      if (e.getValue().isObject()
                          && e.getValue().path("x-secret-set").asBoolean(false)
                          && e.getValue().size() == 1) return;
                      merged.set(e.getKey(), e.getValue());
                    });
            registry.validate(type, merged);
            row.put(
                "config",
                secrets.encrypt(
                    merged.toString(), "provider:" + TenantContext.require() + ":" + id));
          }
          row.put("updatedAt", clock.instant().toString());
          store.save("idp_provider", row);
          List<String> fields = new ArrayList<>();
          patch
              .properties()
              .forEach(
                  e -> {
                    if (e.getKey().equals("config"))
                      e.getValue().properties().forEach(c -> fields.add("config." + c.getKey()));
                    else fields.add(e.getKey());
                  });
          audit.record(
              AuditEntry.builder("iam.provider.update")
                  .target("provider", id)
                  .detail("fields", fields)
                  .build());
          return view(row);
        });
  }

  private void validateAdmission(JsonNode policy) {
    IamError.require(
        policy.path("externalSessionCheckSeconds").asLong(0) == 0,
        "external_session_check_unavailable",
        409);
    // Group mapping requires org's permission ceiling checks before it may be configured.
    for (JsonNode mapping : policy.path("attributeMapping"))
      IamError.require(
          !mapping.path("to").asString().equals("group"), "group_mapping_unavailable", 409);
  }

  ObjectNode view(ObjectNode row) {
    ObjectNode result = row.deepCopy();
    result.put("id", row.path("providerId").asString());
    result.remove("providerId");
    String type = row.path("connectorType").asString();
    JsonNode config = decryptedConfig(row);
    result.set("config", mask(config, registry.get(type).descriptor().path("configSchema")));
    result.put(
        "callbackUrl",
        auriga.common.web.LinkOrigins.currentOr(issuer)
            + "/api/iam/v1/login/callback/"
            + row.path("providerId").asString());
    return result;
  }

  ObjectNode decryptedConfig(ObjectNode row) {
    return Json.read(
        secrets.decrypt(
            row.path("config").asString(),
            "provider:" + TenantContext.require() + ":" + row.path("providerId").asString()));
  }

  private JsonNode mask(JsonNode value, JsonNode schema) {
    if (schema.path("x-secret").asBoolean()) return Json.object("x-secret-set", true);
    // Config is encrypted as a whole. If the schema uses composition/ref, hide all config rather
    // than risk leakage.
    if (schema.has("$ref") || schema.has("allOf") || schema.has("oneOf") || schema.has("anyOf"))
      return Json.object("x-secret-set", true);
    if (value.isObject()) {
      ObjectNode out = Json.object();
      value
          .properties()
          .forEach(
              e ->
                  out.set(
                      e.getKey(),
                      schema.path("properties").has(e.getKey())
                          ? mask(e.getValue(), schema.path("properties").path(e.getKey()))
                          : Json.object("x-secret-set", true)));
      return out;
    }
    if (value.isArray()) {
      var out = Json.MAPPER.createArrayNode();
      value.forEach(v -> out.add(mask(v, schema.path("items"))));
      return out;
    }
    return value;
  }
}
