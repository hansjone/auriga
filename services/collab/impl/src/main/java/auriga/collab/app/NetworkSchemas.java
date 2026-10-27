package auriga.collab.app;

import auriga.common.error.AurigaException;
import com.networknt.schema.SchemaLocation;
import com.networknt.schema.SchemaRegistry;
import com.networknt.schema.SchemaRegistryConfig;
import com.networknt.schema.SpecificationVersion;
import java.io.IOException;
import java.util.HashMap;
import java.util.Map;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Reuses read-only network contracts; no parallel hand-written wire DTOs. */
final class NetworkSchemas {
  private static final SchemaRegistry REGISTRY =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaRegistryConfig(
                      SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build())
                  .schemas(localSchemas()));

  private NetworkSchemas() {}

  static void validate(String definition, JsonNode value) {
    validate("envelope", definition, value);
  }

  static void validate(String file, String definition, JsonNode value) {
    var schema =
        REGISTRY.getSchema(
            SchemaLocation.of(
                "https://auriga.example.com/contracts/network/"
                    + file
                    + ".schema.json#/$defs/"
                    + definition));
    if (!schema.validate(value).isEmpty()) throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
  }

  private static Map<String, String> localSchemas() {
    Map<String, String> schemas = new HashMap<>();
    for (String file :
        new String[] {
          "envelope.schema.json",
          "address.schema.json",
          "protocol.schema.json",
          "source-control.schema.json",
          "keyset.schema.json"
        }) {
      String path = "auriga/contracts/network/" + file;
      try (var input = NetworkSchemas.class.getClassLoader().getResourceAsStream(path)) {
        if (input == null) throw new IllegalStateException("Missing network schema resource");
        JsonNode schema = NetworkJson.JSON.readTree(input);
        // README §1: a consuming/forwarding server retains unknown optional fields. Known
        // forbidden fields (properties: false), enums, sizes and required fields stay enforced.
        if (!file.equals("keyset.schema.json")) allowOptionalFields(schema);
        String json = schema.toString();
        schemas.put("classpath:" + path, json);
        schemas.put(schema.path("$id").asString(), json);
      } catch (IOException e) {
        throw new IllegalStateException("Cannot read network schema resource");
      }
    }
    return schemas;
  }

  private static void allowOptionalFields(JsonNode schema) {
    if (schema instanceof ObjectNode object) {
      if (object.path("additionalProperties").isBoolean()
          && !object.path("additionalProperties").asBoolean())
        object.put("additionalProperties", true);
      for (var entry : object.properties()) allowOptionalFields(entry.getValue());
    } else if (schema.isArray()) for (var item : schema) allowOptionalFields(item);
  }
}
