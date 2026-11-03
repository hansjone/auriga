package auriga.workspace.app;

import java.util.HashSet;
import java.util.Set;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ArrayNode;

/** Project only authorized public model IDs and optional capabilities into sealed node material. */
final class ManagedModelCatalog {
  private static final long MAX_SAFE_INTEGER = 9007199254740991L;

  private ManagedModelCatalog() {}

  static ArrayNode fromGateway(JsonNode data) {
    return project(data, "context_window", "max_output_tokens");
  }

  static boolean hasCatalog(JsonNode material) {
    try {
      project(material.path("models"), "contextWindow", "maxOutputTokens");
      return true;
    } catch (IllegalArgumentException invalid) {
      return false;
    }
  }

  private static ArrayNode project(JsonNode data, String contextName, String outputName) {
    if (!data.isArray() || data.isEmpty() || data.size() > 256)
      throw new IllegalArgumentException("invalid_catalog");
    var models = WorkspaceService.JSON.createArrayNode();
    Set<String> seen = new HashSet<>();
    for (var model : data) {
      String id = model.path("id").asString("");
      if (!model.path("id").isString()
          || id.isBlank()
          || id.length() > 200
          || id.chars().anyMatch(Character::isISOControl))
        throw new IllegalArgumentException("invalid_model_id");
      if (!seen.add(id)) throw new IllegalArgumentException("duplicate_model_id");
      Long contextWindow = capability(model.path(contextName), 2);
      Long maxOutputTokens = capability(model.path(outputName), 1);
      if (contextWindow != null && maxOutputTokens != null && maxOutputTokens >= contextWindow)
        throw new IllegalArgumentException("invalid_model_limits");
      var projected = models.addObject().put("id", id);
      if (contextWindow != null) projected.put("contextWindow", contextWindow);
      if (maxOutputTokens != null) projected.put("maxOutputTokens", maxOutputTokens);
    }
    return models;
  }

  private static Long capability(JsonNode value, long minimum) {
    if (value.isMissingNode()) return null;
    if (!value.isIntegralNumber() || !value.canConvertToLong())
      throw new IllegalArgumentException("invalid_model_limits");
    long result = value.asLong();
    if (result < minimum || result > MAX_SAFE_INTEGER)
      throw new IllegalArgumentException("invalid_model_limits");
    return result;
  }
}
