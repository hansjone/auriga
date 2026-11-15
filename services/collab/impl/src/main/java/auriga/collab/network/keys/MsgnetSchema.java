package auriga.collab.network.keys;

import com.networknt.schema.SchemaLocation;
import com.networknt.schema.SchemaRegistry;
import com.networknt.schema.SchemaRegistryConfig;
import com.networknt.schema.SpecificationVersion;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import tools.jackson.databind.JsonNode;

/** Strict validation against the frozen v2 contract, including unknown-field rejection. */
final class MsgnetSchema {
  private static final String URI =
      "https://auriga.example.com/contracts/network/auto-renewal-v2.schema.json";
  private static final SchemaRegistry REGISTRY = registry();

  private MsgnetSchema() {}

  static void validate(String definition, JsonNode value) {
    if (!REGISTRY
        .getSchema(SchemaLocation.of(URI + "#/$defs/" + definition))
        .validate(value)
        .isEmpty()) throw new IllegalArgumentException("Invalid msgnet " + definition);
    if (value.has("realm")
        && new java.math.BigInteger(value.path("realm").asString()).bitLength() > 64)
      throw new IllegalArgumentException("Invalid msgnet realm");
  }

  private static SchemaRegistry registry() {
    try (var input =
        MsgnetSchema.class
            .getClassLoader()
            .getResourceAsStream("auriga/contracts/network/auto-renewal-v2.schema.json")) {
      if (input == null) throw new IllegalStateException("Missing msgnet v2 schema");
      String schema = new String(input.readAllBytes(), StandardCharsets.UTF_8);
      return SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          builder ->
              builder
                  .schemaRegistryConfig(
                      SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build())
                  .schemas(Map.of(URI, schema)));
    } catch (java.io.IOException e) {
      throw new IllegalStateException("Cannot load msgnet v2 schema", e);
    }
  }
}
