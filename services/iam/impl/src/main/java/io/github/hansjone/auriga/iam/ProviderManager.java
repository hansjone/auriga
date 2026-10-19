package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.audit.*;
import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.persistence.UuidV7;
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
          IamError.require(
              policy.path("externalSessionCheckSeconds").asLong(0) == 0,
              "external_session_check_unavailable",
              409);
          // Group mapping requires org's permission ceiling checks before it may be configured.
          for (JsonNode mapping : policy.path("attributeMapping"))
            IamError.require(
                !mapping.path("to").asString().equals("group"), "group_mapping_unavailable", 409);
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

  ObjectNode view(ObjectNode row) {
    ObjectNode result = row.deepCopy();
    result.put("id", row.path("providerId").asString());
    result.remove("providerId");
    String type = row.path("connectorType").asString();
    JsonNode config = decryptedConfig(row);
    result.set("config", mask(config, registry.get(type).descriptor().path("configSchema")));
    result.put(
        "callbackUrl", issuer + "/api/iam/v1/login/callback/" + row.path("providerId").asString());
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
