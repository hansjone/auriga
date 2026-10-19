package io.github.hansjone.auriga.iam.api;

import tools.jackson.databind.JsonNode;

/** Requests/results follow connector.schema.json Directory* definitions. */
public interface DirectoryCapability {
  JsonNode search(JsonNode request);

  JsonNode lookup(JsonNode request);
}
