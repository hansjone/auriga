package auriga.collab.network.local;

import auriga.collab.app.NetworkErrors;
import auriga.collab.app.NetworkJson;
import auriga.common.error.AurigaException;
import com.networknt.schema.SchemaLocation;
import com.networknt.schema.SchemaRegistry;
import com.networknt.schema.SchemaRegistryConfig;
import com.networknt.schema.SpecificationVersion;
import java.io.IOException;
import java.util.HashMap;
import java.util.Map;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Validate the read-only contracts directly; no second set of wire DTOs. */
final class LocalFrames {
  private static final String BASE = "https://auriga.example.com/contracts/network/";
  private static final SchemaRegistry SCHEMAS = schemas(false);
  private static final SchemaRegistry REGISTER_CONSUMER = schemas(true);

  private static SchemaRegistry schemas(boolean consumer) {
    return SchemaRegistry.withDefaultDialect(
        SpecificationVersion.DRAFT_2020_12,
        b ->
            b.schemaRegistryConfig(
                    SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build())
                .schemas(resources(consumer)));
  }

  static JsonNode read(String definition, String json) {
    JsonNode frame = NetworkJson.parse(json);
    var schemas = "LocalRegister".equals(definition) ? REGISTER_CONSUMER : SCHEMAS;
    var schema =
        schemas.getSchema(SchemaLocation.of(BASE + "protocol.schema.json#/$defs/" + definition));
    if (!schema.validate(frame).isEmpty()) throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    return frame;
  }

  private static Map<String, String> resources(boolean consumer) {
    Map<String, String> resources = new HashMap<>();
    for (String name : new String[] {"address", "envelope", "protocol", "source-control"}) {
      String path = "auriga/contracts/network/" + name + ".schema.json";
      try (var input = LocalFrames.class.getClassLoader().getResourceAsStream(path)) {
        if (input == null) throw new IllegalStateException("Missing local registry schema");
        String json = new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
        if (consumer) {
          var schema = JsonMapper.builder().build().readTree(json);
          allowOptionalFields(schema);
          json = schema.toString();
        }
        resources.put(BASE + name + ".schema.json", json);
      } catch (IOException e) {
        throw new IllegalStateException("Cannot load local registry schema", e);
      }
    }
    return resources;
  }

  private static void allowOptionalFields(JsonNode schema) {
    // Same consumer rule as NetworkSchemas: retain known properties:false and all other
    // constraints.
    if (schema instanceof ObjectNode object) {
      if (object.path("additionalProperties").isBoolean()
          && !object.path("additionalProperties").asBoolean())
        object.put("additionalProperties", true);
      for (var entry : object.properties()) allowOptionalFields(entry.getValue());
    } else if (schema.isArray()) for (var item : schema) allowOptionalFields(item);
  }

  private LocalFrames() {}
}
