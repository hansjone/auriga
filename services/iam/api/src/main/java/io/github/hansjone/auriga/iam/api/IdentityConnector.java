package io.github.hansjone.auriga.iam.api;

import tools.jackson.databind.JsonNode;

/** JSON values are validated directly against contracts/identity; no duplicate wire model. */
public interface IdentityConnector {
  JsonNode descriptor();

  void validateConfig(JsonNode config);

  JsonNode begin(JsonNode context, JsonNode config);

  JsonNode advance(JsonNode context, JsonNode config, JsonNode input);
}
