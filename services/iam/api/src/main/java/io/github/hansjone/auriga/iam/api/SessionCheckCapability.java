package io.github.hansjone.auriga.iam.api;

import tools.jackson.databind.JsonNode;

public interface SessionCheckCapability {
  JsonNode checkSession(JsonNode request);
}
