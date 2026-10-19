package io.github.hansjone.auriga.iam;

import com.networknt.schema.*;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import tools.jackson.databind.JsonNode;

public final class ContractSchemas {
  private final SchemaRegistry registry =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaRegistryConfig(
                  SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()));
  private final Map<String, Schema> schemas = new ConcurrentHashMap<>();

  public void validate(String location, JsonNode value) {
    Schema s =
        schemas.computeIfAbsent(
            location,
            l -> registry.getSchema(SchemaLocation.of("classpath:auriga/iam/contracts/" + l)));
    IamError.require(s.validate(value).isEmpty(), "contract_violation", 400);
  }

  public void config(JsonNode schema, JsonNode value) {
    IamError.require(registry.getSchema(schema).validate(value).isEmpty(), "invalid_config", 400);
  }
}
