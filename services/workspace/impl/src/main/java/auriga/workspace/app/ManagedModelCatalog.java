package auriga.workspace.app;

import java.util.HashSet;
import java.util.Set;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ArrayNode;

/** Project only authorized public model IDs and pool limits into sealed node material. */
final class ManagedModelCatalog {
  private ManagedModelCatalog() {}

  static ArrayNode fromGateway(JsonNode data) {
    return project(data, "context_window", "max_output_tokens");
  }

  static boolean hasLimits(JsonNode material) {
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
      var context = model.path(contextName);
      var output = model.path(outputName);
      if (!context.isIntegralNumber()
          || !context.canConvertToLong()
          || !output.isIntegralNumber()
          || !output.canConvertToLong()) throw new IllegalArgumentException("invalid_model_limits");
      long contextWindow = context.asLong(), maxOutputTokens = output.asLong();
      if (contextWindow < 2
          || contextWindow > 1048576
          || maxOutputTokens < 1
          || maxOutputTokens > 32768
          || maxOutputTokens >= contextWindow)
        throw new IllegalArgumentException("invalid_model_limits");
      models
          .addObject()
          .put("id", id)
          .put("contextWindow", contextWindow)
          .put("maxOutputTokens", maxOutputTokens);
    }
    return models;
  }
}
