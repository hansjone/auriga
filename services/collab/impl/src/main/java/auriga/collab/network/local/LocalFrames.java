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

/** Validate the read-only contracts directly; no second set of wire DTOs. */
final class LocalFrames {
  private static final String BASE = "https://auriga.example.com/contracts/network/";
  private static final SchemaRegistry SCHEMAS =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaRegistryConfig(
                      SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build())
                  .schemas(resources()));

  static JsonNode read(String definition, String json) {
    JsonNode frame = NetworkJson.parse(json);
    var schema =
        SCHEMAS.getSchema(SchemaLocation.of(BASE + "protocol.schema.json#/$defs/" + definition));
    if (!schema.validate(frame).isEmpty()) throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    return frame;
  }

  private static Map<String, String> resources() {
    Map<String, String> resources = new HashMap<>();
    for (String name : new String[] {"address", "envelope", "protocol"}) {
      String path = "auriga/contracts/network/" + name + ".schema.json";
      try (var input = LocalFrames.class.getClassLoader().getResourceAsStream(path)) {
        if (input == null) throw new IllegalStateException("Missing local registry schema");
        resources.put(
            BASE + name + ".schema.json",
            new String(input.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8));
      } catch (IOException e) {
        throw new IllegalStateException("Cannot load local registry schema", e);
      }
    }
    return resources;
  }

  private LocalFrames() {}
}
