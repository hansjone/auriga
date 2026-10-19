package io.github.hansjone.auriga.common.events;

import com.networknt.schema.Error;
import com.networknt.schema.Schema;
import com.networknt.schema.SchemaLocation;
import com.networknt.schema.SchemaRegistry;
import com.networknt.schema.SchemaRegistryConfig;
import com.networknt.schema.SpecificationVersion;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import tools.jackson.databind.JsonNode;

/**
 * Producer-side validation against the event contracts (JSON Schema 2020-12, strict: {@code
 * additionalProperties: false} applies, formats are asserted).
 *
 * <p>Schemas are read from the classpath under {@value #ROOT}; common packages the files from
 * {@code contracts/events}. Type {@code user.permissions_changed} maps to {@code
 * user.schema.json#/$defs/UserPermissionsChanged} (README event table). An event type without a
 * schema is rejected: new types must be registered in the contract first.
 */
public final class EventSchemas {
  public static final String ROOT = "auriga/contracts/events/";

  private final SchemaRegistry registry =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaRegistryConfig(
                  SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()));
  private final Map<String, Schema> cache = new ConcurrentHashMap<>();

  /** Validates a complete envelope (including its data). */
  public void validate(JsonNode envelope) {
    check("envelope", schema("classpath:" + ROOT + "envelope.schema.json"), envelope);
    String type = envelope.path("type").asString();
    check(type, schema(dataSchemaLocation(type)), envelope.path("data"));
  }

  static String dataSchemaLocation(String type) {
    String[] parts = type.split("\\.");
    if (parts.length < 2) throw new InvalidEventException("Invalid event type: " + type);
    StringBuilder def = new StringBuilder();
    for (String part : parts) {
      for (String word : part.split("_")) {
        if (word.isEmpty()) continue;
        def.append(word.substring(0, 1).toUpperCase(Locale.ROOT)).append(word.substring(1));
      }
    }
    return "classpath:" + ROOT + parts[0] + ".schema.json#/$defs/" + def;
  }

  private Schema schema(String location) {
    return cache.computeIfAbsent(
        location,
        l -> {
          String file = l.substring("classpath:".length()).split("#")[0];
          if (Thread.currentThread().getContextClassLoader().getResource(file) == null
              && EventSchemas.class.getClassLoader().getResource(file) == null) {
            throw new InvalidEventException("No contract schema for " + l);
          }
          try {
            Schema s = registry.getSchema(SchemaLocation.of(l));
            s.initializeValidators();
            return s;
          } catch (RuntimeException e) {
            throw new InvalidEventException("No contract schema for " + l + ": " + e.getMessage());
          }
        });
  }

  private static void check(String what, Schema schema, JsonNode node) {
    List<Error> errors = schema.validate(node);
    if (!errors.isEmpty()) {
      throw new InvalidEventException(
          "Event "
              + what
              + " violates its contract schema: "
              + errors.stream().map(Error::toString).limit(10).toList());
    }
  }
}
